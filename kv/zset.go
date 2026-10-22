package kv

import (
	"context"
	"strconv"

	"github.com/redis/go-redis/v9"
)

type ZClient struct {
	c Client
}

func (c Client) ZClient() ZClient {
	return ZClient{c}
}

// Add adds a new score-member into a ZSet
func (z ZClient) Add[N Number](ctx context.Context, key string, score N, value any) error {
	return z.c.conn.ZAdd(ctx, key, redis.Z{
		Score:  float64(score),
		Member: value,
	}).Err()
}

// Max returns the highest score of a ZSet
func (z ZClient) Max(ctx context.Context, key string) (float64, error) {
	m, err := z.MaxMany(ctx, []string{key})
	if err != nil {
		return 0, err
	}

	return m[key], nil
}

func (z ZClient) MaxMany(ctx context.Context, keys []string) (map[string]float64, error) {
	pipeline := z.c.conn.Pipeline()
	cmds := make(map[string]*redis.ZSliceCmd)

	for _, k := range keys {
		cmd := pipeline.ZRangeArgsWithScores(ctx, redis.ZRangeArgs{
			Key:     k,
			Rev:     true,
			Start:   "(+inf",
			Stop:    "(-inf",
			ByScore: true,
			Count:   1,
		})

		cmds[k] = cmd
	}

	_, err := pipeline.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, err
	}

	results := make(map[string]float64)
	for k, cmd := range cmds {
		scores := cmd.Val()
		if len(scores) < 1 {
			results[k] = 0
			continue
		}

		results[k] = scores[0].Score
	}

	return results, nil
}

// Range allows getting members content of a ZSet
func (z ZClient) Range(ctx context.Context, set string, opts ...ZOption) ([]string, error) {
	arg := redis.ZRangeArgs{
		Key:     set,
		Start:   "(-inf",
		Stop:    "(+inf",
		ByScore: true,
	}

	for _, opt := range opts {
		opt(&arg)
	}

	return z.c.conn.ZRangeArgs(ctx, arg).Result()
}

// zsetPipelineChunkSize caps how many commands ride in a single pipeline
// Exec. Key lists can run into the hundreds of thousands of keys, and a
// single unbounded pipeline over all of them risks large buffers and
// round-trip timeouts — chunking keeps each Exec small and, if one chunk
// fails, only that chunk needs retrying instead of the whole scan.
const zsetPipelineChunkSize = 5000

// ZCountResult pairs a requested set key with its ZCOUNT result.
type ZCountResult struct {
	Key   string
	Count int64
}

// CountMany pipelines a ZCOUNT of [min, max], inclusive, across multiple sets
// in chunked round trips — cheaper than RangeByScore (or calling Count in a
// loop) when only the cardinality of each set is needed. Results are
// returned in the same order as keys.
func (z ZClient) CountMany(ctx context.Context, keys []string, minScore, maxScore int64) ([]ZCountResult, error) {
	minStr, maxStr := strconv.FormatInt(minScore, 10), strconv.FormatInt(maxScore, 10)

	type pending struct {
		key string
		cmd *redis.IntCmd
	}

	results := make([]ZCountResult, 0, len(keys))

	for start := 0; start < len(keys); start += zsetPipelineChunkSize {
		end := min(start+zsetPipelineChunkSize, len(keys))
		chunk := keys[start:end]

		cmds := make([]pending, len(chunk))

		pipe := z.c.conn.Pipeline()
		for i, key := range chunk {
			cmds[i] = pending{key, pipe.ZCount(ctx, key, minStr, maxStr)}
		}

		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}

		for _, c := range cmds {
			n, err := c.cmd.Result()
			if err != nil {
				return nil, err
			}
			results = append(results, ZCountResult{Key: c.key, Count: n})
		}
	}

	return results, nil
}

// ZRangeResult pairs a requested set key with its raw ZRANGEBYSCORE members.
type ZRangeResult struct {
	Key     string
	Members []string
}

// RangeByScoreMany pipelines a ZRANGEBYSCORE of [min, max], inclusive, across
// multiple sets in chunked round trips — one call per chunk instead of one
// per key. Results are returned in the same order as keys.
func (z ZClient) RangeByScoreMany(ctx context.Context, keys []string, minScore, maxScore int64) ([]ZRangeResult, error) {
	type pending struct {
		key string
		cmd *redis.StringSliceCmd
	}

	results := make([]ZRangeResult, 0, len(keys))

	for start := 0; start < len(keys); start += zsetPipelineChunkSize {
		end := min(start+zsetPipelineChunkSize, len(keys))
		chunk := keys[start:end]

		cmds := make([]pending, len(chunk))

		pipe := z.c.conn.Pipeline()
		for i, key := range chunk {
			cmds[i] = pending{key, pipe.ZRangeArgs(ctx, redis.ZRangeArgs{
				Key:     key,
				Start:   minScore,
				Stop:    maxScore,
				ByScore: true,
			})}
		}

		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}

		for _, c := range cmds {
			members, err := c.cmd.Result()
			if err != nil {
				return nil, err
			}
			results = append(results, ZRangeResult{Key: c.key, Members: members})
		}
	}

	return results, nil
}

// --------------
// ZRange Options
// --------------

type ZOption func(arg *redis.ZRangeArgs)

// Enable REV option, sorting results from high to low.
// Like ZRANGE REV, the bounds must then be given from high to low too:
// WithMinimum("(+inf"), WithMaximum("(-inf").
func WithReverse() ZOption {
	return func(arg *redis.ZRangeArgs) {
		arg.Rev = true
	}
}

// WithMinimum sets the `start` range, defaults to `(-inf`
func WithMinimum(min any) ZOption {
	return func(arg *redis.ZRangeArgs) {
		arg.Start = min
	}
}

// WithMaximum sets the `stop` range, defaults to `(+inf`
func WithMaximum(max any) ZOption {
	return func(arg *redis.ZRangeArgs) {
		arg.Stop = max
	}
}

// WithLimit sets a limit and offset
func WithLimit[N Number](limit, offset N) ZOption {
	return func(arg *redis.ZRangeArgs) {
		arg.Count = int64(limit)
		arg.Offset = int64(offset)
	}
}

// WithScoreOrdering sorts using `score` instead of `lex`
func WithScoreOrdering() ZOption {
	return func(arg *redis.ZRangeArgs) {
		arg.ByScore = true
		arg.ByLex = false
	}
}

// WithLexOrdering sorts by lexical instead of score
func WithLexOrdering() ZOption {
	return func(arg *redis.ZRangeArgs) {
		arg.ByLex = true
		arg.ByScore = false
	}
}
