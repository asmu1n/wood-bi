package ratelimit

import (
	"context"
	"fmt"
	"time"

	"wood-bi/internal/port"

	"github.com/redis/go-redis/v9"
)

// 固定窗口计数：窗口内超过 limit 次则拒绝。
// KEYS[1]=key ARGV[1]=limit ARGV[2]=window_ms
// 返回 1 允许，0 拒绝。
var allowScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
if current > tonumber(ARGV[1]) then
  return 0
end
return 1
`)

// redisLimiter 基于 Redis 的限流实现。
type redisLimiter struct {
	client redis.UniversalClient
	prefix string
}

// New 创建限流器；prefix 用于隔离 key 命名空间。
func New(client redis.UniversalClient) *redisLimiter {
	return &redisLimiter{
		client: client,
		prefix: "ratelimit:",
	}
}

// Allow 在 window 内最多 limit 次；超限返回 port.ErrRateLimited。
func (l *redisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) error {
	if limit <= 0 {
		return nil
	}
	if window <= 0 {
		window = time.Second
	}
	fullKey := l.prefix + key
	ms := window.Milliseconds()
	if ms < 1 {
		ms = 1
	}

	res, err := allowScript.Run(ctx, l.client, []string{fullKey}, limit, ms).Int()
	if err != nil {
		return fmt.Errorf("ratelimit: %w", err)
	}
	if res == 0 {
		return port.ErrRateLimited
	}
	return nil
}
