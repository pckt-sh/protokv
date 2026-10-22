package kv

import (
	"context"
	"errors"

	"github.com/pckt-sh/protokv/schema"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type ProtoClient struct {
	c Client
}

// Proto returns a ProtoClient for manipulating protobuf types
func (c Client) Proto() ProtoClient {
	return ProtoClient{c}
}

// protoMessage represents a protobuf message via pointer.
// It's used to initialize and unmarshal messages without asking
// the client to initialize it first.
type protoMessage[T any] interface {
	*T
	proto.Message
}

// Get allows fetching a protobuf message from the database by primary key, or by
// indexed value with WithIndex.
func (p ProtoClient) Get[T any, PM protoMessage[T]](ctx context.Context, key string, opts ...protoOptFn) (PM, error) {
	storage, err := schema.GetFromType[PM]()
	if err != nil {
		return nil, err
	}

	opt := newProtoOption(opts)
	if opt.useIndex {
		keys, err := storage.ResolveIndexes(ctx, p.c.conn, opt.indexKeys, []string{key})
		if err != nil {
			return nil, err
		}

		if len(keys) == 0 {
			return nil, ErrNotFound
		}

		key = keys[0]

		// Resolved keys are primary keys, they always need the prefix.
		opt.writePrefix = true
	}

	if opt.writePrefix {
		key = storage.GetKey(key, opt.options)
	}

	bytes, err := p.c.conn.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	msg := PM(new(T))
	if err := proto.Unmarshal(bytes, msg); err != nil {
		return nil, err
	}

	return msg, nil
}

// GetMany allows fetching multiple protobuf messages at once from the database.
//
// Keys are primary keys, or indexed values with WithIndex, like Get. With
// WithOriginalKeys they are full storage keys. Keys that don't exist are skipped.
func (p ProtoClient) GetMany[T any, PM protoMessage[T]](ctx context.Context, keys []string, opts ...protoOptFn) ([]PM, error) {
	storage, err := schema.GetFromType[PM]()
	if err != nil {
		return nil, err
	}

	opt := newProtoOption(opts)
	if opt.useIndex {
		keys, err = storage.ResolveIndexes(ctx, p.c.conn, opt.indexKeys, keys)
		if err != nil {
			return nil, err
		}

		// Resolved keys are primary keys, they always need the prefix.
		opt.writePrefix = true
	}

	if opt.writePrefix {
		keys = storage.GetKeys(keys, opt.options)
	}

	stored, err := p.c.GetMany(ctx, keys...)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return UnmarshalMany[T, PM](stored)
}

// GetPaginatedMsg returns a list of message with a cursor
func (p ProtoClient) Paginate[T any, PM protoMessage[T]](ctx context.Context, cursor, count uint64, opts ...protoOptFn) ([]PM, uint64, error) {
	storage, err := schema.GetFromType[PM]()
	if err != nil {
		return nil, 0, err
	}

	opt := newProtoOption(opts)
	keys, cursor, err := p.c.Scan(ctx, storage.GetKey("*", opt.options), count, cursor)
	if err != nil {
		return nil, 0, err
	}

	msgs, err := p.GetMany[T, PM](ctx, keys, WithOriginalKeys())
	if err != nil {
		return nil, 0, err
	}

	return msgs, cursor, nil
}

// Set saves a protobuf message into the database, along with its indexes.
func (p ProtoClient) Set(ctx context.Context, msg proto.Message) error {
	return p.write(ctx, []proto.Message{msg})
}

// SetMany saves multiple messages into the database, along with their indexes.
func (p ProtoClient) SetMany[T proto.Message](ctx context.Context, msgs []T) error {
	entries := make([]proto.Message, len(msgs))
	for i, msg := range msgs {
		entries[i] = msg
	}

	return p.write(ctx, entries)
}

// write stores messages, and for each of them one key per index that has a value.
//
// Indexes are only ever added or overwritten: when an indexed value changes,
// the entry for the previous value is left behind. Finding it would mean
// reading back every message being overwritten.
func (p ProtoClient) write(ctx context.Context, msgs []proto.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	_, err := p.c.conn.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, msg := range msgs {
			storage, err := schema.GetFromMsg(msg)
			if err != nil {
				return err
			}

			bytes, err := proto.Marshal(msg)
			if err != nil {
				return err
			}

			pipe.Set(ctx, storage.ToKey(msg), bytes, 0)

			if err := storage.SetIndexes(ctx, pipe, msg); err != nil {
				return err
			}
		}

		return nil
	})

	return err
}

func (p ProtoClient) ZRange[T any, PM protoMessage[T]](ctx context.Context, key string, opts ...ZOption) ([]PM, error) {
	results, err := p.c.ZClient().Range(ctx, key, opts...)
	if err != nil {
		return nil, err
	}

	return UnmarshalMany[T, PM](results)
}

// ZRangeByScoreMany pipelines a ZRangeByScore of [min, max], inclusive,
// across multiple ZSETs in chunked round trips, unmarshaling each set's
// members as T. Keyed by the original set key.
func (p ProtoClient) ZRangeByScoreMany[T any, PM protoMessage[T]](ctx context.Context, keys []string, min, max int64) (map[string][]PM, error) {
	raw, err := p.c.ZClient().RangeByScoreMany(ctx, keys, min, max)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]PM, len(raw))
	for _, r := range raw {
		msgs, err := UnmarshalMany[T, PM](r.Members)
		if err != nil {
			return nil, err
		}
		result[r.Key] = msgs
	}

	return result, nil
}

// UnmarshalMany is a helper to unmarshal multiple proto.Message
func UnmarshalMany[T any, PM protoMessage[T]](raw []string) ([]PM, error) {
	msgs := make([]PM, 0, len(raw))
	for _, r := range raw {
		msg := PM(new(T))
		if err := proto.Unmarshal([]byte(r), msg); err != nil {
			return nil, err
		}

		msgs = append(msgs, msg)
	}

	return msgs, nil
}

// ----
// Proto Options
// ----

// protoOption defines a list of usable option for querying
type protoOption struct {
	useIndex    bool
	indexKeys   []string
	options     map[string]string
	writePrefix bool
}

func newProtoOption(opts []protoOptFn) protoOption {
	opt := protoOption{
		writePrefix: true,
		options:     map[string]string{},
	}
	for _, o := range opts {
		o(&opt)
	}

	return opt
}

// protoOptFn defines how a option should be implemented
type protoOptFn func(*protoOption)

// WithIndex allows making a lookup on an index
// before making the real request.
func WithIndex(keys ...string) protoOptFn {
	return func(po *protoOption) {
		po.useIndex = true
		po.indexKeys = keys
	}
}

// WithOption adds a placeholder replacement
func WithOption(key, value string) protoOptFn {
	return func(po *protoOption) {
		po.options[key] = value
	}
}

// WithOriginalKeys makes the request using directly
// passed keys in arguments, without appending prefix.
func WithOriginalKeys() protoOptFn {
	return func(po *protoOption) {
		po.writePrefix = false
	}
}
