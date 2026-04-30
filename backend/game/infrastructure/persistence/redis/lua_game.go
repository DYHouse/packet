package redis

// LuaGrabPacket 抢红包脚本
// KEYS: [availablePacketsKey, userGrabKey, grabbersKey, roundStateKey, playersKey]
// ARGV: [userID, now, grabTimeout, roomID, keyPrefix, packetID]
// 返回: {code, packetID, amount, position, errMsg, isLast}
const LuaGrabPacket = `
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

local playerData = redis.call('HGET', playersKey, userID)
if not playerData then
	return {60, 0, 0, 0, 'only player can grab packet', 0}
end

local phase = redis.call('HGET', roundStateKey, 'phase')
if not phase or phase ~= 'GRABBING' then
	return {40, 0, 0, 0, 'not in grabbing phase', 0}
end

local grabEndTime = tonumber(redis.call('HGET', roundStateKey, 'grab_end_time') or 0)
if grabEndTime > 0 and now > grabEndTime then
	return {41, 0, 0, 0, 'grab timeout', 0}
end

if redis.call('EXISTS', userGrabKey) == 1 then
	return {21, 0, 0, 0, 'already grabbed', 0}
end

local packetKey = keyPrefix .. ':packet:info:' .. packetID
local availableKey = keyPrefix .. ':packet:available:' .. packetID

local available = redis.call('GET', availableKey)
if not available or available ~= '1' then
	return {22, 0, 0, 0, 'packet not available', 0}
end

local packetData = redis.call('GET', packetKey)
if not packetData then
	return {23, 0, 0, 0, 'packet info not found', 0}
end

local packet = cjson.decode(packetData)
local amount = packet.amount
local position = packet.position

redis.call('DEL', availableKey)
redis.call('SET', userGrabKey, '1', 'EX', 86400)
redis.call('SADD', grabbersKey, userID)

packet.is_grabbed = true
packet.grabber_id = userID
packet.grabbed_at = now
redis.call('SET', packetKey, cjson.encode(packet), 'EX', 86400)

local grabbedCount = redis.call('SCARD', grabbersKey)
local totalPackets = tonumber(redis.call('HGET', roundStateKey, 'packet_count') or 5)
local isLast = 0
if grabbedCount >= totalPackets then
	isLast = 1
	redis.call('HSET', roundStateKey, 'phase', 'SETTLING')
end

return {0, tonumber(packetID), amount, position, '', isLast}
`

// LuaAutoDistributePackets 自动分配未抢红包
// KEYS: [availablePacketsKey, grabbersKey, roundStateKey, playersKey, roomHashKey]
// ARGV: [now, keyPrefix, roundID]
// 返回: {code, distributedCount, results}
const LuaAutoDistributePackets = `
local availablePacketsKey = KEYS[1]
local grabbersKey = KEYS[2]
local roundStateKey = KEYS[3]
local playersKey = KEYS[4]
local roomHashKey = KEYS[5]

local now = tonumber(ARGV[1])
local keyPrefix = ARGV[2]
local roundID = ARGV[3]

local allPlayers = redis.call('HKEYS', playersKey)
if not allPlayers or #allPlayers == 0 then
    return {0, 0, {}}
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
    return {0, 0, {}}
end

local packets = redis.call('LRANGE', availablePacketsKey, 0, -1)
if not packets or #packets == 0 then
    return {0, 0, {}}
end

math.randomseed(now)
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
            
            redis.call('SET', packetKey, cjson.encode(packet), 'EX', 86400)
            redis.call('DEL', availableKey)
            redis.call('SADD', grabbersKey, userID)
            
            local userGrabKey = keyPrefix .. ':round:grabbed:' .. roundID .. ':' .. userID
            redis.call('SET', userGrabKey, '1', 'EX', 86400)
            
            table.insert(results, {userID, packet.amount, packet.position})
            playerIdx = playerIdx + 1
        end
    end
end

redis.call('HSET', roundStateKey, 'phase', 'SETTLING')

return {0, #results, results}
`

// LuaSendPacket 统一发红包脚本
// KEYS: [roomHashKey, playersKey, roundStateKey, availablePacketsKey, grabbersKey]
// ARGV: [senderID, senderType, totalAmount, commission, actualAmount, roundNo, now, grabTimeout, keyPrefix, packetAmountsJson, roundID, roomID, scenario, rewardType, rewardAmount]
// scenario: 1=first_round, 2=player_manual, 3=timeout_forced, 4=resume_interrupt
// 返回: {code, roundID, packetIDs}
const LuaSendPacket = `
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

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status ~= 2 then
    return {6, 0, 'game not in playing status'}
end

if scenario == 1 then
    local currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
    if currentRound ~= 0 then
        return {50, 0, 'not first round'}
    end
    local playerCount = redis.call('HLEN', playersKey)
    if playerCount == 0 then
        return {52, 0, 'no players'}
    end
elseif scenario == 2 or scenario == 3 then
    local currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
    if roundNo ~= currentRound + 1 then
        return {50, 0, 'invalid round number'}
    end
    if senderID ~= '0' and roundNo > 1 then
        local nextSender = redis.call('HGET', roomHashKey, 'next_sender_id') or ''
        if nextSender ~= senderID then
            return {51, 0, 'not your turn to send'}
        end
    end
elseif scenario == 4 then
    local playerCount = redis.call('HLEN', playersKey)
    if playerCount == 0 then
        return {52, 0, 'no players'}
    end
    redis.call('HDEL', roomHashKey, 'next_sender_id')
end

local existingPackets = redis.call('LLEN', availablePacketsKey)
if existingPackets > 0 then
    return {20, 0, 'packets already exist for this round'}
end

local currentRoundID = redis.call('HGET', roomHashKey, 'current_round_id') or ''
if currentRoundID ~= '' then
    return {20, 0, 'round already in progress'}
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
    
    redis.call('SET', packetKey, cjson.encode(packet), 'EX', 86400)
    redis.call('SET', availableKey, '1', 'EX', 86400)
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
redis.call('EXPIRE', roundStateKey, 86400)

redis.call('DEL', grabbersKey)
redis.call('HMSET', roomHashKey, 'current_round', roundNo, 'current_round_id', roundID)

return {0, roundID, cjson.encode(packetIDs)}
`

// LuaEndGame 游戏结束脚本
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey]
// ARGV: [now]
// 返回: {code, results}
// results 格式: {userID, nickname}
const LuaEndGame = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]

local now = tonumber(ARGV[1])
local allowedStatus = tonumber(ARGV[2]) or 2

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
	return {1, {}, status}
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
redis.call('EXPIRE', seatsKey, 86400)
redis.call('EXPIRE', seatOwnerKey, 86400)

return {0, results, status}
`

// LuaSettleRound 结算回合（优化版）
// KEYS: [roundStateKey, grabbersKey, playersKey, roomHashKey, availablePacketsKey, sessionPlayerTotalsKey]
// ARGV: [roundID, now, keyPrefix]
// 返回: {code, roundNo, senderID, totalAmount, minAmountPlayer, isGameEnd, results, rewardType, rewardAmount, finalResults}
// results 格式: {userID, grabAmount, nickname, position, avatar, isAutoAssigned, packetID}
// finalResults 格式: {userID, nickname, avatar, totalAmount, rank}
const LuaSettleRound = `
local roundStateKey = KEYS[1]
local grabbersKey = KEYS[2]
local playersKey = KEYS[3]
local roomHashKey = KEYS[4]
local availablePacketsKey = KEYS[5]
local sessionPlayerTotalsKey = KEYS[6]

local roundID = ARGV[1]
local now = tonumber(ARGV[2])
local keyPrefix = ARGV[3]

-- 幂等性检查：检查回合状态
local phase = redis.call('HGET', roundStateKey, 'phase')
if not phase then
    return {1, 0, '', 0, '', 0, {}, 0, 0, {}}
end

-- 如果已经结算，返回成功但不重复执行
if phase == 'SETTLED' or phase == 'WAIT_SEND' or phase == 'GAME_END' then
    -- 返回已结算的标记，让调用方知道这是幂等性返回
    return {2, 0, '', 0, '', 0, {}, 0, 0, {}}
end

-- 只允许在GRABBING或SETTLING阶段结算
if phase ~= 'GRABBING' and phase ~= 'SETTLING' then
    return {3, 0, '', 0, '', 0, {}, 0, 0, {}}
end

local roundInfo = redis.call('HGETALL', roundStateKey)
if not roundInfo or #roundInfo == 0 then
    return {1, 0, '', 0, '', 0, {}, 0, 0, {}}
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
    local packetKey = keyPrefix .. ':packet:info:' .. packetIDStr
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
        return a[3] > b[3]
    end)
    
    -- 添加排名
    for i, t in ipairs(totalsList) do
        table.insert(finalResults, {t[1], t[2], t[3], t[4], i})
    end
    
    -- 清理累计金额数据
    redis.call('DEL', sessionPlayerTotalsKey)
end

return {0, roundNo, senderID, totalAmount, minAmountPlayer, isGameEnd, results, rewardType, rewardAmount, finalResults}
`

// LuaHandlePenalty 处理惩罚
// KEYS: [penaltyCountKey, roomHashKey, playersKey, roundStateKey, penaltyRecordKey]
// ARGV: [userID, roomFee, now, penaltyType]
// 返回: {code, newCount, penaltyAmount, kickRequired}
const LuaHandlePenalty = `
local penaltyCountKey = KEYS[1]
local roomHashKey = KEYS[2]
local playersKey = KEYS[3]
local roundStateKey = KEYS[4]
local penaltyRecordKey = KEYS[5]

local userID = ARGV[1]
local roomFee = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local penaltyType = ARGV[4]

local penaltyCount = tonumber(redis.call('GET', penaltyCountKey) or 0)

local newCount = redis.call('INCR', penaltyCountKey)
redis.call('EXPIRE', penaltyCountKey, 86400)

redis.call('RPUSH', penaltyRecordKey, cjson.encode({
    user_id = userID,
    penalty_type = penaltyType,
    count = newCount,
    amount = roomFee,
    created_at = now
}))
redis.call('EXPIRE', penaltyRecordKey, 86400)

local kickRequired = 0
if newCount >= 2 then
    kickRequired = 1
end

local playerData = redis.call('HGET', playersKey, userID)
if playerData then
    local player = cjson.decode(playerData)
    player.penalty_count = newCount
    player.last_penalty_at = now
    redis.call('HSET', playersKey, userID, cjson.encode(player))
end

return {0, newCount, roomFee, kickRequired}
`

// LuaDistributePenalty 分配惩罚金额
// KEYS: [roomHashKey, playersKey]
// ARGV: [penaltyAmount, excludeUserIDsJson]
// 返回: {code, shareAmount, recipientCount, recipients}
const LuaDistributePenalty = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]

local penaltyAmount = tonumber(ARGV[1])
local excludeUserIDsJson = ARGV[2]

local excludeSet = {}
if excludeUserIDsJson and excludeUserIDsJson ~= '' then
    local excludeList = cjson.decode(excludeUserIDsJson)
    for _, uid in ipairs(excludeList) do
        excludeSet[uid] = true
    end
end

local allPlayers = redis.call('HKEYS', playersKey)
local remainingPlayers = {}
for _, uid in ipairs(allPlayers) do
    if not excludeSet[uid] then
        table.insert(remainingPlayers, uid)
    end
end

if #remainingPlayers == 0 then
    return {0, 0, 0, {}}
end

local shareAmount = math.floor(penaltyAmount / #remainingPlayers)

local recipients = {}
for _, uid in ipairs(remainingPlayers) do
    table.insert(recipients, uid)
end

return {0, shareAmount, #remainingPlayers, recipients}
`
