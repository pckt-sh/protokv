package kv

import (
	"context"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

type ZBatch struct {
	c       Client
	members []ZMember
}

type ZMember struct {
	Key     string
	Score   float64
	Message proto.Message
}

func (z ZMember) ToRedis() (redis.Z, error) {
	b, err := proto.Marshal(z.Message)
	if err != nil {
		return redis.Z{}, err
	}

	return redis.Z{
		Score:  z.Score,
		Member: b,
	}, nil
}

func (c Client) NewZBatch() *ZBatch {
	return &ZBatch{c, []ZMember{}}
}

func (z *ZBatch) Add[N Number](key string, score N, msg proto.Message) {
	z.members = append(z.members, ZMember{key, float64(score), msg})
}

func (z *ZBatch) Exec(ctx context.Context) error {
	_, err := z.c.conn.Pipelined(ctx, func(p redis.Pipeliner) error {
		for _, member := range z.members {
			redisValue, err := member.ToRedis()
			if err != nil {
				return err
			}

			p.ZAdd(ctx, member.Key, redisValue)
		}

		return nil
	})

	z.members = []ZMember{}

	return err
}
