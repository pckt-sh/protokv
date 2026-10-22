package schema

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Index struct {
	// The index column name (ex: `slug`)
	name string

	// The field where's the index can be found
	field protoreflect.FieldDescriptor

	// The index prefix (ex: `index:product:slug:`)
	prefix string
}

// GetName returns the indexed field name
func (i Index) GetName() string {
	return i.name
}

// GetPrefix returns the key prefix of the index entries
func (i Index) GetPrefix() string {
	return i.prefix
}

// indexes is the index cache
var indexes sync.Map

// Errors
var (
	ErrNoIndex = errors.New("index not found")
)

// GetIndex parses a registered index and cache it
func (s Storage) GetIndex(name string) (Index, error) {
	if idx, ok := indexes.Load(string(s.name) + ":" + name); ok {
		if v, ok := idx.(Index); ok {
			return v, nil
		}

		return Index{}, ErrNoIndex
	}

	fields := s.descriptor.Fields()
	field := fields.ByTextName(name)
	if field == nil {
		return Index{}, ErrNoIndex
	}

	index := Index{
		name:   name,
		field:  field,
		prefix: strings.ReplaceAll(s.store.IndexPrefix, "{index}", name) + ":",
	}

	indexes.Store(string(s.name)+":"+name, index)
	return index, nil
}

// ResolveIndexes finds the primary keys from
// given indexes or all indexes if empty.
func (s Storage) ResolveIndexes(ctx context.Context, client redis.Cmdable, indexes []string, keys []string) ([]string, error) {
	names := slices.Clone(s.store.Indexes)
	if len(indexes) > 0 {
		for _, name := range indexes {
			if !slices.Contains(names, name) {
				return nil, ErrNoIndex
			}
		}

		names = slices.DeleteFunc(names, func(s string) bool {
			return !slices.Contains(indexes, s)
		})
	}

	cmds, err := client.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, name := range names {
			idx, err := s.GetIndex(name)
			if err != nil {
				return err
			}

			for _, k := range keys {
				p.Get(ctx, idx.prefix+k)
			}
		}

		return nil
	})
	if err != nil && err != redis.Nil {
		return nil, err
	}

	// Kept in index declaration order, the first match comes first.
	var results []string
	for _, c := range cmds {
		pk := c.(*redis.StringCmd).Val()
		if pk == "" || slices.Contains(results, pk) {
			continue
		}

		results = append(results, pk)
	}

	return results, nil
}

// SetIndexes writes index entries.
// It generates the key (ex: "index:product:slug:<value>")
// and adds the value to the message primary key.
func (s Storage) SetIndexes(ctx context.Context, client redis.Cmdable, msg proto.Message) error {
	if len(s.store.Indexes) == 0 {
		return nil
	}

	rft := msg.ProtoReflect()
	pk := s.GetPrimaryKey(msg)

	for _, name := range s.store.Indexes {
		idx, err := s.GetIndex(name)
		if err != nil {
			return err
		}

		indexValue := rft.Get(idx.field).String()
		if indexValue == "" {
			continue
		}

		client.Set(ctx, idx.prefix+indexValue, pk, 0)
	}

	return nil
}
