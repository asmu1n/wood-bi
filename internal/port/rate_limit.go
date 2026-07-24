package port

import (
	"context"
	"errors"
	"time"
)

// ErrRateLimited 表示触发限流。
var ErrRateLimited = errors.New("请求过于频繁")

// RateLimiter 分布式限流端口。
type RateLimiter interface {
	// Allow 在 key 维度按 limit/window 判定是否放行；超限返回 ErrRateLimited。
	Allow(ctx context.Context, key string, limit int, window time.Duration) error
}
