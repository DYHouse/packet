package idgen

import cRedis "github.com/cashparty/backend/common/redis"

// releaseNodeScriptSrc 释放 nodeID 分配记录的 Lua 脚本源码。
// 仅当 Redis 中存储的 value == ARGV[1]（instanceID）时才 DEL，防止误删他人锁。
//
// 返回值语义（common 类脚本，单值 0/1，不使用 MapLuaError）：
//
//	1：删除成功（nodeID 已释放）
//	0：value 不匹配或 key 不存在（已被他人持有或已过期）
//
// KEYS[1] = nodeID 分配记录 key（cashparty:idgen:node_id:alloc:<nodeID>）
// ARGV[1] = instanceID（持有着校验 token）
//
// 对应 Redis key 前缀：common/rediskeys.KeyIDGenNodeIDAllocPrefix
const releaseNodeScriptSrc = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
else
    return 0
end
`

// releaseNodeScript 通过 cRedis.NewScript 注册，禁止内联 redis.Eval。
// 规约参考 CODING_STANDARD.md §19 L-1。
var releaseNodeScript = cRedis.NewScript("idgen_release_node", releaseNodeScriptSrc)
