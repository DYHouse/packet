package lock

import "errors"

// ErrLockNotAcquired 表示锁未被成功获取（例如锁已被他人持有，或 Redis 不可用导致获取失败）。
// 调用方可通过 errors.Is(err, ErrLockNotAcquired) 判别该错误类型。
var ErrLockNotAcquired = errors.New("lock not acquired")

// ErrRedisUnavailable 表示 Redis 不可用，导致锁操作失败。
// 用于 fail-closed 场景：当 Redis 不可用时返回 error 触发上层重试，而不是 fail-open 放行。
// 调用方可通过 errors.Is(err, ErrRedisUnavailable) 判别该错误类型。
var ErrRedisUnavailable = errors.New("redis unavailable")
