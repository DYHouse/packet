package scripts

// luaDeductBalance 原子扣减虚拟余额 Lua 脚本
// KEYS[1] = 机器人虚拟余额 key (cashparty:robot:virtual_balance:{userID})
// KEYS[2] = 脏数据集合 key (cashparty:robot:virtual_balance:dirty)
// ARGV[1] = 扣减金额（正数）
// ARGV[2] = userID 字符串（用于加入脏数据集合）
//
// 返回值：
//
//	1 = 扣减成功
//	0 = 余额不足（已自动回滚）
//
// 脚本在 Redis 单线程中原子执行 "扣减 → 检查 → 回滚 → 标记 dirty"，
// 消除原 Go 代码三步之间的竞态（两线程并发扣减可凭空创造资金）。
const luaDeductBalance = `
local newBalance = redis.call('INCRBY', KEYS[1], -ARGV[1])
if newBalance < 0 then
    redis.call('INCRBY', KEYS[1], ARGV[1])
    return 0
end
redis.call('SADD', KEYS[2], ARGV[2])
return 1
`
