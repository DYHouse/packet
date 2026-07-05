# TraceID 重构方案

## 1. 问题背景

### 1.1 现象

玩家进入房间后，MySQL `rooms` 表的 `player_count` / `spectator_count` 字段不更新，永远为 0。

### 1.2 根因

调用链断裂在事件发布环节：

```
JoinRoom → PublishRoomEvent(Kafka) → RoomEventConsumer → syncRoomCounts → UpdateRoom(MySQL)
                  ↑ 断裂点
```

**直接原因**：所有 `RoomEvent` 构造函数都不设置 `TraceID`，而 [room_event_publisher.go:42-44](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_publisher.go#L42-L44) 用 fail-closed 校验拒绝空 TraceID，消息根本未发送到 Kafka。

**深层原因**：

1. **TraceID 职责错位**：Publisher 把"必须由调用方设置 TraceID"作为硬约束，但项目没有任何机制让调用方拿到一个贯穿请求链路的 TraceID。
2. **错误被静默吞掉**：全部 11 处 `PublishRoomEvent` 调用都丢弃返回的 error（违反 project memory 规约 "service call return values MUST be checked"）。
3. **缺少请求级 TraceID 传播机制**：项目无 `context.WithValue` 风格的 TraceID 传播工具，gRPC handler / application service / publisher 之间无法共享 TraceID。
4. **GameEvent 同模式问题潜伏**：`GameEventPublisher` 也用同样的 fail-closed 校验，目前仅靠 `GameAppService` 内部手工拼字符串绕过，一旦遗漏即重蹈覆辙。

### 1.3 影响范围

| 受影响点 | 现状 | 风险 |
|---|---|---|
| RoomEvent 全部 10 个构造函数 | 不设置 TraceID | 所有 RoomEvent 发布失败 |
| 11 处 `PublishRoomEvent` 调用 | 吞掉 error | 失败无任何日志 |
| `RoomEventConsumer` | 永远收不到消息 | MySQL 房间计数永远为 0 |
| `RoomEventSubstitute` | Consumer 未注册 handler | 即使消息能到达，替补事件也会进 DLQ |
| `GameEventPublisher` | 同模式 fail-closed | 依赖调用方记得设置，脆弱 |

---

## 2. TraceID 最佳实践原则

### 2.1 核心原则

TraceID 的本质是**一次业务请求/操作的端到端追踪标识**，应当满足：

1. **入口生成**：在请求最外层入口（HTTP/gRPC middleware 或 WS 第一帧处理）生成，避免业务层手工拼字符串。
2. **Context 传播**：通过 `context.Context` 在同步调用链中透传，不污染方法签名。
3. **跨边界注入**：跨越异步边界（Kafka/Redis Pub/Sub/goroutine）时，由消息发布方从 context 提取 TraceID 注入消息 envelope。
4. **跨边界恢复**：消息消费方从消息 envelope 读取 TraceID，注入新的 context，使下游日志/DB 操作可关联。
5. **同一请求内的事件共享 TraceID**：一次"进入房间"操作触发的 `spectator_join` 和 `player_ready` 两个事件应共享同一 TraceID，便于全链路追踪。
6. **TraceID 与幂等键分离**：TraceID 是非确定性追踪 ID，不能作为幂等键（参考 CODING_STANDARD §20.2 SID-9）。RoomEvent 幂等键用 `EventID`，GameEvent 幂等键用业务侧 `RoundTraceID`。

### 2.2 与项目现有规约的对齐

| 项目规约 | 本方案对齐方式 |
|---|---|
| CODING_STANDARD §16.8 SC-8：TraceID MUST 通过 `TraceIDGenerator` 生成 | GameEvent 保持现有 `TraceIDGenerator` 调用；RoomEvent 因不涉及结算幂等，使用 `idgen.IDGenerator` 生成非确定性 TraceID |
| CODING_STANDARD §20.2 SID-9：事件 TraceID 允许使用雪花 ID，但 MUST 与幂等键区分 | RoomEvent 幂等键仍用 `EventID`（UUID），TraceID 仅用于日志追踪 |
| MQ_REFACTOR_PLAN CR-10：幂等 key 命名 `cashparty:{domain}:event:processed:{eventID 或 traceID}` | RoomEvent 用 `eventID`，GameEvent 用 `traceID`，保持不变 |
| project memory：TraceID must be set by message publishers | Publisher 在 context 无 TraceID 时自动生成（兜底），优先使用 context 中的 TraceID |
| project memory：service call return values MUST be checked | 所有 `PublishRoomEvent` / `PublishGameEvent` 调用检查 error 并 Warn 日志 |

### 2.3 方案选型对比

| 方案 | 描述 | 优点 | 缺点 | 评分 |
|---|---|---|---|---|
| A. Publisher 自动生成 | 每次 `PublishRoomEvent` 都生成新 TraceID | 最小侵入 | 每个事件独立 TraceID，无法关联同一次业务操作 | ★★ |
| B. Application 方法入口生成 | 在 `JoinRoom` 等方法入口生成 TraceID，通过参数传递给事件发布 | 粒度适中 | 需改方法签名或加 context；与外部请求 TraceID 脱节 | ★★★ |
| **C. 请求入口生成 + Context 传播** | 在 gRPC handler 入口生成/提取 TraceID 注入 context，publisher 从 context 读取 | 端到端追踪，符合行业最佳实践 | 需新增 context 工具 + 改 publisher | ★★★★★ |

**选型：方案 C**。这是 OpenTelemetry / OpenTracing / Spring Cloud Sleuth / gRPC-Go 等主流框架的标准做法，且为后续接入分布式追踪系统（Jaeger/Tempo）预留扩展点。

---

## 3. 重构方案设计

### 3.1 架构概览

```
┌─────────────────────────────────────────────────────────────────┐
│  Gateway (WS 入口)                                               │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  MessageRouter.Route                                      │  │
│  │  1. 解析 firstMessage                                      │  │
│  │  2. 生成 TraceID（若客户端未带） → 注入 ctx                │  │
│  │  3. forwardToService(ctx, ...)                            │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                              │ gRPC Forward(ctx, req)
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Game Service (gRPC handler)                                     │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  GenericServiceServer.Forward(ctx, req)                   │  │
│  │  1. ctx, _ = trace.WithTraceID(ctx)  // 提取或生成         │  │
│  │  2. logger.Info("...", "trace_id", trace.FromContext(ctx))│  │
│  │  3. handleJoinRoom(ctx, req)  // ctx 透传                  │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                              │ ctx 透传
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Application Layer                                               │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  RoomAppService.JoinRoom(ctx, req)                        │  │
│  │  1. 业务逻辑（Redis Lua 等）                               │  │
│  │  2. publisher.PublishRoomEvent(ctx, event)                │  │
│  │     └─ publisher 从 ctx 提取 TraceID 注入 event           │  │
│  │  3. broadcaster.Broadcast(ctx, ...)                       │  │
│  │     └─ broadcaster 从 ctx 提取 TraceID 注入广播消息       │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                              │ Kafka message (TraceID in envelope)
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Consumer Layer                                                  │
│  ┌───────────────────────────────────────────────────────────┐  │
│  │  RoomEventConsumer.handleMessage(ctx, msg)                │  │
│  │  1. event := ParseRoomEvent(msg.Value)                    │  │
│  │  2. ctx = trace.WithTraceID(ctx, event.TraceID)           │  │
│  │  3. syncRoomCounts(ctx, event)  // ctx 带 TraceID         │  │
│  │     └─ 日志/DB 操作均带 trace_id 字段                      │  │
│  └───────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
```

### 3.2 核心组件：`common/trace` 包

新建 `common/trace/trace.go`，提供 TraceID 的 context 传播工具。

```go
package trace

import (
    "context"
    "fmt"

    "github.com/cashparty/backend/common/idgen"
    "github.com/cashparty/backend/common/logger"
)

type contextKey struct{}

// WithTraceID 在 context 中注入 traceID。若 traceID 为空则自动生成。
// 返回新的 context。调用方 SHOULD 使用返回的 ctx 替换原 ctx。
func WithTraceID(ctx context.Context, traceID string) context.Context {
    if traceID == "" {
        traceID = Generate()
    }
    return context.WithValue(ctx, contextKey{}, traceID)
}

// FromContext 从 context 提取 TraceID。若不存在返回空字符串。
func FromContext(ctx context.Context) string {
    if v, ok := ctx.Value(contextKey{}).(string); ok {
        return v
    }
    return ""
}

// Generate 生成新的 TraceID。
// 使用雪花 ID（非确定性），符合 CODING_STANDARD §20.2 SID-9。
// 格式：tr_<snowflake>，便于日志检索时识别。
func Generate() string {
    gen, err := idgen.GetGenerator()
    if err != nil {
        // 极端情况：idgen 未初始化，降级用 UUID 保证不阻塞业务
        logger.Warn("idgen not initialized, fallback to uuid for traceID")
        return "tr_fallback_" + uuid.NewString()
    }
    id, err := gen.GenerateInt64()
    if err != nil {
        logger.Warn("generate traceID failed, fallback to uuid", "error", err)
        return "tr_fallback_" + uuid.NewString()
    }
    return fmt.Sprintf("tr_%d", id)
}
```

**设计要点**：

- `WithTraceID` 空值自动生成，调用方无需判断
- `FromContext` 不存在返回空字符串，不 panic
- 降级策略：idgen 不可用时 fallback 到 UUID，保证业务不阻塞
- 格式前缀 `tr_` 便于日志检索识别

### 3.3 Publisher 改造

#### 3.3.1 `RoomEventPublisher`

**文件**：[game/infrastructure/messaging/room_event_publisher.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_publisher.go)

**改动**：将 fail-closed 校验改为"context 优先 + 自动生成兜底"。

```go
// Before (line 42-44)
if event.TraceID == "" {
    return fmt.Errorf("event TraceID must be set by caller")
}

// After
// 优先使用 context 中的 TraceID（实现端到端追踪）；
// context 无 TraceID 时使用 event 已有的 TraceID（向后兼容）；
// 两者都为空时自动生成（兜底，保证消息一定能发出）。
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
```

**关键变化**：

- 不再返回 error 阻塞消息发送
- 优先从 context 提取（实现同请求多事件共享 TraceID）
- 自动生成时打 Warn 日志，便于发现未接入 context 传播的调用路径

#### 3.3.2 `GameEventPublisher`

**文件**：[game/infrastructure/messaging/game_event_publisher.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_publisher.go)

**改动**：同样改为 context 优先策略（line 60-62）。

```go
// Before
if event.TraceID == "" {
    return fmt.Errorf("event TraceID must be set by caller")
}

// After
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
```

**对 GameEvent 幂等性的影响**：无。GameEvent 的幂等键是 `traceID`（见 MQ_REFACTOR_PLAN CR-10），但该 traceID 是 `GameAppService` 内部用 `TraceIDGenerator` 确定性生成的业务 TraceID（如 `GAME_SETTLE_<sessionID>`），与日志追踪用的 `event.TraceID` 是同一字段。

**重要澄清**：GameEvent 当前的 `TraceID` 字段同时承担两个职责——日志追踪 + 幂等键。本方案不改变 GameEvent 现有调用方行为（`GameAppService` 仍显式设置确定性 TraceID），只是增加 context 兜底，避免遗漏时阻塞消息发送。

### 3.4 gRPC Handler 入口注入 TraceID

**文件**：[game/server/generic_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go)

**改动**：在 `Forward` 方法入口提取/生成 TraceID 注入 context。

```go
// Before (line 69)
func (s *GenericServiceServer) Forward(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
    logger.Info("[Game<-Gateway] received request",
        "user_id", req.UserId,
        "cmd", req.Cmd,
        "request_id", req.RequestId,
        "data", string(req.Data))

    // ... switch req.Cmd ...
}

// After
func (s *GenericServiceServer) Forward(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
    // 从 gRPC metadata 提取 TraceID（Gateway 透传），无则生成
    ctx = trace.WithTraceID(ctx, extractTraceIDFromMetadata(ctx))

    logger.Info("[Game<-Gateway] received request",
        "user_id", req.UserId,
        "cmd", req.Cmd,
        "request_id", req.RequestId,
        "trace_id", trace.FromContext(ctx),
        "data", string(req.Data))

    // ... switch req.Cmd（ctx 已带 TraceID，透传给 application service）...
}

// extractTraceIDFromMetadata 从 gRPC metadata 提取 TraceID。
// Gateway 在 forwardToService 时通过 metadata header 注入。
func extractTraceIDFromMetadata(ctx context.Context) string {
    md, ok := metadata.FromIncomingContext(ctx)
    if !ok {
        return ""
    }
    values := md.Get("x-trace-id")
    if len(values) > 0 {
        return values[0]
    }
    return ""
}
```

### 3.5 Gateway 侧 TraceID 生成与透传

**文件**：[gateway/router/router.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/router/router.go)

**改动**：在 `Route` 方法入口生成 TraceID，通过 gRPC metadata 透传给 Game Service。

```go
// Before (line 68-124)
func (r *MessageRouter) Route(ctx context.Context, conn *connection.Connection, rawMessage []byte) {
    // ... 解析 req ...
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    resp := r.forwardToService(ctx, serviceName, conn, &req)
    // ...
}

// After
func (r *MessageRouter) Route(ctx context.Context, conn *connection.Connection, rawMessage []byte) {
    // ... 解析 req ...

    // 生成请求级 TraceID，贯穿 Gateway → gRPC → Application → Kafka 全链路
    ctx = trace.WithTraceID(ctx, trace.Generate())
    logger.Debug("routing message",
        "conn_id", conn.ConnID,
        "user_id", conn.UserID,
        "cmd", req.Cmd,
        "request_id", req.RequestID,
        "trace_id", trace.FromContext(ctx))

    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    resp := r.forwardToService(ctx, serviceName, conn, &req)
    // ...
}

// Before (line 137-157) forwardToService
func (r *MessageRouter) forwardToService(ctx context.Context, serviceName string, conn *connection.Connection, req *message.Request) *message.Response {
    // ...
    forwardReq := &commonPb.ForwardRequest{
        UserId:    userID,
        Cmd:       req.Cmd,
        RequestId: req.RequestID,
        Data:      req.Data,
        Timestamp: req.Timestamp,
    }
    // ...
}

// After
func (r *MessageRouter) forwardToService(ctx context.Context, serviceName string, conn *connection.Connection, req *message.Request) *message.Response {
    // ...
    forwardReq := &commonPb.ForwardRequest{
        UserId:    userID,
        Cmd:       req.Cmd,
        RequestId: req.RequestID,
        Data:      req.Data,
        Timestamp: req.Timestamp,
    }

    // 通过 gRPC metadata 透传 TraceID
    ctx = metadata.AppendToOutgoingContext(ctx, "x-trace-id", trace.FromContext(ctx))
    // ...
}
```

### 3.6 Consumer 侧 TraceID 恢复

**文件**：[game/infrastructure/messaging/room_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go)

**改动**：从 event 提取 TraceID 注入 context，使下游日志/DB 操作可关联。

```go
// Before (line 41-120)
func (c *RoomEventConsumer) handleMessage(ctx context.Context, msg kafka.Message) error {
    // ...
    event, err := domain.ParseRoomEvent(msg.Value)
    // ...
    var handleErr error
    switch event.EventType {
    case domain.RoomEventSpectatorJoin:
        handleErr = c.handleSpectatorJoin(ctx, event)
    // ...
    }
    // ...
}

// After
func (c *RoomEventConsumer) handleMessage(ctx context.Context, msg kafka.Message) error {
    // ...
    event, err := domain.ParseRoomEvent(msg.Value)
    // ...

    // 从 event 恢复 TraceID 到 context，使下游日志/DB 操作可关联
    if event.TraceID != "" {
        ctx = trace.WithTraceID(ctx, event.TraceID)
    }

    var handleErr error
    switch event.EventType {
    case domain.RoomEventSpectatorJoin:
        handleErr = c.handleSpectatorJoin(ctx, event)
    // ...
    }

    if handleErr != nil {
        shouldRelease = true
        logger.Error("handle room event failed",
            "event_type", event.EventType,
            "room_id", event.RoomID,
            "user_id", event.UserID,
            "trace_id", event.TraceID,
            "error", handleErr)
        return handleErr
    }

    logger.Info("room event processed",
        "event_type", event.EventType,
        "room_id", event.RoomID,
        "user_id", event.UserID,
        "trace_id", event.TraceID)
    return nil
}
```

**同样改造**：[game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) 的 `HandleEvent` 方法。

### 3.7 RoomEvent 构造函数改造

**文件**：[game/domain/events.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/events.go)

**改动**：构造函数不再需要设置 TraceID（由 Publisher 从 context 注入）。但为了向后兼容（部分调用方可能仍显式传入），保留 `TraceID` 字段，Publisher 优先使用已设置的值。

**无需改动构造函数**——这是本方案的优点：调用方签名不变，TraceID 由 Publisher 自动注入。

### 3.8 所有 PublishRoomEvent 调用点加 error 检查

**文件**：[game/application/room_app_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/room_app_service.go) 和 [game/application/seat_app_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/seat_app_service.go)

**改动**：全部 11 处调用加 error 检查 + Warn 日志。以 `JoinRoom` 为例：

```go
// Before (room_app_service.go:130-132)
if s.publisher != nil {
    s.publisher.PublishRoomEvent(ctx, domain.NewSpectatorJoinEvent(roomID, req.UserID, userInfo.Nickname, userInfo.Avatar))
}

// After
if s.publisher != nil {
    if err := s.publisher.PublishRoomEvent(ctx, domain.NewSpectatorJoinEvent(roomID, req.UserID, userInfo.Nickname, userInfo.Avatar)); err != nil {
        logger.Warn("publish spectator_join event failed",
            "room_id", roomID,
            "user_id", req.UserID,
            "trace_id", trace.FromContext(ctx),
            "error", err)
    }
}
```

**完整改动清单**：

| 文件 | 行号 | 事件 |
|---|---|---|
| room_app_service.go | 131 | spectator_join |
| room_app_service.go | 210 | player_ready |
| room_app_service.go | 339 | substitute |
| room_app_service.go | 398 | queue_join |
| room_app_service.go | 440 | queue_leave |
| room_app_service.go | 522 | spectator_leave |
| room_app_service.go | 596 | player_reconnect |
| seat_app_service.go | 113 | seat_select |
| seat_app_service.go | 175 | seat_cancel |
| seat_app_service.go | 283 | player_ready |
| seat_app_service.go | 390 | spectator_kick |

**设计决策**：事件发布失败只记 Warn 日志，**不阻塞主流程**。原因：

1. 用户入房/上座的 Redis 状态已成功更新（Lua 脚本已执行）
2. MySQL 计数只是 Redis 的快照，可由下一次事件或定时对账补齐
3. 阻塞主流程会导致用户入房失败，体验更差

### 3.9 Consumer 补齐 Substitute 事件处理

**文件**：[game/infrastructure/messaging/room_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go)

**改动**：在 switch 中增加 substitute 分支。

```go
// Before (line 85-102)
switch event.EventType {
case domain.RoomEventSpectatorJoin:
    handleErr = c.handleSpectatorJoin(ctx, event)
case domain.RoomEventSpectatorLeave:
    handleErr = c.handleSpectatorLeave(ctx, event)
case domain.RoomEventPlayerReady:
    handleErr = c.handlePlayerReady(ctx, event)
case domain.RoomEventSeatCancel:
    handleErr = c.handleSeatCancel(ctx, event)
case domain.RoomEventSpectatorKick:
    handleErr = c.handleSpectatorKick(ctx, event)
default:
    // ...
}

// After
switch event.EventType {
case domain.RoomEventSpectatorJoin:
    handleErr = c.handleSpectatorJoin(ctx, event)
case domain.RoomEventSpectatorLeave:
    handleErr = c.handleSpectatorLeave(ctx, event)
case domain.RoomEventPlayerReady:
    handleErr = c.handlePlayerReady(ctx, event)
case domain.RoomEventSeatCancel:
    handleErr = c.handleSeatCancel(ctx, event)
case domain.RoomEventSpectatorKick:
    handleErr = c.handleSpectatorKick(ctx, event)
case domain.RoomEventSubstitute:
    handleErr = c.handleSubstitute(ctx, event)
default:
    // ...
}

// 新增 handler
func (c *RoomEventConsumer) handleSubstitute(ctx context.Context, event *domain.RoomEvent) error {
    // 替补上座：spectator → player，两个计数都变化，需同步
    return c.syncRoomCounts(ctx, event)
}
```

### 3.10 可选增强：Broadcast 消息注入 TraceID

**文件**：[game/infrastructure/broadcast/broadcaster.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/broadcast/broadcaster.go)

**改动**：`Broadcast` 方法从 context 提取 TraceID 注入 `BroadcastMessage`（对齐 MQ_REFACTOR_PLAN BR-2）。

```go
// 概念示意，具体实现需对齐 broadcaster 现有签名
func (b *GameBroadcaster) Broadcast(ctx context.Context, roomID string, cmd string, payload interface{}, excludeUserID string) error {
    msg := message.NewBroadcastMessage(cmd, payload)
    msg.TraceID = trace.FromContext(ctx)  // 新增
    // ... 后续逻辑不变 ...
}
```

**此项为可选增强**，不影响核心问题修复，可在后续迭代中实施。

---

## 4. 改动文件清单

### 4.1 新增文件

| 文件 | 用途 |
|---|---|
| `common/trace/trace.go` | TraceID context 传播工具 |
| `common/trace/trace_test.go` | 单元测试 |

### 4.2 修改文件

| 文件 | 改动内容 | 优先级 |
|---|---|---|
| `game/infrastructure/messaging/room_event_publisher.go` | TraceID 改为 context 优先 + 自动生成兜底 | P0 |
| `game/infrastructure/messaging/game_event_publisher.go` | 同上（保持一致性） | P1 |
| `game/infrastructure/messaging/room_event_consumer.go` | 恢复 TraceID 到 context + 补 substitute handler | P0 |
| `game/infrastructure/messaging/game_event_consumer.go` | 恢复 TraceID 到 context | P1 |
| `game/application/room_app_service.go` | 7 处 PublishRoomEvent 加 error 检查 | P0 |
| `game/application/seat_app_service.go` | 4 处 PublishRoomEvent 加 error 检查 | P0 |
| `game/server/generic_service.go` | Forward 入口注入 TraceID | P1 |
| `gateway/router/router.go` | Route 入口生成 TraceID + metadata 透传 | P1 |

### 4.3 不改动的文件

| 文件 | 原因 |
|---|---|
| `game/domain/events.go` 的 10 个构造函数 | TraceID 由 Publisher 注入，调用方无需改动 |
| `game/application/game_app_service.go` 的 GameEvent 发布 | 已显式设置确定性 TraceID，无需改动 |
| `settlement/service/trace_id_generator.go` | 业务幂等键生成器，与日志 TraceID 职责分离 |

---

## 5. 向后兼容性分析

### 5.1 消息格式兼容

- `RoomEvent.TraceID` 字段语义不变，仍是字符串
- 旧消息（TraceID 为空）经 `ParseRoomEvent` 解析后，Publisher 兜底生成，Consumer 正常处理
- 新消息（TraceID 非空）正常消费，无 schema 变更

### 5.2 调用方兼容

- `PublishRoomEvent(ctx, event)` 签名不变
- `RoomEvent` 构造函数签名不变
- Application service 方法签名不变
- 唯一变化：Publisher 不再因空 TraceID 返回 error

### 5.3 幂等性兼容

- RoomEvent 幂等键：`EventID`（UUID），不受 TraceID 影响
- GameEvent 幂等键：`TraceID`（确定性业务 ID），`GameAppService` 仍显式设置，不受 Publisher 兜底逻辑影响

---

## 6. 测试方案

### 6.1 单元测试

**`common/trace/trace_test.go`**：

```go
func TestWithTraceID_FromContext(t *testing.T) {
    ctx := trace.WithTraceID(context.Background(), "tr_123")
    assert.Equal(t, "tr_123", trace.FromContext(ctx))
}

func TestWithTraceID_AutoGenerate(t *testing.T) {
    ctx := trace.WithTraceID(context.Background(), "")
    id := trace.FromContext(ctx)
    assert.NotEmpty(t, id)
    assert.True(t, strings.HasPrefix(id, "tr_"))
}

func TestFromContext_Empty(t *testing.T) {
    assert.Empty(t, trace.FromContext(context.Background()))
}
```

**Publisher 测试**：

```go
func TestRoomEventPublisher_TraceIDFromContext(t *testing.T) {
    ctx := trace.WithTraceID(context.Background(), "tr_ctx_123")
    event := domain.NewSpectatorJoinEvent("room1", "user1", "nick", "avatar")
    // event.TraceID 为空

    err := publisher.PublishRoomEvent(ctx, event)
    assert.NoError(t, err)
    assert.Equal(t, "tr_ctx_123", event.TraceID)
}

func TestRoomEventPublisher_TraceIDAutoGenerate(t *testing.T) {
    // ctx 无 TraceID，event 也无 TraceID
    event := domain.NewSpectatorJoinEvent("room1", "user1", "nick", "avatar")

    err := publisher.PublishRoomEvent(context.Background(), event)
    assert.NoError(t, err)
    assert.NotEmpty(t, event.TraceID)
    assert.True(t, strings.HasPrefix(event.TraceID, "tr_"))
}
```

### 6.2 集成测试

1. **端到端计数同步**：
   - 玩家加入房间
   - 验证日志中出现 `"trace_id": "tr_xxx"` 贯穿 Gateway → gRPC → Publisher → Consumer
   - 验证 MySQL `rooms` 表 `player_count` / `spectator_count` 与 Redis `HLEN` 一致

2. **全事件覆盖**：
   - spectator_join / spectator_leave / player_ready / seat_cancel / spectator_kick / substitute
   - 每个事件后验证 MySQL 计数正确

3. **故障注入**：
   - Kafka 不可用时，验证 `PublishRoomEvent` 返回 error 且日志打印 Warn
   - Redis 不可用时，验证 Consumer `tryAcquire` 返回 error 触发 Kafka 重试

### 6.3 日志验证

修复后应出现以下日志链路（同一 trace_id 贯穿）：

```
[Gateway]  routing message  cmd=join_room  trace_id=tr_123
[Game]     [Game<-Gateway] received request  cmd=join_room  trace_id=tr_123
[Game]     room event published  event_type=spectator_join  trace_id=tr_123
[Game]     room event processed  event_type=spectator_join  trace_id=tr_123
```

---

## 7. 实施计划

### Phase 1：核心修复（P0，解决计数不更新）

1. 新建 `common/trace/trace.go`
2. 改造 `RoomEventPublisher`（TraceID context 优先 + 自动生成）
3. 改造 `RoomEventConsumer`（恢复 TraceID 到 context + 补 substitute handler）
4. 11 处 `PublishRoomEvent` 调用加 error 检查
5. 单元测试 + 集成测试

**验证标准**：玩家加入房间后，MySQL `rooms` 表计数正确更新。

### Phase 2：全链路追踪（P1，端到端 TraceID）

1. 改造 `GameEventPublisher`（保持一致性）
2. 改造 `GameEventConsumer`（恢复 TraceID 到 context）
3. 改造 `GenericServiceServer.Forward`（入口注入 TraceID）
4. 改造 `Gateway MessageRouter.Route`（生成 TraceID + metadata 透传）

**验证标准**：日志中出现贯穿 Gateway → gRPC → Application → Kafka → Consumer 的同一 trace_id。

### Phase 3：可选增强（P2，广播消息 TraceID）

1. `GameBroadcaster.Broadcast` 注入 TraceID
2. 对齐 MQ_REFACTOR_PLAN BR-2

---

## 8. 风险与缓解

| 风险 | 缓解措施 |
|---|---|
| Publisher 自动生成 TraceID 可能掩盖调用方未接入 context 的问题 | 自动生成时打 Warn 日志，便于监控发现 |
| GameEvent 的 TraceID 双重职责（日志追踪 + 幂等键）可能混淆 | 文档明确：GameEvent.TraceID 仍由 `GameAppService` 显式设置确定性值；Publisher 兜底逻辑仅在遗漏时触发 |
| gRPC metadata 透传可能被中间件截断 | 使用标准 `x-trace-id` header，与 OpenTelemetry 对齐 |
| 降级到 UUID 时 TraceID 格式不一致 | 统一 `tr_` 前缀，便于日志检索 |

---

## 9. 与现有重构计划的对齐

| 现有计划 | 本方案对齐点 |
|---|---|
| MQ_REFACTOR_PLAN §3.2.4 EventEnvelope | 本方案不改 envelope 结构，复用现有 `RoomEvent.TraceID` 字段 |
| MQ_REFACTOR_PLAN CR-10 幂等 key 命名 | 不变，RoomEvent 用 EventID，GameEvent 用 TraceID |
| MQ_REFACTOR_PLAN BR-2 BroadcastMessage 工厂 | Phase 3 可选增强对齐 |
| CODING_STANDARD §16.8 SC-8 TraceID 生成 | RoomEvent 用 `idgen`（非确定性），GameEvent 保持 `TraceIDGenerator`（确定性） |
| CODING_STANDARD §20.2 SID-9 TraceID 与幂等键分离 | 严格遵循 |
| project memory "TraceID must be set by message publishers" | Publisher 负责注入，符合规约 |

---

## 10. 附录：TraceID 生成方式说明

### 10.1 RoomEvent TraceID（日志追踪用）

- **生成方式**：`trace.Generate()` → `idgen.IDGenerator.GenerateInt64()` → `tr_<snowflake>`
- **特性**：非确定性，每次生成不同值
- **用途**：仅用于日志追踪，不参与幂等
- **幂等键**：`EventID`（UUID，在构造函数中生成）

### 10.2 GameEvent TraceID（日志追踪 + 幂等键）

- **生成方式**：`TraceIDGenerator.GenerateGameSettleTraceID(sessionID)` → `GAME_SETTLE_<sessionID>`
- **特性**：确定性，相同 sessionID 生成相同值
- **用途**：日志追踪 + Consumer 幂等键（`cashparty:game:event:processed:<traceID>`）
- **调用方**：`GameAppService` 显式设置，Publisher 不覆盖

### 10.3 两者关系

| 维度 | RoomEvent | GameEvent |
|---|---|---|
| TraceID 生成 | Publisher 自动（context 优先） | Application 显式（确定性） |
| TraceID 类型 | 雪花 ID（非确定性） | 业务语义拼接（确定性） |
| 幂等键 | EventID（UUID） | TraceID |
| 幂等 key Redis | `cashparty:room:event:processed:<eventID>` | `cashparty:game:event:processed:<traceID>` |
| 重试时 TraceID | 每次不同（非幂等要求） | 相同（确定性，支持幂等重试） |

本方案严格保持这一区分，不引入混淆。
