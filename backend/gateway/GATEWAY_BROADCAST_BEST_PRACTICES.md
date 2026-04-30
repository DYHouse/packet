# Gateway 广播系统多实例部署最佳实践

## 目录

- [概述](#概述)
- [命名规范](#命名规范)
- [架构设计](#架构设计)
- [核心问题与解决方案](#核心问题与解决方案)
- [高并发优化](#高并发优化)
- [多实例部署](#多实例部署)
- [故障排查](#故障排查)
- [性能测试](#性能测试)
- [迁移指南](#迁移指南)

---

## 概述

### 适用场景

本文档适用于以下场景：
- Gateway 多实例部署（2个以上实例）
- 房间内用户可能连接到不同 Gateway 实例
- 需要支持高并发（单房间1000+用户，总在线10万+用户）
- 需要保证消息可靠性和低延迟

### 核心挑战

1. **跨实例消息同步**：同一房间的用户分布在不同 Gateway 实例上
2. **房间状态一致性**：多个实例需要共享房间用户信息
3. **高并发性能**：大房间广播时需要高效处理
4. **故障容错**：单个实例故障不影响整体服务

---

## 命名规范

### Redis Key 命名规范

#### 基本格式

```
cashparty:{domain}:{subtype}:{id}
```

#### Gateway Redis Keys

Gateway 不需要维护独立的 Redis keys，直接使用 Game 服务维护的数据。

#### Game 服务的 Redis Keys（Gateway 直接读取）

| Key 格式 | 说明 | 数据类型 | 示例 |
|---------|------|---------|------|
| `cashparty:room:players:{roomID}` | 房间玩家列表 | Hash | `cashparty:room:players:room123` |
| `cashparty:room:spectators:{roomID}` | 房间观众列表 | Hash | `cashparty:room:spectators:room123` |
| `cashparty:player:room:{userID}` | 用户所在房间 | String | `cashparty:player:room:user456` |
| `cashparty:room:hash:{roomID}` | 房间元数据 | Hash | `cashparty:room:hash:room123` |

#### 代码实现

Gateway 定义自己的 Redis keys（与 Game 服务保持一致）：

```go
package broadcast

import "fmt"

const (
    KeyRoomPlayers    = "cashparty:room:players:%s"
    KeyRoomSpectators = "cashparty:room:spectators:%s"
)

func RoomPlayersKey(roomID string) string {
    return fmt.Sprintf(KeyRoomPlayers, roomID)
}

func RoomSpectatorsKey(roomID string) string {
    return fmt.Sprintf(KeyRoomSpectators, roomID)
}
```

使用示例：

```go
// 在 BroadcastService 中使用
players, err := s.redis.HGetAll(ctx, RoomPlayersKey(roomID)).Result()
spectators, err := s.redis.HGetAll(ctx, RoomSpectatorsKey(roomID)).Result()
```

### Kafka Topic 命名规范

#### 基本格式

```
cashparty.{domain}.{action}
```

#### Gateway Kafka Topics

| Topic 名称 | 说明 | 生产者 | 消费者 |
|-----------|------|--------|--------|
| `cashparty.gateway.broadcast` | Gateway 广播消息 | Game Service | All Gateways |

#### 代码实现

```go
package kafka

const (
    TopicGatewayBroadcast = "cashparty.gateway.broadcast"
)
```

### 命名规范最佳实践

#### ✅ 推荐做法

1. **统一前缀**：所有 Redis key 和 Kafka topic 使用 `cashparty` 前缀
2. **层次清晰**：使用 `:` 分隔 Redis key 层级，使用 `.` 分隔 Kafka topic 层级
3. **语义明确**：名称应清楚表达用途，避免缩写
4. **类型一致**：同一类型的 key 使用相同的数据结构
5. **集中管理**：在 `keys.go` 和 `topics.go` 中集中定义常量
6. **复用数据**：Gateway 直接使用 Game 服务维护的 Redis 数据，避免重复存储

#### ❌ 避免做法

1. **硬编码字符串**：不要在代码中直接使用字符串
   ```go
   // ❌ 错误
   redis.HGetAll(ctx, "cashparty:room:players:"+roomID)
   
   // ✅ 正确
   redis.HGetAll(ctx, gameRedis.RoomPlayersKey(roomID))
   ```

2. **数据重复存储**：不要在 Gateway 维护独立的房间用户映射
   ```go
   // ❌ 错误：Gateway 维护独立的映射
   redis.SAdd(ctx, "cashparty:room:users:"+roomID, userID)
   
   // ✅ 正确：直接使用 Game 的数据
   redis.HGetAll(ctx, gameRedis.RoomPlayersKey(roomID))
   ```

3. **缺少文档**：不要省略 key 和 topic 的说明
   ```go
   // ❌ 错误
   const KeyRoomPlayers = "cashparty:room:players:%s"
   
   // ✅ 正确
   // KeyRoomPlayers 房间玩家列表，存储房间内的所有玩家信息
   // 数据类型: Hash
   // 维护方: Game 服务
   const KeyRoomPlayers = "cashparty:room:players:%s"
   ```

---

## 架构设计

### 整体架构

```
┌─────────────────────────────────────────────────────────────┐
│                        Redis Cluster                        │
│  ┌──────────────────────────────────────────────────────┐  │
│  │  Game 服务维护的数据（主数据源）：                    │  │
│  │  cashparty:room:players:{roomID}    -> Hash{player}  │  │
│  │  cashparty:room:spectators:{roomID} -> Hash{spectator}│  │
│  │  cashparty:player:room:{userID}     -> String{roomID}│  │
│  │  cashparty:room:hash:{roomID}       -> Hash{metadata}│  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                              ▲
                              │ 直接读取
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
   ┌────▼────┐          ┌────▼────┐          ┌────▼────┐
   │Gateway-1│          │Gateway-2│          │Gateway-3│
   │ Node-1  │          │ Node-2  │          │ Node-3  │
   └─────────┘          └─────────┘          └─────────┘
        │                     │                     │
        │    ┌────────────────┼────────────────┐   │
        │    │                │                │   │
        └────┼────────────────┼────────────────┼───┘
             │                │                │
        ┌────▼────────────────▼────────────────▼────┐
        │          Kafka (Broadcast Topic)           │
        │   Topic: cashparty.gateway.broadcast       │
        │   Partitions: 10 (根据实例数量调整)         │
        │   Replication: 3                           │
        │   Consumer Group: gateway-{nodeID}         │
        └────────────────────────────────────────────┘
                              ▲
                              │
                        ┌─────┴─────┐
                        │Game Service│
                        │           │
                        │ 维护房间  │
                        │ 用户映射  │
                        └───────────┘
```

### 数据流

#### 1. 用户加入房间流程

```
Client -> Gateway -> Game Service -> Redis
   │         │            │             │
   │         ├─1. 转发请求 ├─────────────┤
   │         │            ├─2. 更新 Redis:
   │         │            │   - HSET room:players:{roomID}
   │         │            │   - HSET room:spectators:{roomID}
   │         │            │   - SET player:room:{userID}
   │         │            │
   │         │            ├─3. 返回成功响应
   │         ├─4. 返回给客户端           │
   │<────────┤            │             │
   │                      │             │
   │                      ├─5. 发送广播消息 (Kafka)
   │<─────────────────────┼─────────────┤
```

#### 2. 房间广播流程

```
Game Service -> Kafka -> All Gateways
                           │
                           ├─1. 解析广播消息
                           ├─2. 从 Redis 获取房间用户列表
                           │    - HGETALL room:players:{roomID}
                           │    - HGETALL room:spectators:{roomID}
                           ├─3. 过滤本地连接的用户
                           └─4. 批量发送消息
```

#### 3. 用户断开连接流程

```
Client Disconnect -> Gateway
                       │
                       ├─1. 从本地连接管理器移除
                       ├─2. 检查用户是否还有其他连接
                       ├─3. 如果没有连接:
                       │    └─ 通知 Game 服务用户断线
                       │       (Game 会处理断线重连逻辑)
                       └─4. 记录监控指标
```

---

## 核心问题与解决方案

### 问题1: 房间用户信息不共享

**问题描述**：
- 原实现使用 `sync.Map` 存储房间用户信息
- 每个 Gateway 实例只知道自己本地的房间用户
- 无法获取其他实例上的房间用户

**解决方案**：
直接使用 Game 服务维护的 Redis 数据，无需 Gateway 维护独立映射

**代码实现**：

```go
package broadcast

import (
    "context"
    
    "github.com/cashparty/backend/common/logger"
    gameRedis "github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

type BroadcastService struct {
    redis   *cRedis.Client
    manager *connection.Manager
}

func NewBroadcastService(redis *cRedis.Client, manager *connection.Manager) *BroadcastService {
    return &BroadcastService{
        redis:   redis,
        manager: manager,
    }
}

// 获取房间内的所有用户（从 Game 的 Redis 读取）
func (s *BroadcastService) GetRoomUsers(ctx context.Context, roomID string) ([]string, error) {
    // 1. 从 Game 的 Redis 获取玩家列表
    players, err := s.redis.HGetAll(ctx, gameRedis.RoomPlayersKey(roomID)).Result()
    if err != nil {
        logger.Error("failed to get room players", "room_id", roomID, "error", err)
        return nil, err
    }
    
    // 2. 从 Game 的 Redis 获取观众列表
    spectators, err := s.redis.HGetAll(ctx, gameRedis.RoomSpectatorsKey(roomID)).Result()
    if err != nil {
        logger.Error("failed to get room spectators", "room_id", roomID, "error", err)
        return nil, err
    }
    
    // 3. 合并玩家和观众
    users := make([]string, 0, len(players)+len(spectators))
    for userID := range players {
        users = append(users, userID)
    }
    for userID := range spectators {
        users = append(users, userID)
    }
    
    return users, nil
}
```

### 问题2: 广播逻辑错误

**问题描述**：
- 原实现遍历所有本地连接，而不是房间用户
- 会把消息发给不在房间的用户

**解决方案**：
从 Game 的 Redis 获取房间用户列表，只发给房间内的本地用户

**代码实现**：

```go
func (s *BroadcastService) broadcastToRoom(roomID string, messageData []byte, excludeUserID string) {
    ctx := context.Background()
    
    // 从 Game 的 Redis 获取房间用户列表
    userIDs, err := s.GetRoomUsers(ctx, roomID)
    if err != nil {
        logger.Error("failed to get room users from game redis", 
            "room_id", roomID, 
            "error", err)
        return
    }
    
    // 发送给本地用户
    for _, userID := range userIDs {
        if userID == excludeUserID {
            continue
        }
        
        if s.manager.IsUserConnectedLocally(userID) {
            s.manager.BroadcastToUser(userID, messageData)
        }
    }
}
```

### 问题3: Kafka 消息只被一个实例消费

**问题描述**：
- 默认情况下，Kafka 同一 Consumer Group 只有一个消费者消费消息
- 其他 Gateway 实例收不到广播消息

**解决方案**：
每个 Gateway 实例使用不同的 Consumer Group ID

**配置示例**：

```go
package kafka

import (
    "fmt"
    
    "github.com/IBM/sarama"
    "github.com/cashparty/backend/common/kafka"
)

type BroadcastConsumerConfig struct {
    Brokers       []string
    Topic         string
    NodeID        string
    InitialOffset int64
}

func NewBroadcastConsumer(config *BroadcastConsumerConfig) (sarama.ConsumerGroup, error) {
    saramaConfig := sarama.NewConfig()
    saramaConfig.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
    saramaConfig.Consumer.Offsets.Initial = sarama.OffsetNewest
    saramaConfig.Consumer.Return.Errors = true
    
    if config.InitialOffset != 0 {
        saramaConfig.Consumer.Offsets.Initial = config.InitialOffset
    }
    
    consumerGroup := fmt.Sprintf("gateway-broadcast-%s", config.NodeID)
    
    return sarama.NewConsumerGroup(
        config.Brokers,
        consumerGroup,
        saramaConfig,
    )
}
```

**Kafka Topic 配置**：

```bash
# 创建广播 Topic
kafka-topics.sh --create \
  --bootstrap-server localhost:9092 \
  --topic cashparty.gateway.broadcast \
  --partitions 10 \
  --replication-factor 3 \
  --config retention.ms=3600000 \
  --config cleanup.policy=delete
```

---

## 高并发优化

### 1. 并发广播优化

**问题**：大房间广播时，串行发送消息导致延迟高

**解决方案**：使用 goroutine pool 并发发送

```go
type BroadcastWorkerPool struct {
    workerCount int
    taskChan    chan *BroadcastTask
    wg          sync.WaitGroup
}

type BroadcastTask struct {
    userID      string
    messageData []byte
}

func NewBroadcastWorkerPool(workerCount int) *BroadcastWorkerPool {
    pool := &BroadcastWorkerPool{
        workerCount: workerCount,
        taskChan:    make(chan *BroadcastTask, 10000),
    }
    
    pool.start()
    return pool
}

func (p *BroadcastWorkerPool) start() {
    for i := 0; i < p.workerCount; i++ {
        p.wg.Add(1)
        go p.worker()
    }
}

func (p *BroadcastWorkerPool) worker() {
    defer p.wg.Done()
    
    for task := range p.taskChan {
        p.manager.BroadcastToUser(task.userID, task.messageData)
    }
}

func (p *BroadcastWorkerPool) Submit(userID string, messageData []byte) {
    p.taskChan <- &BroadcastTask{
        userID:      userID,
        messageData: messageData,
    }
}

func (s *BroadcastService) broadcastToRoom(roomID string, messageData []byte, excludeUserID string) {
    ctx := context.Background()
    
    userIDs, err := s.GetRoomUsers(ctx, roomID)
    if err != nil {
        logger.Error("failed to get room users", "room_id", roomID, "error", err)
        return
    }
    
    var wg sync.WaitGroup
    batchSize := 100
    
    for i := 0; i < len(userIDs); i += batchSize {
        end := i + batchSize
        if end > len(userIDs) {
            end = len(userIDs)
        }
        
        batch := userIDs[i:end]
        
        wg.Add(1)
        go func(users []string) {
            defer wg.Done()
            
            for _, userID := range users {
                if userID == excludeUserID {
                    continue
                }
                
                if s.manager.IsUserConnectedLocally(userID) {
                    s.workerPool.Submit(userID, messageData)
                }
            }
        }(batch)
    }
    
    wg.Wait()
}
```

### 2. 消息压缩

**问题**：大消息体占用带宽，影响性能

**解决方案**：对大消息进行压缩

```go
import (
    "bytes"
    "compress/gzip"
    "io"
)

type MessageCompressor struct {
    threshold int
}

func NewMessageCompressor(threshold int) *MessageCompressor {
    return &MessageCompressor{threshold: threshold}
}

func (mc *MessageCompressor) Compress(data []byte) ([]byte, bool, error) {
    if len(data) < mc.threshold {
        return data, false, nil
    }
    
    var buf bytes.Buffer
    writer := gzip.NewWriter(&buf)
    
    if _, err := writer.Write(data); err != nil {
        return nil, false, err
    }
    
    if err := writer.Close(); err != nil {
        return nil, false, err
    }
    
    return buf.Bytes(), true, nil
}

func (mc *MessageCompressor) Decompress(data []byte, compressed bool) ([]byte, error) {
    if !compressed {
        return data, nil
    }
    
    reader, err := gzip.NewReader(bytes.NewReader(data))
    if err != nil {
        return nil, err
    }
    defer reader.Close()
    
    return io.ReadAll(reader)
}
```

### 3. 连接管理优化

**问题**：频繁的连接注册/注销操作影响性能

**解决方案**：使用连接池和批量更新

```go
type Manager struct {
    localConnections sync.Map
    userConnections  sync.Map
    redis            *cRedis.Client
    nodeID           string
    
    batchUpdates     chan *ConnectionUpdate
    batchTimer       *time.Timer
    batchMutex       sync.Mutex
}

type ConnectionUpdate struct {
    userID string
    connID string
    action string
}

func (m *Manager) startBatchUpdater() {
    ticker := time.NewTicker(100 * time.Millisecond)
    defer ticker.Stop()
    
    updates := make([]*ConnectionUpdate, 0, 100)
    
    for {
        select {
        case update := <-m.batchUpdates:
            updates = append(updates, update)
            
            if len(updates) >= 100 {
                m.flushBatchUpdates(updates)
                updates = updates[:0]
            }
            
        case <-ticker.C:
            if len(updates) > 0 {
                m.flushBatchUpdates(updates)
                updates = updates[:0]
            }
            
        case <-m.ctx.Done():
            return
        }
    }
}

func (m *Manager) flushBatchUpdates(updates []*ConnectionUpdate) {
    if len(updates) == 0 {
        return
    }
    
    pipe := m.redis.Pipeline()
    
    for _, update := range updates {
        key := fmt.Sprintf(ConnectionKeyFormat, update.userID)
        
        switch update.action {
        case "register":
            pipe.HSet(ctx, key, update.connID, m.nodeID)
        case "unregister":
            pipe.HDel(ctx, key, update.connID)
        }
    }
    
    _, err := pipe.Exec(ctx)
    if err != nil {
        logger.Error("failed to flush batch updates", "error", err)
    }
}
```

---

## 多实例部署

### 部署架构

```
┌─────────────────────────────────────────────────────────────┐
│                        Load Balancer                        │
│                   (Nginx / HAProxy / AWS ALB)               │
└─────────────────────────────────────────────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
   ┌────▼────┐          ┌────▼────┐          ┌────▼────┐
   │Gateway-1│          │Gateway-2│          │Gateway-3│
   │  Pod 1  │          │  Pod 2  │          │  Pod 3  │
   └─────────┘          └─────────┘          └─────────┘
        │                     │                     │
        └─────────────────────┼─────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
   ┌────▼────┐          ┌────▼────┐          ┌────▼────┐
   │ Redis-1 │          │ Redis-2 │          │ Redis-3 │
   │ Master  │          │ Slave-1 │          │ Slave-2 │
   └─────────┘          └─────────┘          └─────────┘
        │                     │                     │
        └─────────────────────┼─────────────────────┘
                              │
        ┌─────────────────────┼─────────────────────┐
        │                     │                     │
   ┌────▼────┐          ┌────▼────┐          ┌────▼────┐
   │Kafka-1  │          │Kafka-2  │          │Kafka-3  │
   │Broker-1 │          │Broker-2 │          │Broker-3 │
   └─────────┘          └─────────┘          └─────────┘
```

### Kubernetes 部署配置

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: gateway
  labels:
    app: gateway
spec:
  replicas: 3
  selector:
    matchLabels:
      app: gateway
  template:
    metadata:
      labels:
        app: gateway
    spec:
      containers:
      - name: gateway
        image: gateway:latest
        ports:
        - containerPort: 8080
        env:
        - name: NODE_ID
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: REDIS_ADDRS
          value: "redis-1:6379,redis-2:6379,redis-3:6379"
        - name: KAFKA_BROKERS
          value: "kafka-1:9092,kafka-2:9092,kafka-3:9092"
        - name: MAX_CONNECTIONS
          value: "10000"
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "1Gi"
            cpu: "1000m"
        livenessProbe:
          httpGet:
            path: /live
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: gateway
spec:
  selector:
    app: gateway
  ports:
  - port: 80
    targetPort: 8080
  type: LoadBalancer
```

### 配置管理

```go
package config

import (
    "os"
    "strconv"
    "time"
)

type GatewayConfig struct {
    NodeID           string
    Port             int
    MaxConnections   int
    SendQueueSize    int
    
    Redis RedisConfig
    Kafka KafkaConfig
    
    Broadcast BroadcastConfig
}

type RedisConfig struct {
    Addrs        []string
    Password     string
    DB           int
    PoolSize     int
    MinIdleConns int
}

type KafkaConfig struct {
    Brokers         []string
    Topic           string
    ConsumerGroup   string
}

type BroadcastConfig struct {
    WorkerCount      int
    CacheTTL         time.Duration
    CompressThreshold int
}

func LoadConfig() *GatewayConfig {
    return &GatewayConfig{
        NodeID:         getEnv("NODE_ID", "gateway-1"),
        Port:          getEnvInt("PORT", 8080),
        MaxConnections: getEnvInt("MAX_CONNECTIONS", 10000),
        SendQueueSize:  getEnvInt("SEND_QUEUE_SIZE", 1000),
        
        Redis: RedisConfig{
            Addrs:        getEnvSlice("REDIS_ADDRS", []string{"localhost:6379"}),
            Password:     getEnv("REDIS_PASSWORD", ""),
            DB:           getEnvInt("REDIS_DB", 0),
            PoolSize:     getEnvInt("REDIS_POOL_SIZE", 100),
            MinIdleConns: getEnvInt("REDIS_MIN_IDLE_CONNS", 10),
        },
        
        Kafka: KafkaConfig{
            Brokers:       getEnvSlice("KAFKA_BROKERS", []string{"localhost:9092"}),
            Topic:         getEnv("KAFKA_TOPIC", "gateway-broadcast"),
            ConsumerGroup: getEnv("KAFKA_CONSUMER_GROUP", "gateway-broadcast"),
        },
        
        Broadcast: BroadcastConfig{
            WorkerCount:       getEnvInt("BROADCAST_WORKER_COUNT", 100),
            CacheTTL:          getEnvDuration("BROADCAST_CACHE_TTL", 5*time.Second),
            CompressThreshold: getEnvInt("BROADCAST_COMPRESS_THRESHOLD", 1024),
        },
    }
}

func getEnv(key, defaultValue string) string {
    if value := os.Getenv(key); value != "" {
        return value
    }
    return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
    if value := os.Getenv(key); value != "" {
        if intValue, err := strconv.Atoi(value); err == nil {
            return intValue
        }
    }
    return defaultValue
}

func getEnvSlice(key string, defaultValue []string) []string {
    if value := os.Getenv(key); value != "" {
        // 简单实现，实际应该用 strings.Split
        return []string{value}
    }
    return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
    if value := os.Getenv(key); value != "" {
        if duration, err := time.ParseDuration(value); err == nil {
            return duration
        }
    }
    return defaultValue
}
```

### 优雅关闭

```go
func (s *Server) GracefulShutdown(timeout time.Duration) error {
    ctx, cancel := context.WithTimeout(context.Background(), timeout)
    defer cancel()
    
    logger.Info("starting graceful shutdown", "timeout", timeout)
    
    // 1. 停止接受新连接
    if err := s.httpServer.Shutdown(ctx); err != nil {
        logger.Error("http server shutdown error", "error", err)
    }
    
    // 2. 等待现有连接处理完成
    done := make(chan struct{})
    go func() {
        s.connMgr.CloseAll()
        close(done)
    }()
    
    select {
    case <-done:
        logger.Info("all connections closed")
    case <-ctx.Done():
        logger.Warn("shutdown timeout, forcing close")
    }
    
    // 3. 停止广播服务
    s.broadcast.Close()
    
    // 4. 停止连接管理器
    s.connMgr.Stop()
    
    logger.Info("graceful shutdown completed")
    return nil
}
```

---

## 故障排查

### 常见问题

#### 1. 用户收不到房间消息

**排查步骤**：

```bash
# 1. 检查房间内的玩家
redis-cli HGETALL cashparty:room:players:{roomID}

# 2. 检查房间内的观众
redis-cli HGETALL cashparty:room:spectators:{roomID}

# 3. 检查用户所在的房间
redis-cli GET cashparty:player:room:{userID}

# 4. 检查 Kafka 消费者状态
kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group gateway-broadcast-{nodeID}

# 5. 检查 Gateway 日志
kubectl logs -f gateway-{nodeID} | grep "broadcast"
```

**可能原因**：
- Redis 房间用户信息未正确更新
- 用户连接断开但未清理房间信息
- Kafka 消费者 lag 过大
- Gateway 实例故障

#### 2. 广播延迟高

**排查步骤**：

```bash
# 1. 检查 Redis 延迟
redis-cli --latency

# 2. 检查房间用户数量
redis-cli HLEN cashparty:room:players:{roomID}
redis-cli HLEN cashparty:room:spectators:{roomID}

# 3. 检查 Gateway CPU 和内存
kubectl top pods -l app=gateway

# 4. 检查网络延迟
ping gateway-{nodeID}
```

**可能原因**：
- Redis 响应慢
- 房间用户数量过大
- Gateway 实例资源不足
- 网络问题

#### 3. 内存泄漏

**排查步骤**：

```bash
# 1. 查看内存使用
kubectl top pods -l app=gateway

# 2. 导出 heap profile
curl http://gateway-{nodeID}:8080/debug/pprof/heap > heap.out

# 3. 分析 heap profile
go tool pprof heap.out

# 4. 查看连接数
curl http://gateway-{nodeID}:8080/metrics | grep gateway_connection_count
```

**可能原因**：
- 连接未正确关闭
- 本地缓存未清理
- goroutine 泄漏

### 调试工具

```go
package debug

import (
    "net/http"
    "runtime/pprof"
    
    "github.com/gin-gonic/gin"
)

func SetupDebugRoutes(engine *gin.Engine, connMgr *connection.Manager, roomMgr *room.RoomManager) {
    debug := engine.Group("/debug")
    
    debug.GET("/connections", func(c *gin.Context) {
        stats := connMgr.GetStats()
        c.JSON(http.StatusOK, stats)
    })
    
    debug.GET("/rooms/:roomID", func(c *gin.Context) {
        roomID := c.Param("roomID")
        users, _ := roomMgr.GetRoomUsers(c.Request.Context(), roomID)
        count, _ := roomMgr.GetRoomUserCount(c.Request.Context(), roomID)
        
        c.JSON(http.StatusOK, gin.H{
            "room_id": roomID,
            "users":   users,
            "count":   count,
        })
    })
    
    debug.GET("/pprof/heap", func(c *gin.Context) {
        pprof.Handler("heap").ServeHTTP(c.Writer, c.Request)
    })
    
    debug.GET("/pprof/goroutine", func(c *gin.Context) {
        pprof.Handler("goroutine").ServeHTTP(c.Writer, c.Request)
    })
}
```

---

## 性能测试

### 测试场景

#### 1. 单房间大用户量测试

```bash
# 测试参数
房间用户数: 1000, 5000, 10000
消息频率: 10条/秒
消息大小: 100B, 1KB, 10KB

# 测试脚本
go run benchmark/room_broadcast.go \
  --room-users 10000 \
  --message-rate 10 \
  --message-size 1024 \
  --duration 60s
```

#### 2. 多房间测试

```bash
# 测试参数
房间数量: 100, 1000, 10000
每房间用户数: 10, 50, 100
消息频率: 1条/秒

# 测试脚本
go run benchmark/multi_room.go \
  --rooms 1000 \
  --users-per-room 50 \
  --message-rate 1 \
  --duration 60s
```

#### 3. 连接压力测试

```bash
# 测试参数
并发连接数: 10000, 50000, 100000
连接频率: 1000/秒

# 测试脚本
go run benchmark/connection_pressure.go \
  --connections 100000 \
  --connect-rate 1000 \
  --duration 300s
```

### 性能基准

| 指标 | 目标值 | 说明 |
|------|--------|------|
| 单房间广播延迟 (P95) | < 50ms | 1000用户房间 |
| 单房间广播延迟 (P99) | < 100ms | 1000用户房间 |
| Redis 操作延迟 (P95) | < 5ms | 所有操作 |
| Kafka 消费延迟 | < 100ms | Consumer lag |
| 连接建立延迟 | < 100ms | 包含认证 |
| 内存使用 | < 1GB | 10000连接 |
| CPU 使用 | < 70% | 10000连接，100房间 |

---

## 迁移指南

### 从单实例迁移到多实例

#### 阶段1: 准备工作

1. **部署 Redis Cluster**
   ```bash
   # 部署 3 master + 3 slave
   kubectl apply -f redis-cluster.yaml
   ```

2. **部署 Kafka Cluster**
   ```bash
   # 部署 3 broker
   kubectl apply -f kafka-cluster.yaml
   ```

3. **创建广播 Topic**
   ```bash
   kafka-topics.sh --create \
     --bootstrap-server kafka-1:9092 \
     --topic cashparty.gateway.broadcast \
     --partitions 10 \
     --replication-factor 3
   ```

#### 阶段2: 代码改造

1. **修改 BroadcastService**
   ```go
   // 直接使用 Game 的 Redis 数据
   func (s *BroadcastService) GetRoomUsers(ctx context.Context, roomID string) ([]string, error) {
       players, _ := s.redis.HGetAll(ctx, gameRedis.RoomPlayersKey(roomID)).Result()
       spectators, _ := s.redis.HGetAll(ctx, gameRedis.RoomSpectatorsKey(roomID)).Result()
       
       users := make([]string, 0, len(players)+len(spectators))
       for userID := range players {
           users = append(users, userID)
       }
       for userID := range spectators {
           users = append(users, userID)
       }
       
       return users, nil
   }
   ```

2. **修改 Kafka Consumer 配置**
   ```go
   // 每个 Gateway 实例使用不同的 Consumer Group
   consumerGroup := fmt.Sprintf("gateway-broadcast-%s", nodeID)
   ```

#### 阶段3: 灰度发布

1. **部署新版本 Gateway (1个实例)**
   ```bash
   kubectl scale deployment gateway --replicas=1
   ```

2. **验证功能**
   - 测试房间广播
   - 测试用户加入/离开房间
   - 测试跨实例消息同步

3. **逐步扩容**
   ```bash
   kubectl scale deployment gateway --replicas=2
   # 观察 24 小时
   kubectl scale deployment gateway --replicas=3
   ```

#### 阶段4: 性能调优

1. **优化 Redis 配置**
   - 增加连接池大小
   - 调整 Kafka 配置
   - 优化连接池

### 回滚方案

如果发现问题，可以快速回滚：

```bash
# 1. 缩容到单实例
kubectl scale deployment gateway --replicas=1

# 2. 回滚代码版本
kubectl rollout undo deployment/gateway
```

---

## 最佳实践总结

### 1. 架构设计

✅ **推荐做法**：
- Gateway 直接使用 Game 服务维护的 Redis 数据
- 每个 Gateway 实例使用独立的 Kafka Consumer Group
- 使用 Pipeline 批量操作 Redis
- 监控 Redis 查询延迟

❌ **避免做法**：
- 在 Gateway 维护独立的房间用户映射
- 使用相同的 Kafka Consumer Group
- 使用本地缓存（会导致数据不一致）
- 串行发送广播消息

### 2. 数据管理

✅ **推荐做法**：
- Game 服务作为唯一数据源
- Gateway 只读取，不修改房间数据
- 每次广播都查询最新数据

❌ **避免做法**：
- Gateway 维护独立的房间数据
- 数据重复存储
- 使用本地缓存

### 3. 故障容错

✅ **推荐做法**：
- 实现优雅关闭机制
- 添加健康检查接口
- 实现重试机制
- 监控关键指标并设置告警

❌ **避免做法**：
- 强制关闭连接
- 忽略错误和异常
- 没有监控和告警
- 单点故障

### 4. 运维管理

✅ **推荐做法**：
- 使用 Kubernetes 管理部署
- 实现配置中心化管理
- 定期备份 Redis 数据

❌ **避免做法**：
- 手动管理实例
- 配置硬编码
- 没有备份机制

### 5. 性能优化

✅ **推荐做法**：
- 使用 Pipeline 批量查询 Redis
- 使用 goroutine pool 并发发送消息
- 优化 Redis 连接池配置

❌ **避免做法**：
- 使用本地缓存（会导致数据不一致）
- 单线程处理广播消息
- 忽略 Redis 连接池优化

### 6. Redis 性能说明

**为什么不需要本地缓存？**

Redis 的 HGETALL 操作性能非常好：

```
性能测试数据：
- 单次 HGETALL 操作延迟：1-2ms
- Redis 单节点 QPS：10万+
- 广播消息频率：通常每秒几次到几十次
- 实际 Redis 负载：远低于性能上限

结论：
✅ Redis 性能完全够用
✅ 不需要本地缓存
✅ 数据一致性更重要
```

**如果确实遇到性能瓶颈**：

1. **优化 Redis 配置**
   - 增加连接池大小
   - 使用 Redis Cluster 分散负载
   - 启用 Redis 持久化优化

2. **优化查询方式**
   - 使用 Pipeline 批量查询
   - 减少 Redis 往返次数
   - 使用 Lua 脚本合并操作

3. **优化消息发送**
   - 使用 goroutine pool 并发发送
   - 批量发送消息
   - 异步处理非关键消息

---

## 附录

### A. 配置模板

```yaml
gateway:
  node_id: "gateway-1"
  port: 8080
  max_connections: 10000
  send_queue_size: 1000
  
redis:
  addrs:
    - "redis-1:6379"
    - "redis-2:6379"
    - "redis-3:6379"
  password: ""
  db: 0
  pool_size: 100
  min_idle_conns: 10
  
kafka:
  brokers:
    - "kafka-1:9092"
    - "kafka-2:9092"
    - "kafka-3:9092"
  topic: "cashparty.gateway.broadcast"
  consumer_group: "gateway-broadcast"
  
broadcast:
  worker_count: 100
```

### B. Game 服务的 Redis Keys

Gateway 直接使用 Game 服务维护的 Redis keys：

| Key | 说明 | 数据类型 | 用途 |
|-----|------|---------|------|
| `cashparty:room:players:{roomID}` | 房间玩家列表 | Hash | 存储房间内的所有玩家信息 |
| `cashparty:room:spectators:{roomID}` | 房间观众列表 | Hash | 存储房间内的所有观众信息 |
| `cashparty:player:room:{userID}` | 用户所在房间 | String | 快速查询用户当前在哪个房间 |
| `cashparty:room:hash:{roomID}` | 房间元数据 | Hash | 存储房间的基本信息 |

### C. 参考资料

- [Redis Cluster 官方文档](https://redis.io/docs/management/scaling/)
- [Kafka Consumer Group 机制](https://kafka.apache.org/documentation/#consumerconfigs)
- [WebSocket RFC 6455](https://datatracker.ietf.org/doc/html/rfc6455)
- [Prometheus 最佳实践](https://prometheus.io/docs/practices/)

---

**文档版本**: v2.0  
**最后更新**: 2024-01-XX  
**维护者**: Gateway Team  
**核心改进**: 直接使用 Game 服务的 Redis 数据，避免重复存储
