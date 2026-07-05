package scripts

import (
	cRedis "github.com/cashparty/backend/common/redis"
)

// luaSlidingWindow implements a sliding window rate limiter backed by a Redis
// sorted set. It trims entries older than the window, compares the current
// member count against the limit, and admits the request only when there is
// remaining capacity.
//
// KEYS[1] = rate limit key (sorted set holding request timestamps as scores)
// ARGV[1] = limit (max requests allowed within the window)
// ARGV[2] = window size in nanoseconds
// ARGV[3] = current time in nanoseconds (UnixNano)
// ARGV[4] = unique member (由 Go 侧生成保证唯一,用作 ZADD 的 member,避免同纳秒请求被去重)
//
// Returns: 1 = request allowed (timestamp recorded), 0 = request rejected (limit reached)
// This is a generic limiter script and does not flow through MapLuaError.
const luaSlidingWindow = `
local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local member = ARGV[4]
local windowStart = now - window

redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStart)

local count = redis.call('ZCARD', key)

if count < limit then
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, window / 1000000)
    return 1
end

return 0
`

// SlidingWindowScript is the sliding window rate limit script shared by
// common/limiter and gateway/middleware.
var SlidingWindowScript = cRedis.NewScript("sliding_window", luaSlidingWindow)
