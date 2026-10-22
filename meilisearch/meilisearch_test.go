package meilisearch

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pckt-sh/protokv/internal/testpb"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestParseFields(t *testing.T) {
	fields := ParseFields(GetIndex((&testpb.Product{}).ProtoReflect().Descriptor()))
	if len(fields) != 5 {
		t.Fatalf("wrong field count, got: %d", len(fields))
	}

	sku := fields[2]
	if sku.Name != "sku" || sku.JSONPath != "sku" || len(sku.Operations) != 1 || sku.Operations[0] != (Operation{"split", "/"}) {
		t.Errorf("wrong sku field, got: %+v", sku)
	}

	rank := fields[3]
	if rank.Name != "rank" || rank.JSONPath != "rank.rank" {
		t.Errorf("wrong rank field, got: %+v", rank)
	}
}

func TestDocument(t *testing.T) {
	msg := &testpb.Product{
		Id:       "1",
		Title:    "One",
		Sku:      "A1 / B2",
		Variants: []*testpb.Variant{{Tag: "x"}, {Tag: "y"}},
		Rank:     &testpb.ProductRank{Rank: 7},
	}

	doc, err := Document(msg, ParseFields(GetIndex(msg.ProtoReflect().Descriptor())), protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true})
	if err != nil {
		t.Fatal(err)
	}

	expected := map[string]any{
		"id":    "1",
		"title": "One",
		"sku":   []string{"A1", "B2", "A1 / B2"},
		"rank":  "7", // protojson encodes int64 as a string
		"tags":  []any{"x", "y"},
	}
	if !reflect.DeepEqual(doc, expected) {
		t.Errorf("wrong document\nexpected: %#v\ngot:      %#v", expected, doc)
	}
}

func TestGetIndexWithout(t *testing.T) {
	if idx := GetIndex((&testpb.ProductRank{}).ProtoReflect().Descriptor()); idx != nil {
		t.Errorf("expected no index, got: %v", idx)
	}
}

func TestRegisterOperation(t *testing.T) {
	RegisterOperation("upper", func(value any, _ string) any {
		if s, ok := value.(string); ok {
			return strings.ToUpper(s)
		}
		return value
	})

	if got := (Operation{Name: "upper"}).Apply("abc"); got != "ABC" {
		t.Errorf("wrong value, got: %v", got)
	}

	if got := (Operation{Name: "missing"}).Apply("abc"); got != "abc" {
		t.Errorf("unknown operations should be a no-op, got: %v", got)
	}
}
