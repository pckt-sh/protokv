package kv

import (
	"context"
	"errors"
	"slices"

	"github.com/pckt-sh/protokv/codec"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// WriteHook is called around the writes of ProtoClient.Set and SetMany,
// once per batch.
type WriteHook struct {
	// Previous fetches the stored header of every key before the hooks run,
	// filling WriteOp.Old. It costs one pipelined round trip per batch.
	Previous bool

	// Before runs before the writes. Commands queued on pipe run in the same
	// pipeline as the writes, without an extra round trip.
	Before func(ctx context.Context, pipe redis.Pipeliner, ops []WriteOp) error

	// After runs once the writes succeeded.
	After func(ctx context.Context, ops []WriteOp) error
}

// WriteOp is a message being written.
type WriteOp struct {
	Key string
	Msg proto.Message
	New codec.Meta

	// Old is the meta of the stored value. It is nil when the key doesn't
	// exist, holds a legacy value, or when no hook asked for Previous and
	// WithSkipUnchanged isn't set.
	Old *codec.Meta

	// Skipped is set when the value is unchanged and WithSkipUnchanged is
	// set: it isn't written, nor its indexes.
	Skipped bool

	value []byte
}

// Changed reports whether the message differs from the stored one. Without
// a known previous value, it is considered changed.
func (op WriteOp) Changed() bool {
	return op.Old == nil || op.Old.Hash != op.New.Hash
}

// WriteOption configures writes, per call or for a client returned by
// ProtoClient.With.
type WriteOption func(*writeOptions)

type writeOptions struct {
	hooks         []WriteHook
	skipUnchanged bool
}

func (o writeOptions) previous() bool {
	return o.skipUnchanged || slices.ContainsFunc(o.hooks, func(h WriteHook) bool { return h.Previous })
}

// WithWriteHook adds a hook. Hooks run in the order they are added.
func WithWriteHook(h WriteHook) WriteOption {
	return func(o *writeOptions) {
		o.hooks = append(o.hooks, h)
	}
}

// WithSkipUnchanged doesn't rewrite messages whose stored hash is the same.
// Their timestamp then tells when they last changed, not when they were last
// written.
func WithSkipUnchanged() WriteOption {
	return func(o *writeOptions) {
		o.skipUnchanged = true
	}
}

// With returns a client applying opts to every write, before the options
// given per call.
func (p ProtoClient) With(opts ...WriteOption) ProtoClient {
	p.writeOpts = slices.Concat(p.writeOpts, opts)
	return p
}

func (p ProtoClient) newWriteOptions(opts []WriteOption) writeOptions {
	var o writeOptions
	for _, opt := range p.writeOpts {
		opt(&o)
	}
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// loadPrevious fills the Old meta of ops by reading only the stored headers.
func (p ProtoClient) loadPrevious(ctx context.Context, ops []WriteOp) error {
	cmds := make([]*redis.StringCmd, len(ops))

	pipe := p.c.conn.Pipeline()
	for i, op := range ops {
		cmds[i] = pipe.GetRange(ctx, op.Key, 0, codec.HeaderSize-1)
	}

	// A reply error (like WRONGTYPE) only means there is no previous message:
	// SET overwrites the key anyway. Other errors fail the write.
	if _, err := pipe.Exec(ctx); err != nil && !isReplyError(err) {
		return err
	}

	for i, cmd := range cmds {
		if err := cmd.Err(); err != nil {
			if isReplyError(err) {
				continue
			}
			return err
		}

		if meta, ok := codec.ParseHeader([]byte(cmd.Val())); ok {
			ops[i].Old = &meta
		}
	}

	return nil
}

func isReplyError(err error) bool {
	var rerr redis.Error
	return errors.As(err, &rerr)
}
