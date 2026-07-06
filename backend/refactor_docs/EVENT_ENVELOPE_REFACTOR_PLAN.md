# EventEnvelope 重构方案

> 生成时间:2026-07-05
> 最近更新:2026-07-06(采用 E2 方案:PushMessage 补字段保 type,前端零改动)
> 适用范围:
> - 后端:`/Users/aaron.pan/Desktop/party/RedPacket-master/backend` 下所有消息结构(RoomEvent / GameEvent / BroadcastMessage / PushMessage / EventEnvelope)
> - 前端:`/Users/aaron.pan/Desktop/party/gogain/packages/gift-box` **无需改动**(WS 推送保留 `type` 字段名,前端 16 个 type 分支全部继续可用)
> 配套规约:`CODING_STANDARD.md`、`MQ_REFACTOR_PLAN.md`、`TRACEID_REFACTOR_PLAN.md`、gogain `websocket_protocol.md`、gogain `packages/gift-box/docs/room-ws-flow.md`

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

#### 1.3.3 PushMessage 是历史遗留问题(E2 方案不删除,改为补字段)

`PushMessage`(common/message/push.go)是最老的消息格式,缺少 EventID/TraceID/Version,且 `Data` 是 `interface{}` 类型(序列化时需二次 Marshal,违反 MQ_REFACTOR_PLAN P1-11)。目前在 `gateway/connection/manager.go` 和 `gateway/broadcast/broadcast.go` 中使用,与 BroadcastMessage 并行存在,导致 gateway 层消息格式混乱。

**E2 方案决策**:**不删除 PushMessage**,而是给它补齐 EventHeader 字段 + Data 改 json.RawMessage,但**保留 `type` 字段名不变**(不改为 `event`)。理由:
1. 前端 `gogain/packages/gift-box` 的 `parseWsInboundMessage` 判别器是 `typeof o.type === 'string'`,改字段名会破坏前端 16 个 type 分支
2. 前端零改动可避免前后端发版协调,降低灰度风险
3. PushMessage 补字段后,WS 推送也带 event_id/trace_id/version,符合 project memory "unified envelope" 规约
4. 代价:PushMessage 与 BroadcastMessage 仍并行存在,但字段重复问题通过 EventHeader 嵌入得到缓解(单一真相源)

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

#### 方案 C:删除 EventEnvelope,提取共享 EventHeader,PushMessage 补字段保 type(E2 方案,推荐)

**描述**:
1. 删除死代码 EventEnvelope
2. 提取共享 `EventHeader` 结构,各事件类型嵌入它,消除字段重复
3. **PushMessage 补字段保 type**:嵌入 EventHeader,Data 改 json.RawMessage,但保留 `type` 字段名不变(前端零改动)
4. 统一字段命名(Payload vs Data,仅 GameEvent.Data → Payload,可选)
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

// common/message/push.go(E2 方案:补字段保 type,不删除)
type PushMessage struct {
    EventHeader              // 嵌入共享 Header(补齐 event_id/trace_id/version)
    Type      string          `json:"type"`        // 保留原字段名,前端零改动
    Data      json.RawMessage `json:"data"`        // interface{} → json.RawMessage
}
```

| 优点 | 缺点 |
|---|---|
| 消除字段重复定义,单一真相源 | 需改造 4 种消息结构定义,但调用方代码基本不变 |
| 删除死代码,消除误导 | PushMessage 与 BroadcastMessage 仍并行存在(但字段统一到 EventHeader) |
| **前端零改动**(WS 推送保留 `type` 字段名) | WS 推送字段名 `type` 与 BroadcastMessage 的 `event` 不一致(内部差异,前端无感) |
| 保持现有 WS 推送格式向后兼容(仅增量加字段) | Version 路由仍需各 Consumer 单独实现 |
| 与 TRACEID_REFACTOR_PLAN 决策一致(不改 envelope 结构) | 不如方案 B 的跨域 EventType 统一分类 |
| 改动量适中,可分阶段验证;无前后端协调成本 | — |

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
│  ├── push.go       [改造] PushMessage 嵌入 EventHeader +    │
│  │                       Data 改 json.RawMessage            │
│  │                       (保留 type 字段名,前端零改动)      │
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
│  └── gateway/broadcast/broadcast.go  [无改动,继续用 PushMsg]│
│      gateway/connection/manager.go  [无改动,继续用 PushMsg]│
└─────────────────────────────────────────────────────────────┘
                            ↓ 协议
┌─────────────────────────────────────────────────────────────┐
│  gogain/packages/gift-box/ [前端零改动]                     │
│  └── WS 推送仍为 {type, data, timestamp,                    │
│         event_id?, trace_id?, version?}                     │
│      前端 parseWsInboundMessage 判别器 typeof o.type ===    │
│      'string' 继续生效,16 个 type 分支无需迁移              │
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

// EventHeader 是所有事件消息(RoomEvent/GameEvent/BroadcastMessage/PushMessage)的共享元数据头。
// 各事件类型通过嵌入此结构获得统一的 EventID/TraceID/Timestamp/Version 字段,
// 避免字段重复定义,确保单一真相源。
// 注:PushMessage 嵌入此结构后,仍保留 `type` 字段名(不改名为 `event`),
// 以确保 WS 推送格式向后兼容,前端零改动。
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

### 3.5 PushMessage 补字段保 type(E2 方案,前端零改动)

**当前 PushMessage 使用位置**(Grep 确认):
- `gateway/connection/manager.go` — kick 通知(`NewPushMessage(PushKicked, &KickedPush{...})`)
- `gateway/broadcast/broadcast.go` — 转发广播到 WS 客户端(`NewPushMessage(msg.Event, msg.Data)`)

**改造策略(E2:不删除,补字段保 type)**:

1. **保留 `common/message/push.go`** — 不删除
2. **PushMessage 嵌入 `EventHeader`** — 补齐 EventID/TraceID/Version 字段
3. **`Data` 字段类型 `interface{}` → `json.RawMessage`** — 消除二次序列化(满足 MQ_REFACTOR_PLAN P1-11)
4. **保留 `Type` 字段名不变** — JSON tag 仍为 `type`(不改为 `event`),前端零改动
5. **`Timestamp` 字段移入 EventHeader** — 由嵌入的 EventHeader 提供,删除 PushMessage 自身的 Timestamp 字段
6. **`NewPushMessage` 工厂函数签名保持 `func(msgType string, data interface{}) *PushMessage`** — 调用方代码不变;内部自动把 `data` marshal 为 `json.RawMessage`(若传入已是 `json.RawMessage` 则直接使用,避免重复 marshal)

#### 3.5.1 PushMessage 结构体改造

```go
// common/message/push.go(E2 方案:补字段保 type,不删除)
package message

import "encoding/json"

// PushMessage 是 gateway 层向 WS 客户端直连推送的消息格式。
//
// E2 方案改造说明:
//  1. 嵌入 EventHeader,补齐 EventID/TraceID/Version 字段(原 PushMessage 仅 Type/Data/Timestamp)
//  2. Data 字段类型从 interface{} 改为 json.RawMessage,消除二次序列化(满足 MQ_REFACTOR_PLAN P1-11)
//  3. 保留 Type 字段名不变(JSON tag 仍为 "type",不改为 "event"),前端零改动
//  4. Timestamp 字段由 EventHeader 提供,删除 PushMessage 自身的 Timestamp 字段(避免重复)
//
// 与 BroadcastMessage 的关系:PushMessage 仍并行存在,用于 WS 直连推送出口;
// BroadcastMessage 用于 Redis Pub/Sub + Kafka 跨节点广播。两者通过 EventHeader 共享元数据。
type PushMessage struct {
	EventHeader              // 嵌入共享 Header(补齐 event_id/trace_id/timestamp/version)
	Type      string          `json:"type"` // 保留原字段名,前端零改动
	Data      json.RawMessage `json:"data"` // interface{} → json.RawMessage
}

// NewPushMessageFromJSON 从已序列化的 JSON 数据创建 PushMessage。
// 当 data 已是 json.RawMessage 时优先使用此工厂,避免重复 marshal。
// 推荐调用方:gateway/broadcast/broadcast.go(其 msg.Data 已是 json.RawMessage)。
func NewPushMessageFromJSON(msgType string, data json.RawMessage) *PushMessage {
	return &PushMessage{
		EventHeader: NewEventHeader(""),
		Type:        msgType,
		Data:        data,
	}
}

// NewPushMessage 创建 PushMessage 并自动填充 EventHeader。
// 签名保持兼容:接受 interface{} 数据,内部转 json.RawMessage。
// 调用方代码不变(gateway/broadcast/broadcast.go、gateway/connection/manager.go)。
// 若 data 已是 json.RawMessage,直接使用(fast path,无重复 marshal)。
// 若 data marshal 失败,Data 保留 nil,序列化输出 {"data":null};调用方应避免传入无法序列化的数据。
func NewPushMessage(msgType string, data interface{}) *PushMessage {
	if v, ok := data.(json.RawMessage); ok {
		return NewPushMessageFromJSON(msgType, v)
	}
	var dataBytes json.RawMessage
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			dataBytes = b
		}
		// marshal 失败时 Data 保持 nil,ToJSON 输出 {"data":null};调用方需自行保证 data 可序列化
	}
	return NewPushMessageFromJSON(msgType, dataBytes)
}

// ToJSON 将 PushMessage 序列化为 JSON 字节(保持原方法签名)。
func (p *PushMessage) ToJSON() ([]byte, error) {
	// 兜底:EventHeader 字段为零值时补齐
	p.EventHeader.FillIfEmpty()
	return json.Marshal(p)
}

// ParsePushMessage 从 JSON 字节解析 PushMessage。
// 向前兼容:旧消息无 EventID/TraceID/Version 字段时用零值。
func ParsePushMessage(data []byte) (*PushMessage, error) {
	var msg PushMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
```

#### 3.5.2 调用方代码不变说明

**gateway/broadcast/broadcast.go:117**(转发广播到 WS 客户端):
```go
// 改造前后代码完全一致
pushMsg := message.NewPushMessage(msg.Event, msg.Data)
// ↑ msg.Data 已是 json.RawMessage,NewPushMessage 内部 fast path 直接使用,无重复 marshal
msgBytes, err := pushMsg.ToJSON()
// ↑ ToJSON 内部调用 FillIfEmpty,自动补齐 EventID/Timestamp/Version
```

**gateway/connection/manager.go:158**(kick 通知):
```go
// 改造前后代码完全一致
pushMsg := message.NewPushMessage(message.PushKicked, &message.KickedPush{
	UserID:  c.UserID,
	Reason:  message.ReasonLoginElsewhere,
	Message: message.GetKickMessage(message.ReasonLoginElsewhere),
})
// ↑ 传入 struct,NewPushMessage 内部 json.Marshal 转为 json.RawMessage
data, err := pushMsg.ToJSON()
```

**TraceID 注入策略**:
- PushMessage 的 EventHeader.TraceID 在 `NewPushMessage` 时初始化为空字符串
- gateway/broadcast/broadcast.go:在 `NewPushMessage` 调用后,可从 `msg.TraceID`(BroadcastMessage 的)透传到 pushMsg.EventHeader.TraceID,保持 trace 链路连续
- gateway/connection/manager.go:kick 通知无上游消息,TraceID 可从 ctx 注入(若有)或保持空字符串

```go
// gateway/broadcast/broadcast.go 可选改造(TraceID 透传,推荐但非强制)
pushMsg := message.NewPushMessage(msg.Event, msg.Data)
pushMsg.TraceID = msg.TraceID // 透传 BroadcastMessage 的 TraceID 到 WS 推送
msgBytes, err := pushMsg.ToJSON()
```

#### 3.5.3 前端协议格式对比

| 字段 | 改造前 | 改造后 | 前端感知 |
|---|---|---|---|
| `type` | ✅ string | ✅ string(保留) | 无变化 |
| `data` | ✅ any | ✅ any(底层 json.RawMessage,序列化结果一致) | 无变化 |
| `timestamp` | ✅ number | ✅ number(由 EventHeader 提供,JSON tag 仍为 "timestamp") | 无变化 |
| `event_id` | ❌ | ✅ string(新增) | 增量字段,前端可选消费 |
| `trace_id` | ❌ | ✅ string(新增) | 增量字段,前端可选消费 |
| `version` | ❌ | ✅ number(新增) | 增量字段,前端可选消费 |

**结论**:WS 推送格式仅增量加字段(event_id/trace_id/version),不重命名、不删除现有字段。前端 `parseWsInboundMessage` 判别器 `typeof o.type === 'string'` 继续生效,16 个 type 分支无需迁移。

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

### 3.8 前端(gogain / gift-box)零改动说明

> 本节基于 gogain `packages/gift-box` 当前实现 100% 上下文分析得出,引用文件路径均相对 `gogain/packages/gift-box/`。
> **E2 方案下,前端无需任何改动**。本节说明为何零改动成立,以及前端可选的增量增强(非强制)。

#### 3.8.1 前端当前 WS 推送消费链路

```
WS 原始消息
    │
    ▼
src/net/RoomWebSocketClient.ts → handleMessage()
    │  JSON.parse → parseWsInboundMessage()
    │  判别器:typeof o.cmd === 'string' && typeof o.code === 'number' → 命令响应
    │  判别器:typeof o.type === 'string' → 推送
    ▼
eventBus.emit('room.evt.push', { type, data, timestamp })
    │
    ├──► src/core/systems/room/index.ts (RoomSystem)
    │      处理 type: room_state / reconnect / kicked
    │
    ├──► src/scene/RoomScene.ts (offRoomPush 订阅)
    │      处理 type: penalty / wait_replacement / substitute / packet_grabbed /
    │                game_start / game_resumed / countdown_start / round_start /
    │                round_end / game_end / game_interrupted
    │
    └──► src/core/room/roomWsCommands.ts (registerRoomWsCommandOrchestration)
           处理 type: room_state / reconnect / *(任何含 room_state 字段的推送)
```

**关键定义文件**:
- `src/net/wsTypes.ts` — `WsServerPush = { type: string; data?: unknown; timestamp?: number }`
- `src/events/GameEventMap.ts` — `'room.evt.push': { type: string; data?: unknown; timestamp?: number }`

#### 3.8.2 E2 方案对前端的实际冲击(零破坏性变更)

| 维度 | 改造前(后端 PushMessage) | 改造后(后端 PushMessage 补字段) | 前端影响 |
|---|---|---|---|
| 事件类型字段名 | `type` | `type`(保留不变) | **无** |
| 事件数据字段名 | `data` | `data`(保留不变) | **无** |
| 数据字段序列化 | interface{} → JSON | json.RawMessage → JSON(底层 bytes 一致) | **无**(序列化结果相同) |
| 时间戳字段 | `timestamp`(PushMessage 自身) | `timestamp`(由 EventHeader 提供,JSON tag 不变) | **无** |
| 新增字段 | — | `event_id` / `trace_id` / `version`(增量) | **无**(前端可选消费,见 §3.8.4) |
| 客户端发送方向 | `{cmd, request_id, data, timestamp}` | 不变 | **无** |

**结论**:E2 方案下,WS 推送格式**仅增量加字段**(event_id/trace_id/version),不重命名、不删除任何现有字段。前端 `parseWsInboundMessage` 判别器 `typeof o.type === 'string'` 继续生效,16 个 type 分支无需迁移。

#### 3.8.3 前端零改动成立的四大支柱

1. **`type` 字段名保留** — PushMessage 嵌入 EventHeader 后,`Type string `json:"type"`` 字段定义不变,JSON 序列化输出 `"type":"xxx"` 不变
2. **`data` 字段值序列化结果一致** — `interface{}` 改为 `json.RawMessage` 后,JSON 输出仍为 `"data":{...}`,前端 `payload.data` 读取行为不变
3. **`timestamp` 字段位置不变** — EventHeader 嵌入后字段提升到 PushMessage 顶层,JSON tag 仍为 `"timestamp"`,前端 `payload.timestamp` 读取行为不变
4. **新增字段为可选增量** — `event_id`/`trace_id`/`version` 在前端 `WsServerPush` 类型定义中不存在,TypeScript 不会因多余字段报错(JSON.parse 结果是 any/unknown,多余字段天然忽略)

#### 3.8.4 前端可选增量增强(非强制,可滞后)

虽然 E2 方案下前端零改动即可继续运行,但前端可选择性地(非强制)消费新增的 `event_id`/`trace_id`/`version` 字段,用于:

- **埋点上报**:在 `room.evt.push` 订阅处读取 `payload.trace_id`,上报到监控系统,与后端日志串联
- **排障定位**:用户反馈问题时,前端可上报最近的 `event_id` 列表,后端据此快速定位日志
- **协议演进感知**:未来若后端递增 `version`,前端可提前预警(当前 version=1,无实际作用)

**可选增强示例**(纯增量,不破坏现有逻辑):

```typescript
// src/events/GameEventMap.ts(可选增强,非强制)
'room.evt.push': {
  type: string          // 主字段,不变
  data?: unknown        // 不变
  timestamp?: number    // 不变
  // 以下为可选增量字段,后端 E2 改造后会出现
  event_id?: string     // 可选,用于排障
  trace_id?: string     // 可选,用于埋点
  version?: number      // 可选,用于协议演进感知
}
```

```typescript
// src/net/RoomWebSocketClient.ts(可选增强,非强制)
// 当前代码:
this.eventBus.emit('room.evt.push', { type, data, timestamp })
// 可选增强后(透传增量字段,业务方按需消费):
const push = msg as WsServerPush & { event_id?: string; trace_id?: string; version?: number }
this.eventBus.emit('room.evt.push', {
  type: push.type,
  data: push.data,
  timestamp: push.timestamp,
  event_id: push.event_id,    // 可选透传
  trace_id: push.trace_id,    // 可选透传
  version: push.version,      // 可选透传
})
```

**优先级**:P2(可滞后)。E2 方案上线后,前端可在后续迭代中按需添加,无时间压力。

#### 3.8.5 关键不可破坏的契约(E2 方案保留)

为确保前端零改动,E2 方案必须保证以下契约(已在 §3.5 实现中体现):

| 契约 | 说明 | 违反后果 |
|---|---|---|
| `type` 字段名保留 | PushMessage.Type 的 JSON tag 必须为 `"type"`,不可改为 `"event"` | 前端 16 个 `payload.type === 'xxx'` 分支全部 miss |
| `data` 字段名保留 | PushMessage.Data 的 JSON tag 必须为 `"data"` | 前端所有 `payload.data` 读取失败 |
| `timestamp` 字段名保留 | EventHeader.Timestamp 的 JSON tag 必须为 `"timestamp"` | 前端 `payload.timestamp` 读取失败 |
| `reconnect` 推送特殊路径 | `RoomWebSocketClient.handleMessage` 在无匹配 pending 时,将 `cmd: reconnect` 响应转化为 `type: 'reconnect'` 推送(见 `RoomWebSocketClient.ts:334-340`) | 断线重连后房间状态不刷新 |
| `kicked` 推送字段完整 | kick 推送的 `data` 必须含 `user_id` / `reason` / `message` 字段 | 多地登录互踢弹窗(`attachGlobalWarningAlertModal.ts`)异常 |

**E2 方案验证**:以上 5 条契约全部满足(详见 §3.5.3 前端协议格式对比表与附录 A.4 WS 推送 JSON 示例)。

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

> **注**:`common/message/push.go` 在 E2 方案下**不删除**,改为改造(详见 §3.5 与 §4.3)。

### 4.3 改造文件

| 文件 | 改动内容 | 优先级 |
|---|---|---|
| `common/message/broadcast.go` | BroadcastMessage 嵌入 EventHeader;删除 `BroadcastMessageVersion` 常量;`NewBroadcastMessage` 用 `NewEventHeader` | P0 |
| `common/message/push.go` | PushMessage 嵌入 EventHeader;Data `interface{}` → `json.RawMessage`;保留 `Type` 字段名不变;`Timestamp` 字段由 EventHeader 提供;新增 `NewPushMessageFromJSON` 工厂;`NewPushMessage` 签名不变(内部 fast path 处理 json.RawMessage);`ToJSON` 调用 `FillIfEmpty` 兜底 | P0 |
| `game/domain/events.go` | RoomEvent/GameEvent 嵌入 EventHeader;GameEvent.Data → Payload;删除 `RoomEventVersion`/`GameEventVersion` 常量;10 个构造函数改造;`ParseRoomEvent` 改用 `FillIfEmpty` | P0 |
| `game/infrastructure/messaging/room_event_publisher.go` | 删除单独的 EventID/Version/Timestamp 设置,改用 `event.EventHeader.FillIfEmpty()` | P0 |
| `game/infrastructure/messaging/game_event_publisher.go` | 同上 | P0 |
| `game/infrastructure/messaging/room_event_consumer.go` | (可选)增加 Version 路由 | P1 |
| `game/infrastructure/messaging/game_event_consumer.go` | (可选)增加 Version 路由 | P1 |
| `common/broadcast/redis_pubsub_broadcaster.go` | 无字段访问改动(已通过 `NewBroadcastMessage` 工厂创建) | — |
| `common/broadcast/kafka_broadcaster.go` | 同上 | — |

### 4.4 不改动的文件(E2 方案新增)

| 文件 | 原因 |
|---|---|
| `common/message/payload.go` | Push payload 结构不变 |
| `common/message/types.go` | 命令/推送类型常量不变 |
| `common/message/request.go` / `response.go` | 请求/响应结构不变 |
| `common/message/errors.go` | 错误码不变(单独清理见 DEAD_CODE_REFACTOR_PLAN) |
| `game/application/*_app_service.go` | 调用 `NewBroadcastMessage` / `NewXxxEvent` 工厂,工厂内部改造,调用方无感 |
| `settlement/service/*` | 不直接使用消息结构 |
| **`gateway/broadcast/broadcast.go`** | **E2 方案下不改动**:`handleBroadcastMessage` 继续使用 `NewPushMessage(msg.Event, msg.Data)`,因 `msg.Data` 已是 json.RawMessage,`NewPushMessage` fast path 直接使用,无重复 marshal;可选改造:透传 `pushMsg.TraceID = msg.TraceID` |
| **`gateway/connection/manager.go`** | **E2 方案下不改动**:kick 通知继续使用 `NewPushMessage(PushKicked, &KickedPush{...})`,因 `NewPushMessage` 签名不变,内部自动 marshal struct 为 json.RawMessage |

### 4.5 前端(gogain / gift-box)改动文件清单

> **E2 方案下,前端零改动**。下表仅列出可选增量增强(非强制,可滞后实施)。

| 文件 | 改动内容 | 优先级 | 必需性 |
|---|---|---|---|
| `src/net/wsTypes.ts` | 无改动 | — | 必需:无改动 |
| `src/net/RoomWebSocketClient.ts` | 无改动 | — | 必需:无改动 |
| `src/events/GameEventMap.ts` | 无改动 | — | 必需:无改动 |
| `src/core/systems/room/index.ts` | 无改动 | — | 必需:无改动 |
| `src/scene/RoomScene.ts` | 无改动 | — | 必需:无改动 |
| `src/core/room/roomWsCommands.ts` | 无改动 | — | 必需:无改动 |
| `src/core/room/roomPushPayloads.ts` | 无改动 | — | 必需:无改动 |
| `src/net/wsTypes.ts`(可选) | `WsServerPush` 类型补 `event_id?`/`trace_id?`/`version?` 可选字段 | P2 | 可选:用于 TypeScript 类型提示 |
| `src/net/RoomWebSocketClient.ts`(可选) | `handleMessage` 推送分发处透传 `event_id`/`trace_id`/`version` | P2 | 可选:用于埋点/排障 |
| `src/events/GameEventMap.ts`(可选) | `room.evt.push` payload 类型补可选字段 | P2 | 可选:与 wsTypes.ts 配套 |
| gogain `websocket_protocol.md` §3.3 | 文档补充 WS 推送新增 `event_id`/`trace_id`/`version` 字段说明 | P2 | 可选:文档对齐 |
| gogain `packages/gift-box/docs/room-ws-flow.md` | 步骤与接口对照表注明 WS 推送新增可选字段 | P3 | 可选:文档对齐 |

**前端零改动验证**:E2 方案下,前端无需发版,后端可独立部署。前端既有 16 个 `payload.type === 'xxx'` 分支全部继续可用(详见 §3.8.3 四大支柱与 §3.8.5 不可破坏契约)。

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

### 5.2 PushMessage 补字段的前端协议兼容性(E2 方案:零破坏性变更)

**问题**:WS 客户端收到的消息格式从 `{type, data, timestamp}` → `{event_id, trace_id, timestamp, version, type, data}`,需评估前端影响。

**E2 方案下的实际影响**:**前端零改动**。原因详见 §3.8.2 E2 方案对前端的实际冲击表与 §3.8.3 四大支柱。

**影响范围分析**(基于 gogain `packages/gift-box` 100% 上下文分析):
- 所有 WS 客户端(浏览器 / 移动端)— **无影响**(WS 推送格式仅增量加字段,不重命名、不删除)
- 前端 WS 推送解析入口:`src/net/wsTypes.ts` → `parseWsInboundMessage` 判别器 `typeof o.type === 'string'` — **继续生效**(因 `type` 字段名保留)
- 前端事件总线契约:`src/events/GameEventMap.ts` 中 `'room.evt.push'` payload 类型 — **无需扩容**(多余字段天然忽略)
- 前端推送消费方(共 3 处订阅 `room.evt.push`,合计 16 个 type 分支)— **无影响**:
  - `src/core/systems/room/index.ts`:3 个分支(`room_state` / `reconnect` / `kicked`)
  - `src/scene/RoomScene.ts`:11 个分支(`penalty` / `wait_replacement` / `substitute` / `packet_grabbed` / `game_start` / `game_resumed` / `countdown_start` / `round_start` / `round_end` / `game_end` / `game_interrupted`)
  - `src/core/room/roomWsCommands.ts`:2 个分支(`room_state` / `reconnect`)
- 前端 WS 客户端分发处:`src/net/RoomWebSocketClient.ts:347-353` `eventBus.emit('room.evt.push', { type, data, timestamp })` — **无需改动**

**E2 方案核心保证**(已在 §3.5 实现中体现):
1. `type` 字段名保留(JSON tag 仍为 `"type"`,不改为 `"event"`)
2. `data` 字段名保留(JSON tag 仍为 `"data"`)
3. `timestamp` 字段名保留(JSON tag 仍为 `"timestamp"`,由 EventHeader 提供)
4. 新增 `event_id` / `trace_id` / `version` 为可选增量字段,前端 `WsServerPush` 类型定义不扩容也不会报错

**关键不可破坏的契约**(E2 方案保留,详见 §3.8.5):
- `reconnect` 推送特殊路径:`RoomWebSocketClient.handleMessage` 在无匹配 pending 时,将 `cmd: reconnect` 响应转化为 `type: 'reconnect'` 推送(见 `RoomWebSocketClient.ts:334-340`);E2 方案下 `type` 字段名不变,RoomSystem 与 roomWsCommands 中的 reconnect 分支继续生效
- `kicked` 推送:RoomSystem 据此派发 `room.evt.kicked` / `room.evt.peer_kicked`;E2 方案下 `type: 'kicked'` 与 `data.user_id` / `data.reason` / `data.message` 字段全部保留,多地登录互踢 UI(见 `runtime/attachGlobalWarningAlertModal.ts`)正常触发

**前端可选增量增强**(非强制,详见 §3.8.4):
- 若前端希望消费 `event_id` / `trace_id` / `version` 用于埋点排障,可在后续迭代中扩容 `WsServerPush` 类型与 `room.evt.push` payload 类型,优先级 P2

### 5.3 风险评估

| 风险 | 影响 | 缓解 |
|---|---|---|
| GameEvent.Data → Payload 不兼容 | Consumer 解析旧消息失败 | 部署前确认 Kafka lag=0;或采用双写过渡(详见 §5.1) |
| PushMessage 嵌入 EventHeader 后 JSON 字段顺序变化 | 严格依赖字段顺序的解析器失败(罕见) | JSON 规范不保证字段顺序;前端 `JSON.parse` 不受影响;E2 方案下 `type`/`data`/`timestamp` 字段均保留 |
| PushMessage.Data 从 interface{} 改为 json.RawMessage | 调用方若直接读取 `pushMsg.Data` 并断言为某类型会失败 | 调用方(gateway/broadcast、gateway/connection)均通过 `ToJSON()` 序列化后送 WS,不直接读取 Data 字段;`NewPushMessage` 内部已处理 interface{} → json.RawMessage 转换 |
| `reconnect` 推送特殊路径被破坏 | 断线重连后房间状态不刷新 | E2 方案下 `type` 字段名不变,特殊路径无影响;单测覆盖 |
| `kicked` 推送被破坏 | 多地登录互踢 UI 不触发 | E2 方案下 `type: 'kicked'` 与 data 字段全部保留;`runtime/attachGlobalWarningAlertModal.ts` 订阅 `room.evt.kicked` 不受影响 |
| EventHeader 嵌入导致 JSON 字段顺序变化 | 严格依赖字段顺序的解析器失败(罕见) | JSON 规范不保证字段顺序,正常解析器不受影响 |
| 构造函数改造遗漏 | 部分事件 EventID/Timestamp 为空 | Publisher 的 `FillIfEmpty` 兜底;PushMessage 的 `ToJSON` 也调用 `FillIfEmpty`;单测覆盖 |
| NewPushMessage 内部 marshal 失败 | Data 为 nil,ToJSON 输出 `{"data":null}` | 调用方应避免传入无法序列化的数据(如 channel/func);现有调用方均传入 struct 或 json.RawMessage,无此风险 |
| 删除 EventEnvelope 误伤 | 若有未发现的引用 | Grep 已确认仅自身定义和文档引用,无业务代码 |

### 5.4 回滚策略

- 每个阶段独立提交,可单独 revert
- GameEvent.Data → Payload 改名可通过恢复 `Data` 字段回滚
- **PushMessage 补字段改造可通过恢复 `push.go` 原定义回滚**(E2 方案下 push.go 不删除,仅改造结构体;回滚即恢复 `Type/Data/Timestamp` 三字段定义)
- EventHeader 嵌入可通过恢复原字段定义回滚(JSON tag 不变,字段顺序可能略不同但语义一致)
- gateway/broadcast/broadcast.go 与 gateway/connection/manager.go 在 E2 方案下不改动,无需回滚

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

### Phase 3:PushMessage 补字段保 type(E2 方案,后端独立部署)

**目标**:PushMessage 嵌入 EventHeader,补齐 event_id/trace_id/version;Data 改 json.RawMessage;保留 type 字段名,前端零改动。

**前置条件**:
- **无前端协调要求**(E2 方案下前端零改动)
- Phase 2 已完成(EventHeader 共享结构已就绪)

**任务**(纯后端改造):

**3.1 PushMessage 结构体改造**:
1. 改造 `common/message/push.go`:PushMessage 嵌入 EventHeader;Data `interface{}` → `json.RawMessage`;保留 Type 字段名;删除自身 Timestamp 字段(由 EventHeader 提供)
2. 新增 `NewPushMessageFromJSON(msgType string, data json.RawMessage) *PushMessage` 工厂
3. 改造 `NewPushMessage`:签名不变,内部 fast path 处理 json.RawMessage(避免重复 marshal),其他类型自动 marshal
4. 改造 `ToJSON`:调用 `EventHeader.FillIfEmpty()` 兜底
5. 单测覆盖:`TestPushMessageJSONCompat`(验证序列化输出含 type/data/timestamp/event_id/trace_id/version)、`TestNewPushMessageFromJSON`、`TestNewPushMessageWithRawMessage`(fast path)、`TestNewPushMessageWithStruct`(marshal path)

**3.2 调用方代码不变验证**:
1. `gateway/broadcast/broadcast.go:117` — `NewPushMessage(msg.Event, msg.Data)` 代码不变,验证 msg.Data(json.RawMessage)走 fast path
2. `gateway/connection/manager.go:158` — `NewPushMessage(PushKicked, &KickedPush{...})` 代码不变,验证 struct 走 marshal path
3. 可选改造(推荐但非强制):`gateway/broadcast/broadcast.go` 透传 `pushMsg.TraceID = msg.TraceID` 保持 trace 链路连续

**3.3 前端零改动验证**:
1. 联调环境部署后端 Phase 3 改造
2. 前端不发布任何版本,使用现有线上版本联调
3. 验证 WS 推送消费链路全部正常(详见 §7.6 前端零改动验证清单)

**验证**:
```bash
# 后端
go build ./common/message/... ./gateway/...
go test ./common/message/... ./gateway/...

# JSON 格式验证(关键)
go test -run TestPushMessageJSONCompat ./common/message/
# 验证序列化输出含:type, data, timestamp, event_id, trace_id, version
# 验证 type 字段名不变(不是 event)
# 验证 data 字段为 JSON 对象(不是 base64 编码的 RawMessage)

# 联调验证(前端不发布版本,使用现有线上前端)
# 1. 验证 room_state 推送:RoomSystem 正常更新房间状态
# 2. 验证 reconnect 推送特殊路径:断线重连后房间状态刷新
# 3. 验证 kicked 推送:多地登录互踢弹窗正常弹出
# 4. 验证 11 个 RoomScene type 分支:penalty/wait_replacement/substitute/packet_grabbed/game_start/game_resumed/countdown_start/round_start/round_end/game_end/game_interrupted
# 5. 抓包验证 WS 推送 JSON 含 event_id/trace_id/version 新增字段
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

> **注**:E2 方案下**无 Phase 6 前端兼容层清理**。原 E3 方案的 Phase 6(前端清理 type 兼容字段)在 E2 方案下不适用,因 E2 方案保留 `type` 字段名为 WS 推送的永久契约,不视为 deprecated。

---

## 七、验证清单

### 7.1 死代码清理验证

- [ ] `common/message/event.go` 文件不存在
- [ ] `common/message/push.go` 文件**仍存在**(E2 方案下 push.go 不删除,改为改造)
- [ ] `grep -rn "EventEnvelope" backend/` → 0 matches(除 refactor_docs)
- [ ] `grep -rn "type PushMessage struct" backend/common/message/push.go` → 1 match(结构体仍存在,已改造)

### 7.2 EventHeader 统一验证

- [ ] `grep -rn "type EventHeader struct" backend/common/message/` → 1 match
- [ ] `grep -rn "EventHeader" backend/common/message/push.go backend/common/message/broadcast.go backend/game/domain/events.go` → 出现在 PushMessage/BroadcastMessage/RoomEvent/GameEvent 定义处
- [ ] `grep -rn "EventID.*string.*json:\"event_id\"" backend/game/domain/events.go backend/common/message/broadcast.go backend/common/message/push.go` → 0 matches(字段已移到 EventHeader)
- [ ] `grep -rn "RoomEventVersion\|GameEventVersion\|BroadcastMessageVersion\|EventEnvelopeVersion" backend/` → 0 matches(统一为 EventHeaderVersion)

### 7.3 JSON 兼容性验证

- [ ] `RoomEvent` JSON 序列化后包含 `event_id`/`trace_id`/`timestamp`/`version` 字段(顶层,非嵌套)
- [ ] `GameEvent` JSON 序列化后包含 `event_id`/`trace_id`/`timestamp`/`version` 字段(顶层,非嵌套)
- [ ] `BroadcastMessage` JSON 序列化后包含 `event_id`/`trace_id`/`timestamp`/`version` 字段(顶层,非嵌套)
- [ ] `PushMessage` JSON 序列化后包含 `event_id`/`trace_id`/`timestamp`/`version` 字段(顶层,非嵌套)+ `type`/`data` 字段保留
- [ ] 旧格式 JSON(无 version 字段)能被 `ParseRoomEvent` / `ParseBroadcastMessage` / `ParsePushMessage` 正常解析,Version 默认填充为 1

### 7.4 Publisher 兜底验证

- [ ] 构造函数不设置 EventID 时,Publisher `FillIfEmpty` 补齐
- [ ] 构造函数不设置 Timestamp 时,Publisher `FillIfEmpty` 补齐
- [ ] 构造函数不设置 Version 时,Publisher `FillIfEmpty` 补齐为 1
- [ ] TraceID 仍由 Publisher 从 context 注入(不受 EventHeader 影响)
- [ ] PushMessage 的 `ToJSON` 调用 `EventHeader.FillIfEmpty` 兜底,EventID/Timestamp/Version 一定有值

### 7.5 编译与测试

- [ ] `go build ./...` 通过
- [ ] `go vet ./common/message/... ./game/domain/... ./game/infrastructure/messaging/... ./gateway/...` 通过
- [ ] `go test ./...` 全部通过
- [ ] `go test -race ./game/... ./gateway/...` 通过

### 7.6 前端(gogain / gift-box)零改动验证

> **E2 方案下,前端无代码改动**。本清单验证前端零改动成立的契约。

- [ ] **前端代码无改动**:`git diff` 在 `gogain/packages/gift-box/` 下无任何变更(E2 方案下前端零改动)
- [ ] **type 字段保留验证**:抓包后端推送 JSON,确认含 `"type":"xxx"` 字段(非 `"event":"xxx"`)
- [ ] **data 字段保留验证**:抓包后端推送 JSON,确认含 `"data":{...}` 字段(值为 JSON 对象,非 base64 编码)
- [ ] **timestamp 字段保留验证**:抓包后端推送 JSON,确认含 `"timestamp":1234567890` 字段(数字类型)
- [ ] **增量字段验证**:抓包后端推送 JSON,确认含 `"event_id"`/`"trace_id"`/`"version"` 新增字段(前端可忽略)
- [ ] **WS 推送解析验证**:前端 `parseWsInboundMessage` 判别器 `typeof o.type === 'string'` 继续生效(因 type 字段保留)
- [ ] **16 个 type 分支路由验证**:
  - [ ] `room_state` 推送:RoomSystem 正常更新房间状态
  - [ ] `reconnect` 推送特殊路径:断线重连后房间状态刷新(`RoomWebSocketClient.ts:334-340` 手动构造的 type: 'reconnect' 推送仍能触发 RoomSystem 与 roomWsCommands 分支)
  - [ ] `kicked` 推送:多地登录互踢弹窗正常弹出(`attachGlobalWarningAlertModal.ts` 中 `room.evt.kicked` 订阅正常触发)
  - [ ] `penalty`/`wait_replacement`/`substitute`/`packet_grabbed` 推送:RoomScene 对应分支正常触发
  - [ ] `game_start`/`game_resumed`/`countdown_start`/`round_start`/`round_end`/`game_end`/`game_interrupted` 推送:RoomScene 对应分支正常触发
- [ ] **前端不发布版本**:联调时使用现有线上前端版本,不发布新版本
- [ ] **后端独立部署验证**:后端 Phase 3 改造独立部署后,前端无需配合发版

---

## 八、预期收益

| 指标 | 当前 | 重构后(E2 方案) |
|---|---|---|
| 消息格式种类 | 5 种(RoomEvent/GameEvent/BroadcastMessage/PushMessage/EventEnvelope) | 4 种(RoomEvent/GameEvent/BroadcastMessage/PushMessage,PushMessage 补字段保 type) |
| 死代码文件 | 1 个(event.go) | 0 |
| 历史遗留格式 | 1 个(PushMessage 缺 event_id/trace_id/version,Data 为 interface{}) | PushMessage 补齐 EventHeader,Data 改 json.RawMessage(消除二次序列化) |
| EventID/TraceID/Timestamp/Version 字段重复定义 | 4 处(RoomEvent/GameEvent/BroadcastMessage/EventEnvelope) | 1 处(EventHeader,PushMessage 也嵌入) |
| 版本号常量 | 4 个(RoomEventVersion/GameEventVersion/BroadcastMessageVersion/EventEnvelopeVersion) | 1 个(EventHeaderVersion) |
| generateEventID 实现 | 3 处(domain/events.go/message/broadcast.go/message/event.go) | 1 处(message/header.go) |
| 字段命名一致性 | Payload/Data 混用 | 统一为 Payload(Phase 4 后) |
| WS 客户端消息格式 | {type, data, timestamp}(缺 event_id/trace_id/version) | {event_id, trace_id, timestamp, version, type, data}(增量加字段,前端零改动) |
| 前端 WS 推送消费方分支数 | 16 个 type 分支(分散在 3 文件) | 16 个 type 分支(全部保留,无需迁移) |
| 前端可观测性 | 推送缺 trace_id,排障困难 | 推送带 event_id/trace_id,可串联后端日志(前端可选消费,P2) |
| 前端协议演进能力 | 无 version 字段,破坏性变更需停服 | version 字段已存在,支持灰度升级 |
| **前端协调成本** | — | **0**(E2 方案下前端零改动,后端可独立部署) |
| **前后端发版耦合** | — | **解耦**(后端 Phase 3 改造不依赖前端发版) |
| 代码可维护性 | 5 种格式认知负担重 | 4 种格式 + 共享 Header,认知清晰 |

---

## 九、与现有规约的对齐

| 现有规约 | 本方案对齐方式 |
|---|---|
| `CODING_STANDARD.md` "Dead code must be removed" | 删除 EventEnvelope 死代码 |
| `MQ_REFACTOR_PLAN.md` §3.2.4 EventEnvelope 规划 | 本方案承认 EventEnvelope 未落地,改为 EventHeader 嵌入方式实现"统一字段"目标;原 MQ_REFACTOR_PLAN 的 EventEnvelope 设计被本方案替代 |
| `MQ_REFACTOR_PLAN.md` P1-11 消除 interface{} 二次序列化 | PushMessage.Data 从 interface{} 改为 json.RawMessage(消除二次序列化);`NewPushMessage` 内部 fast path 处理已序列化的 json.RawMessage,避免重复 marshal |
| `MQ_REFACTOR_PLAN.md` P1-12 BroadcastMessage 无 event_id/trace_id | BroadcastMessage 已有这些字段(✅ 已实现),本方案进一步统一到 EventHeader |
| `MQ_REFACTOR_PLAN.md` P2-11 无 schema 版本字段 | EventHeader.Version 已存在,本方案补充 Consumer 端 Version 路由(Phase 5);PushMessage 也获得 version 字段 |
| `TRACEID_REFACTOR_PLAN.md` §3.7 "不改 envelope 结构" | 本方案不改 RoomEvent.TraceID 字段语义,仅提取共享 Header,TraceID 行为不变 |
| `TRACEID_REFACTOR_PLAN.md` TraceID 由 Publisher 注入 | 本方案保持:TraceID 仍由 Publisher 从 context 注入,EventHeader 不感知 context;PushMessage 的 TraceID 由 gateway 层透传(可选) |
| project memory "All messages must use unified envelope with event_id, trace_id, timestamp, version" | 本方案通过 EventHeader 嵌入实现"unified envelope"目标(覆盖 PushMessage),而非独立的 EventEnvelope 包装层 |

---

## 十、决策记录

| # | 决策 | 理由 |
|---|------|------|
| D1 | 删除 EventEnvelope 而非真正落地 | EventEnvelope 从未落地,各事件类型已自带字段;落地需大改且与 TRACEID_REFACTOR_PLAN 决策冲突;EventHeader 嵌入方式更轻量 |
| D2 | 提取 EventHeader 而非保持字段重复 | 单一真相源;消除 4 处字段重复;未来新增消息类型可直接嵌入 |
| D3 | **PushMessage 补字段保 type(E2 方案),不删除** | PushMessage 缺 event_id/trace_id/version,Data 是 interface{} 不安全;但删除 PushMessage 改用 BroadcastMessage 会重命名 `type` → `event`,破坏前端 16 个 type 分支;E2 方案补字段保 type 实现前端零改动 |
| D4 | GameEvent.Data → Payload 统一命名 | 与 RoomEvent.Payload 对齐;但需协调部署时机(不向后兼容) |
| D5 | TraceID 仍由 Publisher 注入,不放入 EventHeader 工厂 | EventHeader 无法感知 context;Publisher 注入符合 TRACEID_REFACTOR_PLAN |
| D6 | Version 路由列为 Phase 5 可选 | 当前 Version=1 无需路由,但 Header 统一后可提取公共路由函数 |
| D7 | GameEvent.Data → Payload 采用直接改名而非双写 | GameEvent 是内部消息无外部消费者;Kafka lag 可控;双写增加复杂度收益不大 |
| D8 | **采用 E2 方案(PushMessage 补字段保 type)而非 E3 方案(删除 PushMessage)** | E2 方案前端零改动,后端可独立部署,无前后端协调成本;E3 方案需前端发版双格式兼容层,协调成本高;E2 方案的代价是 PushMessage 与 BroadcastMessage 仍并行存在,但字段重复问题通过 EventHeader 嵌入得到缓解 |
| D9 | **PushMessage 保留 `type` 字段名为永久契约,不视为 deprecated** | 前端 `parseWsInboundMessage` 判别器 `typeof o.type === 'string'` 是关键契约;改字段名会破坏前端 16 个 type 分支;E2 方案下 `type` 为 WS 推送的永久字段,不进入 deprecation 周期 |
| D10 | `NewPushMessage` 签名保持 `func(msgType string, data interface{}) *PushMessage` 不变 | 调用方(gateway/broadcast、gateway/connection)代码不变;内部 fast path 处理 json.RawMessage,其他类型自动 marshal;新增 `NewPushMessageFromJSON` 作为推荐工厂(显式接受 json.RawMessage) |
| D11 | PushMessage 自身 Timestamp 字段删除,由 EventHeader 提供 | 避免字段重复;EventHeader.Timestamp 的 JSON tag 仍为 `"timestamp"`,前端无感 |
| D12 | gateway/broadcast/broadcast.go 与 gateway/connection/manager.go 在 E2 方案下不改动 | `NewPushMessage` 签名不变,调用方代码无感;可选改造为透传 TraceID(推荐但非强制) |
| D13 | `reconnect` 推送特殊路径保留 `type: 'reconnect'` 不变 | `RoomWebSocketClient` 在无 pending 匹配时手动构造推送,须保证 RoomSystem/roomWsCommands 中的 reconnect 分支不失效;E2 方案下 type 字段名不变,特殊路径无影响 |
| D14 | 前端可选增量增强列为 P2(可滞后),非强制 | E2 方案下前端零改动即可运行;前端可在后续迭代中按需消费 event_id/trace_id/version 用于埋点排障,无时间压力 |

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

### A.4 WS 客户端收到的推送 JSON(Phase 3 后,PushMessage 补字段保 type)

**改造前格式**(后端 PushMessage,Phase 3 前):
```json
{
  "type": "round_start",
  "data": {
    "room_id": "room_001",
    "round_id": "round_005"
  },
  "timestamp": 1735689600000
}
```

**改造后格式**(后端 PushMessage 补字段,E2 方案):
```json
{
  "event_id": "550e8400-e29b-41d4-a716-446655440002",
  "trace_id": "tr_1234567891",
  "timestamp": 1735689600000,
  "version": 1,
  "type": "round_start",
  "data": {
    "room_id": "room_001",
    "round_id": "round_005"
  }
}
```

**字段对照表**:

| 字段 | 改造前 | 改造后 | 前端感知 |
|---|---|---|---|
| `type` | ✅ "round_start" | ✅ "round_start"(保留不变) | 无变化 |
| `data` | ✅ {...} | ✅ {...}(底层 json.RawMessage,序列化结果一致) | 无变化 |
| `timestamp` | ✅ 1735689600000 | ✅ 1735689600000(由 EventHeader 提供,JSON tag 不变) | 无变化 |
| `event_id` | ❌ | ✅ "550e8400-..."(新增) | 增量字段,前端可选消费 |
| `trace_id` | ❌ | ✅ "tr_1234567891"(新增) | 增量字段,前端可选消费 |
| `version` | ❌ | ✅ 1(新增) | 增量字段,前端可选消费 |

**kick 推送示例**(gateway/connection/manager.go):
```json
{
  "event_id": "550e8400-e29b-41d4-a716-446655440003",
  "trace_id": "",
  "timestamp": 1735689600000,
  "version": 1,
  "type": "kicked",
  "data": {
    "user_id": "user_123",
    "reason": "login_elsewhere",
    "message": "您的账号在其他设备登录"
  }
}
```

**关键点**:
- WS 推送格式**仅增量加字段**(event_id/trace_id/version),不重命名、不删除现有字段
- `type` 字段名保留为永久契约(详见 §3.8.5 与 D9 决策),前端 16 个 `payload.type === 'xxx'` 分支无需迁移
- 前端 `parseWsInboundMessage` 判别器 `typeof o.type === 'string'` 继续生效
- `event_id` / `trace_id` / `version` 为可选增量字段,前端可在后续迭代中按需消费(P2,非强制)
- kick 推送的 `data.user_id` / `data.reason` / `data.message` 字段全部保留,多地登录互踢 UI 正常触发

---

**版本**:v2.0(E2 方案:PushMessage 补字段保 type,前端零改动)
**创建日期**:2026-07-05
**最近更新**:2026-07-06(采用 E2 方案全面重写:§3.1/§3.5/§3.8/§4.2-4.5/§5.2-5.4/Phase 3-6/§7.1-7.6/§8-10/附录 A.4)
**作者**:AI 辅助生成,基于 EventEnvelope 死代码分析、现有规约对齐、gogain 前端 PushMessage 消费链路 100% 上下文分析
