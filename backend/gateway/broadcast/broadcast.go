package broadcast

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cashparty/backend/common/broadcast"
	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/gateway"
	"github.com/cashparty/backend/gateway/connection"
)

type roomUsersCacheEntry struct {
	users     []string
	expiresAt time.Time
}

type BroadcastService struct {
	manager  *connection.Manager
	redis    *cRedis.Client
	consumer broadcast.Consumer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	roomUsersCache sync.Map
	cacheTTL       time.Duration
}

func NewBroadcastService(
	manager *connection.Manager,
	redis *cRedis.Client,
	cfg *config.BroadcastConfig,
	kafkaBrokers []string,
	kafkaGroupID string,
) *BroadcastService {
	ctx, cancel := context.WithCancel(context.Background())

	service := &BroadcastService{
		manager:  manager,
		redis:    redis,
		ctx:      ctx,
		cancel:   cancel,
		cacheTTL: 5 * time.Second,
	}

	factory := broadcast.NewConsumerFactory(cfg, kafkaBrokers, kafkaGroupID, redis)

	consumer := factory.CreateConsumer(service.handleBroadcastMessage)

	service.consumer = consumer

	return service
}

func (s *BroadcastService) Start() error {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logger.Error("broadcast consumer panic",
					"panic", r, "stack", string(debug.Stack()))
			}
		}()

		if err := s.consumer.Start(s.ctx); err != nil {
			logger.Error("broadcast consumer stopped with error", "error", err)
		}
	}()

	logger.Info("broadcast service started")
	return nil
}

func (s *BroadcastService) handleBroadcastMessage(ctx context.Context, msg *message.BroadcastMessage) error {
	pushMsg := message.NewPushMessage(msg.Event, msg.Data)
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
	playersCmd := pipe.HGetAll(ctx, gateway.RoomPlayersKey(roomID))
	spectatorsCmd := pipe.HGetAll(ctx, gateway.RoomSpectatorsKey(roomID))

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

	return users, nil
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
