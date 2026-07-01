package scripts

// LuaEnqueue 加入排队队列
// KEYS: [queueKey, spectatorsKey, playersKey, roomHashKey]
// ARGV: [userID, now]
// 返回: {code, queuePosition}
// code: 0=成功, 14=非观众(LuaErrUserNotInRoom), 9=已是玩家(LuaErrAlreadyPlayer), 70=已在队列(LuaErrAlreadyQueued), 72=机器人(LuaErrRobotNotAllowed)
const luaEnqueue = `
local queueKey = KEYS[1]
local spectatorsKey = KEYS[2]
local playersKey = KEYS[3]
local roomHashKey = KEYS[4]

local userID = ARGV[1]
local now = tonumber(ARGV[2])

if redis.call('EXISTS', roomHashKey) == 0 then
	return {1, 0}
end

local existingPlayer = redis.call('HGET', playersKey, userID)
if existingPlayer then
	return {9, 0}
end

local existingSpectator = redis.call('HGET', spectatorsKey, userID)
if not existingSpectator then
	return {14, 0}
end

local spectator = cjson.decode(existingSpectator)
if spectator.is_robot then
	return {72, 0}
end

local existingScore = redis.call('ZSCORE', queueKey, userID)
if existingScore then
	return {70, 0}
end

redis.call('ZADD', queueKey, now, userID)
redis.call('EXPIRE', queueKey, 86400)

local rank = redis.call('ZRANK', queueKey, userID)
return {0, rank + 1}
`

// LuaDequeue 从排队队列移除
// KEYS: [queueKey, roomHashKey]
// ARGV: [userID]
// 返回: {code}
// code: 0=成功, 71=不在队列(LuaErrNotQueued)
const luaDequeue = `
local queueKey = KEYS[1]
local roomHashKey = KEYS[2]

local userID = ARGV[1]

if redis.call('EXISTS', roomHashKey) == 0 then
	return {1}
end

local removed = redis.call('ZREM', queueKey, userID)
if removed == 0 then
	return {71}
end

return {0}
`

// LuaAutoSubstitute 座位释放后从队列队首自动替补
// KEYS: [queueKey, roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey]
// ARGV: [seatNo, now]
// 返回: {code, substituteUserID, playerCount, maxPlayers, shouldStartCountdown, countdownEndTime, currentRound, playerData}
// code: 0=替补成功, 73=无空座(忽略,座位已被占), 75=队列空无替补(自定义), 74=替补失败(LuaErrSubstituteFail)
const luaAutoSubstitute = `
local queueKey = KEYS[1]
local roomHashKey = KEYS[2]
local playersKey = KEYS[3]
local spectatorsKey = KEYS[4]
local seatsKey = KEYS[5]
local seatOwnerKey = KEYS[6]

local seatNo = tonumber(ARGV[1])
local now = tonumber(ARGV[2])

if redis.call('EXISTS', roomHashKey) == 0 then
	return {1, '', 0, 0, 0, 0, 0, ''}
end

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)
if status == 0 then
	return {6, '', 0, 0, 0, 0, 0, ''}
end

if redis.call('GETBIT', seatsKey, seatNo) == 1 then
	return {73, '', 0, 0, 0, 0, 0, ''}
end

local maxPlayers = tonumber(redis.call('HGET', roomHashKey, 'max_players') or 5)

local queueSize = redis.call('ZCARD', queueKey)
if queueSize == 0 then
	return {75, '', 0, 0, 0, 0, 0, ''}
end

local queueMembers = redis.call('ZRANGE', queueKey, 0, queueSize - 1, 'WITHSCORES')
local substituteUserID = ''
local substituteIndex = -1

for i = 1, #queueMembers, 2 do
	local candidateID = queueMembers[i]
	local spectatorData = redis.call('HGET', spectatorsKey, candidateID)
	if spectatorData then
		local spectator = cjson.decode(spectatorData)
		if not spectator.is_robot then
			local isPlayer = redis.call('HEXISTS', playersKey, candidateID)
			if isPlayer == 0 then
				substituteUserID = candidateID
				substituteIndex = i
				break
			end
		end
	end
end

if substituteUserID == '' then
	return {74, '', 0, 0, 0, 0, 0, ''}
end

local spectatorData = redis.call('HGET', spectatorsKey, substituteUserID)
local spectator = cjson.decode(spectatorData)

local oldSeatNo = spectator.seat_no or 0
if oldSeatNo > 0 and oldSeatNo ~= seatNo then
	redis.call('SETBIT', seatsKey, oldSeatNo, 0)
	redis.call('HDEL', seatOwnerKey, tostring(oldSeatNo))
end

local player = {
	user_id = spectator.user_id,
	nickname = spectator.nickname,
	avatar = spectator.avatar,
	seat_no = seatNo,
	disconnected_at = nil,
	is_robot = false,
	ready_at = now
}

redis.call('SETBIT', seatsKey, seatNo, 1)
redis.call('HSET', seatOwnerKey, tostring(seatNo), substituteUserID)
redis.call('HDEL', spectatorsKey, substituteUserID)
redis.call('HSET', playersKey, substituteUserID, cjson.encode(player))
redis.call('ZREM', queueKey, substituteUserID)

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
	substituteUserID,
	playerCount,
	maxPlayers,
	shouldStartCountdown,
	countdownEndTime,
	currentRound,
	cjson.encode(player)
}
`
