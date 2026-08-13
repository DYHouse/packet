package broadcast

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cashparty/backend/common/broadcast"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/gateway/connection"
)

// maxRoomUsersCacheSize bounds the roomUsersCache to prevent unbounded memory
// growth. When exceeded the cache is cleared (simplified LRU eviction).
const maxRoomUsersCacheSize = 10000

type roomUsersCacheEntry struct {
	users     []string
	expiresAt time.Time
}

type BroadcastService struct {
	manager  *connection.Manager
	redis    cRedis.RedisClient
	consumer broadcast.Consumer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	roomUsersCache sync.Map
	cacheTTL       time.Duration
}

func NewBroadcastService(
	manager *connection.Manager,
	redis cRedis.RedisClient,
	cfg *config.BroadcastConfig,
	kafkaBrokers []string,
	kafkaGroupID string,
	kafkaSASL kafka.SASLConfig,
	kafkaTLS kafka.TLSConfig,
) *BroadcastService {
	ctx, cancel := context.WithCancel(context.Background())

	service := &BroadcastService{
		manager:  manager,
		redis:    redis,
		ctx:      ctx,
		cancel:   cancel,
		cacheTTL: 5 * time.Second,
	}

	factory := broadcast.NewConsumerFactory(cfg, kafkaBrokers, kafkaGroupID, kafkaSASL, kafkaTLS, redis)

	consumer := factory.CreateConsumer(service.handleBroadcastMessage)

	service.consumer = consumer

	return service
}

// Start runs the broadcast consumer with retry. It blocks until the consumer
// exits gracefully (ctx cancelled) or until retries are exhausted. On fatal
// failure the error is returned so the caller (Application) can surface it via
// its errChan.
func (s *BroadcastService) Start() error {
	logger.Info("broadcast service started")

	backoff := time.Second
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := s.ctx.Err(); err != nil {
			return nil
		}

		err := s.consumer.Start(s.ctx)
		if err == nil {
			return nil
		}
		if s.ctx.Err() != nil {
			return nil
		}

		logger.Error("broadcast consumer failed",
			"attempt", attempt,
			"max_retries", maxRetries,
			"error", err)

		if attempt < maxRetries {
			select {
			case <-s.ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			continue
		}

		return fmt.Errorf("broadcast consumer failed after %d attempts: %w", maxRetries, err)
	}
	return nil
}

func (s *BroadcastService) handleBroadcastMessage(ctx context.Context, msg *message.BroadcastMessage) error {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("handle broadcast message panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	pushMsg := message.NewPushMessage(msg.Event, msg.Data)
	pushMsg.TraceID = msg.TraceID // 透传 BroadcastMessage 的 TraceID,保持调用链路连续
	msgBytes, err := pushMsg.ToJSON()
	if err != nil {
		logger.Error("failed to marshal push message", "error", err)
		return err
	}

	switch msg.TargetType {
	case message.TargetTypeUser:
		if len(msg.UserIDs) > 0 {
			for _, userID := range msg.UserIDs {
				if s.manager.IsUserConnectedLocally(userID) {
					s.manager.BroadcastToUser(userID, msgBytes)
				}
			}
		} else if msg.TargetID != "" {
			if s.manager.IsUserConnectedLocally(msg.TargetID) {
				s.manager.BroadcastToUser(msg.TargetID, msgBytes)
			}
		}
	case message.TargetTypeRoom:
		s.broadcastToRoom(msg.TargetID, msgBytes, msg.ExcludeID)
	default:
		logger.Warn("unknown broadcast target type", "target_type", msg.TargetType)
	}

	return nil
}

func (s *BroadcastService) GetRoomUsers(ctx context.Context, roomID string) ([]string, error) {
	if entry, ok := s.roomUsersCache.Load(roomID); ok {
		cached := entry.(*roomUsersCacheEntry)
		if time.Now().Before(cached.expiresAt) {
			return cached.users, nil
		}
		s.roomUsersCache.Delete(roomID)
	}

	pipe := s.redis.Pipeline()
	playersCmd := pipe.HGetAll(ctx, rediskeys.RoomPlayersKey(roomID))
	spectatorsCmd := pipe.HGetAll(ctx, rediskeys.RoomSpectatorsKey(roomID))

	_, err := pipe.Exec(ctx)
	if err != nil {
		logger.Error("failed to get room users", "room_id", roomID, "error", err)
		return nil, err
	}

	players, _ := playersCmd.Result()
	spectators, _ := spectatorsCmd.Result()

	users := make([]string, 0, len(players)+len(spectators))
	for userID := range players {
		users = append(users, userID)
	}
	for userID := range spectators {
		users = append(users, userID)
	}

	s.roomUsersCache.Store(roomID, &roomUsersCacheEntry{
		users:     users,
		expiresAt: time.Now().Add(s.cacheTTL),
	})

	// Enforce max cache size to prevent unbounded memory growth.
	// Simplified LRU: when the cache exceeds the limit, clear all entries
	// and re-store only the current one.
	if s.roomUsersCacheExceedsLimit() {
		s.roomUsersCache.Range(func(key, _ interface{}) bool {
			s.roomUsersCache.Delete(key)
			return true
		})
		s.roomUsersCache.Store(roomID, &roomUsersCacheEntry{
			users:     users,
			expiresAt: time.Now().Add(s.cacheTTL),
		})
	}

	return users, nil
}

func (s *BroadcastService) roomUsersCacheExceedsLimit() bool {
	count := 0
	exceeded := false
	s.roomUsersCache.Range(func(_, _ interface{}) bool {
		count++
		if count > maxRoomUsersCacheSize {
			exceeded = true
			return false
		}
		return true
	})
	return exceeded
}

func (s *BroadcastService) broadcastToRoom(roomID string, messageData []byte, excludeUserID string) {
	ctx := s.ctx

	userIDs, err := s.GetRoomUsers(ctx, roomID)
	if err != nil {
		logger.Error("failed to get room users",
			"room_id", roomID,
			"error", err)
		return
	}

	localCount := 0
	for _, userID := range userIDs {
		if userID == excludeUserID {
			continue
		}

		if s.manager.IsUserConnectedLocally(userID) {
			s.manager.BroadcastToUser(userID, messageData)
			localCount++
		}
	}

	logger.Debug("broadcast to room completed",
		"room_id", roomID,
		"total_users", len(userIDs),
		"local_users", localCount)
}

func (s *BroadcastService) Close() {
	s.cancel()

	if s.consumer != nil {
		s.consumer.Close()
	}

	s.wg.Wait()
}

func (s *BroadcastService) Stop() {
	s.Close()
}
