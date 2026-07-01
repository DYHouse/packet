package scripts

const luaJoinAsSpectator = `
local roomHashKey = KEYS[1]
local spectatorsKey = KEYS[2]
local playersKey = KEYS[3]
local userRoomKey = KEYS[4]

local userID = ARGV[1]
local spectatorData = ARGV[2]
local now = tonumber(ARGV[3])
local roomIDStr = ARGV[4]

local roomExists = redis.call('EXISTS', roomHashKey)
if roomExists == 0 then
	return {1, '', '', 0}
end

local existingRoom = redis.call('GET', userRoomKey)
if existingRoom and existingRoom ~= '' and existingRoom ~= '0' then
	return {4, '', '', 0}
end

local alreadySpectator = redis.call('HEXISTS', spectatorsKey, userID)
if alreadySpectator == 1 then
	return {4, '', '', 0}
end

local alreadyPlayer = redis.call('HEXISTS', playersKey, userID)
if alreadyPlayer == 1 then
	return {4, '', '', 0}
end

local playerCount = redis.call('HLEN', playersKey)
local spectatorCount = redis.call('HLEN', spectatorsKey)
local maxPlayers = tonumber(redis.call('HGET', roomHashKey, 'max_players') or 0)
local maxSpectators = tonumber(redis.call('HGET', roomHashKey, 'max_spectators') or 100)

local totalInRoom = playerCount + spectatorCount
local maxTotal = maxPlayers + maxSpectators

if totalInRoom >= maxTotal then
	return {15, '', '', 0}
end

if spectatorCount >= maxSpectators then
	return {3, '', '', 0}
end

local roomNo = redis.call('HGET', roomHashKey, 'room_no') or ''
local configID = tonumber(redis.call('HGET', roomHashKey, 'config_id') or 0)

redis.call('HSET', spectatorsKey, userID, spectatorData)

redis.call('SET', userRoomKey, roomIDStr, 'EX', 86400)

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status == 0 then
	redis.call('HSET', roomHashKey, 'status', 1)
end

redis.call('EXPIRE', roomHashKey, 86400)
redis.call('EXPIRE', spectatorsKey, 86400)
redis.call('EXPIRE', playersKey, 86400)

return {0, roomIDStr, roomNo, configID}
`

const luaSelectSeat = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]

local userID = ARGV[1]
local seatNo = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local isRobot = ARGV[4]

if redis.call('EXISTS', roomHashKey) == 0 then
	return {1, 0, 0, 0}
end

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status == 2 then
	return {6, 0, 0, 0}
end

local maxPlayers = tonumber(redis.call('HGET', roomHashKey, 'max_players') or 5)
if not seatNo or seatNo < 1 or seatNo > maxPlayers then
	return {8, 0, 0, 0}
end

local occupied = redis.call('GETBIT', seatsKey, seatNo)
if occupied == 1 then
	return {7, 0, 0, 0}
end

local existingPlayer = redis.call('HGET', playersKey, userID)
if existingPlayer then
	return {9, 0, 0, 0}
end

local existingSpectator = redis.call('HGET', spectatorsKey, userID)
if not existingSpectator then
	return {14, 0, 0, 0}
end

local spectator = cjson.decode(existingSpectator)
local oldSeatNo = spectator.seat_no or 0

if oldSeatNo > 0 then
	redis.call('SETBIT', seatsKey, oldSeatNo, 0)
	redis.call('HDEL', seatOwnerKey, tostring(oldSeatNo))
end

spectator.seat_no = seatNo
spectator.seat_selected_at = now
if isRobot == '1' or isRobot == 'true' then
	spectator.is_robot = true
else
	spectator.is_robot = false
end
redis.call('HSET', spectatorsKey, userID, cjson.encode(spectator))
redis.call('SETBIT', seatsKey, seatNo, 1)
redis.call('HSET', seatOwnerKey, tostring(seatNo), userID)

redis.call('EXPIRE', seatsKey, 86400)
redis.call('EXPIRE', seatOwnerKey, 86400)

local playerCount = redis.call('HLEN', playersKey)
local spectatorCount = redis.call('HLEN', spectatorsKey)
return {0, playerCount, spectatorCount, oldSeatNo}
`

const luaCancelSeat = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]

local userID = ARGV[1]

if redis.call('EXISTS', roomHashKey) == 0 then
	return {1, 0, 0, 0}
end

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status ~= 1 and status ~= 4 then
	return {6, 0, 0, 0}
end

local playerData = redis.call('HGET', playersKey, userID)
local spectatorData = redis.call('HGET', spectatorsKey, userID)

if playerData then
	local player = cjson.decode(playerData)
	local seatNo = player.seat_no or 0

	if seatNo > 0 then
		redis.call('SETBIT', seatsKey, seatNo, 0)
		redis.call('HDEL', seatOwnerKey, tostring(seatNo))
	end

	local spectator = {
		user_id = player.user_id,
		nickname = player.nickname,
		avatar = player.avatar,
		seat_no = 0,
		is_robot = player.is_robot or false
	}
	redis.call('HSET', spectatorsKey, userID, cjson.encode(spectator))
	redis.call('HDEL', playersKey, userID)

	local playerCount = redis.call('HLEN', playersKey)
	local spectatorCount = redis.call('HLEN', spectatorsKey)

	return {0, playerCount, spectatorCount, seatNo}
elseif spectatorData then
	local spectator = cjson.decode(spectatorData)
	local seatNo = spectator.seat_no or 0

	if seatNo == 0 then
		return {14, 0, 0, 0}
	end

	redis.call('SETBIT', seatsKey, seatNo, 0)
	redis.call('HDEL', seatOwnerKey, tostring(seatNo))

	spectator.seat_no = 0
	spectator.seat_selected_at = nil
	redis.call('HSET', spectatorsKey, userID, cjson.encode(spectator))

	local playerCount = redis.call('HLEN', playersKey)
	local spectatorCount = redis.call('HLEN', spectatorsKey)

	return {0, playerCount, spectatorCount, seatNo}
else
	return {14, 0, 0, 0}
end
`

const luaLeaveRoom = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]
local userRoomKey = KEYS[6]

local userID = ARGV[1]
local now = tonumber(ARGV[2])

if redis.call('EXISTS', roomHashKey) == 0 then
	return {1, 'room_not_found', 0}
end

local playerData = redis.call('HGET', playersKey, userID)
if playerData then
	return {16, 'player_cannot_leave', 0}
end

local spectatorData = redis.call('HGET', spectatorsKey, userID)
if spectatorData then
	local spectator = cjson.decode(spectatorData)
	local seatNo = spectator.seat_no or 0

	if seatNo > 0 then
		redis.call('SETBIT', seatsKey, seatNo, 0)
		redis.call('HDEL', seatOwnerKey, tostring(seatNo))
	end

	redis.call('HDEL', spectatorsKey, userID)
	redis.call('DEL', userRoomKey)

	return {0, 'spectator', seatNo}
end

return {14, 'not_found', 0}
`

const luaKickPlayerAndInterrupt = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local seatsKey = KEYS[3]
local seatOwnerKey = KEYS[4]
local userRoomKey = KEYS[5]

local userID = ARGV[1]
local reason = ARGV[2]
local now = tonumber(ARGV[3])
local roundStateKeyPrefix = ARGV[4]

if redis.call('EXISTS', roomHashKey) == 0 then
    return {1, 'room_not_found', 0, 0}
end

local playerData = redis.call('HGET', playersKey, userID)
if not playerData then
    return {14, 'player_not_found', 0, 0}
end

local player = cjson.decode(playerData)
local seatNo = player.seat_no or 0

local currentRoundID = redis.call('HGET', roomHashKey, 'current_round_id') or ''
if currentRoundID ~= '' then
    local currentRoundStateKey = roundStateKeyPrefix .. currentRoundID
    local senderID = redis.call('HGET', currentRoundStateKey, 'sender_id') or ''
    if senderID == userID then
        return {32, 'player_already_sent_packet', seatNo, 0}
    end
end

if seatNo > 0 then
    redis.call('SETBIT', seatsKey, seatNo, 0)
    redis.call('HDEL', seatOwnerKey, tostring(seatNo))
end

redis.call('HDEL', playersKey, userID)
redis.call('DEL', userRoomKey)

local currentStatus = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
local newStatus = currentStatus

if currentStatus == 2 then
    redis.call('HSET', roomHashKey, 'status', 4)
    redis.call('HSET', roomHashKey, 'interrupted_at', now)
    newStatus = 4
end

return {0, 'success', seatNo, newStatus}
`

const luaTryStartGame = `
local roomHashKey = KEYS[1]

local now = tonumber(ARGV[1])

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status ~= 2 then
	return {0, 'status not countdown'}
end

local endTime = tonumber(redis.call('HGET', roomHashKey, 'countdown_end_time') or 0)
if endTime == 0 then
	return {0, 'no countdown'}
end

if now < endTime then
	return {0, 'countdown not finished'}
end

local startedAt = redis.call('HGET', roomHashKey, 'started_at') or 0
if startedAt ~= 0 and startedAt ~= '0' then
	return {0, 'game already started'}
end

redis.call('HSET', roomHashKey, 'started_at', now)

redis.call('HDEL', roomHashKey, 'countdown_end_time')

return {1, 'success'}
`

// LuaPlayerReady 玩家准备
// KEYS: [roomHashKey, playersKey, spectatorsKey]
// ARGV: [userID, now]
// 返回: {code, playerCount, maxPlayers, shouldStartCountdown, countdownEndTime, currentRound, playerData, message}
const luaPlayerReady = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]

local userID = ARGV[1]
local now = tonumber(ARGV[2])

-- 1. 检查房间是否存在
local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status == 0 then
	return {1, 0, 0, 0, 0, 0, '', ''}
end

-- 2. 检查用户身份（玩家或观众）
local playerData = redis.call('HGET', playersKey, userID)
local spectatorData = redis.call('HGET', spectatorsKey, userID)
local player = nil
local isConvertedFromSpectator = false

if playerData then
	-- 已经是玩家
	player = cjson.decode(playerData)
elseif spectatorData then
	-- 是观众，检查是否已选座
	local spectator = cjson.decode(spectatorData)
	local seatNo = tonumber(spectator.seat_no or 0)
	
	if seatNo == 0 then
		return {12, 0, 0, 0, 0, 0, '', ''}
	end
	
	-- 将观众转换为玩家
	player = {
		user_id = spectator.user_id,
		nickname = spectator.nickname,
		avatar = spectator.avatar,
		seat_no = seatNo,
		disconnected_at = nil,
		is_robot = spectator.is_robot or false
	}
	
	-- 从观众列表删除
	redis.call('HDEL', spectatorsKey, userID)
	
	-- 添加到玩家列表
	redis.call('HSET', playersKey, userID, cjson.encode(player))
	
	isConvertedFromSpectator = true
else
	return {14, 0, 0, 0, 0, 0, '', ''}
end

-- 3. 设置玩家准备状态
player.ready_at = now
redis.call('HSET', playersKey, userID, cjson.encode(player))

-- 4. 获取房间数据
local playerCount = redis.call('HLEN', playersKey)
local maxPlayers = tonumber(redis.call('HGET', roomHashKey, 'max_players') or 0)

-- 5. 判断是否需要开始倒计时或恢复游戏
local shouldStartCountdown = 0
local countdownEndTime = 0
local currentRound = 0

if status == 1 then
	-- RoomStatusWaiting: 检查玩家数量是否达到最大值
	if playerCount >= maxPlayers then
		shouldStartCountdown = 1
		countdownEndTime = now + 3
		redis.call('HSET', roomHashKey, 'status', 2)
		redis.call('HSET', roomHashKey, 'countdown_end_time', countdownEndTime)
	end
elseif status == 4 then
	-- RoomStatusInterrupted: 检查玩家数量是否达到最大值
	if playerCount >= maxPlayers then
		shouldStartCountdown = 2
		currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
		redis.call('HSET', roomHashKey, 'status', 2)
	end
end

return {
	1,
	playerCount,
	maxPlayers,
	shouldStartCountdown,
	countdownEndTime,
	currentRound,
	cjson.encode(player),
	'success'
}
`

// LuaHandleSeatTimeout 处理座位超时
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey, userRoomKey]
// ARGV: [userID]
// 返回: {code, seatNo, message}
// code: 0=失败, 1=成功踢出, 2=用户已不是观众
const luaHandleSeatTimeout = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]
local userRoomKey = KEYS[6]

local userID = ARGV[1]

-- 1. 检查房间状态
local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status == 0 then
	return {0, 0, 'room not found'}
end

-- 2. 检查用户是否是观众
local spectatorData = redis.call('HGET', spectatorsKey, userID)
if not spectatorData then
	return {2, 0, 'user is not spectator'}
end

local spectator = cjson.decode(spectatorData)
local seatNo = spectator.seat_no or 0

-- 3. 检查用户是否已经变成玩家
local playerData = redis.call('HGET', playersKey, userID)
if playerData then
	return {2, seatNo, 'user already became player'}
end

-- 4. 删除观众
redis.call('HDEL', spectatorsKey, userID)

-- 5. 清理座位
if seatNo > 0 then
	redis.call('SETBIT', seatsKey, seatNo, 0)
	redis.call('HDEL', seatOwnerKey, tostring(seatNo))
end

-- 6. 清除用户与房间的关联
redis.call('DEL', userRoomKey)

return {1, seatNo, 'success'}
`

// LuaAutoSeatAndReady 自动选座并准备（合并 LuaSelectSeat + LuaPlayerReady 为单原子脚本）
// KEYS: [roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey]
// ARGV: [userID, now, isRobot]
// 返回: {code, seatNo, playerCount, maxPlayers, shouldStartCountdown, countdownEndTime, currentRound, playerData}
// code: 0=成功上座并准备, 73=无空座(LuaErrNoEmptySeat), 其它非 0=错误
const luaAutoSeatAndReady = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]

local userID = ARGV[1]
local now = tonumber(ARGV[2])
local isRobot = ARGV[3]

if redis.call('EXISTS', roomHashKey) == 0 then
	return {1, 0, 0, 0, 0, 0, 0, ''}
end

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status == 2 then
	return {6, 0, 0, 0, 0, 0, 0, ''}
end

local maxPlayers = tonumber(redis.call('HGET', roomHashKey, 'max_players') or 5)

local existingPlayer = redis.call('HGET', playersKey, userID)
if existingPlayer then
	return {9, 0, 0, 0, 0, 0, 0, ''}
end

local existingSpectator = redis.call('HGET', spectatorsKey, userID)
if not existingSpectator then
	return {14, 0, 0, 0, 0, 0, 0, ''}
end

local spectator = cjson.decode(existingSpectator)
local oldSeatNo = spectator.seat_no or 0

local targetSeatNo = 0
for i = 1, maxPlayers do
	if redis.call('GETBIT', seatsKey, i) == 0 then
		targetSeatNo = i
		break
	end
end

if targetSeatNo == 0 then
	return {73, 0, 0, 0, 0, 0, 0, ''}
end

if oldSeatNo > 0 and oldSeatNo ~= targetSeatNo then
	redis.call('SETBIT', seatsKey, oldSeatNo, 0)
	redis.call('HDEL', seatOwnerKey, tostring(oldSeatNo))
end

spectator.seat_no = targetSeatNo
spectator.seat_selected_at = now
if isRobot == '1' or isRobot == 'true' then
	spectator.is_robot = true
else
	spectator.is_robot = false
end

local player = {
	user_id = spectator.user_id,
	nickname = spectator.nickname,
	avatar = spectator.avatar,
	seat_no = targetSeatNo,
	disconnected_at = nil,
	is_robot = spectator.is_robot or false,
	ready_at = now
}

redis.call('SETBIT', seatsKey, targetSeatNo, 1)
redis.call('HSET', seatOwnerKey, tostring(targetSeatNo), userID)
redis.call('HDEL', spectatorsKey, userID)
redis.call('HSET', playersKey, userID, cjson.encode(player))

redis.call('EXPIRE', seatsKey, 86400)
redis.call('EXPIRE', seatOwnerKey, 86400)

local playerCount = redis.call('HLEN', playersKey)

local shouldStartCountdown = 0
local countdownEndTime = 0
local currentRound = 0

if status == 1 then
	if playerCount >= maxPlayers then
		shouldStartCountdown = 1
		countdownEndTime = now + 3
		redis.call('HSET', roomHashKey, 'status', 2)
		redis.call('HSET', roomHashKey, 'countdown_end_time', countdownEndTime)
	end
elseif status == 4 then
	if playerCount >= maxPlayers then
		shouldStartCountdown = 2
		currentRound = tonumber(redis.call('HGET', roomHashKey, 'current_round') or 0)
		redis.call('HSET', roomHashKey, 'status', 2)
	end
end

return {
	0,
	targetSeatNo,
	playerCount,
	maxPlayers,
	shouldStartCountdown,
	countdownEndTime,
	currentRound,
	cjson.encode(player)
}
`
