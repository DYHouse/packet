package scripts

// 本文件中的 Lua 脚本通过字符串拼接构造 Redis key，对应的 Go 侧常量定义在
// common/rediskeys/keys.go。新增/修改 Lua key 时 MUST 同步更新 Go 常量，避免出现孤儿 key。
//
// Lua 拼接的 key 与 Go 常量映射：
//   - packetInfoPrefix .. packetIDStr → rediskeys.KeyPacketInfoPrefix + packetID
//                                                  （工厂函数 rediskeys.PacketInfoKey(packetID)）
//
// 注意：packetInfoPrefix 由 Go 侧通过 ARGV 传入，值为 rediskeys.KeyPacketInfoPrefix。

// LuaEndGame 游戏结束脚本
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey]
// ARGV: [now, allowedStatus, roomDataTTL]
// 返回: {code, results, status}
// results 格式: {userID, nickname}
// 错误码: LuaErrRoomNotFound(1,状态不匹配), LuaErrIdempotent(2,已结算,历史复用), LuaErrSuccess(0)
const luaEndGame = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]

local now = tonumber(ARGV[1])
local allowedStatus = tonumber(ARGV[2]) or 2
local roomDataTTL = tonumber(ARGV[3])

-- 幂等性检查：检查房间状态
local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)

-- 支持多种状态：Playing(2) 或 Interrupted(4)
-- allowedStatus: 0=允许任意非Waiting状态, 2=Playing, 4=Interrupted
local statusAllowed = false
if allowedStatus == 0 then
	statusAllowed = (status ~= 1)
else
	statusAllowed = (status == allowedStatus)
end

if not statusAllowed then
	return {1, {}, status}  -- LuaErrRoomNotFound(状态不匹配)
end

-- 获取所有玩家ID
local playerIDs = redis.call('HKEYS', playersKey)
local results = {}

-- 更新房间状态
redis.call('HSET', roomHashKey, 'status', 1)
redis.call('HSET', roomHashKey, 'current_round', 0)
redis.call('HDEL', roomHashKey, 'next_sender_id')
redis.call('HDEL', roomHashKey, 'countdown_end_time')
redis.call('HDEL', roomHashKey, 'started_at')
-- 清理已结束 session/round 的引用，避免后续逻辑误用旧 sessionID/roundID。
-- 对结算无影响：SettleGame 的 sessionID 来自 Kafka 事件 payload，SettleRound 在 EndGame 前已执行且有幂等检查。
redis.call('HDEL', roomHashKey, 'current_session_id')
redis.call('HDEL', roomHashKey, 'current_round_id')

-- 清理座位占用状态
redis.call('DEL', seatsKey)
redis.call('DEL', seatOwnerKey)

-- 转换玩家为观众
for _, playerID in ipairs(playerIDs) do
	local playerData = redis.call('HGET', playersKey, playerID)
	if playerData then
		local player = cjson.decode(playerData)

		table.insert(results, {
			playerID,
			player.nickname or ''
		})

		-- 转换为观众（保留座位号）
		local spectator = {
			user_id = player.user_id,
			nickname = player.nickname,
			avatar = player.avatar,
			seat_no = player.seat_no or 0
		}

		redis.call('HSET', spectatorsKey, playerID, cjson.encode(spectator))
		redis.call('HDEL', playersKey, playerID)

		-- 重新占用座位（保留座位）
		if spectator.seat_no > 0 then
			redis.call('SETBIT', seatsKey, spectator.seat_no, 1)
			redis.call('HSET', seatOwnerKey, tostring(spectator.seat_no), playerID)
		end
	end
end

-- 设置座位过期时间
redis.call('EXPIRE', seatsKey, roomDataTTL)
redis.call('EXPIRE', seatOwnerKey, roomDataTTL)

return {0, results, status}  -- LuaErrSuccess
`

// LuaSettleRound 结算回合（优化版）
// KEYS: [roundStateKey, grabbersKey, playersKey, roomHashKey, availablePacketsKey, sessionPlayerTotalsKey]
// ARGV: [roundID, now, packetInfoPrefix]
// 返回: {code, roundNo, senderID, totalAmount, minAmountPlayer, isGameEnd, results, rewardType, rewardAmount, finalResults}
// results 格式: {userID, grabAmount, nickname, position, avatar, isAutoAssigned, packetID}
// finalResults 格式: {userID, nickname, avatar, totalAmount, rank}
// 错误码: LuaErrRoomNotFound(1,回合不存在), LuaErrIdempotent(2,已结算), LuaErrRoomFullTotal(3,阶段不允许)
const luaSettleRound = `
local roundStateKey = KEYS[1]
local grabbersKey = KEYS[2]
local playersKey = KEYS[3]
local roomHashKey = KEYS[4]
local availablePacketsKey = KEYS[5]
local sessionPlayerTotalsKey = KEYS[6]

local roundID = ARGV[1]
local now = tonumber(ARGV[2])
local packetInfoPrefix = ARGV[3]

-- 幂等性检查：检查回合状态
local phase = redis.call('HGET', roundStateKey, 'phase')
if not phase then
    return {1, 0, '', 0, '', 0, {}, 0, 0, {}}  -- LuaErrRoomNotFound(回合不存在)
end

-- 如果已经结算，返回成功但不重复执行
if phase == 'SETTLED' or phase == 'WAIT_SEND' or phase == 'GAME_END' then
    -- 返回已结算的标记，让调用方知道这是幂等性返回
    return {2, 0, '', 0, '', 0, {}, 0, 0, {}}  -- LuaErrIdempotent
end

-- 只允许在GRABBING或SETTLING阶段结算
if phase ~= 'GRABBING' and phase ~= 'SETTLING' then
    return {3, 0, '', 0, '', 0, {}, 0, 0, {}}  -- LuaErrRoomFullTotal(阶段不允许)
end

local roundInfo = redis.call('HGETALL', roundStateKey)
if not roundInfo or #roundInfo == 0 then
    return {1, 0, '', 0, '', 0, {}, 0, 0, {}}  -- LuaErrRoomNotFound(回合不存在)
end

local roundData = {}
for i = 1, #roundInfo, 2 do
    roundData[roundInfo[i]] = roundInfo[i + 1]
end

local roundNo = tonumber(roundData['round_no'] or 0)
local senderID = roundData['sender_id'] or ''
local totalAmount = tonumber(roundData['total_amount'] or 0)
local packetCount = tonumber(roundData['packet_count'] or 5)
local rewardType = tonumber(roundData['reward_type'] or 0)
local rewardAmount = tonumber(roundData['reward_amount'] or 0)

local grabberIDs = redis.call('SMEMBERS', grabbersKey)
local results = {}
local minAmount = -1
local minAmountPlayer = ''
local firstAmount = -1
local allSameAmount = true

-- 通过红包ID列表访问，避免使用KEYS命令
local packetIDs = redis.call('LRANGE', availablePacketsKey, 0, -1)

-- 构建用户红包映射
local userPackets = {}
for _, packetIDStr in ipairs(packetIDs) do
    local packetKey = packetInfoPrefix .. packetIDStr
    local packetData = redis.call('GET', packetKey)
    if packetData then
        local packet = cjson.decode(packetData)
        if packet.grabber_id then
            userPackets[packet.grabber_id] = {
                packet_id = packet.packet_id,
                amount = packet.amount,
                position = packet.position,
                auto_assigned = packet.auto_assigned or false
            }
        end
    end
end

-- 构建结果列表
for _, userID in ipairs(grabberIDs) do
    local playerData = redis.call('HGET', playersKey, userID)
    if playerData then
        local player = cjson.decode(playerData)
        local nickname = player.nickname or ''
        local avatar = player.avatar or ''

        local packetInfo = userPackets[userID] or {packet_id = 0, amount = 0, position = 0, auto_assigned = false}
        local packetID = packetInfo.packet_id
        local grabAmount = packetInfo.amount
        local position = packetInfo.position
        local isAutoAssigned = packetInfo.auto_assigned and 1 or 0

        table.insert(results, {userID, grabAmount, nickname, position, avatar, isAutoAssigned, packetID})

        if firstAmount < 0 then
            firstAmount = grabAmount
        elseif grabAmount ~= firstAmount then
            allSameAmount = false
        end

        if minAmount < 0 or grabAmount < minAmount then
            minAmount = grabAmount
            minAmountPlayer = userID
        end
    end
end

-- 更新每个玩家的累计金额（抢到的红包）
if sessionPlayerTotalsKey and sessionPlayerTotalsKey ~= '' then
    for _, userID in ipairs(grabberIDs) do
        local packetInfo = userPackets[userID]
        if packetInfo then
            local grabAmount = packetInfo.amount
            local currentTotal = tonumber(redis.call('HGET', sessionPlayerTotalsKey, userID) or 0)
            redis.call('HSET', sessionPlayerTotalsKey, userID, currentTotal + grabAmount)
        end
    end

    -- 如果有奖励，给所有玩家加奖励金额
    if rewardAmount > 0 then
        for _, userID in ipairs(grabberIDs) do
            local currentTotal = tonumber(redis.call('HGET', sessionPlayerTotalsKey, userID) or 0)
            redis.call('HSET', sessionPlayerTotalsKey, userID, currentTotal + rewardAmount)
        end
    end
end

-- 更新回合状态为已结算
redis.call('HSET', roundStateKey, 'phase', 'SETTLED')
redis.call('HSET', roundStateKey, 'settled_at', now)

-- 清理可用红包列表
redis.call('DEL', availablePacketsKey)

-- 重置 current_round_id，表示当前回合红包已处理完毕
redis.call('HDEL', roomHashKey, 'current_round_id')

-- 豹子奖励时，下一轮由系统发红包
if rewardType == 2 or allSameAmount then
    minAmountPlayer = '0'
end

if minAmountPlayer ~= '' then
    redis.call('HSET', roomHashKey, 'next_sender_id', minAmountPlayer)
end

local maxRounds = tonumber(redis.call('HGET', roomHashKey, 'max_rounds') or 10)
local isGameEnd = 0
if roundNo >= maxRounds then
	isGameEnd = 1
	redis.call('HSET', roundStateKey, 'phase', 'GAME_END')
end

-- 游戏结束时返回 FinalResults
local finalResults = {}
if isGameEnd == 1 and sessionPlayerTotalsKey and sessionPlayerTotalsKey ~= '' then
    local allTotals = redis.call('HGETALL', sessionPlayerTotalsKey)
    local totalsList = {}

    for i = 1, #allTotals, 2 do
        local userID = allTotals[i]
        local total = tonumber(allTotals[i + 1]) or 0

        -- 从 playersKey 获取昵称和头像
        local nickname = ''
        local avatar = ''
        local playerData = redis.call('HGET', playersKey, userID)
        if playerData then
            local player = cjson.decode(playerData)
            nickname = player.nickname or ''
            avatar = player.avatar or ''
        end

        table.insert(totalsList, {userID, nickname, avatar, total})
    end

    -- 按金额降序排序
    table.sort(totalsList, function(a, b)
        return a[4] > b[4]
    end)

    -- 添加排名
    for i, t in ipairs(totalsList) do
        table.insert(finalResults, {t[1], t[2], t[3], t[4], i})
    end

    -- 清理累计金额数据
    redis.call('DEL', sessionPlayerTotalsKey)
end

return {0, roundNo, senderID, totalAmount, minAmountPlayer, isGameEnd, results, rewardType, rewardAmount, finalResults}  -- LuaErrSuccess
`
