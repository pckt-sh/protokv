package kv

import (
	"context"
	"errors"

	"github.com/pckt-sh/protokv/codec"
	"github.com/pckt-sh/protokv/schema"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type Query struct {
	ctx      context.Context
	template proto.Message
	storage  schema.Storage
	rows     []QueryRecord
	cursor   int
	meta     codec.Meta
	relMeta  map[string]codec.Meta
}

type QueryRecord struct {
	cmd       redis.Cmder
	relations []QueryRelation
}

type QueryRelation struct {
	name string
	cmd  redis.Cmder
}

// NewQuery allows querying dynamically with relations
func (c Client) NewQuery[T KeyValue](ctx context.Context, msg proto.Message, keys []T, opts ...QueryOptFn) (*Query, error) {
	storage, err := schema.GetFromMsg(msg)
	if err != nil {
		return nil, err
	}

	dq := &Query{
		ctx:      ctx,
		template: msg,
		storage:  storage,
		rows:     make([]QueryRecord, 0, len(keys)),
		cursor:   -1,
	}

	options := QueryOption{[]string{}, map[string]string{}}
	for _, opt := range opts {
		opt(&options)
	}

	pipeline := c.conn.Pipeline()
	for _, key := range keys {
		cmd := pipeline.Get(ctx, storage.GetKey(formatString(key), options.options))

		var records []QueryRelation
		for _, relName := range options.relations {
			rel, err := storage.GetRelation(relName)
			if err != nil {
				return nil, err
			}

			records = append(records, QueryRelation{
				name: relName,
				cmd:  pipeline.Get(ctx, rel.GetKey(formatString(key), options.options)),
			})
		}

		dq.rows = append(dq.rows, QueryRecord{
			cmd:       cmd,
			relations: records,
		})
	}

	_, err = pipeline.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	return dq, nil
}

// Query initialize a new query via generics
func (c Client) Query[T any, PM protoMessage[T], K KeyValue](ctx context.Context, keys []K, opts ...QueryOptFn) (*Query, error) {
	msg := PM(new(T))

	return c.NewQuery(ctx, msg, keys, opts...)
}

// Next allows advancing into the results
func (dq *Query) Next() bool {
	if dq.cursor+1 >= len(dq.rows) {
		return false
	}

	dq.cursor++
	return true
}

// decode parses the proto.Message
func (dq *Query) decode(msg proto.Message) error {
	dq.meta = codec.Meta{}
	clear(dq.relMeta)

	idx := max(dq.cursor, 0)
	if idx >= len(dq.rows) {
		return ErrNotFound
	}

	b, err := dq.rows[idx].cmd.(*redis.StringCmd).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return ErrNotFound
		}

		return err
	}

	meta, err := codec.Decode(b, msg)
	if err != nil {
		return err
	}
	dq.meta = meta

	for _, record := range dq.rows[idx].relations {
		rel, err := dq.storage.GetRelation(record.name)
		if err != nil {
			return err
		}

		b, _ := record.cmd.(*redis.StringCmd).Bytes() // err results in empty byte slice
		meta, err := rel.AssignWithMeta(msg, b)
		if err != nil {
			return err
		}

		if len(b) > 0 {
			if dq.relMeta == nil {
				dq.relMeta = make(map[string]codec.Meta, len(dq.rows[idx].relations))
			}
			dq.relMeta[record.name] = meta
		}
	}

	return nil
}

// Meta returns the metadata of the row last loaded by Get or Scan.
func (dq *Query) Meta() codec.Meta {
	return dq.meta
}

// RelationMeta returns the metadata of a relation of the row last loaded by
// Get or Scan. ok is false when the relation wasn't requested with
// WithRelation, or has no stored value.
func (dq *Query) RelationMeta(name string) (meta codec.Meta, ok bool) {
	meta, ok = dq.relMeta[name]
	return meta, ok
}

// Get loads a obscure `proto.Message`.
// It can be casted later.
func (dq *Query) Get() (proto.Message, error) {
	msg := dq.template.ProtoReflect().New().Interface()
	err := dq.decode(msg)

	return msg, err
}

// Scan allows scanning a message based on its generic type
func (dq *Query) Scan[T any, PM protoMessage[T]]() (PM, error) {
	msg := PM(new(T))
	err := dq.decode(msg)

	return msg, err
}

// Rows retrieve all rows directly as proto.Message
func (dq *Query) Rows() ([]proto.Message, error) {
	rows := make([]proto.Message, 0, len(dq.rows))
	for dq.Next() {
		msg, err := dq.Get()
		if err != nil {
			return nil, err
		}

		rows = append(rows, msg)
	}

	return rows, nil
}

// RowsAs retrieves all rows casted as T
func (dq *Query) RowsAs[T any, PM protoMessage[T]]() ([]PM, error) {
	rows := make([]PM, 0, len(dq.rows))
	for dq.Next() {
		msg, err := dq.Scan[T, PM]()
		if err != nil {
			return nil, err
		}

		rows = append(rows, msg)
	}

	return rows, nil
}

// ------------
// Options
// ------------

type QueryOption struct {
	relations []string
	options   map[string]string
}

type QueryOptFn func(*QueryOption)

func WithRelation(name ...string) QueryOptFn {
	return func(qo *QueryOption) {
		qo.relations = name
	}
}

func WithQueryOption(k, v string) QueryOptFn {
	return func(qo *QueryOption) {
		qo.options[k] = v
	}
}

func WithQueryOptions(m map[string]string) QueryOptFn {
	return func(qo *QueryOption) {
		qo.options = m
	}
}
