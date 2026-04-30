package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/redis/go-redis/v9"
)

const LuaEndGame = `
local roomHashKey = KEYS[1]
local playersKey = KEYS[2]
local spectatorsKey = KEYS[3]
local seatsKey = KEYS[4]
local seatOwnerKey = KEYS[5]

local now = tonumber(ARGV[1])
local allowedStatus = tonumber(ARGV[2]) or 2

local status = tonumber(redis.call('HGET', roomHashKey, 'status') or 0)

local statusAllowed = false
if allowedStatus == 0 then
	statusAllowed = (status ~= 1)
else
	statusAllowed = (status == allowedStatus)
end

if not statusAllowed then
	return {1, {}, status}
end

local playerIDs = redis.call('HKEYS', playersKey)
local results = {}

redis.call('HSET', roomHashKey, 'status', 1)
redis.call('HSET', roomHashKey, 'current_round', 0)
redis.call('HDEL', roomHashKey, 'next_sender_id')
redis.call('HDEL', roomHashKey, 'countdown_end_time')
redis.call('HDEL', roomHashKey, 'started_at')

redis.call('DEL', seatsKey)
redis.call('DEL', seatOwnerKey)

for _, playerID in ipairs(playerIDs) do
	local playerData = redis.call('HGET', playersKey, playerID)
	if playerData then
		local player = cjson.decode(playerData)
		
		table.insert(results, {
			playerID,
			player.nickname or ''
		})
		
		local spectator = {
			user_id = player.user_id,
			nickname = player.nickname,
			avatar = player.avatar,
			seat_no = player.seat_no or 0
		}
		
		redis.call('HSET', spectatorsKey, playerID, cjson.encode(spectator))
		redis.call('HDEL', playersKey, playerID)
		
		if spectator.seat_no > 0 then
			redis.call('SETBIT', seatsKey, spectator.seat_no, 1)
			redis.call('HSET', seatOwnerKey, tostring(spectator.seat_no), playerID)
		end
	end
end

redis.call('EXPIRE', seatsKey, 86400)
redis.call('EXPIRE', seatOwnerKey, 86400)

return {0, results, status}
`

func main() {
	roomID := flag.String("room", "", "Room ID to end")
	redisAddr := flag.String("redis", "127.0.0.1:6379", "Redis address")
	redisPassword := flag.String("password", "", "Redis password")
	redisDB := flag.Int("db", 0, "Redis DB")
	force := flag.Bool("force", false, "Force end regardless of status (allowedStatus=0)")
	flag.Parse()

	if *roomID == "" {
		fmt.Println("Usage: go run force_end_room.go -room=<room_id> [-redis=<addr>] [-password=<pwd>] [-db=<num>] [-force]")
		fmt.Println("\nOptions:")
		fmt.Println("  -room     Room ID to end (required)")
		fmt.Println("  -redis    Redis address (default: 127.0.0.1:6379)")
		fmt.Println("  -password Redis password (default: empty)")
		fmt.Println("  -db       Redis DB (default: 0)")
		fmt.Println("  -force    Force end regardless of room status (default: false)")
		fmt.Println("\nExamples:")
		fmt.Println("  go run force_end_room.go -room=12345")
		fmt.Println("  go run force_end_room.go -room=12345 -force")
		fmt.Println("  go run force_end_room.go -room=12345 -redis=192.168.1.100:6379 -password=mypassword")
		os.Exit(1)
	}

	ctx := context.Background()

	client := redis.NewClient(&redis.Options{
		Addr:     *redisAddr,
		Password: *redisPassword,
		DB:       *redisDB,
	})

	defer client.Close()

	roomHashKey := fmt.Sprintf("cashparty:room:hash:%s", *roomID)
	playersKey := fmt.Sprintf("cashparty:room:players:%s", *roomID)
	spectatorsKey := fmt.Sprintf("cashparty:room:spectators:%s", *roomID)
	seatsKey := fmt.Sprintf("cashparty:room:seats:%s", *roomID)
	seatOwnerKey := fmt.Sprintf("cashparty:room:seat_owner:%s", *roomID)

	fmt.Printf("Force ending room: %s\n", *roomID)
	fmt.Printf("Room hash key: %s\n", roomHashKey)

	status, err := client.HGet(ctx, roomHashKey, "status").Result()
	if err != nil {
		fmt.Printf("Error getting room status: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Current room status: %s\n", status)

	allowedStatus := 2
	if *force {
		allowedStatus = 0
		fmt.Println("Force mode: will end room regardless of status")
	}

	keys := []string{roomHashKey, playersKey, spectatorsKey, seatsKey, seatOwnerKey}
	args := []interface{}{time.Now().Unix(), allowedStatus}

	res, err := client.Eval(ctx, LuaEndGame, keys, args...).Slice()
	if err != nil {
		fmt.Printf("Error executing Lua script: %v\n", err)
		os.Exit(1)
	}

	code := converter.ParseInt(res[0])
	if code == 1 {
		statusVal := converter.ParseInt(res[2])
		fmt.Printf("Room cannot be ended. Current status: %d\n", statusVal)
		fmt.Println("Use -force flag to force end the room")
		os.Exit(0)
	}

	var results []struct {
		UserID   string
		Nickname string
	}
	if arr, ok := res[1].([]interface{}); ok {
		for _, item := range arr {
			if tuple, ok := item.([]interface{}); ok && len(tuple) >= 2 {
				results = append(results, struct {
					UserID   string
					Nickname string
				}{
					UserID:   converter.ParseString(tuple[0]),
					Nickname: converter.ParseString(tuple[1]),
				})
			}
		}
	}

	fmt.Printf("\nRoom ended successfully!\n")
	fmt.Printf("Players converted to spectators: %d\n", len(results))
	if len(results) > 0 {
		fmt.Println("\nPlayers:")
		for i, r := range results {
			fmt.Printf("  %d. %s (%s)\n", i+1, r.Nickname, r.UserID)
		}
	}

	newStatus, _ := client.HGet(ctx, roomHashKey, "status").Result()
	fmt.Printf("\nNew room status: %s\n", newStatus)
}
