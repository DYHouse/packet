package scripts

// 本文件中的 Lua 脚本通过字符串拼接构造 Redis key，对应的 Go 侧常量定义在
// common/rediskeys/keys.go。新增/修改 Lua key 时 MUST 同步更新 Go 常量，避免出现孤儿 key。
//
// Lua 拼接的 key 与 Go 常量映射：
//   - keyPrefix .. ':packet:available:' .. packetID  → rediskeys.KeyPacketAvailablePrefix + packetID
//                                                  （工厂函数 rediskeys.PacketAvailableKey(packetID)）
//   - keyPrefix .. ':global:packet_id'               → rediskeys.KeyGlobalPacketID
//   - keyPrefix .. ':packet:info:' .. packetID       → rediskeys.KeyPacketInfoPrefix + packetID
//                                                  （工厂函数 rediskeys.PacketInfoKey(packetID)）
//   - keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID → rediskeys.KeyRoundGrabbed
//                                                  （工厂函数 rediskeys.RoundGrabbedKey(roundID, userID)）
//
// 注意：
//   - keyPrefix 由 Go 侧通过 ARGV 传入，值为 rediskeys.KeyPrefix（"cashparty"）。
//   - luaGrabPacket 为单 packetID 场景，packet info / available key 已改为通过 KEYS[6]/KEYS[7]
//     由 Go 侧直接传入（rediskeys.PacketInfoKey / PacketAvailableKey），不再在 Lua 内拼接 keyPrefix。
//   - luaRobotGrabPacket / luaAutoDistributePackets / luaSendPacket 为循环内动态 packetID 场景，
//     保留 keyPrefix 拼接，对应 KeyPacketInfoPrefix / KeyPacketAvailablePrefix。

// LuaGrabPacket 抢红包脚本
// KEYS: [availablePacketsKey, userGrabKey, grabbersKey, roundStateKey, playersKey, packetInfoKey, packetAvailableKey]
// ARGV: [userID, now, grabTimeout, roomID, keyPrefix, packetID, packetDataTTL]
// 返回: {code, packetID, amount, position, errMsg, isLast}
// 错误码: LuaErrOnlyPlayerCanGrab(60), LuaErrNotInGrabbingPhase(40), LuaErrGrabTimeout(41), LuaErrAlreadyGrabbed(21), LuaErrPacketNotAvailable(22), LuaErrPacketInfoNotFound(23)
const luaGrabPacket = `
local availablePacketsKey = KEYS[1]
local userGrabKey = KEYS[2]
local grabbersKey = KEYS[3]
local roundStateKey = KEYS[4]
local playersKey = KEYS[5]

local userID = ARGV[1]
local now = tonumber(ARGV[2])
local grabTimeout = tonumber(ARGV[3])
local roomID = ARGV[4]
local keyPrefix = ARGV[5]
local packetID = ARGV[6]
local packetDataTTL = tonumber(ARGV[7])

local playerData = redis.call('HGET', playersKey, userID)
if not playerData then
	return {60, 0, 0, 0, 'only player can grab packet', 0}  -- LuaErrOnlyPlayerCanGrab
end

local phase = redis.call('HGET', roundStateKey, 'phase')
if not phase or phase ~= 'GRABBING' then
	return {40, 0, 0, 0, 'not in grabbing phase', 0}  -- LuaErrNotInGrabbingPhase
end

local grabEndTime = tonumber(redis.call('HGET', roundStateKey, 'grab_end_time') or 0)
if grabEndTime > 0 and now > grabEndTime then
	return {41, 0, 0, 0, 'grab timeout', 0}  -- LuaErrGrabTimeout
end

if redis.call('EXISTS', userGrabKey) == 1 then
	return {21, 0, 0, 0, 'already grabbed', 0}  -- LuaErrAlreadyGrabbed
end

local packetKey = KEYS[6]
local availableKey = KEYS[7]

local available = redis.call('GET', availableKey)
if not available or available ~= '1' then
	return {22, 0, 0, 0, 'packet not available', 0}  -- LuaErrPacketNotAvailable
end

local packetData = redis.call('GET', packetKey)
if not packetData then
	return {23, 0, 0, 0, 'packet info not found', 0}  -- LuaErrPacketInfoNotFound
end

local packet = cjson.decode(packetData)
local amount = packet.amount
local position = packet.position

redis.call('DEL', availableKey)
redis.call('SET', userGrabKey, '1', 'EX', packetDataTTL)
redis.call('SADD', grabbersKey, userID)

packet.is_grabbed = true
packet.grabber_id = userID
packet.grabbed_at = now
redis.call('SET', packetKey, cjson.encode(packet), 'EX', packetDataTTL)

local grabbedCount = redis.call('SCARD', grabbersKey)
local totalPackets = tonumber(redis.call('HGET', roundStateKey, 'packet_count') or 5)
local isLast = 0
if grabbedCount >= totalPackets then
	isLast = 1
	redis.call('HSET', roundStateKey, 'phase', 'SETTLING')
end

return {0, tonumber(packetID), amount, position, '', isLast}  -- LuaErrSuccess
`

// LuaRobotGrabPacket 机器人抢红包脚本（原子操作：从可用列表随机选+抢）
// KEYS: [availablePacketsKey, userGrabKey, grabbersKey, roundStateKey, playersKey]
// ARGV: [userID, now, grabTimeout, roomID, keyPrefix, randOffset, packetDataTTL]
// randOffset: Go 侧预生成的随机起始偏移（0 ~ packetCount-1），避免在 Lua 内调用随机函数导致主从复制不一致
// 返回: {code, packetID, amount, position, errMsg, isLast}
// 错误码: LuaErrOnlyPlayerCanGrab(60), LuaErrNotInGrabbingPhase(40), LuaErrGrabTimeout(41), LuaErrAlreadyGrabbed(21), LuaErrPacketNotAvailable(22), LuaErrPacketInfoNotFound(23)
const luaRobotGrabPacket = `
local availablePacketsKey = KEYS[1]
local userGrabKey = KEYS[2]
local grabbersKey = KEYS[3]
local roundStateKey = KEYS[4]
local playersKey = KEYS[5]

local userID = ARGV[1]
local now = tonumber(ARGV[2])
local grabTimeout = tonumber(ARGV[3])
local roomID = ARGV[4]
local keyPrefix = ARGV[5]
local randOffset = tonumber(ARGV[6]) or 0
local packetDataTTL = tonumber(ARGV[7])

local playerData = redis.call('HGET', playersKey, userID)
if not playerData then
	return {60, 0, 0, 0, 'only player can grab packet', 0}  -- LuaErrOnlyPlayerCanGrab
end

local phase = redis.call('HGET', roundStateKey, 'phase')
if not phase or phase ~= 'GRABBING' then
	return {40, 0, 0, 0, 'not in grabbing phase', 0}  -- LuaErrNotInGrabbingPhase
end

local grabEndTime = tonumber(redis.call('HGET', roundStateKey, 'grab_end_time') or 0)
if grabEndTime > 0 and now > grabEndTime then
	return {41, 0, 0, 0, 'grab timeout', 0}  -- LuaErrGrabTimeout
end

if redis.call('EXISTS', userGrabKey) == 1 then
	return {21, 0, 0, 0, 'already grabbed', 0}  -- LuaErrAlreadyGrabbed
end

-- Atomically pick a random available packet from the list
local packetIDs = redis.call('LRANGE', availablePacketsKey, 0, -1)
if not packetIDs or #packetIDs == 0 then
	return {22, 0, 0, 0, 'no available packets', 0}  -- LuaErrPacketNotAvailable
end

local chosenPacketID = nil
local availableKey = nil
local available = nil

-- 从 Go 侧预生成的 randOffset 作为起始偏移，轮询查找第一个可用红包
-- 避免使用 Lua 随机函数（Redis Lua 禁用随机调用，会导致主从复制不一致）
local count = #packetIDs
for i = 0, count - 1 do
	local idx = (randOffset + i) % count + 1
	local pid = packetIDs[idx]
	availableKey = keyPrefix .. ':packet:available:' .. pid
	available = redis.call('GET', availableKey)
	if available and available == '1' then
		chosenPacketID = pid
		break
	end
end

if not chosenPacketID then
	return {22, 0, 0, 0, 'packet not available', 0}  -- LuaErrPacketNotAvailable
end

local packetKey = keyPrefix .. ':packet:info:' .. chosenPacketID
local packetData = redis.call('GET', packetKey)
if not packetData then
	return {23, 0, 0, 0, 'packet info not found', 0}  -- LuaErrPacketInfoNotFound
end

local packet = cjson.decode(packetData)
local amount = packet.amount
local position = packet.position

redis.call('DEL', availableKey)
redis.call('SET', userGrabKey, '1', 'EX', packetDataTTL)
redis.call('SADD', grabbersKey, userID)

packet.is_grabbed = true
packet.grabber_id = userID
packet.grabbed_at = now
redis.call('SET', packetKey, cjson.encode(packet), 'EX', packetDataTTL)

local grabbedCount = redis.call('SCARD', grabbersKey)
local totalPackets = tonumber(redis.call('HGET', roundStateKey, 'packet_count') or 5)
local isLast = 0
if grabbedCount >= totalPackets then
	isLast = 1
	redis.call('HSET', roundStateKey, 'phase', 'SETTLING')
end

return {0, tonumber(chosenPacketID), amount, position, '', isLast}  -- LuaErrSuccess
`

// LuaAutoDistributePackets 自动分配未抢红包
// KEYS: [availablePacketsKey, grabbersKey, roundStateKey, playersKey, roomHashKey]
// ARGV: [now, keyPrefix, roundID, packetDataTTL]
// 返回: {code, distributedCount, results}
// 错误码: LuaErrSuccess(0,本脚本始终返回 0)
const luaAutoDistributePackets = `
local availablePacketsKey = KEYS[1]
local grabbersKey = KEYS[2]
local roundStateKey = KEYS[3]
local playersKey = KEYS[4]
local roomHashKey = KEYS[5]

local now = tonumber(ARGV[1])
local keyPrefix = ARGV[2]
local roundID = ARGV[3]
local packetDataTTL = tonumber(ARGV[4])

local allPlayers = redis.call('HKEYS', playersKey)
if not allPlayers or #allPlayers == 0 then
    return {0, 0, {}}  -- LuaErrSuccess
end

local grabbedPlayers = redis.call('SMEMBERS', grabbersKey)
local grabbedSet = {}
for _, uid in ipairs(grabbedPlayers) do
    grabbedSet[uid] = true
end

local ungrabbedPlayers = {}
for _, uid in ipairs(allPlayers) do
    if not grabbedSet[uid] then
        table.insert(ungrabbedPlayers, uid)
    end
end

if #ungrabbedPlayers == 0 then
    return {0, 0, {}}  -- LuaErrSuccess
end

local packets = redis.call('LRANGE', availablePacketsKey, 0, -1)
if not packets or #packets == 0 then
    return {0, 0, {}}  -- LuaErrSuccess
end

local results = {}
local playerIdx = 1

for i, packetIDStr in ipairs(packets) do
    local availableKey = keyPrefix .. ':packet:available:' .. packetIDStr
    local isAvailable = redis.call('GET', availableKey)

    if isAvailable and playerIdx <= #ungrabbedPlayers then
        local userID = ungrabbedPlayers[playerIdx]

        local packetKey = keyPrefix .. ':packet:info:' .. packetIDStr
        local packetData = redis.call('GET', packetKey)

        if packetData then
            local packet = cjson.decode(packetData)

            packet.is_grabbed = true
            packet.grabber_id = userID
            packet.grabbed_at = now
            packet.auto_assigned = true

            redis.call('SET', packetKey, cjson.encode(packet), 'EX', packetDataTTL)
            redis.call('DEL', availableKey)
            redis.call('SADD', grabbersKey, userID)

            local userGrabKey = keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID
            redis.call('SET', userGrabKey, '1', 'EX', packetDataTTL)

            table.insert(results, {userID, packet.amount, packet.position})
            playerIdx = playerIdx + 1
        end
    end
end

redis.call('HSET', roundStateKey, 'phase', 'SETTLING')

return {0, #results, results}  -- LuaErrSuccess
`

// LuaSendPacket 统一发红包脚本
// KEYS: [roomHashKey, playersKey, roundStateKey, availablePacketsKey, grabbersKey]
// ARGV: [senderID, senderType, totalAmount, commission, actualAmount, roundNo, now, grabTimeout, keyPrefix, packetAmountsJson, roundID, roomID, scenario, rewardType, rewardAmount, packetDataTTL, roundStateTTL]
// scenario: 1=first_round, 2=player_manual, 3=timeout_forced, 4=resume_interrupt
// 返回: {code, roundID, packetIDs}
// 错误码: LuaErrGameNotInPlaying(6), LuaErrNotFirstRound(50), LuaErrNoPlayers(52), LuaErrNotYourTurn(51), LuaErrPacketsAlreadyExist(20)
const luaSendPacket = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local roundStateKey = KEYS[3]
local availablePacketsKey = KEYS[4]
local grabbersKey = KEYS[5]

local senderID = ARGV[1]
local senderType = ARGV[2]
local totalAmount = tonumber(ARGV[3])
local commission = tonumber(ARGV[4])
local actualAmount = tonumber(ARGV[5])
local roundNo = tonumber(ARGV[6])
local now = tonumber(ARGV[7])
local grabTimeout = tonumber(ARGV[8])
local keyPrefix = ARGV[9]
local packetAmountsJson = ARGV[10]
local roundID = ARGV[11]
local roomID = ARGV[12]
local scenario = tonumber(ARGV[13])
local rewardType = tonumber(ARGV[14]) or 0
local rewardAmount = tonumber(ARGV[15]) or 0
local packetDataTTL = tonumber(ARGV[16])
local roundStateTTL = tonumber(ARGV[17])

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status ~= 2 then
    return {6, 0, 'game not in playing status'}  -- LuaErrGameNotInPlaying
end

if scenario == 1 then
    local currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
    if currentRound ~= 0 then
        return {50, 0, 'not first round'}  -- LuaErrNotFirstRound
    end
    local playerCount = redis.call('HLEN', playersKey)
    if playerCount == 0 then
        return {52, 0, 'no players'}  -- LuaErrNoPlayers
    end
elseif scenario == 2 or scenario == 3 then
    local currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
    if roundNo ~= currentRound + 1 then
        return {50, 0, 'invalid round number'}  -- LuaErrNotFirstRound
    end
    if senderID ~= '0' and roundNo > 1 then
        local nextSender = redis.call('HGET', roomHashKey, 'next_sender_id') or ''
        if nextSender ~= senderID then
            return {51, 0, 'not your turn to send'}  -- LuaErrNotYourTurn
        end
    end
elseif scenario == 4 then
    local playerCount = redis.call('HLEN', playersKey)
    if playerCount == 0 then
        return {52, 0, 'no players'}  -- LuaErrNoPlayers
    end
    redis.call('HDEL', roomHashKey, 'next_sender_id')
end

local existingPackets = redis.call('LLEN', availablePacketsKey)
if existingPackets > 0 then
    return {20, 0, 'packets already exist for this round'}  -- LuaErrPacketsAlreadyExist
end

local currentRoundID = redis.call('HGET', roomHashKey, 'current_round_id') or ''
if currentRoundID ~= '' then
    return {20, 0, 'round already in progress'}  -- LuaErrPacketsAlreadyExist
end

local amounts = cjson.decode(packetAmountsJson)
local packetCount = #amounts
local packetIDs = {}

for i, amount in ipairs(amounts) do
    local packetID = redis.call('INCR', keyPrefix .. ':global:packet_id')
    local packetKey = keyPrefix .. ':packet:info:' .. packetID
    local availableKey = keyPrefix .. ':packet:available:' .. packetID

    local packet = {
        packet_id = packetID,
        room_id = roomID,
        round_id = roundID,
        sender_id = senderID,
        sender_type = senderType,
        amount = amount,
        position = i,
        is_grabbed = false,
        created_at = now
    }

    redis.call('SET', packetKey, cjson.encode(packet), 'EX', packetDataTTL)
    redis.call('SET', availableKey, '1', 'EX', packetDataTTL)
    redis.call('RPUSH', availablePacketsKey, packetID)
    table.insert(packetIDs, packetID)
end

local grabEndTime = now + grabTimeout
redis.call('HMSET', roundStateKey,
    'round_id', roundID,
    'round_no', roundNo,
    'sender_id', senderID,
    'sender_type', senderType,
    'total_amount', totalAmount,
    'commission', commission,
    'actual_amount', actualAmount,
    'packet_count', packetCount,
    'phase', 'GRABBING',
    'grab_end_time', grabEndTime,
    'created_at', now,
    'reward_type', rewardType,
    'reward_amount', rewardAmount
)
redis.call('EXPIRE', roundStateKey, roundStateTTL)

redis.call('DEL', grabbersKey)
redis.call('HMSET', roomHashKey, 'current_round', roundNo, 'current_round_id', roundID)

return {0, roundID, cjson.encode(packetIDs)}  -- LuaErrSuccess
`
