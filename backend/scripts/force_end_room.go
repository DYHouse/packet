package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cashparty/backend/common/converter"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	gameScripts "github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	"github.com/redis/go-redis/v9"
)

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

	rdb := redis.NewClient(&redis.Options{
		Addr:     *redisAddr,
		Password: *redisPassword,
		DB:       *redisDB,
	})

	defer rdb.Close()

	client := cRedis.NewClientFromRaw(rdb)

	roomHashKey := rediskeys.RoomHashKey(*roomID)
	playersKey := rediskeys.RoomPlayersKey(*roomID)
	spectatorsKey := rediskeys.RoomSpectatorsKey(*roomID)
	seatsKey := rediskeys.RoomSeatsKey(*roomID)
	seatOwnerKey := rediskeys.RoomSeatOwnerKey(*roomID)

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

	res, err := gameScripts.EndGame.Run(ctx, client, keys, args...).Slice()
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
