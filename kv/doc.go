// Package kv stores protobuf messages in a Redis-compatible key-value
// store (Kvrocks, Redis, Valkey, ...) following their protokv.v1
// options.
//
//	client, err := kv.NewClient("redis://localhost:6666")
//	err = client.Proto().Set(ctx, product)
//	product, err := client.Proto().Get[examplev1.Product](ctx, "slug-123", kv.WithIndex("slug"))
package kv
