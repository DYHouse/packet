package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

type TimeoutType string

const (
	TimeoutTypeSeat    TimeoutType = "seat"
	TimeoutTypeReady   TimeoutType = "ready"
	TimeoutTypeGrab    TimeoutType = "grab"
	TimeoutTypeSend    TimeoutType = "send"
	TimeoutTypeReplace TimeoutType = "replace"
	TimeoutTypeRobot   TimeoutType = "robot"
)

type TimeoutConfig struct {
	Duration      time.Duration
	CheckInterval time.Duration
}

type Config struct {
	Seat    time.Duration
	Ready   time.Duration
	Grab    time.Duration
	Send    time.Duration
	Replace time.Duration
	Robot   time.Duration
}

var defaultCheckIntervals = map[TimeoutType]time.Duration{
	TimeoutTypeSeat:    1 * time.Second,
	TimeoutTypeReady:   500 * time.Millisecond,
	TimeoutTypeGrab:    500 * time.Millisecond,
	TimeoutTypeSend:    1 * time.Second,
	TimeoutTypeReplace: 1 * time.Second,
	TimeoutTypeRobot:   500 * time.Millisecond,
}

type TimeoutHandler func(ctx context.Context, roomID string, data string)

type TimeoutScheduler struct {
	redis    *cRedis.Client
	handlers map[TimeoutType]TimeoutHandler
	configs  map[TimeoutType]TimeoutConfig
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewTimeoutScheduler(redis *cRedis.Client, cfg *Config) *TimeoutScheduler {
	ctx, cancel := context.WithCancel(context.Background())

	configs := make(map[TimeoutType]TimeoutConfig)

	seatDuration := cfg.Seat
	if seatDuration == 0 {
		seatDuration = 30 * time.Second
	}
	configs[TimeoutTypeSeat] = TimeoutConfig{Duration: seatDuration, CheckInterval: defaultCheckIntervals[TimeoutTypeSeat]}

	readyDuration := cfg.Ready
	if readyDuration == 0 {
		readyDuration = 3 * time.Second
	}
	configs[TimeoutTypeReady] = TimeoutConfig{Duration: readyDuration, CheckInterval: defaultCheckIntervals[TimeoutTypeReady]}

	grabDuration := cfg.Grab
	if grabDuration == 0 {
		grabDuration = 20 * time.Second
	}
	configs[TimeoutTypeGrab] = TimeoutConfig{Duration: grabDuration, CheckInterval: defaultCheckIntervals[TimeoutTypeGrab]}

	sendDuration := cfg.Send
	if sendDuration == 0 {
		sendDuration = 30 * time.Second
	}
	configs[TimeoutTypeSend] = TimeoutConfig{Duration: sendDuration, CheckInterval: defaultCheckIntervals[TimeoutTypeSend]}

	replaceDuration := cfg.Replace
	if replaceDuration == 0 {
		replaceDuration = 30 * time.Second
	}
	configs[TimeoutTypeReplace] = TimeoutConfig{Duration: replaceDuration, CheckInterval: defaultCheckIntervals[TimeoutTypeReplace]}

	robotDuration := cfg.Robot
	if robotDuration == 0 {
		robotDuration = 5 * time.Second
	}
	configs[TimeoutTypeRobot] = TimeoutConfig{Duration: robotDuration, CheckInterval: defaultCheckIntervals[TimeoutTypeRobot]}

	return &TimeoutScheduler{
		redis:    redis,
		handlers: make(map[TimeoutType]TimeoutHandler),
		configs:  configs,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (s *TimeoutScheduler) RegisterHandler(timeoutType TimeoutType, handler TimeoutHandler) {
	s.handlers[timeoutType] = handler
}

func (s *TimeoutScheduler) Start() {
	for timeoutType, config := range s.configs {
		s.wg.Add(1)
		go s.runChecker(timeoutType, config)
	}
	logger.Info("timeout scheduler started")
}

func (s *TimeoutScheduler) Stop() {
	s.cancel()
	s.wg.Wait()
	logger.Info("timeout scheduler stopped")
}

func (s *TimeoutScheduler) SetTimeout(ctx context.Context, timeoutType TimeoutType, roomID, data string, customDuration ...time.Duration) {
	config, exists := s.configs[timeoutType]
	if !exists {
		logger.Warn("unknown timeout type", "type", timeoutType)
		return
	}

	duration := config.Duration
	if len(customDuration) > 0 {
		duration = customDuration[0]
	}

	expireAt := time.Now().Add(duration).Unix()
	key := s.getTimeoutKey(timeoutType)
	member := fmt.Sprintf("%s:%s", roomID, data)

	err := s.redis.ZAdd(ctx, key, cRedis.Z{
		Score:  float64(expireAt),
		Member: member,
	}).Err()
	if err != nil {
		logger.Error("failed to set timeout", "type", timeoutType, "room_id", roomID, "error", err)
	}
}

func (s *TimeoutScheduler) ClearTimeout(ctx context.Context, timeoutType TimeoutType, roomID, data string) {
	key := s.getTimeoutKey(timeoutType)
	member := fmt.Sprintf("%s:%s", roomID, data)
	s.redis.ZRem(ctx, key, member)
}

func (s *TimeoutScheduler) ClearRoomTimeouts(ctx context.Context, timeoutType TimeoutType, roomID string) {
	key := s.getTimeoutKey(timeoutType)
	members, err := s.redis.ZRangeByScore(ctx, key, &cRedis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}).Result()
	if err != nil {
		return
	}
	for _, member := range members {
		if strings.HasPrefix(member, roomID+":") {
			s.redis.ZRem(ctx, key, member)
		}
	}
}

func (s *TimeoutScheduler) ClearAllTimeouts(ctx context.Context, roomID string) {
	for timeoutType := range s.configs {
		s.ClearRoomTimeouts(ctx, timeoutType, roomID)
	}
}

func (s *TimeoutScheduler) ClearAllRoomTimeouts(ctx context.Context, roomID string) {
	for timeoutType := range s.configs {
		s.ClearRoomTimeouts(ctx, timeoutType, roomID)
	}
}

func (s *TimeoutScheduler) ClearAllUserTimeouts(ctx context.Context, roomID, userID string) {
	for timeoutType := range s.configs {
		s.ClearTimeout(ctx, timeoutType, roomID, userID)
	}
}

func (s *TimeoutScheduler) getTimeoutKey(timeoutType TimeoutType) string {
	return redis.TimeoutKey(string(timeoutType))
}

func (s *TimeoutScheduler) runChecker(timeoutType TimeoutType, config TimeoutConfig) {
	defer s.wg.Done()

	ticker := time.NewTicker(config.CheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.checkTimeouts(timeoutType)
		}
	}
}

func (s *TimeoutScheduler) checkTimeouts(timeoutType TimeoutType) {
	key := s.getTimeoutKey(timeoutType)
	now := time.Now().Unix()

	members, err := s.redis.ZRangeByScore(s.ctx, key, &cRedis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", now),
	}).Result()

	if err != nil || len(members) == 0 {
		return
	}

	for _, member := range members {
		removed, _ := s.redis.ZRem(s.ctx, key, member).Result()
		if removed == 0 {
			continue
		}

		roomID, data := s.parseMember(member)
		if roomID == "" {
			continue
		}

		if handler, ok := s.handlers[timeoutType]; ok {
			logger.Info("timeout triggered", "type", timeoutType, "room_id", roomID, "data", data)
			go handler(s.ctx, roomID, data)
		}
	}
}

func (s *TimeoutScheduler) parseMember(member string) (string, string) {
	for i := 0; i < len(member); i++ {
		if member[i] == ':' {
			return member[:i], member[i+1:]
		}
	}
	return member, ""
}

func (s *TimeoutScheduler) GetRemainingTime(ctx context.Context, timeoutType TimeoutType, roomID, data string) time.Duration {
	key := s.getTimeoutKey(timeoutType)
	member := fmt.Sprintf("%s:%s", roomID, data)

	score, err := s.redis.ZScore(ctx, key, member).Result()
	if err != nil {
		return 0
	}

	remaining := time.Unix(int64(score), 0).Sub(time.Now())
	if remaining < 0 {
		return 0
	}

	return remaining
}
