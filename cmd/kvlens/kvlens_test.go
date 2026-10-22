package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/alicebob/miniredis/v2"
	"github.com/charmbracelet/x/ansi"
	protokvv1 "github.com/pckt-sh/protokv/gen/go/protokv/v1"
	"github.com/pckt-sh/protokv/internal/testpb"
	"github.com/pckt-sh/protokv/kv"
	"github.com/pckt-sh/protokv/schema"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestScanPattern(t *testing.T) {
	s, _ := schema.GetFromType[*testpb.Price]()

	cases := []struct {
		options map[string]string
		filter  string
		want    string
	}{
		{nil, "", "price:*:*"},
		{map[string]string{"region": "US"}, "", "price:US:*"},
		{nil, "abc", "price:*:*abc*"},
		{nil, "ab?", "price:*:ab?"},
	}

	for _, c := range cases {
		if got := scanPattern(s, c.options, c.filter); got != c.want {
			t.Errorf("scanPattern(%v, %q) = %q, want %q", c.options, c.filter, got, c.want)
		}
	}
}

func TestKeyMatcher(t *testing.T) {
	s, _ := schema.GetFromType[*testpb.Product]()
	m := keyMatcher(s, nil)

	if !m.MatchString("product:1") {
		t.Error("product:1 should match")
	}

	// `product:*` also matches the keys of ProductRank.
	if m.MatchString("product:rank:1") {
		t.Error("product:rank:1 should not match")
	}

	p, _ := schema.GetFromType[*testpb.Price]()
	if !keyMatcher(p, map[string]string{"region": "US"}).MatchString("price:US:1") {
		t.Error("price:US:1 should match")
	}
}

func TestFillOptions(t *testing.T) {
	p, _ := schema.GetFromType[*testpb.Price]()

	got := fillOptions(p, &testpb.Price{Region: testpb.Region_REGION_EU_VAT}, map[string]string{"other": "x"})
	if got["region"] != "EU.VAT-REGISTERED" || got["other"] != "x" {
		t.Errorf("wrong options, got: %v", got)
	}
}

func TestParseOptions(t *testing.T) {
	got := parseOptions(" region = US, market=FR ,broken")
	if len(got) != 2 || got["region"] != "US" || got["market"] != "FR" {
		t.Errorf("wrong options, got: %v", got)
	}

	if s := formatOptions(got); s != "market=FR, region=US" {
		t.Errorf("wrong format, got: %v", s)
	}
}

// TestLoadDescriptors loads a model the binary doesn't know.
func TestLoadDescriptors(t *testing.T) {
	opts := &descriptorpb.MessageOptions{}
	proto.SetExtension(opts, protokvv1.E_Storage, &protokvv1.Storage{Prefix: "dyn:{pk}", PrimaryKey: "id"})

	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("dyn/v1/dyn.proto"),
		Package:    proto.String("dyn.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"protokv/v1/protokv.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:    proto.String("Thing"),
			Options: opts,
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("id"),
				JsonName: proto.String("id"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
			}},
		}},
	}

	set := &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(protokvv1.File_protokv_v1_protokv_proto),
		file,
	}}

	b, err := proto.Marshal(set)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "models.binpb")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := loadDescriptors([]string{path}); err != nil {
		t.Fatal(err)
	}

	s, err := schema.Get("dyn.v1.Thing")
	if err != nil {
		t.Fatal(err)
	}

	if s.GetPrefix() != "dyn:{pk}" {
		t.Errorf("wrong prefix, got: %v", s.GetPrefix())
	}

	names := []string{}
	for _, m := range storedModels() {
		names = append(names, string(m.GetName()))
	}
	if !slices.Contains(names, "dyn.v1.Thing") || !slices.Contains(names, "protokvtest.v1.Product") {
		t.Errorf("missing models, got: %v", names)
	}
}

// harness drives the app like the Bubble Tea runtime, running commands
// synchronously.
type harness struct {
	t   *testing.T
	app *app
}

func newHarness(t *testing.T) *harness {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rc.Close() })
	db := kv.New(rc)

	ctx := t.Context()
	for _, msg := range []proto.Message{
		&testpb.Product{Id: "1", Title: "First", Slug: "first", Price: nil},
		&testpb.Product{Id: "2", Title: "Second", Slug: "second"},
		&testpb.ProductRank{Id: "1", Rank: 42},
		&testpb.Price{Id: "1", Region: testpb.Region_REGION_US, Amount: 999},
	} {
		if err := db.Proto().Set(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}

	var models []schema.Storage
	for _, m := range storedModels() {
		if strings.HasPrefix(string(m.GetName()), "protokvtest.v1.") {
			models = append(models, m)
		}
	}

	h := &harness{t: t, app: newApp(db, "redis://test", models)}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.app.Init())

	return h
}

func (h *harness) send(msg tea.Msg) {
	_, cmd := h.app.Update(msg)
	h.run(cmd)
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}

	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	case pageMsg, recordMsg:
		h.send(msg)
	}
}

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		var key tea.Key
		switch k {
		case "enter":
			key = tea.Key{Code: tea.KeyEnter}
		case "esc":
			key = tea.Key{Code: tea.KeyEscape}
		case "tab":
			key = tea.Key{Code: tea.KeyTab}
		case "down":
			key = tea.Key{Code: tea.KeyDown}
		default:
			key = tea.Key{Code: rune(k[0]), Text: k}
		}
		h.send(tea.KeyPressMsg(key))
	}
}

func (h *harness) screen() string {
	return ansi.Strip(h.app.View().Content)
}

func (h *harness) expect(substr ...string) {
	h.t.Helper()

	screen := h.screen()
	for _, s := range substr {
		if !strings.Contains(screen, s) {
			h.t.Fatalf("screen does not contain %q:\n%s", s, screen)
		}
	}
}

func TestBrowse(t *testing.T) {
	h := newHarness(t)

	// Models are sorted: Item, Price, Product, ProductRank.
	h.expect("Models", "Item", "Price", "Product", "ProductRank")

	h.press("down", "down", "enter")
	h.expect("Product", "2 keys", "product:1", "product:2")

	// The ProductRank key matches `product:*` but isn't listed.
	if strings.Contains(h.screen(), "product:rank:1") {
		t.Fatalf("product:rank:1 listed with products:\n%s", h.screen())
	}

	// Open the record, then its rank relation.
	h.press("enter")
	h.expect(`"title": "First"`, "protokvtest.v1.Product", "rank", "price")

	h.press("tab")
	h.expect("product:rank:1", `"rank": "42"`)

	// price:{region}:1 needs a region.
	h.press("tab")
	h.expect("set them with `o`")

	// Follow the rank relation.
	h.press("tab", "tab", "enter")
	h.expect("protokvtest.v1.ProductRank", `"rank": "42"`)

	h.press("esc")
	h.expect("Product › rank", "product:rank:1")

	h.press("esc")
	h.expect("product:1", "product:2")
}

func TestFilterByIndex(t *testing.T) {
	h := newHarness(t)

	h.press("down", "down", "enter", "/")
	for _, r := range "slug=second" {
		h.press(string(r))
	}
	h.press("enter")

	h.expect("1 key ", "product:2")
	if strings.Contains(h.screen(), "product:1") {
		t.Fatalf("product:1 should be filtered out:\n%s", h.screen())
	}
}

func TestOptions(t *testing.T) {
	h := newHarness(t)

	// Price, then set the region placeholder.
	h.press("down", "enter", "o")
	for _, r := range "region=US" {
		h.press(string(r))
	}
	h.press("enter")
	h.expect("price:US:*", "price:US:1")

	h.press("enter")
	h.expect(`"amount": "999"`, `"region": "REGION_US"`)
}
