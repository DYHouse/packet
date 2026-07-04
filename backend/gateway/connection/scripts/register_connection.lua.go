package scripts

import (
	cRedis "github.com/cashparty/backend/common/redis"
)

// luaRegisterConnection 注册用户连接信息并在检测到旧连接(不同 connID)时返回旧连接信息以便踢出。
// 同一 connID 重复注册视为幂等成功,不触发踢旧逻辑。
//
// KEYS[1] = 用户连接 hash key( gateway.GatewayConnKey(userID) )
// ARGV[1] = 新 conn_id
// ARGV[2] = 新 node_id
// ARGV[3] = platform
// ARGV[4] = device_id
// ARGV[5] = connected_at(unix 秒)
// ARGV[6] = conn_ttl(连接 hash key TTL,秒)
//
// 返回值(数组):
//
//	{0, '', ''}                = 首次注册或同 connID 重复注册(无需踢旧)
//	{1, oldConnID, oldNodeID}  = 已存在不同 connID 的旧连接,需踢旧
//
// 注:本脚本为通用脚本,返回值由调用方解析,不走 MapLuaError。
const luaRegisterConnection = `
local userConnKey = KEYS[1]
local newConnID = ARGV[1]
local newNodeID = ARGV[2]
local platform = ARGV[3]
local deviceID = ARGV[4]
local connectedAt = tonumber(ARGV[5])
local connTTL = tonumber(ARGV[6])

local oldConnID = ''
local oldNodeID = ''

local oldData = redis.call('HGETALL', userConnKey)
if #oldData > 0 then
    for i = 1, #oldData, 2 do
        if oldData[i] == 'conn_id' then oldConnID = oldData[i+1] end
        if oldData[i] == 'node_id' then oldNodeID = oldData[i+1] end
    end
end

redis.call('HMSET', userConnKey,
    'conn_id', newConnID,
    'node_id', newNodeID,
    'platform', platform,
    'device_id', deviceID,
    'connected_at', connectedAt
)
redis.call('EXPIRE', userConnKey, connTTL)

if oldConnID ~= '' and oldConnID ~= newConnID then
    return {1, oldConnID, oldNodeID}
end
return {0, '', ''}
`

// RegisterConnectionScript 注册用户连接并按需返回旧连接信息以供踢旧。
var RegisterConnectionScript = cRedis.NewScript("register_connection", luaRegisterConnection)
