package schema

import (
	"errors"
	"slices"
	"testing"

	"github.com/pckt-sh/protokv/internal/testpb"
	"google.golang.org/protobuf/proto"
)

func TestGet(t *testing.T) {
	s, err := Get("protokvtest.v1.Product")
	if err != nil {
		t.Fatal(err)
	}

	if s.GetPrefix() != "product:{pk}" {
		t.Errorf("wrong prefix, got: %v", s.GetPrefix())
	}
}

func TestGetFromType(t *testing.T) {
	if _, err := GetFromType[*testpb.Product](); err != nil {
		t.Error(err)
	}
}

func TestGetWithoutStorage(t *testing.T) {
	if _, err := GetFromType[*testpb.Plain](); !errors.Is(err, ErrNoStorage) {
		t.Errorf("expected ErrNoStorage, got: %v", err)
	}
}

func TestEnum(t *testing.T) {
	enum, err := GetEnum("protokvtest.v1.Region")
	if err != nil {
		t.Fatal(err)
	}

	cases := map[testpb.Region]string{
		testpb.Region_REGION_US:     "US",
		testpb.Region_REGION_EU_VAT: "EU.VAT-REGISTERED",
		testpb.Region_REGION_ASIA:   "protokvtest.v1.REGION_ASIA",
	}
	for value, expected := range cases {
		if got := enum.ValueName(value.Number()); got != expected {
			t.Errorf("wrong name for %v, expected %v; got %v", value, expected, got)
		}
	}
}

func TestGetKey(t *testing.T) {
	s, err := GetFromType[*testpb.Price]()
	if err != nil {
		t.Fatal(err)
	}

	key := s.GetKey("examplePk", map[string]string{"region": "US"})
	if key != "price:US:examplePk" {
		t.Error("got wrong key, got:", key)
	}
}

func TestToKey(t *testing.T) {
	s, err := GetFromType[*testpb.Price]()
	if err != nil {
		t.Fatal(err)
	}

	key := s.ToKey(&testpb.Price{Id: "123", Region: testpb.Region_REGION_EU_VAT})
	if key != "price:EU.VAT-REGISTERED:123" {
		t.Errorf("wrong key, expected: price:EU.VAT-REGISTERED:123; got: %v", key)
	}
}

func TestToKeyNumeric(t *testing.T) {
	s, err := GetFromType[*testpb.Item]()
	if err != nil {
		t.Fatal(err)
	}

	if key := s.ToKey(&testpb.Item{Id: 42}); key != "item:42" {
		t.Errorf("wrong key, got: %v", key)
	}
}

func TestGetPlaceholders(t *testing.T) {
	got := GetPlaceholders("price:{region}:{pk}")
	if !slices.Equal(got, []string{"region", "pk"}) {
		t.Errorf("wrong placeholders, got: %v", got)
	}
}

func TestTrimPrefix(t *testing.T) {
	s, err := GetFromType[*testpb.Product]()
	if err != nil {
		t.Fatal(err)
	}

	if pk := s.TrimPrefix("product:abc"); pk != "abc" {
		t.Errorf("wrong pk, got: %v", pk)
	}
}

func TestGetIndex(t *testing.T) {
	s, err := GetFromType[*testpb.Product]()
	if err != nil {
		t.Fatal(err)
	}

	idx, err := s.GetIndex("slug")
	if err != nil {
		t.Fatal(err)
	}

	if idx.GetPrefix() != "index:product:slug:" {
		t.Errorf("wrong index prefix, got: %v", idx.GetPrefix())
	}
}

func TestGetRelation(t *testing.T) {
	s, err := GetFromType[*testpb.Product]()
	if err != nil {
		t.Fatal(err)
	}

	rel, err := s.GetRelation("rank")
	if err != nil {
		t.Fatal(err)
	}

	if rel.GetName() != "rank" {
		t.Error("wrong relation name, got:", rel.GetName())
	}

	if rel.GetStorage().GetName() != "protokvtest.v1.ProductRank" {
		t.Error("wrong relation storage, got:", rel.GetStorage().GetName())
	}

	if _, err := s.GetRelation("title"); !errors.Is(err, ErrRelationNotRegistered) {
		t.Errorf("expected ErrRelationNotRegistered, got: %v", err)
	}
}

func TestAssignRelation(t *testing.T) {
	s, err := GetFromType[*testpb.Product]()
	if err != nil {
		t.Fatal(err)
	}

	rel, err := s.GetRelation("rank")
	if err != nil {
		t.Fatal(err)
	}

	bytes, err := proto.Marshal(&testpb.ProductRank{Id: "123", Rank: 666})
	if err != nil {
		t.Fatal(err)
	}

	root := &testpb.Product{Id: "123"}
	if err := rel.Assign(root, bytes); err != nil {
		t.Fatal(err)
	}

	if root.GetRank().GetRank() != 666 {
		t.Error("relation was not assigned")
	}
}

func BenchmarkGetStorage(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		GetFromType[*testpb.Product]()
	}
}

func BenchmarkToKey(b *testing.B) {
	b.ReportAllocs()

	msg := &testpb.Price{Id: "123", Region: testpb.Region_REGION_EU_VAT}
	s, err := GetFromType[*testpb.Price]()
	if err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		s.ToKey(msg)
	}
}
