# 游戏后端架构重构方案

## 目录

- [一、重构概述](#一重构概述)
- [二、Redis Sentinel重构](#二redis-sentinel重构)
- [三、MySQL架构分析](#三mysql架构分析)
- [四、Kafka集群重构](#四kafka集群重构)
- [五、广播消息优化](#五广播消息优化)
- [六、实施计划](#六实施计划)
- [七、总结](#七总结)

---

## 一、重构概述

### 1.1 重构目标

1. ✅ 提高系统高可用性
2. ✅ 支持水平扩展
3. ✅ 优化性能和延迟
4. ✅ 降低运维复杂度
5. ✅ 保持向后兼容

### 1.2 重构范围

| 组件 | 当前架构 | 目标架构 | 优先级 |
|------|---------|---------|--------|
| Redis | 单机 | 主从+Sentinel | ⭐⭐⭐⭐⭐ |
| MySQL | 单机 | 保持单机 | ⭐⭐ |
| Kafka | 单机 | 集群 | ⭐⭐⭐⭐ |
| 广播消息 | Kafka | Redis Pub/Sub | ⭐⭐⭐⭐⭐ |

### 1.3 架构演进

**当前架构**：
```
┌─────────────┐
│ Game Service│
└──────┬──────┘
       │
       ├─→ Redis (单机) ← 实时数据
       │
       ├─→ MySQL (单机) ← 持久化
       │
       └─→ Kafka (单机) ← 消息队列
              │
              └─→ Gateway (广播)
```

**目标架构**：
```
┌─────────────┐
│ Game Service│
└──────┬──────┘
       │
       ├─→ Redis Sentinel ← 实时数据 + 广播
       │   ├─ Master
       │   ├─ Slave
       │   └─ Sentinel Cluster
       │
       ├─→ MySQL (单机) ← 持久化
       │
       └─→ Kafka Cluster ← 事件驱动
           ├─ Broker 1
           ├─ Broker 2
           └─ Broker 3
```

---

## 二、Redis Sentinel重构

### 2.1 重构必要性

#### 当前问题

1. ❌ 单点故障：Master宕机导致服务不可用
2. ❌ 无自动故障转移：需要人工干预
3. ❌ 数据备份不可靠：依赖RDB/AOF，可能有数据丢失

#### Sentinel优势

1. ✅ 自动故障转移：Master宕机自动切换
2. ✅ 自动发现：无需手动配置Master地址
3. ✅ 高可用：主从复制保证数据安全
4. ✅ 向后兼容：单机模式无需修改配置

### 2.2 架构设计

#### 部署架构

```
┌─────────────────────────────────────────┐
│         Application Layer                │
│  ┌────────────────────────────────────┐ │
│  │  Game Service                      │ │
│  │  Gateway Service                   │ │
│  │  Settlement Service                │ │
│  └────────────────────────────────────┘ │
└─────────────┬───────────────────────────┘
              │
              ↓
┌─────────────────────────────────────────┐
│         Sentinel Client                 │
│  (自动发现Master，自动故障转移)          │
└─────────────┬───────────────────────────┘
              │
              ↓
┌─────────────────────────────────────────┐
│         Sentinel Cluster (3节点)         │
│  ┌────────────────────────────────────┐ │
│  │  Sentinel 1: 192.168.1.101:26379   │ │
│  │  Sentinel 2: 192.168.1.102:26379   │ │
│  │  Sentinel 3: 192.168.1.103:26379   │ │
│  └────────────────────────────────────┘ │
└─────────────┬───────────────────────────┘
              │
              ↓
┌─────────────────────────────────────────┐
│         Redis Master-Slave               │
│  ┌────────────────────────────────────┐ │
│  │  Master: 192.168.1.100:6379        │ │
│  │  Slave:  192.168.1.101:6379        │ │
│  └────────────────────────────────────┘ │
└─────────────────────────────────────────┘
```

#### 故障转移流程

```
时间轴：
T0:  Master宕机
T5:  Sentinel检测到Master下线
T8:  Sentinel开始选举
T10: Slave提升为新Master
T11: 客户端自动连接到新Master
T12: 业务恢复正常

总耗时：约12秒
```

### 2.3 代码修改

#### 配置结构修改

**文件**: `common/config/config.go`

```go
type RedisConfig struct {
    // 单机模式配置
    Addr         string        `mapstructure:"addr"`
    Password     string        `mapstructure:"password"`
    DB           int           `mapstructure:"db"`
    PoolSize     int           `mapstructure:"pool_size"`
    MinIdleConns int           `mapstructure:"min_idle_conns"`
    DialTimeout  time.Duration `mapstructure:"dial_timeout"`
    ReadTimeout  time.Duration `mapstructure:"read_timeout"`
    WriteTimeout time.Duration `mapstructure:"write_timeout"`

    // Sentinel模式配置（新增）
    Mode          string   `mapstructure:"mode"`           // "standalone" 或 "sentinel"
    MasterName    string   `mapstructure:"master_name"`    // Sentinel监控的master名称
    SentinelAddrs []string `mapstructure:"sentinel_addrs"` // Sentinel节点地址列表
}
```

#### 客户端初始化修改

**文件**: `common/redis/redis.go`

```go
func NewClient(cfg *config.RedisConfig) (*Client, error) {
    var rdb *redis.Client
    var err error

    switch cfg.Mode {
    case "sentinel":
        rdb, err = newSentinelClient(cfg)
    case "standalone", "":
        rdb, err = newStandaloneClient(cfg)
    default:
        return nil, fmt.Errorf("unsupported redis mode: %s", cfg.Mode)
    }

    if err != nil {
        return nil, err
    }

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    if err := rdb.Ping(ctx).Err(); err != nil {
        return nil, fmt.Errorf("failed to connect to redis: %w", err)
    }

    logger.Info("redis connected", "mode", cfg.Mode, "db", cfg.DB)
    return &Client{rdb: rdb}, nil
}

func newSentinelClient(cfg *config.RedisConfig) (*redis.Client, error) {
    if len(cfg.SentinelAddrs) == 0 {
        return nil, fmt.Errorf("sentinel_addrs is required for sentinel mode")
    }
    if cfg.MasterName == "" {
        return nil, fmt.Errorf("master_name is required for sentinel mode")
    }

    rdb := redis.NewFailoverClient(&redis.FailoverOptions{
        MasterName:    cfg.MasterName,
        SentinelAddrs: cfg.SentinelAddrs,
        Password:      cfg.Password,
        DB:            cfg.DB,
        PoolSize:      cfg.PoolSize,
        MinIdleConns:  cfg.MinIdleConns,
        DialTimeout:   cfg.DialTimeout,
        ReadTimeout:   cfg.ReadTimeout,
        WriteTimeout:  cfg.WriteTimeout,
    })

    logger.Info("redis sentinel mode",
        "master_name", cfg.MasterName,
        "sentinel_addrs", cfg.SentinelAddrs)
    return rdb, nil
}
```

### 2.4 配置文件

#### 开发环境（单机模式）

**文件**: `config/game.yaml`

```yaml
redis:
  mode: "standalone"
  addr: "127.0.0.1:6379"
  password: ""
  db: 0
  pool_size: 100
  min_idle_conns: 10
  dial_timeout: 5s
  read_timeout: 3s
  write_timeout: 3s
  master_name: ""
  sentinel_addrs: []
```

#### 生产环境（Sentinel模式）

**文件**: `config/game.yaml`

```yaml
redis:
  mode: "sentinel"
  master_name: "mymaster"
  sentinel_addrs:
    - "192.168.1.101:26379"
    - "192.168.1.102:26379"
    - "192.168.1.103:26379"
  password: ""
  db: 0
  pool_size: 200
  min_idle_conns: 50
  dial_timeout: 5s
  read_timeout: 3s
  write_timeout: 3s
```

### 2.5 部署配置

#### Sentinel配置

**文件**: `sentinel.conf`

```bash
port 26379
sentinel monitor mymaster 192.168.1.100 6379 2
sentinel down-after-milliseconds mymaster 5000
sentinel failover-timeout mymaster 60000
sentinel parallel-syncs mymaster 1
```

#### Redis Master配置

**文件**: `redis.conf`

```bash
bind 0.0.0.0
port 6379
daemonize yes
logfile "/var/log/redis/redis.log"
dir "/var/lib/redis"

# 持久化配置
appendonly yes
appendfsync everysec

# 性能配置
maxmemory 4gb
maxmemory-policy allkeys-lru
```

#### Redis Slave配置

**文件**: `redis.conf`

```bash
bind 0.0.0.0
port 6379
daemonize yes
logfile "/var/log/redis/redis.log"
dir "/var/lib/redis"

# 主从复制配置
replicaof 192.168.1.100 6379
replica-read-only yes

# 持久化配置
appendonly yes
appendfsync everysec
```

### 2.6 优势总结

| 优势 | 说明 |
|------|------|
| ✅ 自动故障转移 | Master宕机自动切换，无需人工干预 |
| ✅ 自动发现 | 无需手动配置Master地址 |
| ✅ 向后兼容 | 单机模式无需修改配置 |
| ✅ 最小修改 | 只修改配置和客户端初始化 |
| ✅ 运维简单 | 只需维护配置文件 |

---

## 三、MySQL架构分析

### 3.1 当前使用情况

#### 数据流向

```
游戏实时数据：
Client → Redis (主存储) → MySQL (持久化)
         ↓
    实时读写
         ↓
    异步持久化
         ↓
      MySQL
```

#### 使用场景

| 模块 | 数据表 | 读写特点 | 实时性要求 |
|------|--------|---------|-----------|
| 游戏模块 | rooms, rounds, sessions | 写多读少 | 高 |
| 结算模块 | bill_records, round_settlements | 写多读少 | 高 |
| 用户模块 | users | 读写均衡 | 中 |
| 配置模块 | room_configs, reward_configs | 读多写少 | 低 |

### 3.2 主从架构分析

#### 优点

1. ✅ 读写分离：读操作分散到Slave
2. ✅ 数据备份：Slave作为实时备份
3. ✅ 高可用：Master故障时可切换

#### 缺点

1. ❌ 主从延迟：可能导致数据不一致
2. ❌ 复杂度增加：需要维护主从同步
3. ❌ 写操作瓶颈：所有写操作仍在Master

### 3.3 游戏业务特点

#### 关键问题

**问题1：主从延迟影响业务**
```
场景：用户刚结束游戏
1. Redis更新用户余额
2. MySQL写入账单记录（Master）
3. 用户立即查询账单（读Slave）
4. Slave还没同步到最新数据
5. 用户看到旧数据 ❌
```

**问题2：结算业务需要强一致性**
```
场景：结算扣款
1. 创建扣款记录
2. 调用第三方支付
3. 更新账单状态
4. 如果读写分离，可能导致数据不一致 ❌
```

**问题3：Redis已承担主要压力**
```
当前架构：
- Redis：实时读写（高并发）
- MySQL：数据持久化（写多读少）

结论：MySQL压力可控，单机足够
```

### 3.4 最终建议

## ❌ **不建议使用MySQL主从架构**

### 理由

1. **Redis已承担主要压力**：MySQL压力可控
2. **写操作频繁**：主从架构无法分担写压力
3. **实时性要求高**：主从延迟会影响业务
4. **架构简单**：运维成本低，不易出错

### 优化建议

#### 配置优化

```yaml
mysql:
  dsn: "user:pass@tcp(127.0.0.1:3306)/db?charset=utf8mb4&parseTime=True&loc=Local"
  max_open_conns: 200
  max_idle_conns: 50
  conn_max_lifetime: 3600
```

#### 性能优化

```sql
-- MySQL配置优化
SET GLOBAL innodb_buffer_pool_size = 4G;
SET GLOBAL innodb_log_file_size = 512M;
SET GLOBAL innodb_flush_log_at_trx_commit = 2;
```

#### 数据归档

```sql
-- 定期归档历史数据
-- 保留最近3个月数据
DELETE FROM bill_records WHERE created_at < DATE_SUB(NOW(), INTERVAL 3 MONTH);
```

---

## 四、Kafka集群重构

### 4.1 重构必要性

#### 当前问题

1. ❌ 单点故障：单节点宕机导致消息丢失
2. ❌ 性能瓶颈：单节点吞吐量有限
3. ❌ 无数据备份：节点故障可能导致数据丢失

#### 集群优势

1. ✅ 高可用性：多节点部署，单节点故障不影响
2. ✅ 数据可靠性：多副本机制，防止数据丢失
3. ✅ 水平扩展：可以动态添加节点扩展容量
4. ✅ 负载均衡：分区机制实现负载均衡

### 4.2 架构设计

#### 集群架构

```
┌─────────────────────────────────────────┐
│         Kafka Cluster (3节点)            │
│  ┌────────────────────────────────────┐ │
│  │  Broker 1 (kafka-node1:9092)       │ │
│  │  - Partition 0,1,2 (Leader)        │ │
│  │  - Partition 3,4,5 (Follower)      │ │
│  └────────────────────────────────────┘ │
│  ┌────────────────────────────────────┐ │
│  │  Broker 2 (kafka-node2:9092)       │ │
│  │  - Partition 2,3,4 (Leader)        │ │
│  │  - Partition 0,1,5 (Follower)      │ │
│  └────────────────────────────────────┘ │
│  ┌────────────────────────────────────┐ │
│  │  Broker 3 (kafka-node3:9092)       │ │
│  │  - Partition 4,5,0 (Leader)        │ │
│  │  - Partition 1,2,3 (Follower)      │ │
│  └────────────────────────────────────┘ │
└─────────────────────────────────────────┘
           ↑                    ↑
           │                    │
    ┌──────┴──────┐      ┌─────┴──────┐
    │  Zookeeper  │      │  Zookeeper  │
    │  Cluster    │      │  Cluster    │
    └─────────────┘      └─────────────┘
```

#### Topic分区策略

| Topic | 分区数 | 副本数 | 最小同步副本 | 说明 |
|-------|--------|--------|-------------|------|
| game_events | 6 | 3 | 2 | 高吞吐，按roomID分区 |
| room_events | 3 | 3 | 2 | 中等吞吐 |
| settlement | 3 | 3 | 2 | 重要数据，高可靠性 |

### 4.3 代码修改

#### 配置结构修改

**文件**: `common/config/config.go`

```go
type KafkaConfig struct {
    Brokers []string `mapstructure:"brokers"`
    
    Mode string `mapstructure:"mode"`
    
    Producer ProducerConfig `mapstructure:"producer"`
    Consumer ConsumerConfig `mapstructure:"consumer"`
    
    Topics map[string]TopicConfig `mapstructure:"topics"`
}

type ProducerConfig struct {
    BatchSize      int           `mapstructure:"batch_size"`
    BatchTimeout   time.Duration `mapstructure:"batch_timeout"`
    WriteTimeout   time.Duration `mapstructure:"write_timeout"`
    RequiredAcks   int           `mapstructure:"required_acks"`
    Async          bool          `mapstructure:"async"`
    Compression    string        `mapstructure:"compression"`
    MaxAttempts    int           `mapstructure:"max_attempts"`
    QueueCapacity  int           `mapstructure:"queue_capacity"`
}

type ConsumerConfig struct {
    GroupID        string        `mapstructure:"group_id"`
    MinBytes       int           `mapstructure:"min_bytes"`
    MaxBytes       int           `mapstructure:"max_bytes"`
    MaxWait        time.Duration `mapstructure:"max_wait"`
    CommitInterval time.Duration `mapstructure:"commit_interval"`
    StartOffset    int64         `mapstructure:"start_offset"`
    MaxAttempts    int           `mapstructure:"max_attempts"`
}

type TopicConfig struct {
    Partitions        int   `mapstructure:"partitions"`
    ReplicationFactor int   `mapstructure:"replication_factor"`
    MinInSyncReplicas int   `mapstructure:"min_in_sync_replicas"`
    RetentionMs       int64 `mapstructure:"retention_ms"`
    SegmentBytes      int64 `mapstructure:"segment_bytes"`
}
```

#### 生产者配置修改

**文件**: `common/kafka/producer.go`

```go
func NewProducer(cfg *config.KafkaConfig) *Producer {
    return &Producer{
        writers: make(map[string]*kafka.Writer),
        brokers: cfg.Brokers,
        config:  cfg.Producer,
    }
}

func (p *Producer) GetWriter(topic string) *kafka.Writer {
    if w, ok := p.writers[topic]; ok {
        return w
    }

    compression := kafka.Compression(codec.None)
    switch p.config.Compression {
    case "gzip":
        compression = kafka.Compression(codec.Gzip)
    case "snappy":
        compression = kafka.Compression(codec.Snappy)
    case "lz4":
        compression = kafka.Compression(codec.Lz4)
    }

    w := &kafka.Writer{
        Addr:          kafka.TCP(p.brokers...),
        Topic:         topic,
        Balancer:      &kafka.LeastBytes{},
        BatchSize:     p.config.BatchSize,
        BatchTimeout:  p.config.BatchTimeout,
        WriteTimeout:  p.config.WriteTimeout,
        RequiredAcks:  kafka.RequiredAcks(p.config.RequiredAcks),
        Async:         p.config.Async,
        Compression:   compression,
        MaxAttempts:   p.config.MaxAttempts,
        QueueCapacity: p.config.QueueCapacity,
    }
    p.writers[topic] = w
    return w
}
```

### 4.4 配置文件

#### 开发环境（单机模式）

**文件**: `config/game.yaml`

```yaml
kafka:
  mode: "standalone"
  brokers:
    - "127.0.0.1:9092"
  
  producer:
    batch_size: 100
    batch_timeout: 10ms
    write_timeout: 10s
    required_acks: 1
    async: false
    compression: "none"
    max_attempts: 3
    queue_capacity: 1000
  
  consumer:
    group_id: "cashparty-game"
    min_bytes: 1
    max_bytes: 10485760
    max_wait: 500ms
    commit_interval: 1s
    start_offset: -1
    max_attempts: 3
  
  topics:
    game_events:
      partitions: 1
      replication_factor: 1
      min_in_sync_replicas: 1
      retention_ms: 604800000
```

#### 生产环境（集群模式）

**文件**: `config/game.yaml`

```yaml
kafka:
  mode: "cluster"
  brokers:
    - "kafka-node1:9092"
    - "kafka-node2:9092"
    - "kafka-node3:9092"
  
  producer:
    batch_size: 1000
    batch_timeout: 10ms
    write_timeout: 10s
    required_acks: -1
    async: false
    compression: "lz4"
    max_attempts: 5
    queue_capacity: 10000
  
  consumer:
    group_id: "cashparty-game-prod"
    min_bytes: 1
    max_bytes: 10485760
    max_wait: 500ms
    commit_interval: 1s
    start_offset: -1
    max_attempts: 5
  
  topics:
    game_events:
      partitions: 6
      replication_factor: 3
      min_in_sync_replicas: 2
      retention_ms: 604800000
    room_events:
      partitions: 3
      replication_factor: 3
      min_in_sync_replicas: 2
      retention_ms: 604800000
    settlement:
      partitions: 3
      replication_factor: 3
      min_in_sync_replicas: 2
      retention_ms: 2592000000
```

### 4.5 部署配置

#### Kafka Server配置

**文件**: `server.properties`

```properties
# Broker配置
broker.id=1
listeners=PLAINTEXT://:9092
advertised.listeners=PLAINTEXT://kafka-node1:9092

# 日志配置
log.dirs=/var/kafka-logs
num.partitions=3
default.replication.factor=3
min.insync.replicas=2

# 日志保留
log.retention.hours=168
log.retention.bytes=1073741824
log.segment.bytes=1073741824

# 性能配置
num.network.threads=3
num.io.threads=8
socket.send.buffer.bytes=102400
socket.receive.buffer.bytes=102400
socket.request.max.bytes=104857600

# 复制配置
num.replica.fetchers=2
replica.fetch.max.bytes=1048576
replica.fetch.wait.max.ms=500

# Zookeeper
zookeeper.connect=zk1:2181,zk2:2181,zk3:2181
zookeeper.connection.timeout.ms=6000
```

---

## 五、广播消息优化

### 5.1 当前架构分析

#### 当前实现

```
Game Service (Producer)
    │
    ├─→ Kafka Topic: cashparty.gateway.broadcast
    │
    ├─→ Gateway-1 (Consumer Group: gateway-1)
    ├─→ Gateway-2 (Consumer Group: gateway-2)
    └─→ Gateway-3 (Consumer Group: gateway-3)
```

#### 问题分析

1. ❌ **延迟较高**：Kafka延迟10-50ms
2. ❌ **资源浪费**：独立Kafka集群仅用于广播
3. ❌ **功能冗余**：不需要Kafka的持久化、确认等特性

### 5.2 广播消息特点

#### 消息生命周期

```
游戏广播消息的生命周期：

1. 游戏进行中：消息需要实时推送给用户
   ↓
2. 用户收到消息：立即更新游戏状态
   ↓
3. 消息使命完成：不再需要

结论：消息的生命周期只有几毫秒到几秒
```

#### 关键需求

| 需求 | 优先级 | 说明 |
|------|--------|------|
| 实时性 | ⭐⭐⭐⭐⭐ | 延迟<10ms |
| 多实例消费 | ⭐⭐⭐⭐⭐ | 所有Gateway都要收到 |
| 高可用 | ⭐⭐⭐⭐ | 单节点故障不影响 |
| 运维简单 | ⭐⭐⭐ | 降低维护成本 |
| 消息持久化 | ⭐ | 不需要 |

### 5.3 方案对比

#### Redis Pub/Sub vs Kafka

| 对比项 | Redis Pub/Sub | Kafka | 优势方 |
|--------|---------------|-------|--------|
| **延迟** | <1ms | 10-50ms | ✅ Redis Pub/Sub |
| **实现复杂度** | 极简 | 复杂 | ✅ Redis Pub/Sub |
| **资源消耗** | 几乎为0 | 高 | ✅ Redis Pub/Sub |
| **运维成本** | 低 | 高 | ✅ Redis Pub/Sub |
| **消息持久化** | ❌ 不支持 | ✅ 支持 | ❌ 不需要 |
| **高可用** | ✅ 主从切换 | ✅ 多副本 | 平局 |

### 5.4 推荐方案

## ✅ **推荐：Redis Pub/Sub**

### 理由

1. **完美匹配游戏广播需求**
   - ✅ 实时性要求高（<1ms延迟）
   - ✅ 不需要消息持久化
   - ✅ 不需要消费者组
   - ✅ 不需要消息确认

2. **架构最简单**
   - ✅ 代码量最少
   - ✅ 无需额外中间件
   - ✅ 运维成本最低

3. **资源消耗最小**
   - ✅ 复用现有Redis
   - ✅ 几乎无内存开销
   - ✅ 无需独立集群

### 5.5 架构设计

#### 新架构

```
Game Service (Publisher)
    │
    ├─→ Redis Channel: cashparty:gateway:broadcast
    │
    ├─→ Gateway-1 (Subscriber) → 推送给本地用户
    ├─→ Gateway-2 (Subscriber) → 推送给本地用户
    └─→ Gateway-3 (Subscriber) → 推送给本地用户

特点：
✅ 每个Gateway都订阅同一个Channel
✅ 每个Gateway都收到所有消息
✅ 每个Gateway独立处理
```

### 5.6 代码实现

#### 生产者实现

**文件**: `common/broadcast/redis_pubsub_producer.go`

```go
package broadcast

import (
    "context"
    
    "github.com/cashparty/backend/common/logger"
    "github.com/cashparty/backend/common/message"
    cRedis "github.com/cashparty/backend/common/redis"
)

type RedisPubSubBroadcaster struct {
    redis   *cRedis.Client
    channel string
}

func NewRedisPubSubBroadcaster(redis *cRedis.Client, channel string) *RedisPubSubBroadcaster {
    return &RedisPubSubBroadcaster{
        redis:   redis,
        channel: channel,
    }
}

func (b *RedisPubSubBroadcaster) Broadcast(roomID string, event string, data interface{}, excludeUserID string) error {
    msg, err := message.NewRoomBroadcastMessage(roomID, event, data)
    if err != nil {
        logger.Error("failed to create broadcast message", "error", err)
        return err
    }
    msg.WithExcludeID(excludeUserID)

    msgData, err := msg.Marshal()
    if err != nil {
        logger.Error("failed to marshal broadcast message", "error", err)
        return err
    }

    ctx := context.Background()
    if err := b.redis.Publish(ctx, b.channel, msgData).Err(); err != nil {
        logger.Error("failed to publish broadcast message", "error", err)
        return err
    }

    logger.Debug("broadcast message published",
        "room_id", roomID,
        "event", event,
        "channel", b.channel)
    
    return nil
}
```

#### 消费者实现

**文件**: `common/broadcast/redis_pubsub_consumer.go`

```go
package broadcast

import (
    "context"
    "sync"
    
    "github.com/cashparty/backend/common/logger"
    "github.com/cashparty/backend/common/message"
    cRedis "github.com/cashparty/backend/common/redis"
)

type RedisPubSubConsumer struct {
    redis    *cRedis.Client
    channel  string
    handler  MessageHandler
    ctx      context.Context
    cancel   context.CancelFunc
    wg       sync.WaitGroup
    pubsub   *redis.PubSub
}

type MessageHandler func(ctx context.Context, msg *message.BroadcastMessage) error

func NewRedisPubSubConsumer(
    redis *cRedis.Client,
    channel string,
    handler MessageHandler,
) *RedisPubSubConsumer {
    ctx, cancel := context.WithCancel(context.Background())
    
    return &RedisPubSubConsumer{
        redis:   redis,
        channel: channel,
        handler: handler,
        ctx:     ctx,
        cancel:  cancel,
    }
}

func (c *RedisPubSubConsumer) Start() error {
    logger.Info("redis pubsub consumer starting", "channel", c.channel)
    
    c.pubsub = c.redis.Subscribe(c.ctx, c.channel)
    
    _, err := c.pubsub.Receive(c.ctx)
    if err != nil {
        return err
    }
    
    c.wg.Add(1)
    go c.consumeMessages()
    
    logger.Info("redis pubsub consumer started", "channel", c.channel)
    return nil
}

func (c *RedisPubSubConsumer) consumeMessages() {
    defer c.wg.Done()
    
    ch := c.pubsub.Channel()
    
    for {
        select {
        case <-c.ctx.Done():
            logger.Info("redis pubsub consumer stopping")
            return
        case msg, ok := <-ch:
            if !ok {
                return
            }
            
            if err := c.processMessage(msg); err != nil {
                logger.Error("failed to process message", "error", err)
            }
        }
    }
}

func (c *RedisPubSubConsumer) processMessage(msg *redis.Message) error {
    broadcastMsg, err := message.ParseBroadcastMessage([]byte(msg.Payload))
    if err != nil {
        return err
    }
    
    return c.handler(c.ctx, broadcastMsg)
}

func (c *RedisPubSubConsumer) Close() error {
    c.cancel()
    
    if c.pubsub != nil {
        c.pubsub.Close()
    }
    
    c.wg.Wait()
    return nil
}
```

### 5.7 配置文件

**文件**: `config/game.yaml`

```yaml
broadcast:
  mode: "redis_pubsub"
  
  redis_pubsub:
    channel: "cashparty:gateway:broadcast"
  
  kafka:
    topic: "cashparty.gateway.broadcast"
    consumer_group: "gateway-{nodeID}"
```

### 5.8 迁移方案

#### 阶段1：双写阶段

```go
type DualBroadcaster struct {
    kafkaBroadcaster *KafkaBroadcaster
    redisBroadcaster *RedisPubSubBroadcaster
}

func (b *DualBroadcaster) Broadcast(roomID string, event string, data interface{}, excludeUserID string) {
    b.kafkaBroadcaster.Broadcast(roomID, event, data, excludeUserID)
    b.redisBroadcaster.Broadcast(roomID, event, data, excludeUserID)
}
```

#### 阶段2：验证阶段

```bash
# 1. 部署新版本Gateway（使用Redis Pub/Sub）
# 2. 验证广播功能正常
# 3. 对比延迟和性能
```

#### 阶段3：切换阶段

```bash
# 1. 停止Kafka消费者
# 2. 只使用Redis Pub/Sub
# 3. 监控系统运行状态
```

---

## 六、实施计划

### 6.1 实施阶段

#### 阶段1：Redis Sentinel重构（优先级：⭐⭐⭐⭐⭐）

**时间**：1周

**任务**：
- [x] 修改配置结构
- [x] 修改Redis客户端
- [x] 更新配置文件
- [ ] 部署Redis主从+Sentinel
- [ ] 测试故障转移
- [ ] 灰度发布

**预期收益**：
- ✅ 自动故障转移
- ✅ 高可用性
- ✅ 数据备份

#### 阶段2：广播消息优化（优先级：⭐⭐⭐⭐⭐）

**时间**：1周

**任务**：
- [ ] 实现Redis Pub/Sub广播
- [ ] 双写验证
- [ ] 性能对比测试
- [ ] 切换到Redis Pub/Sub
- [ ] 下线Kafka广播

**预期收益**：
- ✅ 延迟降低50倍（<1ms）
- ✅ 资源节省（复用Redis）
- ✅ 运维简化

#### 阶段3：Kafka集群重构（优先级：⭐⭐⭐⭐）

**时间**：2周

**任务**：
- [ ] 部署Kafka集群
- [ ] 修改配置结构
- [ ] 修改生产者配置
- [ ] 修改消费者配置
- [ ] 测试验证
- [ ] 灰度发布

**预期收益**：
- ✅ 高可用性
- ✅ 数据可靠性
- ✅ 水平扩展能力

#### 阶段4：MySQL优化（优先级：⭐⭐）

**时间**：持续优化

**任务**：
- [ ] 配置优化
- [ ] 性能监控
- [ ] 数据归档
- [ ] 定期备份

**预期收益**：
- ✅ 性能提升
- ✅ 运维规范

### 6.2 风险控制

#### 风险点

| 风险 | 影响 | 应对措施 |
|------|------|---------|
| Redis主从切换延迟 | 业务短暂中断 | 客户端自动重连 |
| Kafka集群故障 | 消息丢失 | 多副本机制 |
| 广播消息丢失 | 用户界面不同步 | 客户端定时同步状态 |

#### 回滚方案

```
每个阶段都保留回滚能力：

1. Redis Sentinel → 单机Redis：修改配置即可
2. Redis Pub/Sub → Kafka：切换配置即可
3. Kafka集群 → 单机Kafka：修改配置即可
```

### 6.3 监控指标

#### Redis监控

```yaml
监控项:
  - Master状态
  - 主从延迟
  - 连接池使用率
  - 内存使用率
  - 命令执行延迟
```

#### Kafka监控

```yaml
监控项:
  - Broker状态
  - Topic分区状态
  - 生产者吞吐量
  - 消费者延迟
  - 副本同步状态
```

#### 广播监控

```yaml
监控项:
  - 消息延迟
  - 消息吞吐量
  - 订阅者数量
  - 消息丢失率
```

---

## 七、总结

### 7.1 重构收益

| 组件 | 重构前 | 重构后 | 收益 |
|------|--------|--------|------|
| Redis | 单机 | 主从+Sentinel | 自动故障转移、高可用 |
| MySQL | 单机 | 单机（优化） | 性能提升、运维规范 |
| Kafka | 单机 | 集群 | 高可用、数据可靠 |
| 广播 | Kafka | Redis Pub/Sub | 延迟降低50倍、资源节省 |

### 7.2 架构对比

**重构前**：
```
┌─────────────┐
│ Game Service│
└──────┬──────┘
       │
       ├─→ Redis (单机) ← 实时数据
       ├─→ MySQL (单机) ← 持久化
       └─→ Kafka (单机) ← 消息队列 + 广播
```

**重构后**：
```
┌─────────────┐
│ Game Service│
└──────┬──────┘
       │
       ├─→ Redis Sentinel ← 实时数据 + 广播
       │   ├─ Master
       │   ├─ Slave
       │   └─ Sentinel Cluster
       │
       ├─→ MySQL (单机优化) ← 持久化
       │
       └─→ Kafka Cluster ← 事件驱动
           ├─ Broker 1
           ├─ Broker 2
           └─ Broker 3
```

### 7.3 关键决策

| 决策 | 选择 | 理由 |
|------|------|------|
| Redis架构 | 主从+Sentinel | 自动故障转移、高可用、向后兼容 |
| MySQL架构 | 保持单机 | Redis已承担主要压力、单机足够 |
| Kafka架构 | 集群 | 高可用、数据可靠、水平扩展 |
| 广播消息 | Redis Pub/Sub | 延迟最低、架构最简、资源最省 |

### 7.4 最终建议

1. ✅ **Redis Sentinel**：必须实施，提高高可用性
2. ❌ **MySQL主从**：不建议实施，单机足够
3. ✅ **Kafka集群**：建议实施，提高可靠性
4. ✅ **Redis Pub/Sub广播**：必须实施，优化性能

### 7.5 预期效果

#### 性能提升

```
Redis延迟：<1ms（保持不变）
广播延迟：从10-50ms降低到<1ms（提升50倍）
Kafka吞吐量：从单机10万TPS提升到集群30万TPS（提升3倍）
```

#### 可用性提升

```
Redis可用性：从99%提升到99.9%（故障自动转移）
Kafka可用性：从99%提升到99.9%（多副本机制）
系统整体可用性：从99%提升到99.9%
```

#### 运维简化

```
Redis：无需人工干预故障转移
广播：无需维护独立Kafka集群
监控：统一的监控告警体系
```

---

## 附录

### A. 配置文件示例

详见：
- [Redis Sentinel配置示例](./config/redis-sentinel-example.yaml)
- [Kafka集群配置示例](./config/kafka-cluster-example.yaml)

### B. 部署文档

详见：
- [Redis Sentinel部署文档](./docs/redis-sentinel-deployment.md)
- [Kafka集群部署文档](./docs/kafka-cluster-deployment.md)

### C. 监控配置

详见：
- [Prometheus监控配置](./monitoring/prometheus.yml)
- [Grafana仪表盘](./monitoring/grafana-dashboard.json)

---

**文档版本**：v1.0  
**更新日期**：2026-04-17  
**作者**：架构团队
