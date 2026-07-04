package kafka

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cashparty/backend/common/logger"
)

// PermanentError 表示永久性错误（如 parse 失败），不进入重试循环。
type PermanentError struct {
	err error
}

// NewPermanentError 包装一个错误为 PermanentError。
func NewPermanentError(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{err: err}
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("permanent error: %v", e.err)
}

func (e *PermanentError) Unwrap() error {
	return e.err
}

// IsPermanent 判断 err 是否为 PermanentError。
func IsPermanent(err error) bool {
	_, ok := err.(*PermanentError)
	return ok
}

// processWithRetry 调用 handler 处理 msg，失败按指数退避重试。
// 永久错误（PermanentError）不重试，直接返回。
// 重试耗尽返回最后一次错误。
func processWithRetry(ctx context.Context, handler MessageHandler, msg Message, cfg ConsumerConfig) error {
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := handler(ctx, msg)
		if err == nil {
			return nil
		}
		lastErr = err
		if IsPermanent(err) {
			logger.Error("kafka handler permanent error, skip retry",
				"topic", cfg.Topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
				"error", err)
			return err
		}
		if attempt < cfg.MaxRetries {
			backoff := cfg.RetryBackoff * time.Duration(1<<uint(attempt))
			jitter := time.Duration(rand.Intn(100)) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff + jitter):
			}
			logger.Warn("kafka handler retry",
				"topic", cfg.Topic,
				"partition", msg.Partition,
				"offset", msg.Offset,
				"attempt", attempt+1,
				"max_retries", cfg.MaxRetries,
				"error", err)
		}
	}
	return lastErr
}
