package scheduler

import (
	"context"
	"fmt"
	"runtime/debug"
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
	Seat           time.Duration
	Ready          time.Duration
	Grab           time.Duration
	Send           time.Duration
	Replace        time.Duration
	Robot          time.Duration
	CheckInterval  time.Duration
	HandlerTimeout time.Duration
}

type TimeoutHandler func(ctx context.Context, roomID string, data string)

type TimeoutScheduler struct {
	redis          *cRedis.Client
	handlers       map[TimeoutType]TimeoutHandler
	configs        map[TimeoutType]TimeoutConfig
	handlerTimeout time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	handlerWg      sync.WaitGroup // 跟踪 handler goroutine 退出，确保 Stop 时等待 handler 完成
}

func NewTimeoutScheduler(redis *cRedis.Client, cfg *Config) *TimeoutScheduler {
	checkInterval := cfg.CheckInterval
	if checkInterval == 0 {
		checkInterval = 1 * time.Second
	}

	handlerTimeout := cfg.HandlerTimeout
	if handlerTimeout == 0 {
		handlerTimeout = 30 * time.Second
	}

	configs := make(map[TimeoutType]TimeoutConfig)

	seatDuration := cfg.Seat
	if seatDuration == 0 {
		seatDuration = 30 * time.Second
	}
	configs[TimeoutTypeSeat] = TimeoutConfig{Duration: seatDuration, CheckInterval: checkInterval}

	readyDuration := cfg.Ready
	if readyDuration == 0 {
		readyDuration = 3 * time.Second
	}
	configs[TimeoutTypeReady] = TimeoutConfig{Duration: readyDuration, CheckInterval: checkInterval}

	grabDuration := cfg.Grab
	if grabDuration == 0 {
		grabDuration = 20 * time.Second
	}
	configs[TimeoutTypeGrab] = TimeoutConfig{Duration: grabDuration, CheckInterval: checkInterval}

	sendDuration := cfg.Send
	if sendDuration == 0 {
		sendDuration = 30 * time.Second
	}
	configs[TimeoutTypeSend] = TimeoutConfig{Duration: sendDuration, CheckInterval: checkInterval}

	replaceDuration := cfg.Replace
	if replaceDuration == 0 {
		replaceDuration = 30 * time.Second
	}
	configs[TimeoutTypeReplace] = TimeoutConfig{Duration: replaceDuration, CheckInterval: checkInterval}

	robotDuration := cfg.Robot
	if robotDuration == 0 {
		robotDuration = 5 * time.Second
	}
	configs[TimeoutTypeRobot] = TimeoutConfig{Duration: robotDuration, CheckInterval: checkInterval}

	return &TimeoutScheduler{
		redis:          redis,
		handlers:       make(map[TimeoutType]TimeoutHandler),
		configs:        configs,
		handlerTimeout: handlerTimeout,
	}
}

func (s *TimeoutScheduler) RegisterHandler(timeoutType TimeoutType, handler TimeoutHandler) {
	s.handlers[timeoutType] = handler
}

func (s *TimeoutScheduler) Name() string {
	return "timeout_scheduler"
}

func (s *TimeoutScheduler) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	for timeoutType, config := range s.configs {
		s.wg.Add(1)
		go s.runChecker(timeoutType, config)
	}
	logger.Info("timeout scheduler started")
	return nil
}

func (s *TimeoutScheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait() // 等 checker goroutine 退出

	// 等 handler goroutine 退出，带超时防止永久阻塞
	done := make(chan struct{})
	go func() {
		s.handlerWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		logger.Info("timeout scheduler stopped (all handlers completed)")
	case <-time.After(10 * time.Second):
		logger.Warn("timeout scheduler stop timeout, some handlers may still be running")
	}
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
	defer func() {
		if r := recover(); r != nil {
			logger.Error("run checker panic",
				"type", timeoutType, "panic", r, "stack", string(debug.Stack()))
		}
	}()

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
			s.handlerWg.Add(1)
			go func(handler TimeoutHandler, roomID, data string) {
				defer s.handlerWg.Done()
				defer func() {
					if r := recover(); r != nil {
						logger.Error("timeout handler panic",
							"type", timeoutType, "room_id", roomID, "data", data,
							"panic", r, "stack", string(debug.Stack()))
					}
				}()
				handlerCtx, cancel := context.WithTimeout(s.ctx, s.handlerTimeout)
				defer cancel()
				handler(handlerCtx, roomID, data)
			}(handler, roomID, data)
		}
	}
}

// parseMember 解析 ZSET member 为 (roomID, data)。
// 约定：member 格式为 "roomID:data"，以第一个 ':' 分隔；roomID 不得包含 ':'（否则会被截断）。
// 当前 data 由调用方构造（见 addTimeout），格式为 "userID:action:retryCount" 等，允许包含 ':'。
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
