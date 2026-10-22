package kv

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	conn redis.UniversalClient
}

// NewClient connects to a database from a `redis://` URL
func NewClient(url string) (Client, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return Client{}, fmt.Errorf("cannot parse db url: %w", err)
	}

	opts.ReadBufferSize = 1024 * 1024
	opts.WriteBufferSize = 1024 * 1024

	c := redis.NewClient(opts)
	if err := c.Ping(context.TODO()).Err(); err != nil {
		return Client{}, fmt.Errorf("cannot ping database: %w", err)
	}

	return Client{c}, nil
}

// New wraps an existing connection
func New(conn redis.UniversalClient) Client {
	return Client{conn}
}

// Conn returns the underlying connection
func (c Client) Conn() redis.UniversalClient {
	return c.conn
}

// |------------------------
// | Basic Operations
// |------------------------

// Get retrieve a single value
func (c Client) Get(ctx context.Context, key string) (string, error) {
	str, err := c.conn.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrNotFound
		}
		return "", err
	}

	return str, nil
}

// GetMany retrieves multiple values at once
func (c Client) GetMany(ctx context.Context, keys ...string) ([]string, error) {
	if len(keys) == 0 {
		// MGET rejects an empty key list, and a SCAN page can be empty.
		return nil, nil
	}

	results, err := c.conn.MGet(ctx, keys...).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	r := make([]string, 0, len(results))
	for _, k := range results {
		if val, ok := k.(string); ok {
			r = append(r, val)
		}
	}

	return r, nil
}

// Set saves a basic key-value
func (c Client) Set(ctx context.Context, key string, value any) error {
	return c.conn.Set(ctx, key, value, 0).Err()
}

// SetMany saves multiple key-value
func (c Client) SetMany(ctx context.Context, kv ...string) error {
	if len(kv)%2 != 0 {
		return errors.New("invalid number of parameters")
	}

	return c.conn.MSet(ctx, kv).Err()
}

// IterSet allows setting multiple values at once using
// a function to generate keys and values
func (c Client) IterSet[T any](ctx context.Context, values []T, fn func(val T) (key string, value any, ok bool)) error {
	_, err := c.conn.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, v := range values {
			k, v, ok := fn(v)
			if !ok {
				continue
			}

			p.Set(ctx, k, v, 0)
		}

		return nil
	})

	return err
}

// Scan gets a set number of keys by prefix
func (c Client) Scan(ctx context.Context, prefix string, count, cursor uint64) ([]string, uint64, error) {
	results, cursor, err := c.conn.Scan(ctx, cursor, prefix, int64(count)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}

	return results, cursor, nil
}

// ScanAll retrieves all keys that starts with a prefix.
// It shouldn't be used for user-facing API as it might be slow.
func (c Client) ScanAll(ctx context.Context, prefix string) ([]string, error) {
	var (
		keys    = []string{}
		seen    = map[string]struct{}{}
		cursor  uint64
		err     error
		results []string
	)

	for {
		results, cursor, err = c.Scan(ctx, prefix, 5000, cursor)
		if err != nil {
			return nil, err
		}

		// SCAN may return a key more than once.
		for _, k := range results {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				keys = append(keys, k)
			}
		}

		if cursor == 0 {
			break
		}
	}

	return keys, nil
}

// DeleteMany deletes keys in chunked DEL calls (a single DEL takes multiple
// keys), instead of one round trip per key.
func (c Client) DeleteMany(ctx context.Context, keys []string) error {
	for chunk := range slices.Chunk(keys, 5000) {
		if err := c.conn.Del(ctx, chunk...).Err(); err != nil {
			return err
		}
	}

	return nil
}
