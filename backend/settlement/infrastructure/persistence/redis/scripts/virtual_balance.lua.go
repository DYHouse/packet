package scripts

// luaDeductBalance 原子扣减虚拟余额 Lua 脚本
// KEYS[1] = 机器人虚拟余额 key (cashparty:{userID}:robot:virtual_balance)
// KEYS[2] = per-user 脏标记 key (cashparty:{userID}:robot:dirty，STRING，与 KEYS[1] 同 slot)
// ARGV[1] = 扣减金额（正数）
// ARGV[2] = userID 字符串（兼容保留，SET 模式下脚本内不引用）
//
// 返回值：
//
//	1 = 扣减成功
//	0 = 余额不足（已自动回滚）
//
// 脚本在 Redis 单线程中原子执行 "扣减 → 检查 → 回滚 → 标记 dirty"，
// 消除原 Go 代码三步之间的竞态（两线程并发扣减可凭空创造资金）。
// dirty 标记由 SADD 全局集合改为 SET per-user STRING，避免 Cluster 跨 slot。
const luaDeductBalance = `
local newBalance = redis.call('INCRBY', KEYS[1], -ARGV[1])
if newBalance < 0 then
    redis.call('INCRBY', KEYS[1], ARGV[1])
    return 0
end
redis.call('SET', KEYS[2], '1')
return 1
`

// luaCreditBalance 原子入账虚拟余额 Lua 脚本
// KEYS[1] = 机器人虚拟余额 key (cashparty:{userID}:robot:virtual_balance)
// KEYS[2] = per-user 脏标记 key (cashparty:{userID}:robot:dirty，STRING，与 KEYS[1] 同 slot)
// ARGV[1] = 入账金额（正数）
// ARGV[2] = userID 字符串（兼容保留，SET 模式下脚本内不引用）
//
// 返回值：
//
//	1 = 入账成功
//
// 脚本在 Redis 单线程中原子执行 "INCRBY + SET dirty"，消除原 Go 代码两步之间的竞态
// （INCRBY 成功但 dirty 标记丢失会导致 SyncToDB 漏同步该用户余额）。
// dirty 标记由 SADD 全局集合改为 SET per-user STRING，避免 Cluster 跨 slot。
const luaCreditBalance = `
redis.call('INCRBY', KEYS[1], ARGV[1])
redis.call('SET', KEYS[2], '1')
return 1
`
