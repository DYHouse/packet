package messaging

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cashparty/backend/common/kafka"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/trace"
	"github.com/cashparty/backend/game/domain"
)

type GameEventPublisher struct {
	producer *kafka.Producer
}

func NewGameEventPublisher(producer *kafka.Producer) *GameEventPublisher {
	return &GameEventPublisher{
		producer: producer,
	}
}

// PublishGameEvent 实现 domain.EventPublisher 接口。
func (p *GameEventPublisher) PublishGameEvent(ctx context.Context, event *domain.GameEvent) error {
	return p.publish(ctx, event)
}

// PublishRoomEvent 实现 domain.EventPublisher 接口，但 GameEventPublisher 不支持发布 RoomEvent。
func (p *GameEventPublisher) PublishRoomEvent(ctx context.Context, event *domain.RoomEvent) error {
	return fmt.Errorf("GameEventPublisher does not support PublishRoomEvent")
}

// PublishSessionStart 发布会话开始事件（语义化包装）。
func (p *GameEventPublisher) PublishSessionStart(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventSessionStart
	return p.publish(ctx, event)
}

// PublishPacketCreated 发布红包创建事件（语义化包装）。
func (p *GameEventPublisher) PublishPacketCreated(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventPacketCreated
	return p.publish(ctx, event)
}

// PublishRoundSettle 发布单轮结算事件（语义化包装）。
func (p *GameEventPublisher) PublishRoundSettle(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventRoundSettle
	return p.publish(ctx, event)
}

// PublishSessionEnd 发布会话结束事件（语义化包装）。
func (p *GameEventPublisher) PublishSessionEnd(ctx context.Context, event *domain.GameEvent) error {
	event.EventType = domain.GameEventSessionEnd
	return p.publish(ctx, event)
}

func (p *GameEventPublisher) publish(ctx context.Context, event *domain.GameEvent) error {
	// 优先使用 event 已有的 TraceID（GameAppService 显式设置的确定性 TraceID，同时作为 Consumer 幂等键）；
	// 其次从 context 提取 TraceID（实现端到端追踪）；
	// 两者都为空时自动生成（兜底，避免阻塞消息发送）。
	if event.TraceID == "" {
		event.TraceID = trace.FromContext(ctx)
	}
	if event.TraceID == "" {
		event.TraceID = trace.Generate()
		logger.Warn("event TraceID not in ctx, auto-generated",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"trace_id", event.TraceID)
	}
	event.EventHeader.FillIfEmpty()

	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event failed: %w", err)
	}

	key := fmt.Sprintf("%s_%s", event.RoomID, event.SessionID)

	if err := p.producer.Send(ctx, kafka.TopicGameEvents, []byte(key), data); err != nil {
		logger.Error("publish game event failed",
			"event_type", event.EventType,
			"room_id", event.RoomID,
			"session_id", event.SessionID,
			"trace_id", event.TraceID,
			"error", err)
		return err
	}

	logger.Info("game event published",
		"event_type", event.EventType,
		"room_id", event.RoomID,
		"session_id", event.SessionID,
		"trace_id", event.TraceID)

	return nil
}
