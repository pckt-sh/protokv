# protokv

Describe how protobuf messages are stored in a key-value store and indexed in Meilisearch, right in your `.proto` files, then read and write them with a typed Go client.

- **Protobuf options:** [`buf.build/pckt/protokv`](https://buf.build/pckt/protokv)
- **Go SDK:** `github.com/pckt-sh/protokv`
  - `schema`: reads the options (key templates, indexes, relations, enum names)
  - `kv`: stores messages in Kvrocks, Redis or any Redis-compatible server
  - `meilisearch`: syncs stored messages into Meilisearch
- **`kvlens`:** a terminal UI to browse the stored messages

## Define your models

Add the dependency to your `buf.yaml`, then run `buf dep update`:

```yaml
version: v2
deps:
  - buf.build/pckt/protokv
```

Annotate the messages:

```protobuf
syntax = "proto3";

package example.v1;

import "protokv/v1/protokv.proto";

enum Region {
  REGION_UNSPECIFIED = 0;
  REGION_EU_VAT = 1 [(protokv.v1.value).name = "EU.VAT-REGISTERED"];
}

message Product {
  option (protokv.v1.storage) = {
    prefix: "product:{pk}"
    primaryKey: "id"
    indexPrefix: "index:product:{index}"
    indexes: ["slug"]
    relations: ["rank"]
  };

  string id = 1;
  string title = 2;
  string slug = 3;
  string sku = 4;
  ProductRank rank = 5;
}

message ProductRank {
  option (protokv.v1.storage) = {
    prefix: "product:rank:{pk}"
    primaryKey: "id"
  };

  string id = 1;
  int64 rank = 2;
}

message Price {
  option (protokv.v1.storage) = {
    prefix: "price:{region}:{pk}"
    primaryKey: "id"
  };

  string id = 1;
  Region region = 2;
  int64 amount = 3;
}
```

## Store and read messages

```go
import (
	"github.com/pckt-sh/protokv/kv"
	examplev1 "example.com/gen/example/v1"
)

client, err := kv.NewClient("redis://localhost:6666")

// Writes `product:1` and the index entry `index:product:slug:my-product`.
err = client.Proto().Set(ctx, &examplev1.Product{Id: "1", Slug: "my-product"})

product, err := client.Proto().Get[examplev1.Product](ctx, "1")
product, err = client.Proto().Get[examplev1.Product](ctx, "my-product", kv.WithIndex("slug"))

// Placeholders other than {pk} are filled from the message on write…
err = client.Proto().Set(ctx, &examplev1.Price{Id: "1", Region: examplev1.Region_REGION_EU_VAT})
// …and given as options on read: `price:EU.VAT-REGISTERED:1`.
price, err := client.Proto().Get[examplev1.Price](ctx, "1", kv.WithOption("region", "EU.VAT-REGISTERED"))

// Load many messages with their relations in one round trip.
q, err := client.Query[examplev1.Product](ctx, []string{"1", "2"}, kv.WithRelation("rank"))
products, err := q.RowsAs[examplev1.Product]()
```

`kv.New` wraps an existing `redis.UniversalClient`. The client also exposes plain key-value helpers (`Get`, `SetMany`, `ScanAll`, `DeleteMany`), sorted sets (`ZClient`, `NewZBatch`, `Proto().ZRange`) and the raw connection (`Conn`).

Index entries are only added or overwritten: when an indexed value changes, the entry for the previous value is left in place.

## Sync to Meilisearch

Search indexing is a separate option, in its own file. Add it to stored messages:

```protobuf
import "protokv/meilisearch/v1/meilisearch.proto";

message Product {
  option (protokv.v1.storage) = { ... };
  option (protokv.meilisearch.v1.index) = {
    name: "products"
    fields: ["id", "title", "sku:sku:split=/", "rank:rank.rank"]
    searchable: ["title", "sku"]
    relations: ["rank"]
  };
}
```

Then sync them:

```go
import (
	"github.com/meilisearch/meilisearch-go"
	protokvmeili "github.com/pckt-sh/protokv/meilisearch"
)

ms := meilisearch.New(url, meilisearch.WithAPIKey(key))
indexer := protokvmeili.NewIndexer(client, ms, protokvmeili.WithLogger(slog.Default()))

err := indexer.Sync(ctx, "example.v1.Product")
```

`Sync` applies the index settings, scans every stored message of that type, loads the listed `relations` and pushes the documents in batches.

Each entry of `fields` is `<name>[:<json_path>[:<op>[=<arg>]]...]`. The path is a [gjson](https://github.com/tidwall/gjson) path over the protojson encoding of the message, using proto field names. `split=<sep>` is built in. Add your own operations with:

```go
protokvmeili.RegisterOperation("trim_suffix", func(value any, arg string) any {
	if s, ok := value.(string); ok {
		return strings.TrimSuffix(s, arg)
	}
	return value
})
```

## Browse stored data

`kvlens` is a terminal UI for the stored messages. Its sidebar lists every model with a storage option. The main view lists a model's keys; open one to see the decoded record, with one tab per relation.

```sh
go install github.com/pckt-sh/protokv/cmd/kvlens@latest

buf build -o models.binpb   # in the repository of your .proto files
KV_URL=redis://localhost:6666 KVLENS_DESCRIPTORS=models.binpb kvlens
```

| Variable | Flag | |
|---|---|---|
| `KV_URL`, then `KVROCKS_URL`, `REDIS_URL` | `-url` | Database URL |
| `KVLENS_DESCRIPTORS` | `-descriptors` | Comma-separated buf images or FileDescriptorSets |

| Key | |
|---|---|
| `↑↓` `enter` | Pick a model, browse its keys, open a record |
| `/` | Filter keys by primary key glob (`abc*`), or look up an index (`slug=my-product`) |
| `o` | Set placeholder values, like `region=US` for `price:{region}:{pk}` |
| `tab` | In a record, switch between the record and its relations |
| `enter` | On a relation, navigate to it |
| `esc` | Close the record, go back |

Placeholder values are shared by every model, and default to the `options` of the models' `(protokv.meilisearch.v1.index)`. A relation's placeholders are also filled from the fields of the record that have the same name.

## Options reference

| Option | Target | Number |
|---|---|---|
| `protokv.v1.storage` | `MessageOptions` | 51642 |
| `protokv.v1.value` | `EnumValueOptions` | 51642 |
| `protokv.meilisearch.v1.index` | `MessageOptions` | 51643 |

These numbers are in the range for private use (50000-99999), they aren't in the protobuf global extension registry. Another options library using the same number on the same options type can't be imported in the same file set.

`Storage`:

| Field | Description |
|---|---|
| `primaryKey` | Field used as the primary key |
| `prefix` | Key template. `{pk}` is the primary key, other `{field}` placeholders are filled from the message (enums use their `(protokv.v1.value).name` option) |
| `indexPrefix` | Secondary index key prefix, `{index}` is the index name |
| `indexes` | Fields with a secondary index |
| `relations` | Message fields stored under their own key with the same primary key, loaded with `kv.WithRelation` |

`meilisearch.v1.Index`:

| Field | Description |
|---|---|
| `name` | Meilisearch index name |
| `fields` | Document fields, `<name>[:<json_path>[:<op>[=<arg>]]...]` |
| `searchable`, `filterable`, `sortable`, `rankingRules` | Index settings |
| `maxTotalHits`, `searchCutoffMs` | Index settings, Meilisearch defaults when unset |
| `relations` | Relations loaded before building documents |
| `options` | Values of the key placeholders, to scan messages and load relations |

## Development

```sh
buf lint
buf format -w
buf generate   # regenerates gen/go and internal/testpb
go test ./...
```

`internal/testdata/proto` holds the test fixtures. It's a second, unnamed module in the buf workspace and isn't pushed to the BSR.

CI runs buf checks and Go tests on pull requests, and pushes the module to the BSR on `main` and on `v*` tags. It needs a `BUF_TOKEN` repository secret.

## License

[MIT](LICENSE)
