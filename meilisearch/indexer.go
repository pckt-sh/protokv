package meilisearch

import (
	"context"
	"errors"
	"log/slog"
	"time"

	ms "github.com/meilisearch/meilisearch-go"
	"github.com/pckt-sh/protokv/kv"
	"github.com/pckt-sh/protokv/schema"
	"github.com/tidwall/gjson"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var ErrNotIndexed = errors.New("message has no (protokv.meilisearch.v1.index) option")

// Indexer pushes stored messages into Meilisearch
type Indexer struct {
	db        kv.Client
	client    ms.ServiceManager
	batchSize uint64
	interval  time.Duration
	logger    *slog.Logger
}

type Option func(*Indexer)

// WithBatchSize sets how many messages are read and pushed at once, default 1,000
func WithBatchSize(n uint64) Option {
	return func(i *Indexer) {
		i.batchSize = n
	}
}

// WithPollInterval sets how often Meilisearch tasks are polled, default 1s
func WithPollInterval(d time.Duration) Option {
	return func(i *Indexer) {
		i.interval = d
	}
}

// WithLogger logs each pushed batch, nothing is logged by default
func WithLogger(l *slog.Logger) Option {
	return func(i *Indexer) {
		i.logger = l
	}
}

func NewIndexer(db kv.Client, client ms.ServiceManager, opts ...Option) *Indexer {
	i := &Indexer{
		db:        db,
		client:    client,
		batchSize: 1_000,
		interval:  time.Second,
		logger:    slog.New(slog.DiscardHandler),
	}
	for _, opt := range opts {
		opt(i)
	}

	return i
}

// Sync applies the index settings of a message, then pushes every stored
// message of that type. It returns once Meilisearch processed all documents.
func (i *Indexer) Sync(ctx context.Context, name protoreflect.FullName) error {
	s, err := schema.Get(name)
	if err != nil {
		return err
	}

	idx := GetIndex(s.GetDescriptor())
	if idx == nil {
		return ErrNotIndexed
	}

	index := i.client.Index(idx.Name)

	settings := &ms.Settings{
		SearchCutoffMs:       idx.SearchCutoffMs,
		RankingRules:         idx.RankingRules,
		SearchableAttributes: idx.Searchable,
		FilterableAttributes: idx.Filterable,
		SortableAttributes:   idx.Sortable,
	}
	if idx.MaxTotalHits > 0 {
		settings.Pagination = &ms.Pagination{MaxTotalHits: idx.MaxTotalHits}
	}

	task, err := index.UpdateSettingsWithContext(ctx, settings)
	if err != nil {
		return err
	}

	if _, err := index.WaitForTaskWithContext(ctx, task.TaskUID, i.interval); err != nil {
		return err
	}

	var (
		template    = dynamicpb.NewMessage(s.GetDescriptor())
		fields      = ParseFields(idx)
		keys        []string
		cursor      uint64
		marshalOpts = protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}
		taskId      int64
		pushed      bool
	)

	for {
		t := time.Now()
		keys, cursor, err = i.db.Scan(ctx, s.GetKey("*", idx.Options), i.batchSize, cursor)
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			query, err := i.db.NewQuery(
				ctx,
				template,
				s.TrimPrefixes(keys),
				kv.WithRelation(idx.Relations...),
				kv.WithQueryOptions(idx.Options),
			)
			if err != nil {
				return err
			}

			batch := make([]map[string]any, 0, len(keys))
			for query.Next() {
				msg, err := query.Get()
				if err != nil {
					return err
				}

				doc, err := Document(msg, fields, marshalOpts)
				if err != nil {
					return err
				}

				batch = append(batch, doc)
			}

			task, err := index.AddDocumentsWithContext(ctx, batch, nil)
			if err != nil {
				return err
			}

			taskId = task.TaskUID
			pushed = true
			i.logger.InfoContext(ctx, "pushed meilisearch batch", "duration", time.Since(t).String(), "model", string(name), "documents", len(batch))
		}

		if cursor == 0 {
			break
		}
	}

	if !pushed {
		return nil
	}

	_, err = index.WaitForTaskWithContext(ctx, taskId, i.interval)
	return err
}

// Document builds the Meilisearch document of a message
func Document(msg proto.Message, fields []Field, opts protojson.MarshalOptions) (map[string]any, error) {
	b, err := opts.Marshal(msg)
	if err != nil {
		return nil, err
	}

	doc := make(map[string]any, len(fields))
	gj := gjson.ParseBytes(b)
	for _, field := range fields {
		v := gj.Get(field.JSONPath).Value()

		for _, op := range field.Operations {
			v = op.Apply(v)
		}

		doc[field.Name] = v
	}

	return doc, nil
}
