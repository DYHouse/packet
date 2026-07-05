# MQ 重构方案（Message Queue Refactor Plan）v1

> 适用范围：`/Users/aaron.pan/Desktop/party/RedPacket-master/backend` 下所有 Kafka producer / consumer、Redis Pub/Sub broadcaster / consumer、事件发布与消息模型相关代码。
> 配套规约：`CODING_STANDARD.md`、`GOROUTINE_REFACTOR_PLAN.md`、`NACOS_REFACTOR_PLAN.md`。
> 本文与 goroutine/context 重构方案解耦但风格一致：分阶段、可验证、有规约。

---

## 目录

- [1. 现状审计（37 类问题）](#1-现状审计37-类问题)
- [2. 设计目标与原则](#2-设计目标与原则)
- [3. 整体架构设计](#3-整体架构设计)
- [4. 文件结构规划](#4-文件结构规划)
- [5. 重构阶段（8 个阶段）](#5-重构阶段8-个阶段)
- [6. 规约](#6-规约)
- [7. 验证清单](#7-验证清单)
- [8. 风险与回滚](#8-风险与回滚)

---

## 1. 现状审计（37 类问题）

通过 4 个并行 review agent 全面扫描 `common/kafka`、`common/broadcast`、`common/message`、`game/infrastructure/messaging`、`game/infrastructure/broadcast`、`gateway/broadcast`、`gateway/connection`、`game/domain/events.go` 等共 30+ 文件，归纳出 37 类问题。按严重程度分级。

### 1.1 严重问题（P0 — 影响消息可靠性与业务正确性）

| # | 问题 | 文件:行 | 后果 |
|---|------|---------|------|
| P0-1 | Kafka Consumer handler 失败仍 commit offset | `common/kafka/consumer.go:113-124` | 消息丢失，违背 at-least-once 语义；GameEventConsumer/RoomEventConsumer 设计的 tryAcquire+releaseAcquire+重试链条被截断 |
| P0-2 | Producer.Balancer = `LeastBytes{}` 忽略消息 Key | `common/kafka/producer.go:44` | GameEventPublisher 精心设计的 `roomID_sessionID` Key 失效，同会话事件分散到不同分区，破坏顺序性保证 |
| P0-3 | RoomEventPublisher 不设消息 Key | `game/infrastructure/messaging/room_event_publisher.go:29` | 同房间事件乱序，业务行为不可预测 |
| P0-4 | Kafka `StartOffset = kafka.LastOffset` 默认值 | `common/kafka/consumer.go:37,56` | 新 consumer group 首次启动跳过所有历史消息，部署/升级时丢消息 |
| P0-5 | Offset 双重 commit（自动 1s + 手动每条） | `common/kafka/consumer.go:55,74,122` | 自动 commit 可能在 handler 完成前提交 offset，崩溃时丢消息；手动 commit 与之重复 |
| P0-6 | Redis Pub/Sub 订阅断开期间消息全部丢失，无重连 | `common/broadcast/redis_pubsub_consumer.go:54-85` | gateway 节点网络抖动或 Redis 重启期间，game 广播的所有房间状态变更、红包抓取等推送全部丢失 |
| P0-7 | `Producer.GetWriter` 并发不安全 | `common/kafka/producer.go:36-53` | check-then-write 无锁；多 goroutine 首次对同 topic 发送会创建多个 Writer，旧的被覆盖未关闭，资源泄漏 |

### 1.2 中等问题（P1 — 影响可维护性与行为一致性）

| # | 问题 | 文件:行 | 后果 |
|---|------|---------|------|
| P1-1 | 两个独立的 `KafkaConfig` 定义 | `common/config/types.go:80` + `gateway/config/config.go:47` | gateway 有 `Enabled` 字段可守卫，game 无守卫，brokers 为空时仍创建 producer |
| P1-2 | `BroadcastConfig` 重复定义 | `common/config/types.go:84` + `gateway/config/config.go:52-64` | gateway 内手动逐字段拷贝构造 common 版本，冗余转换层 |
| P1-3 | `domain.Broadcaster` 与 `common/broadcast.Broadcaster` 重复定义 | `game/domain/repository.go:85-88` | 签名一致但独立定义，common 层签名变更不会编译报错 |
| P1-4 | `domain.EventPublisher` 仅覆盖 RoomEvent，GameEventPublisher 是 concrete struct | `game/domain/repository.go:81-83` + `game/infrastructure/messaging/game_event_publisher.go` | 两种事件走两种注入风格，GameEventPublisher 无法 mock 替换 |
| P1-5 | GameEventConsumer 不持有 `kafka.Consumer`，RoomEventConsumer 内部持有 | `game/infrastructure/messaging/game_event_consumer.go` + `room_event_consumer.go` | 构造模式不统一，Close 职责不清（两者都无 Close） |
| P1-6 | RoomEventConsumer parse 错误 fail-open（return nil） | `game/infrastructure/messaging/room_event_consumer.go:36-39` | 毒消息静默丢失；GameEventConsumer 对应路径返回 error，行为不一致 |
| P1-7 | RoomEventConsumer 未知 event type fail-open（return nil） | `game/infrastructure/messaging/room_event_consumer.go:67-70` | 与 GameEventConsumer（返回 error）不一致 |
| P1-8 | 幂等 TTL 不一致（7 天 vs 24 小时） | `game_event_consumer.go` vs `room_event_consumer.go` | RoomEvent 24h 后重投丢幂等保护，无统一依据 |
| P1-9 | 幂等 key 维度不一致（traceID vs eventID） | 同上 | 两套机制，缺乏统一约定 |
| P1-10 | GroupID per-node（`{groupID}-{nodeID}`） | `game/bootstrap/app.go:160-173` | 每个节点消费全量消息，靠 Redis SetNX 互斥；未利用 Kafka consumer group 的分区级负载均衡 |
| P1-11 | `GameEvent.Data` / `RoomEvent.Payload` 是 `interface{}` | `game/domain/events.go:98-106,29-36` | consumer 端需二次 `json.Marshal/Unmarshal`，性能损耗 + 失去类型安全 |
| P1-12 | `BroadcastMessage` 无 `event_id` / `trace_id` / `timestamp` | `common/message/broadcast.go:5-12` | 无法做幂等、无法追踪、无法重放 |
| P1-13 | 事件命名风格不一（verb vs past tense） | `game/domain/events.go` | `session_start` / `round_settle` / `session_end` 是动词原形，`packet_created` 是过去分词 |
| P1-14 | `RoomEventType` int vs `GameEventType` string | `game/domain/events.go` | 跨事件域无法用统一方式判断类型，可读性差 |
| P1-15 | `RoomEvent.OccurredAt time.Time` vs `GameEvent.Timestamp int64(秒)` vs `PushMessage.Timestamp int64(毫秒)` | 多文件 | 时间戳类型/单位不统一，易出 bug |
| P1-16 | Kafka Pub/Sub 启动失败无重试 | `common/broadcast/redis_pubsub_consumer.go:41-45` | gateway 启动期间 Redis 短暂不可用 → 广播服务永久不工作直到重启 |
| P1-17 | kick pub/sub 无重连 | `gateway/connection/manager.go:185-216` | Redis 短暂断开导致跨节点踢人失效，同一用户可能在两节点同时在线 |
| P1-18 | `GameEventPublisher` 强制 caller 设置 TraceID | `game/infrastructure/messaging/game_event_publisher.go` | ✅ 设计良好；但 RoomEventPublisher 无类似守卫 |
| P1-19 | `Producer.Close` 永远返回 nil | `common/kafka/producer.go:78-85` | 调用方无法感知关闭失败 |
| P1-20 | `RoomEventPublisher.Publish` 失败不记日志 | `game/infrastructure/messaging/room_event_publisher.go:23-30` | 行为与其他 publisher 不一致 |
| P1-21 | Game 服务 Producer 没有空 brokers 防御 | `game/bootstrap/app.go:95` | brokers 为空时仍创建 producer，首次 Send 才失败 |
| P1-22 | Gateway Producer 创建后未使用 | `gateway/bootstrap/app.go:87-90` | 创建即关闭的空转资源，徒增 Close 负担 |
| P1-23 | `handleBroadcastMessage` 无 panic recovery | `gateway/broadcast/broadcast.go:82-110` | 单条消息异常会冒泡到 consumeMessages（Redis 路径有外层 recover，Kafka 路径会被 processMessage recover 后仍 commit，丢消息） |

### 1.3 低等问题（P2 — 代码整洁度）

| # | 问题 | 文件 |
|---|------|------|
| P2-1 | 4 个 RoomEvent 类型死代码（PlayerJoin/PlayerLeave/StatusChange/PlayerDisconnect） | `game/domain/events.go` |
| P2-2 | 13 个 Kafka topic 死代码（TopicGameResult 等） | `common/kafka/topics.go` |
| P2-3 | `NewProducer(cfg)` 全代码库无调用方 | `common/kafka/producer.go:18` |
| P2-4 | `SendBatch` 方法无调用方 | `common/kafka/producer.go:68` |
| P2-5 | `TopicGatewayBroadcast` 与 `BroadcastTopicKafka` 字符串重复定义 | `common/kafka/topics.go:29` + `common/broadcast/constants.go:5` |
| P2-6 | `roomUsersCache` 无大小限制 | `gateway/broadcast/broadcast.go:32` |
| P2-7 | 多处忽略 JSON 序列化错误（`_ = ...ToJSON()`） | `gateway/connection/manager.go:159,181,212` |
| P2-8 | 无 DLQ / 死信队列 | 全局 |
| P2-9 | 无指数退避（fetch 错误固定 sleep 1s） | `common/kafka/consumer.go:109` |
| P2-10 | Settlement 不发布任何事件 | `settlement/service/*` |
| P2-11 | 无 schema 版本字段 / 无 registry | 全局 |
| P2-12 | Consumer 无独立 panic recovery，依赖 common 层 | `game/infrastructure/messaging/*_consumer.go` |
| P2-13 | GameEventConsumer/RoomEventConsumer 无 Close 方法 | 同上 |
| P2-14 | `BroadcastService.Start` 失败静默失效，不重启 | `gateway/broadcast/broadcast.go:62-80` |

---

## 2. 设计目标与原则

### 2.1 设计目标

1. **可靠性优先**：消息不丢失、不重复处理（at-least-once + 幂等）。
2. **顺序性保证**：同房间/同会话事件落入同一 Kafka 分区，consumer 顺序消费。
3. **可观测性**：每条消息有 `event_id` + `trace_id` + `timestamp`，可追踪、可重放、可做幂等。
4. **统一抽象**：producer / consumer / broadcaster 接口全项目唯一，避免重复定义。
5. **可测试性**：所有 publisher / consumer 依赖接口而非 concrete struct，便于 mock。
6. **优雅生命周期**：所有 consumer / broadcaster 启停受 Application 生命周期管理，无泄漏。
7. **演进友好**：消息带 `schema_version`，支持向后兼容演进。

### 2.2 设计原则

| 原则 | 说明 |
|------|------|
| **单一真相源** | 每个 topic 常量、每个接口、每个配置结构体只在一处定义 |
| **分层清晰** | common（抽象）→ infrastructure（实现）→ application（使用）→ bootstrap（装配） |
| **fail-closed 默认** | 消息处理失败时不 commit offset，让 Kafka 重投；Redis 不可用时返回 error 触发重试 |
| **幂等显式化** | 每条业务消息必须有幂等键，幂等 TTL 统一为 7 天 |
| **Key 必填** | 业务消息（事件类）必须有 Kafka Key（roomID 或 roomID_sessionID），使用 `Hash` balancer 保序 |
| **配置守卫** | brokers 为空时不创建 producer/consumer，记 Warn 跳过 |
| **生命周期受管** | 所有 goroutine 由 TaskRunner 或 bootstrap wg 管理，无裸 `go func()` |
| **panic 兜底** | 每层（common / infrastructure / application）独立 recover，不依赖外层 |
| **重连退避** | 订阅断开后指数退避重连，重连后触发一次状态同步 |

---

## 3. 整体架构设计

### 3.1 分层架构

```
┌─────────────────────────────────────────────────────────────┐
│  bootstrap（装配层）                                          │
│  game/bootstrap/mq_helper.go  gateway/bootstrap/mq_helper.go │
│  - 创建 Producer / Consumer / Broadcaster                    │
│  - 注入到 Container                                          │
│  - 启停顺序受 Application 管                                  │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│  application（使用层）                                        │
│  game/application/*_app_service.go                           │
│  - 通过 domain.EventPublisher / domain.Broadcaster 接口调用  │
│  - 异步发布走 TaskRunner.Submit                              │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│  domain（契约层）                                             │
│  game/domain/events.go  game/domain/messaging.go（新增）     │
│  - EventEnvelope / Broadcaster / EventPublisher 接口          │
│  - RoomEvent / GameEvent / Payload 结构                      │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│  infrastructure（实现层）                                     │
│  game/infrastructure/messaging/                              │
│  - RoomEventPublisher / GameEventPublisher                   │
│  - RoomEventConsumer / GameEventConsumer                     │
│  game/infrastructure/broadcast/broadcaster.go                │
│  gateway/broadcast/broadcast.go                              │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│  common（基础抽象层）                                         │
│  common/kafka/      common/broadcast/   common/message/      │
│  - Producer / Consumer 抽象与实现                            │
│  - Broadcaster / Consumer 接口                               │
│  - Message envelope / 序列化                                 │
└─────────────────────────────────────────────────────────────┘
```

### 3.2 核心抽象

#### 3.2.1 Producer 抽象（common/kafka）

```go
// common/kafka/producer.go
type Producer struct {
    mu      sync.RWMutex        // 保护 writers map
    writers map[string]*kafka.Writer
    brokers []string
    cfg     ProducerConfig
}

type ProducerConfig struct {
    Brokers        []string
    Balancer       kafka.Balancer    // 默认 &kafka.Hash{}
    BatchSize      int               // 默认 100
    BatchTimeout   time.Duration     // 默认 10ms
    WriteTimeout   time.Duration     // 默认 10s
    RequiredAcks   kafka.RequiredAcks // 默认 RequireOne
    Async          bool              // 默认 false（同步）
}

func NewProducer(cfg ProducerConfig) (*Producer, error)  // 返回 error：brokers 为空时拒绝
func (p *Producer) Send(ctx, topic, key, value) error    // key 允许 nil（广播场景）
func (p *Producer) SendBatch(ctx, topic, messages) error
func (p *Producer) Close() error                         // 返回真实错误
```

**关键变化**：
- `Balancer` 默认 `&kafka.Hash{}`（按 key 哈希分区），可配置
- `GetWriter` 加 `sync.RWMutex` 保护，并发安全
- `NewProducer` 校验 brokers 非空，否则返回 error
- `Close` 返回真实错误（聚合后返回第一个）

#### 3.2.2 Consumer 抽象（common/kafka）

```go
// common/kafka/consumer.go
type MessageHandler func(ctx context.Context, msg Message) error

type ConsumerConfig struct {
    Brokers        []string
    Topic          string
    GroupID        string
    MinBytes       int
    MaxBytes       int
    MaxWait        time.Duration
    CommitInterval time.Duration     // 默认 0（禁用自动 commit）
    StartOffset    int64             // 默认 kafka.FirstOffset
    MaxRetries     int               // 新增：handler 失败重试次数，默认 3
    RetryBackoff   time.Duration     // 新增：重试退避初始值，默认 1s
    DLQTopic       string            // 新增：死信队列 topic，空表示不投 DLQ
}

type Consumer struct {
    reader  *kafka.Reader
    handler MessageHandler
    cfg     ConsumerConfig
    topic   string
    dlq     *Producer            // 可选，DLQ 生产者
}

func NewConsumer(cfg ConsumerConfig, handler MessageHandler) (*Consumer, error)
func (c *Consumer) Start(ctx context.Context) error
func (c *Consumer) Close() error
```

**关键变化**：
- `CommitInterval` 默认 0（禁用自动 commit），仅手动 commit
- `StartOffset` 默认 `kafka.FirstOffset`（新 group 不丢历史消息）
- handler 失败时进入重试循环（`MaxRetries` + 指数退避），重试耗尽后投 DLQ 并 commit；成功才 commit
- `NewConsumer` 返回 error（参数校验）

#### 3.2.3 Broadcaster 抽象（common/broadcast）

```go
// common/broadcast/broadcaster.go
type Broadcaster interface {
    Broadcast(ctx context.Context, roomID string, event string, data interface{}, excludeUserID string) error
    BroadcastToUser(ctx context.Context, userID string, event string, data interface{}) error
}

type Consumer interface {
    Start(ctx context.Context) error
    Close() error
}

type MessageHandler func(ctx context.Context, msg *message.BroadcastMessage) error
```

**关键变化**：
- 接口保持不变（goroutine 重构已对齐 ctx + error）
- `BroadcastMessage` 增加 `EventID` / `TraceID` / `Timestamp` 字段（见 3.2.4）

#### 3.2.4 消息 envelope 统一（common/message）

```go
// common/message/broadcast.go
type BroadcastMessage struct {
    EventID   string          `json:"event_id"`              // 新增：UUID
    TraceID   string          `json:"trace_id"`              // 新增：贯穿日志
    Timestamp int64           `json:"timestamp"`             // 新增：Unix 毫秒
    Version   int             `json:"version"`               // 新增：schema 版本，当前 1
    TargetType string         `json:"target_type"`
    TargetID   string         `json:"target_id,omitempty"`
    UserIDs    []string       `json:"user_ids,omitempty"`
    ExcludeID  string         `json:"exclude_id,omitempty"`
    Event      string         `json:"event"`
    Data       json.RawMessage `json:"data"`
}

// common/message/event.go（新增）
type EventEnvelope struct {
    EventID   string          `json:"event_id"`              // UUID，所有事件统一
    EventType string          `json:"event_type"`            // 统一为 string
    TraceID   string          `json:"trace_id"`              // 贯穿日志
    Timestamp int64           `json:"timestamp"`             // Unix 毫秒，统一
    Version   int             `json:"version"`               // schema 版本
    Source    string          `json:"source"`                // 发送方标识（如 "game-service"）
    Payload   json.RawMessage `json:"payload"`               // 类型安全：序列化后的 payload
}
```

**关键变化**：
- `BroadcastMessage` 与 `EventEnvelope` 都有 `event_id` / `trace_id` / `timestamp` / `version`
- 时间戳统一为 Unix 毫秒（int64）
- `EventEnvelope.Payload` 改为 `json.RawMessage`，避免 `interface{}` 二次序列化
- `EventType` 统一为 string（RoomEventType 从 int 改为 string）

### 3.3 重试与 DLQ 流程

```
Consumer.Start:
  loop:
    msg = FetchMessage(ctx)
    err = processWithRetry(ctx, msg)
    if err == nil:
      CommitMessages(msg)              # 成功才 commit
      continue
    if dlq != nil:
      sendToDLQ(msg, err)              # 投死信
    CommitMessages(msg)                # DLQ 投递成功后 commit，避免毒消息永久阻塞
    log.Error("message moved to DLQ", ...)

processWithRetry:
  for i in 0..MaxRetries:
    err = handler(ctx, msg)
    if err == nil: return nil
    if isPermanent(err): return err     # 永久错误（parse 失败等）不重试
    sleep(RetryBackoff * 2^i + jitter)
  return err
```

### 3.4 Redis Pub/Sub 重连流程

```
RedisPubSubConsumer.Start:
  loop:
    pubsub = redis.Subscribe(channel)
    ensureSubscription()                # Receive 确认
    consumeMessages(pubsub)             # 阻塞消费，断开时返回
    if ctx.Done: return
    backoff = min(backoff*2, 30s)       # 指数退避，上限 30s
    sleep(backoff + jitter)
    log.Warn("redis pubsub reconnecting", "attempt", n)
```

---

## 4. 文件结构规划

### 4.1 新增文件

```
backend/
├── common/
│   ├── kafka/
│   │   ├── producer.go              [改写] 加锁、Hash balancer、brokers 校验、Close 返回 error
│   │   ├── consumer.go              [改写] fail-closed commit、FirstOffset、重试+DLQ
│   │   ├── topics.go                [清理] 删除 13 个死代码 topic 常量
│   │   ├── config.go                [新增] ProducerConfig / ConsumerConfig 统一定义
│   │   ├── dlq.go                   [新增] DLQ 投递辅助函数
│   │   ├── retry.go                 [新增] 重试 + 指数退避辅助
│   │   └── doc.go                   [新增] 包文档
│   ├── broadcast/
│   │   ├── broadcaster.go           [保持] 接口不变
│   │   ├── constants.go             [删除] 合并到 kafka/topics.go
│   │   ├── consumer.go              [保持] 接口不变
│   │   ├── consumer_factory.go      [改写] 支持重连配置
│   │   ├── factory.go               [保持]
│   │   ├── kafka_broadcaster.go     [改写] key 策略：广播 nil，用户推送 userID
│   │   ├── kafka_consumer.go        [保持] 简单包装
│   │   ├── redis_pubsub_broadcaster.go [改写] 填充 event_id/trace_id/timestamp
│   │   ├── redis_pubsub_consumer.go [改写] 加重连 + 指数退避
│   │   └── doc.go                   [新增] 包文档
│   ├── message/
│   │   ├── broadcast.go             [改写] 加 EventID/TraceID/Timestamp/Version
│   │   ├── event.go                 [新增] EventEnvelope 统一信封
│   │   ├── push.go                  [保持]
│   │   ├── payload.go               [保持]
│   │   ├── types.go                 [保持]
│   │   ├── request.go               [保持]
│   │   ├── response.go              [保持]
│   │   └── errors.go                [保持]
│   └── config/
│       └── types.go                 [改写] KafkaConfig 加 Enabled 字段；删除重复
├── game/
│   ├── domain/
│   │   ├── events.go                [改写] RoomEventType 改 string；删除死代码类型；时间戳统一毫秒
│   │   ├── messaging.go             [新增] EventPublisher（覆盖 Room + Game）/ Broadcaster 接口统一
│   │   └── repository.go            [改写] 删除重复的 Broadcaster/EventPublisher 接口，引用 messaging.go
│   ├── infrastructure/
│   │   ├── broadcast/
│   │   │   └── broadcaster.go       [改写] 实现 common/broadcast.Broadcaster；保留 Warn 日志层
│   │   └── messaging/
│   │       ├── game_event_publisher.go  [改写] 实现 EventPublisher 接口；key 用 roomID_sessionID
│   │       ├── game_event_consumer.go   [改写] 实现 Close；统一幂等 TTL 7d；parse 错误返回 error
│   │       ├── room_event_publisher.go  [改写] 实现 EventPublisher 接口；key 用 roomID；补日志
│   │       ├── room_event_consumer.go   [改写] 实现 Close；parse/unknown 返回 error；幂等 TTL 7d
│   │       └── doc.go                   [新增] 包文档
│   ├── application/
│   │   ├── game_app_service.go      [改写] GameEventPublisher 改为接口注入
│   │   ├── room_app_service.go      [保持] 已用 EventPublisher 接口
│   │   └── seat_app_service.go      [保持]
│   └── bootstrap/
│       ├── app.go                   [改写] consumer 启动加 Close 资源管理
│       ├── container.go             [改写] 注入接口而非 concrete struct
│       └── mq_helper.go             [新增] 统一创建 producer/consumer/broadcaster
├── gateway/
│   ├── broadcast/
│   │   └── broadcast.go             [改写] handleBroadcastMessage 加 panic recovery；Start 失败重试
│   ├── connection/
│   │   └── manager.go               [改写] kick pub/sub 加重连；JSON 错误不忽略
│   └── bootstrap/
│       ├── app.go                   [改写] producer 创建加 brokers 守卫
│       ├── container.go             [改写] 删除本地 KafkaConfig 重复定义
│       └── mq_helper.go             [新增] 统一创建
└── settlement/
    └── service/
        └── event_publisher.go       [新增] SettlementEventPublisher 发布结算事件（可选，Phase 8）
```

### 4.2 删除文件

无完整文件删除，但会删除以下死代码：
- `common/broadcast/constants.go` 合并到 `common/kafka/topics.go` 后删除整个文件
- `common/kafka/topics.go` 中 13 个未使用的 topic 常量
- `game/domain/events.go` 中 4 个未使用的 RoomEvent 类型
- `common/kafka/producer.go` 中 `NewProducer(cfg *config.KafkaConfig)` 函数（无调用方）
- `gateway/config/config.go` 中重复的 `KafkaConfig` / `BroadcastConfig` / `BroadcastKafkaConfig` / `BroadcastRedisConfig`

---

## 5. 重构阶段（8 个阶段）

### Phase 1: common/kafka Producer 改造（P0-2, P0-7, P1-19, P1-21, P2-3, P2-4）

**目标**：修复 Producer 的并发安全、Key 失效、Close 错误吞没、brokers 守卫缺失。

**任务**：
1. 新建 `common/kafka/config.go`：定义 `ProducerConfig` 结构体
2. 改写 `common/kafka/producer.go`：
   - `Producer` 加 `sync.RWMutex` 保护 `writers`
   - `NewProducer(cfg ProducerConfig) (*Producer, error)`：brokers 为空返回 error
   - `GetWriter` 加 RWMutex（读多写少）
   - 默认 `Balancer: &kafka.Hash{}`
   - `Close` 聚合错误返回（不再是 nil）
   - 删除 `NewProducer(cfg *config.KafkaConfig)`（无调用方）
   - 保留 `SendBatch`（虽然当前无调用方，但属于公共 API，保留以备批量场景）
3. 改写 `common/kafka/topics.go`：
   - 删除 13 个未使用 topic 常量（TopicGameResult 等）
   - 保留 `TopicGameEvents` / `TopicRoomEvents` / `TopicGatewayBroadcast`
   - 合并 `common/broadcast/constants.go` 中的 `BroadcastTopicKafka`
4. 删除 `common/broadcast/constants.go`
5. 更新所有调用方：
   - `game/bootstrap/app.go:95`：`kafka.NewProducerWithBrokers` → `kafka.NewProducer(kafka.ProducerConfig{Brokers: cfg.Kafka.Brokers})`，加 error 处理
   - `gateway/bootstrap/app.go:87-90`：同样改造，brokers 为空时跳过创建

**验证**：
- `go build ./common/kafka/... ./game/... ./gateway/...`
- `grep -r "NewProducerWithBrokers" backend/` → 0 matches
- `grep -r "LeastBytes" backend/common/kafka/` → 0 matches
- `grep -r "BroadcastTopicKafka" backend/` → 0 matches

---

### Phase 2: common/kafka Consumer 改造（P0-1, P0-4, P0-5, P1-19, P2-9, P2-12）

**目标**：修复 handler 失败仍 commit、StartOffset 默认值、双重 commit、无重试无 DLQ。

**任务**：
1. 改写 `common/kafka/config.go`：完善 `ConsumerConfig`，加 `MaxRetries` / `RetryBackoff` / `DLQTopic`
2. 新建 `common/kafka/retry.go`：
   - `processWithRetry(ctx, handler, msg, cfg) error`：指数退避重试
   - `isPermanentError(err) bool`：判断永久错误（parse 失败等）
3. 新建 `common/kafka/dlq.go`：
   - `sendToDLQ(ctx, producer, dlqTopic, msg, err) error`：包装原消息 + 错误信息投递到 DLQ
4. 改写 `common/kafka/consumer.go`：
   - `ConsumerConfig` 默认值：`CommitInterval=0`（禁用自动）、`StartOffset=FirstOffset`、`MaxRetries=3`、`RetryBackoff=1s`
   - `Start` 流程改为：`FetchMessage` → `processWithRetry` → 成功才 `CommitMessages`；失败投 DLQ 后再 commit
   - `processMessage` 保留 panic recovery（最后一道防线）
   - `Close` 返回 reader.Close 真实错误
5. 更新调用方：
   - `game/bootstrap/app.go` / `game/bootstrap/container.go`：使用新 `ConsumerConfig`
   - `gateway/bootstrap/container.go`：同上
   - `common/broadcast/consumer_factory.go`：`createKafkaConsumer` 使用新 config

**验证**：
- `go build ./common/kafka/... ./game/... ./gateway/...`
- `grep -n "CommitInterval: time.Second" backend/common/kafka/consumer.go` → 0 matches
- `grep -n "StartOffset.*LastOffset" backend/common/kafka/` → 0 matches
- 单测：新建 `common/kafka/consumer_test.go`，覆盖重试 + DLQ + fail-closed commit

---

### Phase 3: 消息 envelope 与事件模型统一（P1-11, P1-12, P1-13, P1-14, P1-15, P1-18, P2-1, P2-11）

**目标**：统一消息信封、事件命名、时间戳类型、消除 interface{} 二次序列化。

**任务**：
1. 改写 `common/message/broadcast.go`：
   - `BroadcastMessage` 加 `EventID` / `TraceID` / `Timestamp` / `Version` 字段
   - `NewBroadcastMessage(event, data, target...)` 工厂函数自动填 event_id/timestamp/version
2. 新建 `common/message/event.go`：
   - `EventEnvelope` 结构体（统一信封）
   - `NewEventEnvelope(eventType, source, payload) EventEnvelope` 工厂
3. 改写 `game/domain/events.go`：
   - `RoomEventType` 从 `int` 改为 `string`（如 `"spectator_join"`）
   - 删除 4 个死代码类型（PlayerJoin/PlayerLeave/StatusChange/PlayerDisconnect）
   - 命名统一为动词原形：`session_start` / `packet_created` / `round_settle` / `session_end`（保留现状，文档化为"事件类型用动词原形"）
   - `RoomEvent` 加 `TraceID` / `Version` 字段；`OccurredAt` 改为 `Timestamp int64`（Unix 毫秒）
   - `GameEvent` 加 `EventID` / `Version` 字段；`Timestamp` 改为毫秒
   - `Payload` / `Data` 改为 `json.RawMessage`，提供 `SetPayload(v interface{}) error` / `GetPayload(v interface{}) error` 辅助方法
4. 新建 `game/domain/messaging.go`：
   - 统一 `EventPublisher` 接口（覆盖 Room + Game）：
     ```go
     type EventPublisher interface {
         PublishRoomEvent(ctx context.Context, event *RoomEvent) error
         PublishGameEvent(ctx context.Context, event *GameEvent) error
     }
     ```
   - `Broadcaster` 接口引用 `common/broadcast.Broadcaster`（类型别名 `type Broadcaster = broadcast.Broadcaster`）
5. 改写 `game/domain/repository.go`：删除重复的 `Broadcaster` / `EventPublisher` 接口，引用 `messaging.go`
6. 改写 `game/infrastructure/messaging/*_publisher.go`：
   - `GameEventPublisher` 实现 `EventPublisher` 接口的 `PublishGameEvent`
   - `RoomEventPublisher` 实现 `PublishRoomEvent`，补 TraceID 必填校验，补失败日志
   - 两者使用 `EventEnvelope` 包装
7. 改写 `game/infrastructure/messaging/*_consumer.go`：
   - 解析 `EventEnvelope`，根据 `Version` 路由（向前兼容）
   - 使用 `GetPayload` 反序列化，避免二次 Marshal/Unmarshal

**验证**：
- `go build ./common/message/... ./game/...`
- `grep -n "interface{}" backend/game/domain/events.go | grep -i "payload\|data"` → 0 matches
- `grep -n "RoomEventType" backend/game/domain/events.go | grep "int"` → 0 matches
- 单测：events_test.go 覆盖序列化/反序列化/版本路由

---

### Phase 4: Producer/Consumer 调用方迁移（P0-3, P1-1, P1-4, P1-5, P1-20, P1-22, P2-13）

**目标**：所有 publisher 补 Key、统一注入接口、consumer 加 Close、删除重复配置。

**任务**：
1. 改写 `game/infrastructure/messaging/room_event_publisher.go`：
   - `Publish` 用 `event.RoomID` 作为 Kafka Key
2. 改写 `game/infrastructure/messaging/game_event_publisher.go`：
   - 保持 `roomID_sessionID` 作为 Key
3. 改写 `game/infrastructure/messaging/game_event_consumer.go`：
   - 加 `Close() error` 方法（委托给持有的 kafka.Consumer）
   - 结构体持有 `*kafka.Consumer`（与 RoomEventConsumer 模式统一）
4. 改写 `game/infrastructure/messaging/room_event_consumer.go`：
   - 加 `Close() error` 方法
5. 改写 `gateway/config/config.go`：
   - 删除本地 `KafkaConfig` / `BroadcastConfig` / `BroadcastKafkaConfig` / `BroadcastRedisConfig`
   - 引用 `commonconfig.KafkaConfig` / `commonconfig.BroadcastConfig`
   - `KafkaConfig` 加 `Enabled` 字段（统一到 common 层）
6. 改写 `game/bootstrap/app.go`：
   - Producer 创建加 `if len(cfg.Kafka.Brokers) == 0 { logger.Warn(...); 跳过 }` 守卫
7. 改写 `gateway/bootstrap/app.go`：
   - 若 producer 创建后无使用方，删除创建逻辑（P1-22）
8. 改写 `game/bootstrap/container.go`：
   - `GameAppService` 注入 `EventPublisher` 接口而非 `*GameEventPublisher` concrete struct
9. 改写 `game/application/game_app_service.go`：
   - struct 字段从 `*messaging.GameEventPublisher` 改为 `domain.EventPublisher`
   - 构造函数参数对应改

**验证**：
- `go build ./...`
- `grep -n "key, _ := nil" backend/game/infrastructure/messaging/` → 0 matches
- `grep -n "NewGameEventPublisher\b" backend/game/` → 仅出现在 container.go
- `grep -rn "config.KafkaConfig" backend/gateway/config/` → 0 matches（已统一引用 common）

---

### Phase 5: Consumer 幂等与错误处理统一（P1-6, P1-7, P1-8, P1-9, P2-12）

**目标**：parse/unknown 错误统一 fail-closed，幂等 TTL 统一 7 天，consumer 内独立 panic recovery。

**任务**：
1. 改写 `game/infrastructure/messaging/room_event_consumer.go`：
   - `parseRoomEvent` 失败返回 error（不再 return nil）
   - 未知 event type 返回 error（不再 return nil）
   - 幂等 TTL 从 24h 改为 7*24h
   - `handleMessage` 顶部加 `defer recover()` + logger.Error + debug.Stack
2. 改写 `game/infrastructure/messaging/game_event_consumer.go`：
   - `HandleEvent` 顶部加 `defer recover()` + logger.Error + debug.Stack
   - 幂等 TTL 保持 7 天
3. 改写 `common/broadcast/consumer_factory.go`：
   - `wrapper` parse 失败返回 error（保持），但文档说明会被 common/kafka.Consumer 重试 + DLQ 处理
4. 统一幂等 key 命名：
   - RoomEvent：`cashparty:room:event:processed:<eventID>`（不变）
   - GameEvent：`cashparty:game:event:processed:<traceID>`（不变）
   - 文档说明：RoomEvent 用 eventID，GameEvent 用 traceID（因 GameEvent 是聚合事件，traceID 跨多个原子操作）

**验证**：
- `grep -n "return nil" backend/game/infrastructure/messaging/room_event_consumer.go | grep -i "parse\|unknown"` → 0 matches
- `grep -n "24 \* time.Hour" backend/game/infrastructure/messaging/` → 0 matches
- `grep -n "defer func()" backend/game/infrastructure/messaging/*_consumer.go` → ≥2 matches

---

### Phase 6: Redis Pub/Sub 重连与广播可靠性（P0-6, P1-16, P1-17, P1-23, P2-7, P2-14）

**目标**：Redis Pub/Sub 订阅断开自动重连，广播服务启动失败可重试，handleBroadcastMessage 加 recovery。

**任务**：
1. 改写 `common/broadcast/redis_pubsub_consumer.go`：
   - `Start` 改为重连循环：Subscribe → consumeMessages → 断开后指数退避（1s→2s→4s→...→30s 上限）→ 重新 Subscribe
   - `consumeMessages` 在 `pubsub.Channel()` 关闭时返回 error 而非 return
   - 加重连计数 metrics（log.Warn 每次）
2. 改写 `common/broadcast/redis_pubsub_broadcaster.go`：
   - `Broadcast` / `BroadcastToUser` 使用 `NewBroadcastMessage` 工厂填充 event_id/trace_id/timestamp
3. 改写 `common/broadcast/kafka_broadcaster.go`：
   - `BroadcastToUser` 用 `userID` 作为 Kafka Key（同一用户推送落同分区，保序）
   - `Broadcast` 用 `roomID` 作为 Key
   - 使用 `NewBroadcastMessage` 工厂
4. 改写 `gateway/broadcast/broadcast.go`：
   - `handleBroadcastMessage` 顶部加 `defer recover()` + logger.Error + debug.Stack
   - `Start` goroutine 中 consumer.Start 失败后重试（最多 3 次，指数退避），仍失败才发 errChan
5. 改写 `gateway/connection/manager.go`：
   - `subscribeKickChannel` 改为重连循环（同 RedisPubSubConsumer 模式）
   - `kickLocalConnection` / `publishKickNotification` / `subscribeKickChannel` 中 JSON 序列化错误不忽略，记 Error
6. 改写 `gateway/broadcast/broadcast.go`：
   - `roomUsersCache` 加最大条目限制（默认 10000，LRU 淘汰）

**验证**：
- `go build ./common/broadcast/... ./gateway/...`
- `grep -n "Receive(c.ctx)" backend/common/broadcast/redis_pubsub_consumer.go` → 0 matches（改为重连循环）
- `grep -n "_ = .*ToJSON\(\)" backend/gateway/` → 0 matches
- 单测：redis_pubsub_consumer_test.go 覆盖重连逻辑（mock redis client）

---

### Phase 7: Bootstrap 装配统一与生命周期（P1-5, P1-10, P2-13）

**目标**：producer/consumer/broadcaster 创建逻辑统一到 mq_helper，consumer GroupID 策略文档化。

**任务**：
1. 新建 `game/bootstrap/mq_helper.go`：
   - `createKafkaProducer(cfg) (*kafka.Producer, error)`：brokers 守卫
   - `createRoomEventConsumer(cfg, producer, ...) (*messaging.RoomEventConsumer, error)`
   - `createGameEventConsumer(cfg, producer, ...) (*messaging.GameEventConsumer, error)`
   - `createBroadcaster(cfg, producer, redis) (*broadcast.GameBroadcaster, error)`
2. 新建 `gateway/bootstrap/mq_helper.go`：
   - `createKafkaProducer(cfg) (*kafka.Producer, error)`
   - `createBroadcastService(cfg, redis, manager, appCtx) (*broadcast.BroadcastService, error)`
3. 改写 `game/bootstrap/app.go`：
   - `Start` 中两个 consumer 启动后，`Application` struct 加 `kafkaConsumers []io.Closer` 字段管理 Close
   - `Stop()` 顺序：taskRunner.Stop → cancel → Container.Stop → wg.Wait(10s) → taskRunner.Wait(30s) → consumer.Close → producer.Close → redis/mysql close
4. 改写 `game/bootstrap/container.go`：
   - `Container` 加 `KafkaConsumers []io.Closer` 字段
   - 初始化时填充
5. GroupID 策略文档化（不改为固定 group，保持 per-node）：
   - 在 `mq_helper.go` 注释说明："GameEvent/RoomEvent 使用 per-node group（`{groupID}-{nodeID}`），每个节点消费全量消息，靠 Redis SetNX 互斥保证恰好一次处理。这是有意设计，因为业务需要每个节点都感知事件（如 robot scheduler、connection manager）；若未来需要分区级负载均衡，可改为固定 groupID 并移除 SetNX 互斥。"

**验证**：
- `go build ./...`
- `grep -rn "kafka.NewConsumer\b\|kafka.NewConsumerWithConfig\b" backend/game/bootstrap/` → 仅出现在 mq_helper.go
- `grep -n "io.Closer" backend/game/bootstrap/app.go` → ≥1 matches

---

### Phase 8: Settlement 事件发布（P2-10，可选）

**目标**：Settlement 服务发布关键事件，供下游（风控/审计/通知）订阅。

**任务**：
1. 新建 `settlement/service/event_publisher.go`：
   - `SettlementEventPublisher` 接口
   - `PublishRoundSettled(ctx, event *RoundSettledEvent) error`
   - `PublishGameSettled(ctx, event *GameSettledEvent) error`
   - `PublishRefundCompleted(ctx, event *RefundCompletedEvent) error`
2. 新建 `settlement/infrastructure/messaging/settlement_event_publisher.go`：
   - Kafka 实现，topic `cashparty.settlement.events`
   - key 用 `roundTraceID` 或 `billID`
3. 改写 `settlement/service/settlement_service.go`：
   - `SettleRound` 成功后异步发布 `RoundSettled` 事件
   - `SettleGame` 成功后异步发布 `GameSettled` 事件
   - 通过 TaskRunner.Submit 异步发布，失败记 Warn 不阻塞主流程
4. 在 `common/kafka/topics.go` 重新加入 `TopicSettlementEvents`（之前删除的死代码现在启用）

**验证**：
- `go build ./settlement/...`
- `grep -n "SettlementEventPublisher" backend/settlement/` → ≥3 matches

---

## 6. 规约

### 6.1 Producer 规约

| 规则 | 说明 |
|------|------|
| **PR-1** | Producer 必须使用 `kafka.NewProducer(kafka.ProducerConfig{...})` 创建，禁止 `NewProducerWithBrokers` |
| **PR-2** | `ProducerConfig.Brokers` 为空时 `NewProducer` 返回 error，调用方必须处理 |
| **PR-3** | `ProducerConfig.Balancer` 默认 `&kafka.Hash{}`，业务消息禁止改为 `LeastBytes` |
| **PR-4** | 业务事件消息（RoomEvent/GameEvent）必须传非 nil Key：RoomEvent 用 `roomID`，GameEvent 用 `roomID_sessionID` |
| **PR-5** | 广播消息（BroadcastMessage）的 `Broadcast` 用 `roomID` 作 Key，`BroadcastToUser` 用 `userID` 作 Key |
| **PR-6** | `Producer.Close()` 必须检查返回 error，记 Warn |
| **PR-7** | `Producer` 实例在 Application 生命周期内单例，禁止每处自建 |
| **PR-8** | `Send` 失败时必须返回 error 给调用方，调用方决定是 log Warn（广播场景）还是触发重试（事件场景） |

### 6.2 Consumer 规约

| 规则 | 说明 |
|------|------|
| **CR-1** | Consumer 必须使用 `kafka.NewConsumer(kafka.ConsumerConfig{...}, handler)` 创建 |
| **CR-2** | `ConsumerConfig.CommitInterval` 必须为 0（禁用自动 commit），仅手动 commit |
| **CR-3** | `ConsumerConfig.StartOffset` 业务默认 `kafka.FirstOffset`；仅明确不需要历史消息的场景才用 `LastOffset` |
| **CR-4** | handler 返回 error 时不得 commit，必须进入重试循环（`MaxRetries` + 指数退避） |
| **CR-5** | 重试耗尽后必须投递 DLQ（若配置了 `DLQTopic`），DLQ 投递成功后才 commit |
| **CR-6** | parse 错误、未知 event type 必须返回 error（fail-closed），禁止 return nil 吞掉 |
| **CR-7** | Consumer 必须实现 `Close() error`，由 bootstrap 统一管理生命周期 |
| **CR-8** | Consumer handler 必须自带 `defer recover()` + logger.Error + debug.Stack，不依赖 common 层 |
| **CR-9** | 幂等 TTL 统一为 7 天（`7*24*time.Hour`） |
| **CR-10** | 幂等 key 命名：`cashparty:{domain}:event:processed:{eventID 或 traceID}` |
| **CR-11** | Redis 不可用时 `tryAcquire` 返回 error（fail-closed），触发 Kafka 重试 |
| **CR-12** | GroupID 策略：广播类用固定 group（`gateway-broadcast`），事件类 per-node group（`{groupID}-{nodeID}`）+ Redis SetNX 互斥 |

### 6.3 Broadcaster 规约

| 规则 | 说明 |
|------|------|
| **BR-1** | `Broadcaster` 接口全项目唯一，定义在 `common/broadcast/broadcaster.go`；`domain.Broadcaster` 必须用类型别名引用 |
| **BR-2** | `BroadcastMessage` 必须通过 `NewBroadcastMessage` 工厂创建，自动填 `event_id`/`trace_id`/`timestamp`/`version` |
| **BR-3** | 广播失败记 Warn（不阻塞主流程），事件发布失败记 Error（触发重试） |
| **BR-4** | Redis Pub/Sub consumer 必须实现重连：断开后指数退避（1s→30s 上限）+ jitter，重连成功记 Info |
| **BR-5** | `BroadcastService.Start` 失败必须重试（最多 3 次），仍失败才发 errChan |
| **BR-6** | `handleBroadcastMessage` 必须自带 `defer recover()` |
| **BR-7** | `roomUsersCache` 必须有最大条目限制（默认 10000），LRU 淘汰 |
| **BR-8** | JSON 序列化错误禁止用 `_ =` 忽略，必须记 Error |

### 6.4 事件模型规约

| 规则 | 说明 |
|------|------|
| **ER-1** | 所有事件必须有 `EventID`（UUID）/ `TraceID` / `Timestamp`（Unix 毫秒）/ `Version`（当前 1） |
| **ER-2** | `EventType` 统一为 string，命名用动词原形（`session_start` / `round_settle`） |
| **ER-3** | `Payload` / `Data` 必须为 `json.RawMessage`，通过 `SetPayload` / `GetPayload` 辅助方法操作 |
| **ER-4** | 事件结构变更必须递增 `Version`，consumer 端按 Version 路由 |
| **ER-5** | `EventPublisher` 接口必须覆盖 Room + Game 两种事件，禁止 concrete struct 注入 |
| **ER-6** | Publisher 必须校验必填字段（如 TraceID），缺失返回 error |
| **ER-7** | 死代码事件类型必须删除，新增类型必须有 consumer 处理 |
| **ER-8** | Topic 命名：`cashparty.<domain>.<action>`，常量定义集中在 `common/kafka/topics.go` |

### 6.5 配置规约

| 规则 | 说明 |
|------|------|
| **CF-1** | `KafkaConfig` 全项目唯一，定义在 `common/config/types.go`，包含 `Enabled` / `Brokers` 字段 |
| **CF-2** | `BroadcastConfig` 全项目唯一，定义在 `common/config/types.go` |
| **CF-3** | gateway/config 禁止重复定义 Kafka/Broadcast 配置结构体 |
| **CF-4** | Producer/Consumer 创建前必须校验 `Enabled` 或 `len(Brokers) > 0` |

### 6.6 生命周期规约

| 规则 | 说明 |
|------|------|
| **LF-1** | 所有 consumer/broadcaster goroutine 必须由 Application wg 或 TaskRunner 管理 |
| **LF-2** | `Application.Stop()` 顺序：taskRunner.Stop → cancel appCtx → Container.Stop → wg.Wait(10s) → taskRunner.Wait(30s) → consumer.Close → producer.Close → redis/mysql close |
| **LF-3** | consumer.Close 必须幂等，重复调用安全 |
| **LF-4** | 所有长驻 goroutine 必须有 `defer recover()` + logger.Error + debug.Stack |

---

## 7. 验证清单

### 7.1 编译与格式

- [ ] `go build ./common/... ./game/... ./gateway/... ./stats/... ./cmd/... ./api/... ./settlement/...` BUILD_OK
- [ ] `go vet ./common/kafka/... ./common/broadcast/... ./common/message/... ./game/infrastructure/messaging/... ./game/infrastructure/broadcast/... ./gateway/broadcast/... ./gateway/connection/...` VET_OK
- [ ] `gofmt -l common/kafka/ common/broadcast/ common/message/ game/infrastructure/messaging/ game/infrastructure/broadcast/ gateway/broadcast/ gateway/connection/` 无输出
- [ ] `go test ./common/kafka/... ./common/broadcast/... ./game/domain/...` 全部 ok

### 7.2 P0 问题修复验证

- [ ] `grep -n "CommitMessages" backend/common/kafka/consumer.go` 在 processWithRetry 成功路径之后
- [ ] `grep -n "LeastBytes" backend/common/kafka/producer.go` → 0 matches
- [ ] `grep -n "key, nil\|nil, data" backend/game/infrastructure/messaging/room_event_publisher.go` → 0 matches（RoomEvent 必须传 roomID 作 key）
- [ ] `grep -n "StartOffset.*LastOffset" backend/common/kafka/` → 0 matches（默认 FirstOffset）
- [ ] `grep -n "CommitInterval: time.Second" backend/common/kafka/consumer.go` → 0 matches（默认 0）
- [ ] `grep -n "Receive(c.ctx)" backend/common/broadcast/redis_pubsub_consumer.go` → 0 matches（改为重连循环）
- [ ] `common/kafka/producer.go` 的 `GetWriter` 有 `sync.RWMutex` 保护

### 7.3 接口统一验证

- [ ] `grep -rn "type Broadcaster interface" backend/` → 仅 1 处（common/broadcast/broadcaster.go）
- [ ] `grep -rn "type EventPublisher interface" backend/` → 仅 1 处（game/domain/messaging.go）
- [ ] `grep -rn "type KafkaConfig struct" backend/` → 仅 1 处（common/config/types.go）
- [ ] `grep -rn "type BroadcastConfig struct" backend/` → 仅 1 处（common/config/types.go）
- [ ] `grep -rn "BroadcastTopicKafka" backend/` → 0 matches（已合并）

### 7.4 错误处理统一验证

- [ ] `grep -n "return nil" backend/game/infrastructure/messaging/room_event_consumer.go | grep -iE "parse|unknown"` → 0 matches
- [ ] `grep -n "24 \* time.Hour" backend/game/infrastructure/messaging/` → 0 matches（幂等 TTL 统一 7d）
- [ ] `grep -rn "_ = .*ToJSON\(\)\|_ = json.Marshal" backend/gateway/` → 0 matches
- [ ] 每个 consumer 文件有 `defer func() { recover() ... }` → ≥2 matches

### 7.5 消息信封验证

- [ ] `BroadcastMessage` 有 `EventID` / `TraceID` / `Timestamp` / `Version` 字段
- [ ] `EventEnvelope` 在 `common/message/event.go` 定义
- [ ] `RoomEvent` / `GameEvent` 有 `EventID` / `TraceID` / `Timestamp`（毫秒）/ `Version` 字段
- [ ] `RoomEventType` 是 string 类型
- [ ] `grep -n "Payload.*interface{}\|Data.*interface{}" backend/game/domain/events.go` → 0 matches

### 7.6 死代码清理验证

- [ ] `grep -n "TopicGameResult\|TopicRoundResult\|TopicSettlementReply\|TopicPlayerAction" backend/common/kafka/topics.go` → 0 matches
- [ ] `grep -n "RoomEventPlayerJoin\|RoomEventPlayerLeave\|RoomEventStatusChange\|RoomEventPlayerDisconnect" backend/game/domain/events.go` → 0 matches
- [ ] `grep -n "func NewProducer(cfg \*config.KafkaConfig)" backend/common/kafka/producer.go` → 0 matches
- [ ] `common/broadcast/constants.go` 文件不存在

---

## 8. 风险与回滚

### 8.1 风险评估

| 阶段 | 风险 | 缓解 |
|------|------|------|
| Phase 1 | Producer 改造可能影响现有消息发送 | brokers 守卫在配置完整时无影响；Hash balancer 与 LeastBytes 行为差异：同 key 落同分区，需确认分区数足够 |
| Phase 2 | Consumer fail-closed commit 改造影响最大 | 重试 + DLQ 兜底；先在测试环境验证；保留旧 commit 逻辑作为 feature flag（若需）|
| Phase 3 | 事件结构变更不向后兼容 | `Version` 字段 + consumer 端版本路由；新旧 consumer 共存一段时间 |
| Phase 4 | Publisher Key 变更导致分区分布变化 | 部署期间可能有短暂乱序；建议低峰期部署 |
| Phase 5 | parse 错误 fail-closed 会让毒消息进 DLQ | DLQ 需监控告警；运维需有 DLQ 处理流程 |
| Phase 6 | Redis Pub/Sub 重连可能导致短暂重复消费 | 业务层已有 SetNX 幂等，无影响 |
| Phase 7 | 无业务行为变更，仅装配重构 | 低风险 |
| Phase 8 | 新增 Settlement 事件，可选阶段 | 不实施无影响 |

### 8.2 回滚策略

- 每个阶段独立提交，可单独 revert
- Phase 2（Consumer fail-closed）若线上问题严重，可通过 `ConsumerConfig.CommitInterval = time.Second` + `MaxRetries = 0` 临时回退到旧行为
- Phase 3（事件结构）通过 `Version` 字段保留旧格式消费能力
- Phase 8 不实施不影响其他阶段

### 8.3 灰度策略

1. Phase 1-2 在测试环境跑 1 周，验证消息不丢失、不重复
2. Phase 3-5 灰度发布：先发 game 服务，观察 1 天，再发 gateway
3. Phase 6-7 全量发布
4. Phase 8 视业务需求决定

---

## 附录 A：决策记录

| # | 决策 | 理由 |
|---|------|------|
| D1 | Balancer 用 `Hash` 而非 `ReferenceHash` | Hash 简单且足够；ReferenceHash 用于跨集群一致性场景，本项目不需要 |
| D2 | 保留 per-node GroupID + SetNX 互斥 | 业务需要每个节点感知事件（robot scheduler 等）；改为固定 group 需更大改造，暂不做 |
| D3 | DLQ 投递后仍 commit | 避免毒消息永久阻塞分区；DLQ 有监控告警 |
| D4 | 事件命名保留动词原形 | 改为过去分词涉及 consumer 端解析，改动大收益小；文档化即可 |
| D5 | `RoomEventType` 从 int 改为 string | 一次性改造，避免 JSON 序列化为数字的可读性问题 |
| D6 | 时间戳统一为 Unix 毫秒 | 与 `PushMessage.Timestamp` 对齐；kafka-go 内部也用毫秒 |
| D7 | Settlement 事件发布列为 Phase 8 可选 | 当前业务无强需求，但为未来风控/审计预留 |
| D8 | 不引入 Redis Streams 替代 Pub/Sub | 改动过大；通过重连 + 业务层补偿机制缓解消息丢失 |

---

## 附录 B：与现有规约的关系

| 现有规约 | 本文关系 |
|---------|---------|
| `CODING_STANDARD.md` | 本文的规约（§6）作为 CODING_STANDARD 的 MQ 专项补充 |
| `GOROUTINE_REFACTOR_PLAN.md` | 本文 Phase 7 依赖 goroutine 重构的 TaskRunner 与 Application 生命周期 |
| `NACOS_REFACTOR_PLAN.md` | 无直接依赖；MQ 配置可由 nacos 热更新，但不在本文范围 |

---

**版本**：v1
**创建日期**：2026-07-04
**作者**：AI 辅助生成，基于 4 个并行 review agent 的全面审计
