# 资金结算系统设计文档

## 一、系统概述

### 1.1 系统定位

资金结算系统是 CASH PARTY 游戏的核心资金管理平台，负责处理所有涉及资金的操作，确保资金流转的安全性、完整性和一致性。

### 1.2 核心职责

- **资金扣款**: 发红包扣款、惩罚罚款
- **资金加款**: 抢红包收入、惩罚分红
- **账单管理**: 统一记录所有资金操作
- **事务保证**: 确保资金操作的原子性和一致性
- **异常处理**: 重试机制、补偿机制、对账机制

---

## 二、资金流转场景

### 2.1 场景列表

| 场景 | 触发时机 | 操作类型 | 金额来源 | 处理方式 |
|------|---------|---------|---------|---------|
| **首回合发红包** | 游戏开始 | 扣款 | 平台账户 | 立即扣款 |
| **玩家发红包** | 玩家发红包时 | 扣款 | 玩家账户 | 立即扣款 |
| **回合结算** | 每回合结束时 | 加款 | 抢到的红包金额 | 立即加款 |
| **惩罚罚款** | 超时/离场时 | 扣款 | 违规玩家账户 | 立即扣款 |
| **惩罚分红** | 罚款后 | 加款 | 分给其他玩家 | 立即加款 |

### 2.2 资金流转时间线

```
游戏开始
    ↓
回合1: 平台发红包
    ├─ 扣款: 平台账户 → 红包池 (立即)
    └─ 抢红包阶段
         ↓
    回合1结束
         └─ 结算: 红包池 → 玩家账户 (立即)
              ↓
回合2: 玩家A发红包
    ├─ 扣款: 玩家A账户 → 红包池 (立即)
    └─ 抢红包阶段
         ↓
    回合2结束
         └─ 结算: 红包池 → 玩家账户 (立即)
              ↓
... (重复回合)
    ↓
玩家B超时未发红包
    ├─ 罚款: 玩家B账户 → 罚款池 (立即)
    └─ 分发: 罚款池 → 其他玩家账户 (立即)
              ↓
游戏结束
```

---

## 三、核心设计原则

### 3.1 统一资金管理

所有资金操作都通过 **SettlementService** 统一处理：

```
GameService → SettlementService → Platform API
                  ↓
              BillManager → Database
```

### 3.2 统一账单记录

所有资金操作都记录到统一的账单表 `bill_record`：

- 每笔资金操作都有唯一的 TraceID
- 记录操作前后的余额
- 记录操作状态和错误信息
- 支持重试和补偿

### 3.3 实时结算

- **发红包**: 立即扣款，不生成红包
- **回合结束**: 立即结算，不等游戏结束
- **惩罚触发**: 立即处理，不延迟

---

## 四、数据模型设计

### 4.1 账单记录表 (bill_record)

```sql
CREATE TABLE `bill_record` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID',
    `trace_id` VARCHAR(32) NOT NULL COMMENT '追踪ID(雪花算法)',
    `bill_type` INT NOT NULL COMMENT '账单类型:1=发红包扣款,2=抢红包收入,3=惩罚罚款,4=惩罚分红',
    `room_id` BIGINT NOT NULL COMMENT '房间ID',
    `session_id` BIGINT DEFAULT 0 COMMENT '会话ID',
    `round_id` BIGINT NOT NULL COMMENT '回合ID',
    `user_id` BIGINT NOT NULL COMMENT '用户ID',
    `amount` BIGINT NOT NULL COMMENT '金额(正数=收入,负数=支出)',
    `balance_before` BIGINT NOT NULL DEFAULT 0 COMMENT '操作前余额',
    `balance_after` BIGINT NOT NULL DEFAULT 0 COMMENT '操作后余额',
    `status` INT NOT NULL DEFAULT 0 COMMENT '状态:0=处理中,1=成功,2=失败',
    `retry_count` INT NOT NULL DEFAULT 0 COMMENT '重试次数',
    `error_message` VARCHAR(512) DEFAULT '' COMMENT '错误信息',
    `remark` VARCHAR(256) DEFAULT '' COMMENT '备注',
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_trace_id` (`trace_id`),
    KEY `idx_bill_type` (`bill_type`),
    KEY `idx_room_id` (`room_id`),
    KEY `idx_session_id` (`session_id`),
    KEY `idx_round_id` (`round_id`),
    KEY `idx_user_id` (`user_id`),
    KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账单记录表';
```

**账单类型说明**:
- `1` - 发红包扣款: 玩家或平台发红包时的扣款
- `2` - 抢红包收入: 玩家抢到红包后的加款
- `3` - 惩罚罚款: 玩家违规时的罚款扣款
- `4` - 惩罚分红: 罚款分发给其他玩家的加款

**金额说明**:
- 正数: 表示收入（加款）
- 负数: 表示支出（扣款）

### 4.2 回合结算表 (round_settlement)

```sql
CREATE TABLE `round_settlement` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID',
    `trace_id` VARCHAR(32) NOT NULL COMMENT '追踪ID',
    `room_id` BIGINT NOT NULL COMMENT '房间ID',
    `session_id` BIGINT NOT NULL COMMENT '会话ID',
    `round_id` BIGINT NOT NULL COMMENT '回合ID',
    `round_no` INT NOT NULL COMMENT '回合编号',
    `sender_id` BIGINT NOT NULL COMMENT '发红包者ID',
    `sender_type` VARCHAR(20) NOT NULL COMMENT '发送者类型:platform/player',
    `total_amount` BIGINT NOT NULL COMMENT '红包总金额',
    `commission` BIGINT NOT NULL COMMENT '平台佣金',
    `player_count` INT NOT NULL COMMENT '参与玩家数',
    `min_player_id` BIGINT NOT NULL COMMENT '最小金额玩家ID',
    `status` INT NOT NULL DEFAULT 0 COMMENT '状态:0=处理中,1=成功,2=失败',
    `settled_at` DATETIME DEFAULT NULL COMMENT '结算完成时间',
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_trace_id` (`trace_id`),
    UNIQUE KEY `uk_round_id` (`round_id`),
    KEY `idx_room_id` (`room_id`),
    KEY `idx_session_id` (`session_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='回合结算表';
```

---

## 五、核心接口设计

### 5.1 SettlementService 接口

```go
type SettlementService interface {
    // 发红包扣款
    DeductForSendPacket(ctx context.Context, req *SendPacketRequest) error
    
    // 回合结算
    SettleRound(ctx context.Context, req *RoundSettleRequest) error
    
    // 惩罚处理
    HandlePenalty(ctx context.Context, req *PenaltyRequest) error
}
```

### 5.2 请求模型

#### SendPacketRequest - 发红包扣款请求

```go
type SendPacketRequest struct {
    TraceID     string // 追踪ID（可选，不传则自动生成）
    RoomID      int64  // 房间ID
    SessionID   int64  // 会话ID
    RoundID     int64  // 回合ID
    UserID      int64  // 发红包用户ID
    SenderType  string // 发送者类型: "platform" 或 "player"
    RoomFee     int64  // 房费（红包总金额）
    Commission  int64  // 平台佣金
}
```

#### RoundSettleRequest - 回合结算请求

```go
type RoundSettleRequest struct {
    TraceID     string              // 追踪ID（可选）
    RoomID      int64               // 房间ID
    SessionID   int64               // 会话ID
    RoundID     int64               // 回合ID
    RoundNo     int                 // 回合编号
    SenderID    int64               // 发红包者ID
    SenderType  string              // 发送者类型
    TotalAmount int64               // 红包总金额
    Commission  int64               // 平台佣金
    MinPlayerID int64               // 最小金额玩家ID
    Players     []*PlayerSettleInfo // 玩家结算信息
}

type PlayerSettleInfo struct {
    UserID int64 // 玩家ID
    Amount int64 // 抢到的金额
    IsMin  bool  // 是否最小金额
}
```

#### PenaltyRequest - 惩罚处理请求

```go
type PenaltyRequest struct {
    TraceID      string  // 追踪ID（可选）
    RoomID       int64   // 房间ID
    SessionID    int64   // 会话ID
    RoundID      int64   // 回合ID
    UserID       int64   // 被惩罚用户ID
    PenaltyType  string  // 惩罚类型
    Amount       int64   // 罚款金额
    Recipients   []int64 // 罚款接收者ID列表
}
```

---

## 六、数据一致性保证

### 6.1 幂等性保证

**三层幂等性保证**:

```
1. TraceID 唯一索引
   └─ 数据库层面保证唯一性

2. RoundID + BillType 检查
   └─ 业务层面防止重复操作

3. 分布式锁
   └─ 并发层面防止竞争
```

**实现示例**:

```go
func (s *SettlementService) DeductForSendPacket(ctx context.Context, req *SendPacketRequest) error {
    // 1. 幂等性检查
    if s.billMgr.ExistsByRoundAndType(ctx, req.RoundID, BillTypeSendPacket) {
        return nil // 已处理，直接返回
    }
    
    // 2. 分布式锁
    lockKey := fmt.Sprintf("lock:send_packet:%d", req.RoundID)
    return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
        // 双重检查
        if s.billMgr.ExistsByRoundAndType(ctx, req.RoundID, BillTypeSendPacket) {
            return nil
        }
        
        // 执行扣款...
    })
}
```

### 6.2 状态机管理

```
状态流转:
Processing (处理中)
    ├─→ Success (成功) [最终状态]
    └─→ Failed (失败)
           └─→ Retry → Processing (重试)
                  └─→ Success / Failed
```

### 6.3 重试机制

**重试策略**:

```
最大重试次数: 3次
重试间隔: 指数退避
    ├─ 第1次: 5秒后
    ├─ 第2次: 10秒后
    └─ 第3次: 15秒后

超过重试次数:
    └─ 记录错误日志
    └─ 发送告警通知
    └─ 人工介入处理
```

**实现示例**:

```go
func (s *SettlementService) retryBill(traceID string) {
    bill, _ := s.billMgr.GetBillByTraceID(ctx, traceID)
    
    if bill.RetryCount >= MaxRetryCount {
        logger.Error("max retry count exceeded", "trace_id", traceID)
        // TODO: 发送告警
        return
    }
    
    // 指数退避
    time.Sleep(time.Duration(bill.RetryCount+1) * 5 * time.Second)
    
    // 增加重试次数
    s.billMgr.IncrementRetryCount(ctx, traceID)
    
    // 重新执行
    if bill.Amount < 0 {
        s.executeDeduct(ctx, bill, -bill.Amount, bill.RoundID)
    } else {
        s.executeCredit(ctx, bill, bill.Amount, bill.RoundID)
    }
}
```

### 6.4 对账机制

**定期对账**:

```
执行频率: 每小时一次
对账范围: 最近24小时的账单
对账逻辑:
    1. 查询状态为 Success 的账单
    2. 调用平台接口查询用户余额
    3. 对比 balance_after 是否一致
    4. 发现不一致则记录并告警
```

---

## 七、异常场景处理

### 7.1 发红包扣款异常

| 场景 | 处理方式 |
|------|---------|
| 扣款成功，记录失败 | 平台接口返回成功，但本地记录失败 → 重试记录 |
| 扣款失败 | 返回错误，不生成红包，游戏逻辑处理 |
| 扣款超时 | 标记为失败，重试扣款 |

### 7.2 回合结算异常

| 场景 | 处理方式 |
|------|---------|
| 部分玩家加款成功 | 标记失败，继续重试未成功的玩家 |
| 全部玩家加款失败 | 标记失败，整体重试 |
| 加款超时 | 标记为处理中，定时任务检测并重试 |

### 7.3 惩罚处理异常

| 场景 | 处理方式 |
|------|---------|
| 罚款扣款失败 | 返回错误，游戏逻辑处理 |
| 部分分红失败 | 记录错误，继续处理其他玩家 |
| 全部分红失败 | 记录错误，等待人工处理 |

---

## 八、系统架构

### 8.1 模块结构

```
settlement/
├── model/
│   ├── bill.go           # 账单模型
│   └── request.go        # 请求模型
├── service/
│   ├── settlement_service.go  # 核心结算服务
│   └── bill_manager.go        # 账单管理器
├── consumer/
│   └── event_consumer.go      # 结算事件消费者
└── scheduler/
    └── retry_scheduler.go     # 重试调度器
```

### 8.2 调用关系

```
GameService
    ↓
SettlementService
    ├─→ Platform API (扣款/加款)
    └─→ BillManager
            └─→ Database (账单记录)
```

---

## 九、性能优化

### 9.1 数据库优化

- 所有查询字段都添加索引
- TraceID 使用唯一索引
- 状态字段添加索引，优化查询性能

### 9.2 并发控制

- 使用分布式锁防止并发操作
- 锁超时时间设置为 30 秒
- 使用 Redis 实现分布式锁

### 9.3 异步处理

- 重试操作使用 goroutine 异步执行
- 避免阻塞主流程
- 使用 channel 控制并发数量

---

## 十、监控告警

### 10.1 监控指标

```
业务指标:
- 账单处理成功率
- 账单处理平均耗时
- 重试次数分布
- 失败账单数量

系统指标:
- 数据库连接数
- Redis 连接数
- 接口响应时间
```

### 10.2 告警规则

```
告警级别:
- P0: 账单处理失败率 > 5%
- P1: 账单处理失败率 > 1%
- P2: 重试次数超过3次的账单数量 > 10
- P3: 对账发现不一致的账单数量 > 0
```

---

## 十一、部署说明

### 11.1 数据库迁移

```bash
# 创建表
mysql -u root -p cashparty < migrations/create_bill_tables.sql
```

### 11.2 配置说明

```yaml
settlement:
  max_retry_count: 3        # 最大重试次数
  retry_interval: 5         # 重试间隔(秒)
  lock_timeout: 30          # 分布式锁超时(秒)
  
database:
  max_open_conns: 100       # 最大连接数
  max_idle_conns: 10        # 最大空闲连接数
  
redis:
  addr: "localhost:6379"
  password: ""
  db: 0
```

---

## 十二、未来扩展

### 12.1 支持更多账单类型

- 系统奖励
- 活动奖励
- 充值
- 提现

### 12.2 账单查询接口

- 按用户查询账单
- 按房间查询账单
- 按时间范围查询账单

### 12.3 统计报表

- 每日资金流水统计
- 用户资金变动统计
- 平台收入统计

---

## 附录

### A. 错误码定义

| 错误码 | 说明 |
|--------|------|
| 10001 | 账单不存在 |
| 10002 | 账单已处理 |
| 10003 | 扣款失败 |
| 10004 | 加款失败 |
| 10005 | 余额不足 |

### B. 日志规范

```
日志格式: [时间] [级别] [TraceID] [消息] [字段]

示例:
[2025-04-09 10:00:00] [INFO] [1234567890123456789] 账单处理成功 trace_id=1234567890123456789 user_id=12345 amount=100
```

### C. 测试用例

参见 `settlement/service/settlement_service_test.go`
