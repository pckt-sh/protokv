package kv

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/pckt-sh/protokv/codec"
	"github.com/pckt-sh/protokv/internal/testpb"
	"github.com/pckt-sh/protokv/schema"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type Tester interface {
	miniredis.Tester
	Helper()
}

// newTestClient returns a Client backed by a throwaway in-memory server.
func newTestClient(tester Tester) Client {
	tester.Helper()

	mr := miniredis.RunT(tester)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	tester.Cleanup(func() { rc.Close() })

	return New(rc)
}

func TestSetMany(t *testing.T) {
	c := newTestClient(t)

	if err := c.SetMany(t.Context(), "key:1", "value:1", "key:2", "value:2"); err != nil {
		t.Fatal(err)
	}

	if v, _ := c.Get(t.Context(), "key:2"); v != "value:2" {
		t.Errorf("key:2 is not equal to value:2")
	}

	if _, err := c.Get(t.Context(), "key:3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestScanAll(t *testing.T) {
	c := newTestClient(t)

	for i := range 12_000 {
		if err := c.Set(t.Context(), fmt.Sprintf("key:%d", i), "v"); err != nil {
			t.Fatal(err)
		}
	}

	keys, err := c.ScanAll(t.Context(), "key:*")
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != 12_000 {
		t.Errorf("wrong key count, got: %d", len(keys))
	}
}

func TestSetGet(t *testing.T) {
	client := newTestClient(t)

	if err := client.Proto().Set(t.Context(), &testpb.Product{Id: "123", Title: "666", Slug: "slug-987"}); err != nil {
		t.Fatal(err)
	}

	product, err := client.Proto().Get[testpb.Product](t.Context(), "123")
	if err != nil {
		t.Fatal(err)
	}
	if product.Title != "666" {
		t.Errorf("wrong title, expected: 666; got: %v", product.Title)
	}

	product, err = client.Proto().Get[testpb.Product](t.Context(), "slug-987", WithIndex("slug"))
	if err != nil {
		t.Fatal(err)
	}
	if product.Id != "123" {
		t.Errorf("wrong id, expected: 123; got: %v", product.Id)
	}
}

func TestGetByIndexMiss(t *testing.T) {
	client := newTestClient(t)

	// A product whose id looks like the missing slug must not be returned.
	if err := client.Proto().Set(t.Context(), &testpb.Product{Id: "unknown"}); err != nil {
		t.Fatal(err)
	}

	_, err := client.Proto().Get[testpb.Product](t.Context(), "unknown", WithIndex("slug"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestGetByUnknownIndex(t *testing.T) {
	client := newTestClient(t)

	_, err := client.Proto().Get[testpb.Product](t.Context(), "slug", WithIndex("unknown"))
	if !errors.Is(err, schema.ErrNoIndex) {
		t.Errorf("expected ErrNoIndex, got: %v", err)
	}
}

func TestSetGetOptions(t *testing.T) {
	client := newTestClient(t)

	msg := &testpb.Price{Id: "123", Region: testpb.Region_REGION_US, Amount: 999}
	if err := client.Proto().Set(t.Context(), msg); err != nil {
		t.Fatal(err)
	}

	price, err := client.Proto().Get[testpb.Price](t.Context(), "123", WithOption("region", "US"))
	if err != nil {
		t.Fatal(err)
	}

	if price.Amount != 999 {
		t.Errorf("wrong amount, expected 999; got %v", price.Amount)
	}
}

func TestPaginate(t *testing.T) {
	client := newTestClient(t)

	msgs := []*testpb.Product{
		{Id: "p-123", Title: "123-title"},
		{Id: "p-234", Title: "234-title"},
		{Id: "p-345", Title: "345-title"},
	}
	if err := client.Proto().SetMany(t.Context(), msgs); err != nil {
		t.Fatal(err)
	}

	products, _, err := client.Proto().Paginate[testpb.Product](t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	if len(products) != 3 {
		t.Error("wrong result count, got:", len(products))
	}
}

func TestQueryRelations(t *testing.T) {
	client := newTestClient(t)

	if err := client.Proto().Set(t.Context(), &testpb.Product{Id: "1", Title: "One"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Proto().Set(t.Context(), &testpb.ProductRank{Id: "1", Rank: 42}); err != nil {
		t.Fatal(err)
	}

	q, err := client.Query[testpb.Product](t.Context(), []string{"1"}, WithRelation("rank"))
	if err != nil {
		t.Fatal(err)
	}

	rows, err := q.RowsAs[testpb.Product]()
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 || rows[0].GetRank().GetRank() != 42 {
		t.Errorf("relation was not loaded, got: %v", rows)
	}

	meta, ok := q.RelationMeta("rank")
	if !ok || meta.Version != codec.V1 || meta.Hash == 0 || meta.Timestamp.IsZero() {
		t.Errorf("wrong relation meta: %+v, %v", meta, ok)
	}
	if _, ok := q.RelationMeta("price"); ok {
		t.Error("meta for a relation that wasn't requested")
	}
}

func TestQueryNumericKeys(t *testing.T) {
	client := newTestClient(t)

	if err := client.Proto().Set(t.Context(), &testpb.Item{Id: 123, Title: "Example 123"}); err != nil {
		t.Fatal(err)
	}

	q, err := client.Query[testpb.Item](t.Context(), []float64{123})
	if err != nil {
		t.Fatal(err)
	}

	item, err := q.Scan[testpb.Item]()
	if err != nil {
		t.Fatal(err)
	}

	if item.Title != "Example 123" {
		t.Error("wrong item received")
	}
}

func TestZBatch(t *testing.T) {
	client := newTestClient(t)

	zb := client.NewZBatch()
	zb.Add("ranks", 2, &testpb.ProductRank{Id: "b", Rank: 2})
	zb.Add("ranks", 1, &testpb.ProductRank{Id: "a", Rank: 1})
	if err := zb.Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	ranks, err := client.Proto().ZRange[testpb.ProductRank](t.Context(), "ranks", WithReverse(), WithMinimum("(+inf"), WithMaximum("(-inf"))
	if err != nil {
		t.Fatal(err)
	}

	if len(ranks) != 2 || ranks[0].Id != "b" {
		t.Errorf("wrong ranks, got: %v", ranks)
	}

	max, err := client.ZClient().Max(t.Context(), "ranks")
	if err != nil {
		t.Fatal(err)
	}
	if max != 2 {
		t.Errorf("wrong max, got: %v", max)
	}
}

// setLegacy stores msg the way values were written before the codec: raw
// protobuf bytes, without header.
func setLegacy(t *testing.T, c Client, key string, msg proto.Message) {
	t.Helper()

	b, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Conn().Set(t.Context(), key, b, 0).Err(); err != nil {
		t.Fatal(err)
	}
}

func TestReadLegacyValues(t *testing.T) {
	client := newTestClient(t)

	setLegacy(t, client, "product:1", &testpb.Product{Id: "1", Title: "Legacy"})
	setLegacy(t, client, "product:rank:1", &testpb.ProductRank{Id: "1", Rank: 7})
	if err := client.Proto().Set(t.Context(), &testpb.Product{Id: "2", Title: "New"}); err != nil {
		t.Fatal(err)
	}

	product, meta, err := client.Proto().GetWithMeta[testpb.Product](t.Context(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if product.Title != "Legacy" || meta.Version != codec.V0 || !meta.Timestamp.IsZero() {
		t.Errorf("wrong legacy read: %v, %+v", product, meta)
	}

	entries, err := client.Proto().GetManyWithMeta[testpb.Product](t.Context(), []string{"1", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Meta.Version != codec.V0 || entries[1].Meta.Version != codec.V1 {
		t.Errorf("wrong versions: %+v", entries)
	}

	q, err := client.Query[testpb.Product](t.Context(), []string{"1", "2"}, WithRelation("rank"))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; q.Next(); i++ {
		row, err := q.Scan[testpb.Product]()
		if err != nil {
			t.Fatal(err)
		}

		relMeta, relOK := q.RelationMeta("rank")

		switch i {
		case 0:
			if row.GetRank().GetRank() != 7 || q.Meta().Version != codec.V0 {
				t.Errorf("wrong legacy row: %v, %+v", row, q.Meta())
			}
			if !relOK || relMeta.Version != codec.V0 {
				t.Errorf("wrong legacy relation meta: %+v, %v", relMeta, relOK)
			}
		case 1:
			if row.Title != "New" || q.Meta().Version != codec.V1 {
				t.Errorf("wrong new row: %v, %+v", row, q.Meta())
			}
			// Product 2 has no rank stored: the previous row's meta must not leak.
			if relOK {
				t.Errorf("missing relation has meta: %+v", relMeta)
			}
		}
	}
}

func TestGetWithMeta(t *testing.T) {
	client := newTestClient(t)
	before := time.Now()

	if err := client.Proto().Set(t.Context(), &testpb.Product{Id: "1", Title: "One"}); err != nil {
		t.Fatal(err)
	}

	product, meta, err := client.Proto().GetWithMeta[testpb.Product](t.Context(), "1")
	if err != nil {
		t.Fatal(err)
	}

	if product.Title != "One" {
		t.Errorf("wrong title: %v", product.Title)
	}
	if meta.Version != codec.V1 || meta.Hash == 0 {
		t.Errorf("wrong meta: %+v", meta)
	}
	if meta.Timestamp.Before(before) || meta.Timestamp.After(time.Now()) {
		t.Errorf("timestamp out of range: %v", meta.Timestamp)
	}

	raw, err := client.Get(t.Context(), "product:1")
	if err != nil {
		t.Fatal(err)
	}
	if raw[0] != codec.Magic {
		t.Errorf("value was not written with a header: %x", raw)
	}
}

func TestZBatchStaysLegacy(t *testing.T) {
	client := newTestClient(t)

	zb := client.NewZBatch()
	zb.Add("ranks", 1, &testpb.ProductRank{Id: "a", Rank: 1})
	zb.Add("ranks", 2, &testpb.ProductRank{Id: "a", Rank: 1})
	if err := zb.Exec(t.Context()); err != nil {
		t.Fatal(err)
	}

	members, err := client.ZClient().Range(t.Context(), "ranks")
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0][0] == codec.Magic {
		t.Errorf("expected one raw member, got: %x", members)
	}
}

func BenchmarkGet(b *testing.B) {
	client := newTestClient(b)
	if err := client.Proto().Set(b.Context(), &testpb.Product{Id: "555", Title: "Example"}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := client.Proto().Get[testpb.Product](b.Context(), "555"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSet(b *testing.B) {
	client := newTestClient(b)
	msg := &testpb.Product{Id: "666", Title: "Example 2"}

	b.ReportAllocs()
	for b.Loop() {
		if err := client.Proto().Set(b.Context(), msg); err != nil {
			b.Fatal(err)
		}
	}
}
