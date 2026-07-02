package scripts

// luaReleaseAssignLock 原子校验并释放锁：仅当 key 的 value == token 时才 DEL
// 防止 TTL 过期后被其他实例抢占，原持有者恢复后误删新持有者的锁
// KEYS[1] = 锁 key
// ARGV[1] = 持有者 token
// 返回: 1=删除成功, 0=token 不匹配（锁已被他人持有，不删）
const luaReleaseAssignLock = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`
