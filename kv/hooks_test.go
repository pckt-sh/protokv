package kv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/pckt-sh/protokv/codec"
	"github.com/pckt-sh/protokv/internal/testpb"
	"github.com/redis/go-redis/v9"
)

// recorder is a hook keeping the ops it saw.
type recorder struct {
	before, after [][]WriteOp
}

func (r *recorder) hook(previous bool) WriteHook {
	return WriteHook{
		Previous: previous,
		Before: func(_ context.Context, _ redis.Pipeliner, ops []WriteOp) error {
			r.before = append(r.before, append([]WriteOp{}, ops...))
			return nil
		},
		After: func(_ context.Context, ops []WriteOp) error {
			r.after = append(r.after, append([]WriteOp{}, ops...))
			return nil
		},
	}
}

func (r *recorder) last(t *testing.T) WriteOp {
	t.Helper()

	if len(r.after) == 0 || len(r.after[len(r.after)-1]) != 1 {
		t.Fatalf("expected one op, got: %+v", r.after)
	}

	return r.after[len(r.after)-1][0]
}

func TestWriteHookChanges(t *testing.T) {
	client := newTestClient(t)
	rec := &recorder{}
	p := client.Proto().With(WithWriteHook(rec.hook(true)))

	product := &testpb.Product{Id: "1", Title: "One"}

	// New key.
	if err := p.Set(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	first := rec.last(t)
	if first.Old != nil || !first.Changed() || first.Key != "product:1" {
		t.Errorf("new key: %+v", first)
	}

	// Same message.
	if err := p.Set(t.Context(), product); err != nil {
		t.Fatal(err)
	}
	same := rec.last(t)
	if same.Old == nil || same.Old.Hash != first.New.Hash || same.Changed() || same.Skipped {
		t.Errorf("unchanged: %+v", same)
	}

	// Modified message.
	if err := p.Set(t.Context(), &testpb.Product{Id: "1", Title: "Two"}); err != nil {
		t.Fatal(err)
	}
	modified := rec.last(t)
	if modified.Old == nil || modified.Old.Hash != first.New.Hash || !modified.Changed() {
		t.Errorf("modified: %+v", modified)
	}

	if len(rec.before) != 3 {
		t.Errorf("Before ran %d times", len(rec.before))
	}
}

func TestWriteHookWithoutPrevious(t *testing.T) {
	client := newTestClient(t)
	rec := &recorder{}

	for range 2 {
		if err := client.Proto().Set(t.Context(), &testpb.Product{Id: "1"}, WithWriteHook(rec.hook(false))); err != nil {
			t.Fatal(err)
		}
	}

	if op := rec.last(t); op.Old != nil {
		t.Errorf("Old loaded without Previous: %+v", op)
	}
}

func TestSkipUnchanged(t *testing.T) {
	client := newTestClient(t)
	rec := &recorder{}
	p := client.Proto().With(WithSkipUnchanged(), WithWriteHook(rec.hook(false)))

	if err := p.Set(t.Context(), &testpb.Product{Id: "1", Title: "One", Slug: "one"}); err != nil {
		t.Fatal(err)
	}
	_, written, err := client.Proto().GetWithMeta[testpb.Product](t.Context(), "1")
	if err != nil {
		t.Fatal(err)
	}

	// Remove the index: a skipped write must not recreate it.
	if err := client.Conn().Del(t.Context(), "index:product:slug:one").Err(); err != nil {
		t.Fatal(err)
	}

	if err := p.Set(t.Context(), &testpb.Product{Id: "1", Title: "One", Slug: "one"}); err != nil {
		t.Fatal(err)
	}
	if op := rec.last(t); !op.Skipped || op.Changed() {
		t.Errorf("expected skipped op: %+v", op)
	}

	_, meta, err := client.Proto().GetWithMeta[testpb.Product](t.Context(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if !meta.Timestamp.Equal(written.Timestamp) {
		t.Errorf("skipped write updated the timestamp: %v -> %v", written.Timestamp, meta.Timestamp)
	}
	if n, _ := client.Conn().Exists(t.Context(), "index:product:slug:one").Result(); n != 0 {
		t.Error("skipped write recreated its index")
	}

	// A changed message is written.
	if err := p.Set(t.Context(), &testpb.Product{Id: "1", Title: "Two", Slug: "one"}); err != nil {
		t.Fatal(err)
	}
	if op := rec.last(t); op.Skipped {
		t.Errorf("changed op skipped: %+v", op)
	}
	product, err := client.Proto().Get[testpb.Product](t.Context(), "1")
	if err != nil || product.Title != "Two" {
		t.Errorf("changed write not stored: %v, %v", product, err)
	}
}

func TestSkipUnchangedLegacy(t *testing.T) {
	client := newTestClient(t)
	product := &testpb.Product{Id: "1", Title: "Legacy"}
	setLegacy(t, client, "product:1", product)

	rec := &recorder{}
	if err := client.Proto().Set(t.Context(), product, WithSkipUnchanged(), WithWriteHook(rec.hook(false))); err != nil {
		t.Fatal(err)
	}

	if op := rec.last(t); op.Old != nil || op.Skipped {
		t.Errorf("legacy value must be rewritten: %+v", op)
	}

	_, meta, err := client.Proto().GetWithMeta[testpb.Product](t.Context(), "1")
	if err != nil || meta.Version != codec.V1 {
		t.Errorf("legacy value not migrated: %+v, %v", meta, err)
	}
}

func TestWriteHookWrongType(t *testing.T) {
	client := newTestClient(t)
	if err := client.Conn().LPush(t.Context(), "product:1", "x").Err(); err != nil {
		t.Fatal(err)
	}

	if err := client.Proto().Set(t.Context(), &testpb.Product{Id: "1"}, WithSkipUnchanged()); err != nil {
		t.Fatal(err)
	}

	if _, err := client.Proto().Get[testpb.Product](t.Context(), "1"); err != nil {
		t.Error(err)
	}
}

func TestWriteHookPipeline(t *testing.T) {
	client := newTestClient(t)

	track := WriteHook{
		Previous: true,
		Before: func(ctx context.Context, pipe redis.Pipeliner, ops []WriteOp) error {
			for _, op := range ops {
				if p, ok := op.Msg.(*testpb.Product); ok && op.Changed() {
					pipe.ZAdd(ctx, "changed:products", redis.Z{Score: float64(op.New.Timestamp.Unix()), Member: p.Id})
				}
			}
			return nil
		},
	}
	p := client.Proto().With(WithSkipUnchanged(), WithWriteHook(track))

	batch := []*testpb.Product{{Id: "1", Title: "One"}, {Id: "2", Title: "Two"}}
	if err := p.SetMany(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	if err := client.Conn().Del(t.Context(), "changed:products").Err(); err != nil {
		t.Fatal(err)
	}

	batch[1] = &testpb.Product{Id: "2", Title: "Two, changed"}
	if err := p.SetMany(t.Context(), batch); err != nil {
		t.Fatal(err)
	}

	changed, err := client.Conn().ZRange(t.Context(), "changed:products", 0, -1).Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0] != "2" {
		t.Errorf("wrong changed products: %v", changed)
	}
}

func TestWriteHookBeforeError(t *testing.T) {
	client := newTestClient(t)
	errHook := errors.New("hook failed")

	afterCalled := false
	hook := WriteHook{
		Before: func(context.Context, redis.Pipeliner, []WriteOp) error { return errHook },
		After: func(context.Context, []WriteOp) error {
			afterCalled = true
			return nil
		},
	}

	err := client.Proto().Set(t.Context(), &testpb.Product{Id: "1"}, WithWriteHook(hook))
	if !errors.Is(err, errHook) {
		t.Errorf("expected hook error, got: %v", err)
	}
	if afterCalled {
		t.Error("After ran although Before failed")
	}
	if _, err := client.Proto().Get[testpb.Product](t.Context(), "1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("message written although Before failed: %v", err)
	}
}

func TestWithScoping(t *testing.T) {
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rc.Close() })
	client := New(rc)

	wrapped := &recorder{}
	perCall := &recorder{}
	base := client.Proto()
	p := base.With(WithWriteHook(wrapped.hook(false)))

	if err := p.Set(t.Context(), &testpb.Product{Id: "1"}, WithWriteHook(perCall.hook(false))); err != nil {
		t.Fatal(err)
	}
	if len(wrapped.after) != 1 || len(perCall.after) != 1 {
		t.Errorf("hooks not both called: %d, %d", len(wrapped.after), len(perCall.after))
	}

	// The base client has no hooks, and no previous read.
	before := mr.CommandCount()
	if err := base.Set(t.Context(), &testpb.Item{Id: 1}); err != nil {
		t.Fatal(err)
	}
	if len(wrapped.after) != 1 {
		t.Error("base client ran the wrapped hook")
	}
	if n := mr.CommandCount() - before; n != 1 {
		t.Errorf("plain write sent %d commands, expected a single SET", n)
	}
}

// benchClient targets KV_BENCH_URL when set (e.g. a local KVrocks),
// miniredis otherwise.
func benchClient(b *testing.B) Client {
	url := os.Getenv("KV_BENCH_URL")
	if url == "" {
		return newTestClient(b)
	}

	client, err := NewClient(url)
	if err != nil {
		b.Fatal(err)
	}
	if err := client.Conn().FlushDB(b.Context()).Err(); err != nil {
		b.Fatal(err)
	}

	return client
}

func benchmarkProducts(n int, title string) []*testpb.Product {
	products := make([]*testpb.Product, n)
	for i := range products {
		products[i] = &testpb.Product{
			Id:       fmt.Sprintf("p%d", i),
			Title:    title,
			Slug:     fmt.Sprintf("slug-%d", i),
			Sku:      "SKU/123/456",
			Variants: []*testpb.Variant{{Tag: "red"}, {Tag: "blue"}},
		}
	}

	return products
}

func BenchmarkSetMany(b *testing.B) {
	products := benchmarkProducts(1000, "A product title")
	noop := WriteHook{Previous: true, Before: func(context.Context, redis.Pipeliner, []WriteOp) error { return nil }}

	cases := map[string][]WriteOption{
		"plain":          nil,
		"previous":       {WithWriteHook(noop)},
		"skip_unchanged": {WithSkipUnchanged()},
		"skip_and_hook":  {WithSkipUnchanged(), WithWriteHook(noop)},
	}

	for name, opts := range cases {
		b.Run(name, func(b *testing.B) {
			client := benchClient(b)
			p := client.Proto().With(opts...)
			if err := p.SetMany(b.Context(), products); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			for b.Loop() {
				if err := p.SetMany(b.Context(), products); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
