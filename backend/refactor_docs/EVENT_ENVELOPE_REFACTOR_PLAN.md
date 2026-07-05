# EventEnvelope 重构方案

> 生成时间:2026-07-05
> 适用范围:`/Users/aaron.pan/Desktop/party/RedPacket-master/backend` 下所有消息结构(RoomEvent / GameEvent / BroadcastMessage / PushMessage / EventEnvelope)
> 配套规约:`CODING_STANDARD.md`、`MQ_REFACTOR_PLAN.md`、`TRACEID_REFACTOR_PLAN.md`

---

## 一、问题诊断

### 1.1 核心问题:EventEnvelope 是死代码,从未落地

`common/message/event.go` 定义的 `EventEnvelope` 结构,在整个 backend 目录中**从未被任何业务代码引用**(仅在自身定义处和 `refactor_docs/` 规划文档中出现)。

**验证依据**:
- Grep `EventEnvelope` 在 `backend/` 目录 → 仅出现在 `common/message/event.go`(定义)、`refactor_docs/`(规划文档)、`BACKEND_AUDIT_REFACTOR.md`(审计文档)
- Publisher(`RoomEventPublisher` / `GameEventPublisher`)直接 `json.Marshal(event)` 序列化具体事件类型,**未使用 EventEnvelope 包装**
- Consumer(`RoomEventConsumer` / `GameEventConsumer`)直接 `json.Unmarshal` 解析具体事件类型,**未使用 EventEnvelope 解包**
- Broadcaster(`RedisPubSubBroadcaster` / `KafkaBroadcaster`)使用 `BroadcastMessage`,**未使用 EventEnvelope**

### 1.2 根因分析:为何 EventEnvelope 没用起来

| # | 原因 | 证据 |
|---|------|------|
| R1 | **各事件类型已自带完整元数据字段,EventEnvelope 无增量价值** | `RoomEvent` 已有 EventID/EventType/TraceID/Version/Timestamp;`GameEvent` 同样齐全;`BroadcastMessage` 也已补齐这四个字段 |
| R2 | **MQ_REFACTOR_PLAN 规划与实际实现路线偏离** | MQ_REFACTOR_PLAN §3.2.4 规划用 EventEnvelope 统一,但 Phase 3 实际实施时选择了"各事件类型自带字段"路线;TRACEID_REFACTOR_PLAN §3.7 明确"不改 envelope 结构,复用现有 RoomEvent.TraceID 字段" |
| R3 | **Publisher 实现简单粗暴:直接序列化具体类型** | `game_event_publisher.go:84` `data, err := json.Marshal(event)`;`room_event_publisher.go:66` 同样 |
| R4 | **没有迁移动力** | 各事件字段已齐全,TraceID 已通过 context 传播解决,幂等键已用 EventID/TraceID 解决,Version 字段已存在(虽未路由) |
| R5 | **EventEnvelope 设计与具体类型字段命名不一致** | EventEnvelope 用 `Payload`,GameEvent 用 `Data`,BroadcastMessage 用 `Data` — 即使想用 EventEnvelope 包装,字段映射也有歧义 |

### 1.3 实际存在的统一性问题(被 EventEnvelope 死代码掩盖)

虽然 EventEnvelope 是死代码,但背后暴露的真正问题是:**4 种消息格式并行存在,字段重复定义但命名/语义不统一**。

#### 1.3.1 四种消息格式现状对比

| 维度 | RoomEvent | GameEvent | BroadcastMessage | PushMessage | EventEnvelope(死代码) |
|---|---|---|---|---|---|
| **位置** | `game/domain/events.go` | `game/domain/events.go` | `common/message/broadcast.go` | `common/message/push.go` | `common/message/event.go` |
| **EventID** | ✅ string | ✅ string | ✅ string | ❌ 缺失 | ✅ string |
| **EventType** | ✅ RoomEventType(string) | ✅ GameEventType(string) | ✅ Event(string,命名不同) | ✅ Type(string,命名不同) | ✅ EventType(string) |
| **TraceID** | ✅ string | ✅ string | ✅ string | ❌ 缺失 | ✅ string |
| **Timestamp** | ✅ int64 毫秒 | ✅ int64 毫秒 | ✅ int64 毫秒 | ✅ int64 毫秒 | ✅ int64 毫秒 |
| **Version** | ✅ int(常量=1) | ✅ int(常量=1) | ✅ int(常量=1) | ❌ 缺失 | ✅ int(常量=1) |
| **Source** | ❌ 缺失 | ❌ 缺失 | ❌ 缺失 | ❌ 缺失 | ✅ string |
| **Payload 字段名** | `Payload` | `Data` | `Data` | `Data`(interface{}) | `Payload` |
| **Payload 类型** | json.RawMessage | json.RawMessage | json.RawMessage | interface{}(不安全) | json.RawMessage |
| **传输通道** | Kafka topic `room.events` | Kafka topic `game.events` | Redis Pub/Sub + Kafka topic `gateway.broadcast` | WS 直连推送 | 未使用 |
| **Version 路由** | ❌ Consumer 未按 Version 路由 | ❌ 同上 | ❌ 同上 | — | — |

#### 1.3.2 字段重复定义清单

| 字段 | 重复定义位置 | 重复次数 |
|---|---|---|
| `EventID` | RoomEvent / GameEvent / BroadcastMessage / EventEnvelope | 4 处 |
| `TraceID` | RoomEvent / GameEvent / BroadcastMessage / EventEnvelope | 4 处 |
| `Timestamp` | RoomEvent / GameEvent / BroadcastMessage / PushMessage / EventEnvelope | 5 处 |
| `Version` | RoomEvent / GameEvent / BroadcastMessage / EventEnvelope + 4 个独立常量 | 4 处 |
| `generateEventID()` / `uuid.New().String()` | domain/events.go / message/broadcast.go / message/event.go | 3 处实现 |

#### 1.3.3 PushMessage 是历史遗留问题

`PushMessage`(common/message/push.go)是最老的消息格式,缺少 EventID/TraceID/Version,且 `Data` 是 `interface{}` 类型(序列化时需二次 Marshal,违反 MQ_REFACTOR_PLAN P1-11)。目前在 `gateway/connection/manager.go` 和 `gateway/broadcast/broadcast.go` 中使用,与 BroadcastMessage 并行存在,导致 gateway 层消息格式混乱。

### 1.4 EventEnvelope 死代码的危害

1. **误导维护者**:看到 EventEnvelope 会以为它是"统一信封",实际从未生效,维护者可能错误地在新代码中使用它
2. **掩盖真实问题**:让人以为"统一信封已实现",实际 4 种格式并行、PushMessage 缺字段的问题被忽视
3. **违反 CODING_STANDARD**:死代码违反"Dead code must be removed"规约
4. **增加认知负担**:维护者需理解 5 种消息格式的关系,实际只有 4 种在用

---

## 二、方案选型

### 2.1 三个候选方案

#### 方案 A:删除 EventEnvelope,保持现状

**描述**:仅删除 `common/message/event.go` 死代码,不处理 4 种格式的统一性问题。

| 优点 | 缺点 |
|---|---|
| 改动最小,零风险 | 字段重复定义仍在;PushMessage 缺字段问题仍在;Version 路由缺失问题仍在 |
| 快速清理死代码 | 未来新增消息类型时仍会复制粘贴字段,问题累积 |

#### 方案 B:真正落地 EventEnvelope,统一所有消息

**描述**:用 EventEnvelope 统一包装所有消息,Payload 内放具体事件类型。Publisher 发送前包装,Consumer 接收后拆包。

```go
// 消息格式统一为:
{
  "event_id": "uuid",
  "event_type": "room.spectator_join",  // 跨域统一分类
  "trace_id": "tr_xxx",
  "timestamp": 1735689600000,
  "version": 1,
  "source": "game-service",
  "payload": { ... 具体事件内容 ... }
}
```

| 优点 | 缺点 |
|---|---|
| 真正实现"统一信封",未来扩展性好 | **改动巨大**:所有 Publisher/Consumer/Broadcaster 需改;消息格式不向后兼容;需双写过渡 |
| 跨域 EventType 统一分类,便于路由 | 现有 RoomEvent/GameEvent 已自带字段,包装后字段重复(EventID 在外层和内层都有) |
| Version 路由可统一在 envelope 层实现 | 与 TRACEID_REFACTOR_PLAN "不改 envelope 结构" 决策冲突 |

#### 方案 C:删除 EventEnvelope,提取共享 EventHeader,统一 PushMessage(推荐)

**描述**:
1. 删除死代码 EventEnvelope
2. 提取共享 `EventHeader` 结构,各事件类型嵌入它,消除字段重复
3. 废弃 PushMessage,统一改用 BroadcastMessage
4. 统一字段命名(Payload vs Data)
5. Consumer 端按 Version 路由(可选,后续迭代)

```go
// common/message/header.go(新增)
type EventHeader struct {
    EventID   string `json:"event_id"`
    TraceID   string `json:"trace_id"`
    Timestamp int64  `json:"timestamp"` // Unix 毫秒
    Version   int    `json:"version"`
}

// game/domain/events.go(改造)
type RoomEvent struct {
    EventHeader              // 嵌入共享 Header
    EventType RoomEventType `json:"event_type"`
    RoomID    string        `json:"room_id"`
    UserID    string        `json:"user_id,omitempty"`
    Payload   json.RawMessage `json:"payload,omitempty"`
}
```

| 优点 | 缺点 |
|---|---|
| 消除字段重复定义,单一真相源 | 需改造 4 种消息结构定义,但调用方代码基本不变 |
| 删除死代码,消除误导 | PushMessage 废弃需改 gateway 层调用方 |
| 保持现有消息格式向后兼容(字段名不变) | Version 路由仍需各 Consumer 单独实现(但 Header 统一后可提取公共路由函数) |
| 与 TRACEID_REFACTOR_PLAN 决策一致(不改 envelope 结构) | 不如方案 B 的跨域 EventType 统一分类 |
| 改动量适中,可分阶段验证 | — |

### 2.2 选型决策:方案 C

**理由**:
1. 方案 A 治标不治本,问题会持续累积
2. 方案 B 改动过大且与现有决策冲突,收益不成正比
3. 方案 C 消除重复、删除死代码、统一格式,改动可控且收益明确
4. 方案 C 与现有规约(MQ_REFACTOR_PLAN 已实现的字段、TRACEID_REFACTOR_PLAN 不改 envelope 的决策)对齐
5. 方案 C 的 EventHeader 嵌入方式是 Go 语言标准做法,符合 CODING_STANDARD §17 组合优于继承

---

## 三、重构方案设计(方案 C)

### 3.1 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│  common/message/                                            │
│  ├── header.go     [新增] EventHeader 共享结构 + 工厂函数   │
│  ├── broadcast.go  [改造] BroadcastMessage 嵌入 EventHeader │
│  ├── push.go       [废弃] 删除文件,PushMessage 迁移到       │
│  │                       BroadcastMessage                    │
│  ├── event.go     [删除] EventEnvelope 死代码               │
│  ├── payload.go   [保持]                                    │
│  ├── types.go     [保持]                                    │
│  ├── request.go   [保持]                                    │
│  ├── response.go  [保持]                                    │
│  └── errors.go    [保持]                                    │
└─────────────────────────────────────────────────────────────┘
                            ↓ 嵌入
┌─────────────────────────────────────────────────────────────┐
│  game/domain/events.go [改造]                               │
│  ├── RoomEvent 嵌入 EventHeader                             │
│  ├── GameEvent 嵌入 EventHeader                             │
│  └── 统一 Payload 字段名(GameEvent.Data → Payload)          │
└─────────────────────────────────────────────────────────────┘
                            ↓ 影响
┌─────────────────────────────────────────────────────────────┐
│  Publisher/Consumer/Broadcaster [改造调用方]                │
│  ├── game/infrastructure/messaging/*_publisher.go           │
│  ├── game/infrastructure/messaging/*_consumer.go            │
│  ├── common/broadcast/*.go                                  │
│  └── gateway/broadcast/broadcast.go                         │
│      gateway/connection/manager.go                          │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 核心组件:EventHeader

新建 `common/message/header.go`:

```go
package message

import (
	"time"

	"github.com/google/uuid"
)

// EventHeaderVersion 是当前所有事件消息的 schema 版本。
// 当事件结构发生破坏性变更时递增此版本号,Consumer 端按 Version 路由处理。
const EventHeaderVersion = 1

// EventHeader 是所有事件消息(RoomEvent/GameEvent/BroadcastMessage)的共享元数据头。
// 各事件类型通过嵌入此结构获得统一的 EventID/TraceID/Timestamp/Version 字段,
// 避免字段重复定义,确保单一真相源。
type EventHeader struct {
	EventID   string `json:"event_id"`
	TraceID   string `json:"trace_id"`
	Timestamp int64  `json:"timestamp"` // Unix 毫秒
	Version   int    `json:"version"`
}

// NewEventHeader 创建 EventHeader 并自动填充 EventID/Timestamp/Version。
// traceID 为空时保留空字符串,由调用方(Publisher)负责从 context 注入或自动生成。
func NewEventHeader(traceID string) EventHeader {
	return EventHeader{
		EventID:   uuid.New().String(),
		TraceID:   traceID,
		Timestamp: time.Now().UnixMilli(),
		Version:   EventHeaderVersion,
	}
}

// FillIfEmpty 在字段为零值时自动填充。
// Publisher 在发送前调用此方法,确保 EventID/Timestamp/Version 一定有值。
func (h *EventHeader) FillIfEmpty() {
	if h.EventID == "" {
		h.EventID = uuid.New().String()
	}
	if h.Timestamp == 0 {
		h.Timestamp = time.Now().UnixMilli()
	}
	if h.Version == 0 {
		h.Version = EventHeaderVersion
	}
}

// IsEmpty 判断 Header 是否为零值(未初始化)。
func (h *EventHeader) IsEmpty() bool {
	return h.EventID == "" && h.TraceID == "" && h.Timestamp == 0 && h.Version == 0
}
```

**设计要点**:
- `EventHeader` 是值类型(非指针),嵌入后字段直接属于外层结构,JSON 序列化/反序列化无嵌套层级
- `NewEventHeader` 是工厂函数,但允许调用方传入已知的 traceID
- `FillIfEmpty` 用于 Publisher 兜底:调用方可能只设置了部分字段,此方法补齐缺失项
- `EventHeaderVersion` 是全局唯一版本常量,替代原来的 `RoomEventVersion` / `GameEventVersion` / `BroadcastMessageVersion` / `EventEnvelopeVersion` 四个独立常量

### 3.3 RoomEvent / GameEvent 改造

改造 `game/domain/events.go`:

```go
// Before
type RoomEvent struct {
	EventID   string          `json:"event_id"`
	EventType RoomEventType   `json:"event_type"`
	RoomID    string          `json:"room_id"`
	UserID    string          `json:"user_id,omitempty"`
	TraceID   string          `json:"trace_id,omitempty"`
	Version   int             `json:"version"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// After
type RoomEvent struct {
	message.EventHeader              // 嵌入共享 Header
	EventType        RoomEventType   `json:"event_type"`
	RoomID           string          `json:"room_id"`
	UserID           string          `json:"user_id,omitempty"`
	Payload          json.RawMessage `json:"payload,omitempty"`
}

// Before
type GameEvent struct {
	EventID   string          `json:"event_id"`
	EventType GameEventType   `json:"event_type"`
	RoomID    string          `json:"room_id"`
	SessionID string          `json:"session_id"`
	RoundID   string          `json:"round_id,omitempty"`
	TraceID   string          `json:"trace_id"`
	Version   int             `json:"version"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// After
type GameEvent struct {
	message.EventHeader              // 嵌入共享 Header
	EventType        GameEventType   `json:"event_type"`
	RoomID           string          `json:"room_id"`
	SessionID        string          `json:"session_id"`
	RoundID          string          `json:"round_id,omitempty"`
	Payload          json.RawMessage `json:"payload"` // 统一字段名:Data → Payload
}
```

**关键变化**:
- 嵌入 `message.EventHeader`,字段名/JSON tag 保持不变(`event_id`/`trace_id`/`timestamp`/`version`)
- `GameEvent.Data` 字段重命名为 `Payload`,与 `RoomEvent.Payload` 统一(⚠️ 不向后兼容,见 §5.1)
- 删除 `RoomEventVersion` / `GameEventVersion` 常量,统一使用 `message.EventHeaderVersion`
- 构造函数改造:用 `message.NewEventHeader(traceID)` 替代手动设置 4 个字段

**构造函数改造示例**:

```go
// Before
func NewSpectatorJoinEvent(roomID, userID string, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventID:   generateEventID(),
		EventType: RoomEventSpectatorJoin,
		RoomID:    roomID,
		UserID:    userID,
		Version:   RoomEventVersion,
		Timestamp: time.Now().UnixMilli(),
	}
	_ = event.SetPayload(SpectatorJoinPayload{...})
	return event
}

// After
func NewSpectatorJoinEvent(roomID, userID string, nickname, avatar string) *RoomEvent {
	event := &RoomEvent{
		EventHeader: message.NewEventHeader(""),  // TraceID 由 Publisher 注入
		EventType:   RoomEventSpectatorJoin,
		RoomID:      roomID,
		UserID:      userID,
	}
	_ = event.SetPayload(SpectatorJoinPayload{...})
	return event
}
```

**ParseRoomEvent 改造**:

```go
// Before
func ParseRoomEvent(data []byte) (*RoomEvent, error) {
	var event RoomEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	if event.EventID == "" {
		event.EventID = generateEventID()
	}
	return &event, nil
}

// After
func ParseRoomEvent(data []byte) (*RoomEvent, error) {
	var event RoomEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	// 向前兼容:旧消息无 Version 字段时补默认值
	event.EventHeader.FillIfEmpty()
	return &event, nil
}
```

### 3.4 BroadcastMessage 改造

改造 `common/message/broadcast.go`:

```go
// Before
type BroadcastMessage struct {
	EventID    string          `json:"event_id"`
	TraceID    string          `json:"trace_id"`
	Timestamp  int64           `json:"timestamp"`
	Version    int             `json:"version"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id,omitempty"`
	UserIDs    []string        `json:"user_ids,omitempty"`
	ExcludeID  string          `json:"exclude_id,omitempty"`
	Event      string          `json:"event"`
	Data       json.RawMessage `json:"data"`
}

// After
type BroadcastMessage struct {
	message.EventHeader              // 嵌入共享 Header
	TargetType        string         `json:"target_type"`
	TargetID          string         `json:"target_id,omitempty"`
	UserIDs           []string       `json:"user_ids,omitempty"`
	ExcludeID         string         `json:"exclude_id,omitempty"`
	Event             string         `json:"event"`
	Data              json.RawMessage `json:"data"`
}
```

**关键变化**:
- 嵌入 `message.EventHeader`,字段名/JSON tag 保持不变
- 删除 `BroadcastMessageVersion` 常量,统一使用 `message.EventHeaderVersion`
- `NewBroadcastMessage` 工厂改造:用 `NewEventHeader` 替代手动设置

```go
// After
func NewBroadcastMessage(event string, data interface{}, targetType, targetID string, excludeID string) (*BroadcastMessage, error) {
	dataBytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &BroadcastMessage{
		EventHeader: NewEventHeader(""),
		TargetType:  targetType,
		TargetID:    targetID,
		ExcludeID:   excludeID,
		Event:       event,
		Data:        dataBytes,
	}, nil
}
```

### 3.5 废弃 PushMessage,统一改用 BroadcastMessage

**当前 PushMessage 使用位置**(Grep 确认):
- `gateway/connection/manager.go` — kick 通知等
- `gateway/broadcast/broadcast.go` — 转发广播到 WS 客户端

**改造策略**:

1. **删除 `common/message/push.go`**
2. **gateway 层所有 PushMessage 使用点改用 BroadcastMessage**

```go
// gateway/broadcast/broadcast.go Before
func (s *BroadcastService) handleBroadcastMessage(ctx context.Context, msg *message.BroadcastMessage) {
	// 解析 BroadcastMessage,转发给 WS 客户端
	pushMsg := message.NewPushMessage(msg.Event, msg.Data)
	pushData, _ := pushMsg.ToJSON()
	conn.Send(pushData)
}

// gateway/broadcast/broadcast.go After
func (s *BroadcastService) handleBroadcastMessage(ctx context.Context, msg *message.BroadcastMessage) {
	// 直接序列化 BroadcastMessage,转发给 WS 客户端
	// WS 客户端协议升级:从 {type, data, timestamp} → {event_id, trace_id, timestamp, version, event, data, ...}
	pushData, err := msg.Marshal()
	if err != nil {
		logger.Error("marshal broadcast message failed", "error", err)
		return
	}
	conn.Send(pushData)
}
```

```go
// gateway/connection/manager.go Before(kick 通知)
kickMsg := message.NewPushMessage("kicked", kickData)
data, _ := kickMsg.ToJSON()
conn.Send(data)

// gateway/connection/manager.go After
kickMsg, err := message.NewUserBroadcastMessage([]string{userID}, "kicked", kickData)
if err != nil { ... }
data, err := kickMsg.Marshal()
if err != nil { ... }
conn.Send(data)
```

**⚠️ 前端协议兼容性**:此改造会改变 WS 客户端收到的消息格式(从 `{type, data, timestamp}` → `{event_id, trace_id, timestamp, version, event, data, ...}`),需与前端同步升级。详见 §5.2。

### 3.6 Publisher 改造

改造 `game/infrastructure/messaging/room_event_publisher.go` 和 `game_event_publisher.go`:

```go
// Before (room_event_publisher.go:42-64)
func (p *RoomEventPublisher) publish(ctx context.Context, event *domain.RoomEvent) error {
	if event.TraceID == "" {
		event.TraceID = trace.FromContext(ctx)
	}
	if event.TraceID == "" {
		event.TraceID = trace.Generate()
		logger.Warn("event TraceID not in ctx, auto-generated", ...)
	}
	if event.EventID == "" {
		event.EventID = generateEventID()
	}
	if event.Version == 0 {
		event.Version = domain.RoomEventVersion
	}
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}
	// ...
}

// After
func (p *RoomEventPublisher) publish(ctx context.Context, event *domain.RoomEvent) error {
	// 优先使用 event 已有的 TraceID;其次从 context 提取;最后自动生成
	if event.TraceID == "" {
		event.TraceID = trace.FromContext(ctx)
	}
	if event.TraceID == "" {
		event.TraceID = trace.Generate()
		logger.Warn("event TraceID not in ctx, auto-generated", ...)
	}
	// 统一通过 EventHeader.FillIfEmpty 补齐 EventID/Timestamp/Version
	event.EventHeader.FillIfEmpty()
	// ...
}
```

**关键变化**:
- 删除 Publisher 中对 EventID/Version/Timestamp 的单独设置逻辑
- 统一调用 `EventHeader.FillIfEmpty()` 补齐缺失字段
- TraceID 仍在 Publisher 层注入(因为需要从 context 提取,EventHeader 无法感知 context)

### 3.7 Consumer 改造(可选:Version 路由)

改造 `game/infrastructure/messaging/room_event_consumer.go` 和 `game_event_consumer.go`:

```go
// After: 增加 Version 路由
func (c *RoomEventConsumer) handleMessage(ctx context.Context, msg kafka.Message) error {
	event, err := domain.ParseRoomEvent(msg.Value)
	if err != nil {
		return fmt.Errorf("parse room event failed: %w", err)
	}

	// Version 路由:未来版本演进时按 Version 分发处理
	switch event.Version {
	case 1:
		// 当前版本,正常处理
	default:
		// 未来版本,记 Warn 并跳过(或投 DLQ)
		logger.Warn("unsupported event version, skip",
			"event_id", event.EventID,
			"version", event.Version,
			"event_type", event.EventType)
		return nil
	}

	// 从 event 恢复 TraceID 到 context
	if event.TraceID != "" {
		ctx = trace.WithTraceID(ctx, event.TraceID)
	}
	// ... 后续处理 ...
}
```

**此改造为可选**,当前 Version=1 无需路由,但为未来演进预留扩展点。

---

## 四、改动文件清单

### 4.1 新增文件

| 文件 | 用途 |
|---|---|
| `common/message/header.go` | EventHeader 共享结构 + 工厂函数 |
| `common/message/header_test.go` | EventHeader 单元测试 |

### 4.2 删除文件

| 文件 | 理由 |
|---|---|
| `common/message/event.go` | EventEnvelope 死代码,从未落地 |
| `common/message/push.go` | PushMessage 历史遗留,统一改用 BroadcastMessage |

### 4.3 改造文件

| 文件 | 改动内容 | 优先级 |
|---|---|---|
| `common/message/broadcast.go` | BroadcastMessage 嵌入 EventHeader;删除 `BroadcastMessageVersion` 常量;`NewBroadcastMessage` 用 `NewEventHeader` | P0 |
| `game/domain/events.go` | RoomEvent/GameEvent 嵌入 EventHeader;GameEvent.Data → Payload;删除 `RoomEventVersion`/`GameEventVersion` 常量;10 个构造函数改造;`ParseRoomEvent` 改用 `FillIfEmpty` | P0 |
| `game/infrastructure/messaging/room_event_publisher.go` | 删除单独的 EventID/Version/Timestamp 设置,改用 `event.EventHeader.FillIfEmpty()` | P0 |
| `game/infrastructure/messaging/game_event_publisher.go` | 同上 | P0 |
| `game/infrastructure/messaging/room_event_consumer.go` | (可选)增加 Version 路由 | P1 |
| `game/infrastructure/messaging/game_event_consumer.go` | (可选)增加 Version 路由 | P1 |
| `gateway/broadcast/broadcast.go` | `handleBroadcastMessage` 改用 `msg.Marshal()` 直接送 WS;删除 PushMessage 使用 | P0 |
| `gateway/connection/manager.go` | kick 通知改用 `BroadcastMessage` | P0 |
| `common/broadcast/redis_pubsub_broadcaster.go` | 无字段访问改动(已通过 `NewBroadcastMessage` 工厂创建) | — |
| `common/broadcast/kafka_broadcaster.go` | 同上 | — |

### 4.4 不改动的文件

| 文件 | 原因 |
|---|---|
| `common/message/payload.go` | Push payload 结构不变 |
| `common/message/types.go` | 命令/推送类型常量不变 |
| `common/message/request.go` / `response.go` | 请求/响应结构不变 |
| `common/message/errors.go` | 错误码不变(单独清理见 DEAD_CODE_REFACTOR_PLAN) |
| `game/application/*_app_service.go` | 调用 `NewBroadcastMessage` / `NewXxxEvent` 工厂,工厂内部改造,调用方无感 |
| `settlement/service/*` | 不直接使用消息结构 |

---

## 五、向后兼容性与风险

### 5.1 GameEvent.Data → Payload 的不兼容性

**问题**:`GameEvent.Data` 字段重命名为 `Payload`,JSON tag 从 `data` → `payload`,旧消息无法解析。

**影响范围**:
- Kafka topic `cashparty.game.events` 中已有的消息(但 Kafka 消息通常短期消费,7 天后过期)
- GameEventConsumer 解析逻辑
- 任何持久化存储中已有的 GameEvent JSON

**缓解策略**:
1. **双写过渡期**(推荐):
   - 阶段一:GameEvent 同时保留 `Data` 和 `Payload` 字段,JSON tag 分别为 `data` 和 `payload_new`,Publisher 写入时两者都写,Consumer 读取时优先读 `Payload`,空则读 `Data`
   - 阶段二:Kafka 消息全部替换后,删除 `Data` 字段,JSON tag 改为 `payload`
2. **直接改名 + 接受短暂不兼容**:
   - 部署时机选择低峰期
   - 部署前确认 Kafka 中无未消费的 GameEvent(Consumer group lag = 0)
   - 部署后旧消息解析失败进 DLQ,人工处理

**推荐**:策略 2(直接改名),因为 GameEvent 是内部消息,无外部消费者,Kafka 消费 lag 可控。

### 5.2 PushMessage 废弃的前端协议兼容性

**问题**:WS 客户端收到的消息格式从 `{type, data, timestamp}` → `{event_id, trace_id, timestamp, version, event, data, ...}`,前端需同步升级。

**影响范围**:
- 所有 WS 客户端(浏览器 / 移动端)
- 前端消息解析逻辑

**缓解策略**:
1. **前端先行升级**:
   - 前端先发布版本,同时兼容新旧格式(检测 `event` 字段存在则用新格式,否则回退到 `type` 字段)
   - 前端全量升级后,后端再废弃 PushMessage
2. **Gateway 层适配**(若前端无法立即升级):
   - 在 `handleBroadcastMessage` 中将 `BroadcastMessage` 转换为旧格式 `PushMessage` 再发送
   - 保留 PushMessage 仅作为 gateway 出口适配器,内部统一用 BroadcastMessage

**推荐**:策略 1(前端先行升级),需与前端团队协调。若短期无法协调,采用策略 2 作为过渡。

### 5.3 风险评估

| 风险 | 影响 | 缓解 |
|---|---|---|
| GameEvent.Data → Payload 不兼容 | Consumer 解析旧消息失败 | 部署前确认 Kafka lag=0;或采用双写过渡 |
| PushMessage 废弃影响前端 | 前端解析失败 | 前端先行升级;或 gateway 层适配 |
| EventHeader 嵌入导致 JSON 字段顺序变化 | 严格依赖字段顺序的解析器失败(罕见) | JSON 规范不保证字段顺序,正常解析器不受影响 |
| 构造函数改造遗漏 | 部分事件 EventID/Timestamp 为空 | Publisher 的 `FillIfEmpty` 兜底;单测覆盖 |
| 删除 EventEnvelope 误伤 | 若有未发现的引用 | Grep 已确认仅自身定义和文档引用,无业务代码 |

### 5.4 回滚策略

- 每个阶段独立提交,可单独 revert
- GameEvent.Data → Payload 改名可通过恢复 `Data` 字段回滚
- PushMessage 废弃可通过恢复 `push.go` 文件回滚
- EventHeader 嵌入可通过恢复原字段定义回滚(JSON tag 不变,字段顺序可能略不同但语义一致)

---

## 六、实施计划

### Phase 1:删除 EventEnvelope 死代码(低风险)

**目标**:清理死代码,消除误导。

**任务**:
1. 删除 `common/message/event.go`
2. 删除 `common/message/errors.go` 中对 EventEnvelope 的引用(如有)
3. 验证编译通过

**验证**:
```bash
go build ./common/message/... ./game/... ./gateway/...
go test ./common/message/... ./game/domain/...
```

### Phase 2:提取 EventHeader,改造消息结构

**目标**:消除字段重复定义,单一真相源。

**任务**:
1. 新建 `common/message/header.go`(EventHeader + NewEventHeader + FillIfEmpty)
2. 改造 `common/message/broadcast.go`:BroadcastMessage 嵌入 EventHeader
3. 改造 `game/domain/events.go`:RoomEvent/GameEvent 嵌入 EventHeader
4. **暂不**改 GameEvent.Data → Payload(留到 Phase 4)
5. 改造 10 个 RoomEvent 构造函数 + GameEvent 构造逻辑
6. 改造 Publisher:`event.EventHeader.FillIfEmpty()` 替代单独字段设置
7. 验证编译 + 单测

**验证**:
```bash
go build ./...
go test ./common/message/... ./game/domain/... ./game/infrastructure/messaging/...
# JSON 字段不变验证
go test -run TestRoomEventJSONCompat ./game/domain/
go test -run TestBroadcastMessageJSONCompat ./common/message/
```

### Phase 3:废弃 PushMessage,统一 BroadcastMessage

**目标**:消除 PushMessage 历史遗留,统一消息格式。

**前置条件**:与前端团队协调确认升级计划(见 §5.2)。

**任务**:
1. 改造 `gateway/broadcast/broadcast.go`:`handleBroadcastMessage` 直接用 `msg.Marshal()` 发送 WS
2. 改造 `gateway/connection/manager.go`:kick 通知改用 BroadcastMessage
3. 删除 `common/message/push.go`
4. 验证 gateway 层单测

**验证**:
```bash
go build ./gateway/...
go test ./gateway/...
# 前端联调验证 WS 消息格式
```

### Phase 4:统一 GameEvent 字段名(可选,需协调部署时机)

**目标**:统一 GameEvent.Payload 字段名,与 RoomEvent 对齐。

**任务**:
1. 改造 `game/domain/events.go`:GameEvent.Data → Payload(JSON tag: `data` → `payload`)
2. 改造 `game/infrastructure/messaging/game_event_consumer.go`:解析逻辑同步
3. 改造任何引用 `event.Data` 的代码 → `event.Payload`
4. 部署前确认 Kafka game.events topic lag=0
5. 验证端到端 GameEvent 流转

**验证**:
```bash
go build ./...
go test ./game/...
# 集成测试:发送 GameEvent,验证 Consumer 正常处理
```

### Phase 5:Version 路由机制(可选,未来演进)

**目标**:为消息 schema 演进预留扩展点。

**任务**:
1. 改造 Consumer:增加 `switch event.Version` 路由
2. 文档化版本演进流程

**此阶段为可选**,当前 Version=1 无需实施。

---

## 七、验证清单

### 7.1 死代码清理验证

- [ ] `common/message/event.go` 文件不存在
- [ ] `common/message/push.go` 文件不存在
- [ ] `grep -rn "EventEnvelope" backend/` → 0 matches(除 refactor_docs)
- [ ] `grep -rn "PushMessage" backend/` → 0 matches(除 refactor_docs)

### 7.2 EventHeader 统一验证

- [ ] `grep -rn "type EventHeader struct" backend/common/message/` → 1 match
- [ ] `grep -rn "message.EventHeader" backend/` → 出现在 RoomEvent/GameEvent/BroadcastMessage 定义处
- [ ] `grep -rn "EventID.*string.*json:\"event_id\"" backend/game/domain/events.go backend/common/message/broadcast.go` → 0 matches(字段已移到 EventHeader)
- [ ] `grep -rn "RoomEventVersion\|GameEventVersion\|BroadcastMessageVersion\|EventEnvelopeVersion" backend/` → 0 matches(统一为 EventHeaderVersion)

### 7.3 JSON 兼容性验证

- [ ] `RoomEvent` JSON 序列化后包含 `event_id`/`trace_id`/`timestamp`/`version` 字段(顶层,非嵌套)
- [ ] `GameEvent` JSON 序列化后包含 `event_id`/`trace_id`/`timestamp`/`version` 字段(顶层,非嵌套)
- [ ] `BroadcastMessage` JSON 序列化后包含 `event_id`/`trace_id`/`timestamp`/`version` 字段(顶层,非嵌套)
- [ ] 旧格式 JSON(无 version 字段)能被 `ParseRoomEvent` / `ParseBroadcastMessage` 正常解析,Version 默认填充为 1

### 7.4 Publisher 兜底验证

- [ ] 构造函数不设置 EventID 时,Publisher `FillIfEmpty` 补齐
- [ ] 构造函数不设置 Timestamp 时,Publisher `FillIfEmpty` 补齐
- [ ] 构造函数不设置 Version 时,Publisher `FillIfEmpty` 补齐为 1
- [ ] TraceID 仍由 Publisher 从 context 注入(不受 EventHeader 影响)

### 7.5 编译与测试

- [ ] `go build ./...` 通过
- [ ] `go vet ./common/message/... ./game/domain/... ./game/infrastructure/messaging/... ./gateway/...` 通过
- [ ] `go test ./...` 全部通过
- [ ] `go test -race ./game/... ./gateway/...` 通过

---

## 八、预期收益

| 指标 | 当前 | 重构后 |
|---|---|---|
| 消息格式种类 | 5 种(RoomEvent/GameEvent/BroadcastMessage/PushMessage/EventEnvelope) | 3 种(RoomEvent/GameEvent/BroadcastMessage) |
| 死代码文件 | 1 个(event.go) | 0 |
| 历史遗留格式 | 1 个(PushMessage) | 0 |
| EventID/TraceID/Timestamp/Version 字段重复定义 | 4 处 | 1 处(EventHeader) |
| 版本号常量 | 4 个(RoomEventVersion/GameEventVersion/BroadcastMessageVersion/EventEnvelopeVersion) | 1 个(EventHeaderVersion) |
| generateEventID 实现 | 3 处(domain/events.go/message/broadcast.go/message/event.go) | 1 处(message/header.go) |
| 字段命名一致性 | Payload/Data 混用 | 统一为 Payload(Phase 4 后) |
| WS 客户端消息格式 | {type, data, timestamp}(缺 event_id/trace_id/version) | {event_id, trace_id, timestamp, version, event, data, ...} |
| 代码可维护性 | 5 种格式认知负担重 | 3 种格式 + 共享 Header,认知清晰 |

---

## 九、与现有规约的对齐

| 现有规约 | 本方案对齐方式 |
|---|---|
| `CODING_STANDARD.md` "Dead code must be removed" | 删除 EventEnvelope 死代码 |
| `MQ_REFACTOR_PLAN.md` §3.2.4 EventEnvelope 规划 | 本方案承认 EventEnvelope 未落地,改为 EventHeader 嵌入方式实现"统一字段"目标;原 MQ_REFACTOR_PLAN 的 EventEnvelope 设计被本方案替代 |
| `MQ_REFACTOR_PLAN.md` P1-11 消除 interface{} 二次序列化 | 废弃 PushMessage(其 Data 是 interface{}),统一用 BroadcastMessage(Data 是 json.RawMessage) |
| `MQ_REFACTOR_PLAN.md` P1-12 BroadcastMessage 无 event_id/trace_id | BroadcastMessage 已有这些字段(✅ 已实现),本方案进一步统一到 EventHeader |
| `MQ_REFACTOR_PLAN.md` P2-11 无 schema 版本字段 | EventHeader.Version 已存在,本方案补充 Consumer 端 Version 路由(Phase 5) |
| `TRACEID_REFACTOR_PLAN.md` §3.7 "不改 envelope 结构" | 本方案不改 RoomEvent.TraceID 字段语义,仅提取共享 Header,TraceID 行为不变 |
| `TRACEID_REFACTOR_PLAN.md` TraceID 由 Publisher 注入 | 本方案保持:TraceID 仍由 Publisher 从 context 注入,EventHeader 不感知 context |
| project memory "All messages must use unified envelope with event_id, trace_id, timestamp, version" | 本方案通过 EventHeader 嵌入实现"unified envelope"目标,而非独立的 EventEnvelope 包装层 |

---

## 十、决策记录

| # | 决策 | 理由 |
|---|------|------|
| D1 | 删除 EventEnvelope 而非真正落地 | EventEnvelope 从未落地,各事件类型已自带字段;落地需大改且与 TRACEID_REFACTOR_PLAN 决策冲突;EventHeader 嵌入方式更轻量 |
| D2 | 提取 EventHeader 而非保持字段重复 | 单一真相源;消除 4 处字段重复;未来新增消息类型可直接嵌入 |
| D3 | 废弃 PushMessage 改用 BroadcastMessage | PushMessage 缺 event_id/trace_id/version,Data 是 interface{} 不安全;与 BroadcastMessage 功能重叠 |
| D4 | GameEvent.Data → Payload 统一命名 | 与 RoomEvent.Payload 对齐;但需协调部署时机(不向后兼容) |
| D5 | TraceID 仍由 Publisher 注入,不放入 EventHeader 工厂 | EventHeader 无法感知 context;Publisher 注入符合 TRACEID_REFACTOR_PLAN |
| D6 | Version 路由列为 Phase 5 可选 | 当前 Version=1 无需路由,但 Header 统一后可提取公共路由函数 |
| D7 | GameEvent.Data → Payload 采用直接改名而非双写 | GameEvent 是内部消息无外部消费者;Kafka lag 可控;双写增加复杂度收益不大 |

---

## 附录 A:EventHeader 嵌入后的 JSON 示例

### A.1 RoomEvent JSON

```json
{
  "event_id": "550e8400-e29b-41d4-a716-446655440000",
  "trace_id": "tr_1234567890",
  "timestamp": 1735689600000,
  "version": 1,
  "event_type": "spectator_join",
  "room_id": "room_001",
  "user_id": "user_123",
  "payload": {
    "nickname": "Alice",
    "avatar": "https://example.com/avatar.png"
  }
}
```

### A.2 GameEvent JSON(Phase 4 后)

```json
{
  "event_id": "550e8400-e29b-41d4-a716-446655440001",
  "trace_id": "GAME_SETTLE_session_001",
  "timestamp": 1735689600000,
  "version": 1,
  "event_type": "round_settle",
  "room_id": "room_001",
  "session_id": "session_001",
  "round_id": "round_005",
  "payload": {
    "round_no": 5,
    "sender_id": "user_123",
    "results": [...]
  }
}
```

### A.3 BroadcastMessage JSON

```json
{
  "event_id": "550e8400-e29b-41d4-a716-446655440002",
  "trace_id": "tr_1234567891",
  "timestamp": 1735689600000,
  "version": 1,
  "target_type": "room",
  "target_id": "room_001",
  "exclude_id": "user_123",
  "event": "round_start",
  "data": {
    "room_id": "room_001",
    "round_id": "round_005"
  }
}
```

**关键点**:三个 JSON 的 `event_id`/`trace_id`/`timestamp`/`version` 字段都在顶层(因 Go 嵌入字段提升),格式完全一致,便于统一的日志检索、幂等处理和 schema 路由。

---

**版本**:v1
**创建日期**:2026-07-05
**作者**:AI 辅助生成,基于 EventEnvelope 死代码分析与现有规约对齐
