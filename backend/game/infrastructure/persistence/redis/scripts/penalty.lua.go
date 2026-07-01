package scripts

// LuaHandlePenalty 处理惩罚
// KEYS: [penaltyCountKey, roomHashKey, playersKey, roundStateKey, penaltyRecordKey]
// ARGV: [userID, roomFee, now, penaltyType]
// 返回: {code, newCount, penaltyAmount, kickRequired}
const luaHandlePenalty = `
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
const luaDistributePenalty = `
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
