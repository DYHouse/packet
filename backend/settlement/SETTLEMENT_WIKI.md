# 结算模块 Wiki（Settlement Wiki）

> 本文档对 `RedPacket-master/backend/settlement` 结算模块进行结构化梳理，覆盖整体架构、设计思想、核心流程、模块职责、关键类与函数、数据模型、Redis 键规范、调度器、依赖关系、并发与幂等设计等关键信息。
>
> 文档中所有文件引用均为可点击链接，便于直接跳转到源码位置。

---

## 目录

1. [模块定位与概览](#1-模块定位与概览)
2. [整体架构](#2-整体架构)
3. [设计思想](#3-设计思想)
4. [数据安全保障机制](#4-数据安全保障机制)
5. [核心流程图](#5-核心流程图)
6. [目录结构与模块职责](#6-目录结构与模块职责)
7. [关键类与函数说明](#7-关键类与函数说明)
8. [数据模型（数据库表）](#8-数据模型数据库表)
9. [Redis 键规范](#9-redis-键规范)
10. [调度器机制](#10-调度器机制)
11. [配置与常量](#11-配置与常量)
12. [依赖关系](#12-依赖关系)
13. [设计模式](#13-设计模式)
14. [幂等性与并发安全](#14-幂等性与并发安全)
15. [错误处理与重试策略](#15-错误处理与重试策略)
16. [状态机](#16-状态机)
17. [集成与上下游调用](#17-集成与上下游调用)
18. [接口与 DTO 参考](#18-接口与-dto-参考)

---

## 1. 模块定位与概览

结算模块（`settlement`）是整个 CashParty 红包游戏后端的**资金流转中枢**，负责处理游戏中所有资金动作：扣款（Debit）、入账（Credit）、退款（Refund）、佣金（Commission）、惩罚（Penalty）、奖励（Reward），并通过分布式锁 + 调度器机制保证数据一致性与完整性。

### 1.1 核心职责

- **扣款**：开局前预扣房费、玩家发红包扣款、后续回合最小玩家扣款、系统红包扣款、惩罚扣款。
- **入账**：玩家抢红包入账、惩罚分配入账、系统奖励入账、会话级结算入账。
- **退款**：首回合扣款部分失败后的自动退款、游戏异常的人工退款。
- **对账与补偿**：定时扫描异常状态、自动重试入账、超时强制结算、异常记录告警。
- **机器人虚拟通道**：机器人不参与真实资金流转，通过虚拟余额通道保持业务逻辑统一。

### 1.2 关键设计原则

| 原则 | 说明 |
|------|------|
| 扣款与入账分离 | 扣款走平台真实钱包（`platform.Debit`），入账分两阶段：回合级仅内部记账（`BillRecord`），会话级才真正调用 `platform.Credit` 移动资金 |
| 资金不可凭空创造 | 机器人虚拟通道独立于真实资金流，所有真实资金进出都通过 `platform.Client` 接口 |
| 系统账号虚拟化 | 平台无系统账号，`UserID=0` 的账单只记录不调平台接口；系统收入 = 玩家总扣款 - 玩家总收入 |
| 扣款失败不重试 | 扣款失败直接标记异常，由人工或退款流程处理（避免重复扣款风险） |
| 入账失败必重试 | 入账失败采用指数退避重试，超限后标记异常（资金已扣，必须入账） |
| 配对账目平衡 | 惩罚扣款、系统红包等成对操作在同一事务创建 player + platform 两条配对 Bill |
| 资金不丢 | Kafka SetNX 抢占+失败释放、DB 事务回滚、入账指数退避重试、异常记录兜底、对账补偿多道防线 |
| 最终一致性 | 状态机只能向前推进 + 5 个调度器周期性扫描卡住状态并补偿 + 超时强制兜底 |
| 数据完整性 | DB 事务边界严格、唯一索引防重、配对账目平衡、余额快照可追溯、平台调用日志全记录 |
| 幂等三层防线 | Redis SetNX 全局去重 + DB 唯一索引/FirstOrCreate + 业务状态机/BillRecord 存在性检查 |
| 补偿优先于自动修复 | 可确定资金归属的场景自动补偿；不确定的异常仅告警交人工，避免误操作 |

---

## 2. 整体架构

### 2.1 分层架构

结算模块采用**面向领域服务的分层架构**，与 game 服务同进程部署：

```
┌─────────────────────────────────────────────────────────────────┐
│                      game-service（同进程）                       │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │              game/application（业务编排层）                 │ │
│  │   RoomAppService / GameAppService / SeatAppService ...     │ │
│  └──────────────────────┬─────────────────────────────────────┘ │
│                         │ 同步调用                                │
│  ┌──────────────────────▼─────────────────────────────────────┐ │
│  │              settlement/service（结算服务层）               │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐  │ │
│  │  │SettlementSvc│  │ DeductSvc    │  │ GameSettleSvc    │  │ │
│  │  │  (门面/编排)  │  │ (扣款)       │  │ (会话级结算)     │  │ │
│  │  └──────┬───────┘  └──────┬───────┘  └────────┬─────────┘  │ │
│  │  ┌──────▼──────────────────▼───────────────────▼──────────┐│ │
│  │  │ RewardSettler / RefundService / CreditRetryService ...  ││ │
│  │  └────────────────────────────────────────────────────────┘│ │
│  │  ┌─────────────────────────────────────────────────────────┐│ │
│  │  │ BillManager / ExceptionManager / PlatformCallManager    ││ │
│  │  │               (Repository / 数据访问层)                 ││ │
│  │  └─────────────────────────────────────────────────────────┘│ │
│  └─────────────────────────────────────────────────────────────┘ │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │              settlement/scheduler（调度器层）               │ │
│  │   CreditRetry / GameSettleRetry / GameSettleTimeout         │ │
│  │   RefundProcess / SettlementCheck                           │ │
│  └─────────────────────────────────────────────────────────────┘ │
└──────────────────────────┬──────────────────────────────────────┘
                           │
        ┌──────────────────┼──────────────────┐
        ▼                  ▼                  ▼
   ┌─────────┐        ┌─────────┐        ┌──────────┐
   │ MySQL   │        │ Redis   │        │ Platform │
   │ (GORM)  │        │ (锁/虚拟│        │ API      │
   │         │        │  余额)  │        │ Debit/   │
   │bill_    │        │         │        │ Credit/  │
   │record   │        │         │        │ GetBalance│
   │round_   │        │         │        │ Settle   │
   │settlement│       │         │        │          │
   │refund_  │        │         │        │          │
   │audit    │        │         │        │          │
   │exception│        │         │        │          │
   │_record  │        │         │        │          │
   │platform_│        │         │        │          │
   │call_log │        │         │        │          │
   └─────────┘        └─────────┘        └──────────┘
```

### 2.2 资金流转分层

```
┌─────────────────────────────────────────────────────────────┐
│  回合级（Round-level）：内部账簿记录（BillRecord, Success）   │
│  creditRound / settleCommission / SettleReward             │
│  ├─ 不调用 platform.Credit，仅写 DB 记账                     │
│  └─ 用于回合内分摊与结算汇总                                 │
└──────────────────────────────┬──────────────────────────────┘
                               │ 游戏结束后触发
                               ▼
┌─────────────────────────────────────────────────────────────┐
│  会话级（Session-level）：真实资金移动（platform.Credit）     │
│  GameSettleService.SettleGame → creditSessionPayouts        │
│  ├─ 聚合每个玩家的 bet/payout                               │
│  ├─ 对 payout>0 的玩家调用 platform.Credit（真人）           │
│  └─ 机器人走虚拟余额通道，跳过 platform.Settle               │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. 设计思想

### 3.1 两阶段入账（核心设计）

这是结算模块最关键的设计决策。资金入账被拆分为两个阶段：

- **阶段一：回合级内部记账**（`SettlementService.creditRound`）
  - 在 `SettleRound` 时为抢红包、佣金创建 `Success` 状态的 `BillRecord`
  - **不调用 `platform.Credit`**，仅记录账簿
  - 目的：快速完成回合结算，让玩家能立即进入下一回合

- **阶段二：会话级真实入账**（`GameSettleService.SettleGame`）
  - 在整局游戏结束后触发
  - 聚合每个玩家的总 `bet`（扣款）和 `payout`（应入账）
  - 对 `payout > 0` 的玩家调用 `platform.Credit` 移动真实资金
  - 机器人跳过 `platform.Settle`，仅更新状态

**设计动机**：游戏一局可能有多达数十个回合，若每回合都调用平台入账，会产生大量平台调用、延迟高且失败面大。两阶段入账将真实资金移动收敛到会话结束时一次性完成，大幅降低平台调用次数和失败概率。

### 3.2 机器人虚拟通道

所有涉及资金的操作都通过 `robotChecker.IsRobot` 分流：

| 操作 | 真人玩家 | 机器人 |
|------|---------|--------|
| 扣款（Debit） | `platform.Debit` | `virtualBalance.Deduct`（Lua 原子） |
| 入账（Credit） | `platform.Credit` | `virtualBalance.Credit`（INCRBY + SADD dirty） |
| 余额查询 | `platform.GetBalance` | `virtualBalance.GetBalance` |
| 游戏结算上报 | `platform.Settle` | 跳过，仅更新状态 |
| 惩罚扣款 | `platform.Debit` | `virtualBalance.Deduct` |

**目的**：机器人不参与真实资金流转，避免平台账户出现虚假交易，同时保持业务逻辑统一（只需在关键节点分流，不需要为机器人单独写一套流程）。

**虚拟余额原子扣减**：使用 Lua 脚本 `luaDeductBalance` 保证"扣减→检查→回滚→标记 dirty"四步原子执行，避免并发扣减凭空创造资金（详见 [service/lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go)）。

### 3.3 系统账号虚拟化

平台无真实"系统账号"，系统收入通过账目差额自动计算：

- `UserID = dto.PlatformAccountID (0)` 的 Bill 表示系统账号
- 系统 Bill 只记录、**不调用**平台接口
- 系统收入 = 玩家总扣款（amount<0 取绝对值之和）- 玩家总收入（amount>0 之和）

### 3.4 补偿优先于自动修复

对于不确定的异常场景，采用"告警优先"策略：

- **可确定的失败**：自动退款（如首回合扣款部分失败）
- **不确定的异常**：仅创建 `ExceptionRecord` 供人工审查（如"已扣款但未结算"，可能说明游戏结果事件丢失，自动退款可能误退）

### 3.5 日志切面/审计

所有对平台的调用前后都记录 `PlatformCallLog`，便于追踪和故障排查：

```
callLog, _ := callMgr.CreateLog(ctx, params)      // 调用前
result, err := platform.Debit(...)                  // 平台调用
callMgr.UpdateLog(ctx, updateParams)                // 调用后
```

---

## 4. 数据安全保障机制

结算模块作为资金流转中枢，其核心挑战在于：在网络抖动、节点宕机、并发请求、Kafka 重投递等不可靠环境下，仍需保证**资金不丢、最终一致、数据完整**。本章系统梳理这些保障机制。

### 4.1 数据不丢机制

资金不丢通过"Kafka 消费不丢 + 扣款/入账失败不丢 + 余额快照 + 平台日志 + 退款兜底"五道防线实现。

#### 4.1.1 Kafka 消费消息不丢（三层防御）

文件：[game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go)

**第一层：SetNX 原子抢占（全局去重）**

```go
// game_event_consumer.go L449-462
func (c *GameEventConsumer) tryAcquire(ctx context.Context, traceID string) bool {
    if c.redis == nil { return true }
    key := redisKeys.GameEventProcessedKey(traceID)
    ok, err := c.redis.SetNX(ctx, key, 1, 7*24*time.Hour).Result()  // 7天TTL
    if err != nil {
        logger.Warn("tryAcquire SetNX failed, fail-open", ...)
        return true   // fail-open: Redis故障时让流程继续，依赖DB层幂等兜底
    }
    return ok
}
```

- 每个 GameEvent 在生成时由 `idgen.GenerateString()` 产生全局唯一 `traceID`
- key 为 `GameEventProcessedKey(traceID)`，TTL=7天，保证同一 traceID 在全集群范围内只能被一个 consumer 首次处理
- **fail-open 策略**：Redis 故障时返回 true 让流程继续，避免 Redis 抖动阻塞消费；牺牲严格性换取可用性，依赖 DB 层二次幂等兜底

**第二层：失败释放让 Kafka 重试能重新进入**

```go
// game_event_consumer.go L76-83
if err != nil {
    c.releaseAcquire(ctx, event.TraceID)   // Del key 释放标记
    return err                              // 让 Kafka 重投
}
```

- 处理失败时**必须** `releaseAcquire` 删除 SetNX key
- 否则 Kafka 重试时 `tryAcquire` 返回 false（L57-60 直接 return nil），消息被"吞掉"
- 这是"失败释放"机制：失败不锁死，给重试留出通道

**第三层：DB 事务回滚保证状态一致**

`handleRoundSettle`（L234-367）和 `handleSessionEnd`（L377-445）都把 DB 状态变更与 `SettleRound` / `SettleGame` 调用包在**同一 DB 事务**内：

```
handleRoundSettle 事务:
  ├─ 更新 round.status = Ended
  ├─ 创建 grab_record (FirstOrCreate 幂等)
  ├─ 更新 session_player 统计
  ├─ 创建 special_reward 记录
  └─ settlementService.SettleRound(ctx, settleReq)
       └─ 失败 → return err → 整个事务回滚
```

**关键注释**（L359-362）：
> SettleRound 失败必须 return err 触发事务回滚，避免 round 标记 Ended 但平台账未结算。重试时依赖 SettleRound 内部幂等（RoundStatusCredited 返回 nil）和上面 grab_record 的 FirstOrCreate。

**三层防御的协作**：
- SetNX 抢占防并发重复处理
- DB 事务回滚保证状态原子性（要么全部成功，要么全部回滚）
- 业务层幂等（FirstOrCreate / status 检查）兜底 SetNX fail-open 的风险

#### 4.1.2 扣款/入账失败的资金不丢

**扣款失败（不可恢复）→ 异常记录兜底**

- `executeSingleDeduct` 中 `platform.Debit` 失败时，调用 `creditRetrySvc.CreateDebitFailedException(ctx, bill)` 创建 `ExceptionTypeDebitFailed` 异常记录
- **设计动机**：扣款失败不重试（见 4.4 错误分级），直接进入异常表等人工处理
- 扣款失败 = 资金未动，无需补偿，但需为已成功扣款的其他玩家退款（首回合批量扣款场景）

**入账失败（可恢复）→ 指数退避重试**

- `executeSessionCredit` 失败时：`UpdateBillStatus(Failed)` + `SetNextRetryTime(5s)`
- 不阻断其他玩家结算（仅记日志）
- `CreditRetryScheduler` 每 30s 扫描重试，指数退避：5s → 10s → 20s，封顶 5min
- 重试超限（3次）→ 创建 `ExceptionTypeCreditRetryExceed` 异常，兜底人工处理
- **设计动机**：入账是"加钱"，平台通过 BizID 幂等，重试安全；不重试会导致玩家资金"丢失"（已扣款未入账）

**对账补偿兜底**

- `SettlementCheckScheduler` 每 5min 扫描：
  - 首回合扣款失败 10min 后 → `ensureRefundCreated` 自动申请退款
  - 已扣款未结算 5min 后 → 创建异常记录人工审核

#### 4.1.3 余额快照机制（BalanceBefore / BalanceAfter）

文件：[model/bill.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/bill.go)

```go
BalanceBefore int64 `gorm:"not null;default:0"`  // 操作前余额
BalanceAfter  int64 `gorm:"not null;default:0"`  // 操作后余额
```

- 每次扣款/入账成功后，从平台返回值解析余额写入 `BalanceAfter`（如 `deduct_service.go` L251、`game_settle_service.go` L379）
- **解析失败容错**：平台返回成功但 ParseAmount 失败时，标记 Success 但 balance=0 + 记录 error 日志——优先保证业务推进，余额差异由对账修正
- **作用**：余额快照是事后对账的依据，可追溯每笔操作前后的真实余额，用于发现平台返回与本地记账的差异

#### 4.1.4 平台调用日志可追溯（PlatformCallLog）

文件：[service/platform_call_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/platform_call_manager.go)

所有对平台的调用前后都创建/更新 `PlatformCallLog`：

| 调用阶段 | 操作 | 字段 |
|---------|------|------|
| 调用前 | `CreateLog` | CallType（debit/credit/settle）、BizOrderNo、ReqBody、Status=Pending、RequestTime |
| 调用后（成功） | `UpdateLog` | RespBody、Status=Success、ResponseTime |
| 调用后（失败） | `UpdateLog` | ErrorMessage、Status=Failed、ResponseTime、RetryCount+1 |

- **保证**：每次平台调用都有完整的"请求体-响应体-状态-时间"记录
- 即使平台返回丢失，也能通过日志重建
- 是资金追溯的关键证据链，用于对账和故障排查

#### 4.1.5 退款资金不丢

退款流程通过"状态机 + 事务 + 调度器补偿"保证：

1. **申请阶段**：`applyForRefundLocked` 事务内同时创建 `refund_audit` + 更新 `bill.refund_status=Pending`，保证两者一致
2. **执行阶段**：`UpdateRefundSuccessInTransaction` 事务内同时更新 `refund_audit` 为 Refunded + `bill` 为 Refunded
3. **补偿阶段**：
   - `RefundProcessScheduler` 每 1min 扫描 Pending 退款，对 `FirstRoundFail` 类型自动 Approve
   - `SettlementCheckService.ensureRefundCreated` 对"成功但未退款"的 bill 自动 `ApplyForRefund`
4. **双重防御**：即使退款申请创建后系统崩溃，调度器也会重新拾取并处理

### 4.2 最终一致性机制

最终一致性通过"两阶段入账 + 调度器补偿 + 对账发现 + 强制兜底 + 状态机驱动"共同实现。

#### 4.2.1 两阶段入账保证最终一致

**阶段一：回合级内部记账**（`creditRound`，`settlement_service.go` L131-182）

- 创建 `BillTypeGrabPacket` / `BillTypeCommission` 账单，`Status=Success`
- **不调用 `platform.Credit`**，纯本地记账，保证必定成功
- **设计动机**：将"记账"和"资金移动"解耦。回合级记账保证游戏不阻塞（内部操作必成功）

**阶段二：会话级真实入账**（`creditSessionPayouts`，`game_settle_service.go` L269-323）

- 游戏结束后聚合每个玩家的总 `bet`（扣款）和 `payout`（应入账）
- 对 `payout > 0` 的玩家调用 `platform.Credit` 移动真实资金
- 失败可独立重试，不影响游戏流程
- **设计动机**：资金移动延迟到会话级批量处理，减少平台调用次数，且失败可独立重试

**一致性保证**：回合级记账（必成功）+ 会话级入账（可重试）= 最终资金到位。即使会话级入账暂时失败，调度器会持续重试直到成功或转异常。

#### 4.2.2 五个调度器的补偿逻辑

| 调度器 | 间隔 | 补偿逻辑 | 文件 |
|--------|------|----------|------|
| `CreditRetryScheduler` | 30s | 扫描 `amount>0 AND status IN (Processing,Failed) AND retry_count<3` 的账单重试入账 | [credit_retry_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/credit_retry_scheduler.go) |
| `SettlementCheckScheduler` | 5min | ① 首回合扣款失败自动退款 ② 已扣款未结算创建异常 | [settlement_check_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/settlement_check_scheduler.go) |
| `GameSettleTimeoutScheduler` | 5min | 扫描"所有 round Credited 但 GameSettleStatus=None 且超过1小时"的会话，强制 `SettleGame` | [game_settle_timeout_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/game_settle_timeout_scheduler.go) |
| `RefundProcessScheduler` | 1min | 扫描 Pending 退款，对 `FirstRoundFail` 类型自动 Approve | [refund_process_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/refund_process_scheduler.go) |
| `GameSettleRetryScheduler` | 30s | 扫描 GameSettleStatus=Failed 的会话，逐玩家重试结算 | [game_settle_retry_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/game_settle_retry_scheduler.go) |

**补偿链路示例**：
- 入账失败 → `CreditRetryScheduler` 30s 重试 → 3次失败 → 异常记录 → 人工
- 扣款失败 → `SettlementCheckScheduler` 5min 扫描 → `ensureRefundCreated` → `RefundProcessScheduler` 1min 自动 Approve → `executeRefund` 入账
- 游戏未结算 → `GameSettleTimeoutScheduler` 1小时强制 `SettleGame`

#### 4.2.3 对账机制发现不一致

文件：[settlement_check_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_check_service.go)

**① 首回合扣款失败对账**（L34-47 + L94-119）

- 查询 `deduct_scene=FirstRoundShare AND status=Failed AND created_at < (now-10min)` 的 RoundSettlement
- 对每个 `Status=Success AND RefundStatus=None` 的 bill 调用 `ApplyForRefund`
- **10分钟延迟窗口**避免误判（等扣款流程完全结束）

**② 已扣款未结算对账**（L53-66 + L68-92）

- 查询 `status=Deducted AND settle_amount IS NULL AND created_at < (now-5min)`
- **关键设计注释**（L49-52）：
  > 在 creditRound 总是成功的新模型下，此场景说明游戏结果事件可能丢失。不自动退款（可能游戏实际已进行），创建异常记录人工审核

- 这是**自动 vs 人工的判断分界**：能确定资金归属的自动处理，不能确定的交人工

#### 4.2.4 强制兜底机制

`GameSettleTimeoutScheduler`（5min 间隔，1小时超时窗口）：
- 查询所有回合已 Credited 但 `GameSettleStatus=None` 且超过 1 小时的会话
- 强制调用 `SettleGame`，依赖其内部幂等（`allSettled` 早返回）保证不重复
- **设计动机**：1小时超时窗口给正常流程和重试足够时间，超时后强制推进，避免会话永久卡在中间态

#### 4.2.5 状态机驱动最终一致性

所有状态只能**向前推进**，不能回退：

```
回合级:  Deducting(0) → Deducted(1) → Credited(6)
                          ↓失败
                        Failed(5)

会话级:  None(0) → Settling(1) → Success(2) / Failed(3)

账单级:  Processing(0) → Success(1) / Failed(2)
                          ↓退款
                        Refunded(3)

退款级:  None(0) → Pending(1) → Approved(2) → Refunded(3)
                              └→ Rejected(4)
```

- 每个状态变迁都是**幂等的**（重复设置同状态无副作用）
- 调度器扫描"卡住"的状态（如 Deducted 但未 Credited、Credited 但未 Settled）进行补偿
- 状态只能向前推进，保证最终到达终态

### 4.3 数据完整性机制

数据完整性通过"事务边界 + 唯一索引 + 配对账目 + 账目平衡 + 资金隔离 + 余额一致性"共同保证。

#### 4.3.1 事务边界设计

**原则**：必须同生共死的操作放在同一事务；涉及网络调用的操作不放入事务（避免连接池耗尽）。

| 方法 | 事务内操作 | 原因 | 文件位置 |
|------|-----------|------|----------|
| `CreateRoundSettlementAndBills` | 创建 settlement + 所有 bills | settlement 和 bills 必须同生共死 | [bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) L157-169 |
| `CreateBillsPairInTransaction` | 创建 player bill + platform bill | 配对账目必须同时存在，否则账目不平衡 | L171-181 |
| `CreateBillsInTransaction` | 批量创建 bills | 罚款分配的多条 share bill 必须同时存在 | L146-155 |
| `UpdateRefundSuccessInTransaction` | 更新 refund_audit + 更新 bill | 退款状态必须一致 | L243-273 |
| `applyForRefundLocked` | 创建 refund_audit + 更新 bill refund_status | 退款申请和 bill 状态联动 | [refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) L105-120 |
| `handleRoundSettle` | round 更新 + grab_record + 统计 + reward + SettleRound | 游戏结果和结算必须原子 | [game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) L234-367 |
| `handleSessionEnd` | session 更新 + player 统计 + SettleGame | 会话状态和结算必须原子 | L405-442 |

**关键边界说明**：

- `SettleRound` 内部的 `creditRound`（写 BillRecord）**不在** DB 事务内，而是用 Redis 分布式锁 + 状态机保证。这是因为 `creditRound` 内部可能涉及 reward 结算的网络调用，DB 事务持有时间过长会导致连接池耗尽
- 调用方（`handleRoundSettle`）将 `SettleRound` 包在外层事务内，失败时整体回滚
- `platform.Debit` / `platform.Credit` 等网络调用**绝不在 DB 事务内**，调用前创建 Bill(Processing)，调用后更新 Bill 状态

#### 4.3.2 唯一索引保证

文件：[model/bill.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/bill.go)

| 表 | 字段 | 索引类型 | 防止的异常 |
|----|------|---------|-----------|
| `bill_record` | `BizOrderNo` | uniqueIndex | 重复入账/扣款 |
| `bill_record` | (round_id, bill_type, user_id) | 复合查询 | 同一回合同一类型同一用户重复 Bill（通过 `GetBillByRoundTypeAndUser` 检查） |
| `round_settlement` | `RoundTraceID` | uniqueIndex | 重复创建回合结算记录 |
| `round_settlement` | `RoundID` | uniqueIndex | 同一回合重复结算 |
| `exception_record` | `ExceptionNo` | uniqueIndex | 重复创建异常 |
| `refund_audit` | `RefundOrderNo` | uniqueIndex | 重复创建退款单 |

**唯一索引的兜底作用**：即使业务层幂等检查遗漏，数据库唯一索引也会阻止重复插入，调用方处理 err 保证数据完整。

#### 4.3.3 配对账目设计

**核心原则**：每一笔资金流动都有对应的"借方"和"贷方"账单，在同一事务创建。

**惩罚扣款配对**（[settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go) L213-315 `DeductPenaltyToPlatform`）：

```
事务内:
  playerBill:   UserID=玩家,  Amount=-req.Amount, Status=Processing  (玩家被扣)
  platformBill: UserID=0(系统), Amount=+req.Amount, Status=Success   (平台收入)
  → CreateBillsPairInTransaction
```

**罚款分配配对**（L317-364 `DistributePenaltyFromPlatform`）：

```
事务内:
  platformBill: UserID=0(系统), Amount=-req.Amount, Status=Success   (平台支出)
  shareBill × N: UserID=玩家i, Amount=share, Status=Success          (玩家收入)
  → CreateBillsInTransaction
  (均分逻辑处理余数: remainder 逐个 +1, 保证总额严格相等)
```

**系统红包配对**（[deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L410-461 `DeductForSystemPacket`）：

```
platformBill: UserID=0(系统), Amount=-req.TotalAmount, Status=Success (平台支出)
(玩家抢红包的 grab bill 在 creditRound 中创建, 形成配对)
```

#### 4.3.4 账目平衡公式

**系统收入 = 玩家总扣款 - 玩家总收入**

聚合查询保证（[bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go)）：

- `AggregateBetBySession`（L371-391）：`SUM(ABS(amount)) WHERE amount<0 AND status=Success AND user_id != PlatformAccountID` — 玩家总扣
- `AggregatePayOutBySession`（L414-434）：`SUM(amount) WHERE amount>0 AND status=Success AND user_id != PlatformAccountID AND bill_type != SessionCredit` — 玩家总收入
  - **关键**：`bill_type != BillTypeSessionCredit` 排除会话级入账，因为回合级 grab bill 已计入，避免重复计算

**会话级入账 = 玩家总收入**（`game_settle_service.go` L121 `creditSessionPayouts` 用 payOutMap 入账），两阶段保证总额一致。

#### 4.3.5 机器人与真人资金隔离

通过 `RobotChecker` 在每笔账单操作前判断身份，分流到不同通道：

| 操作 | 真人玩家 | 机器人 |
|------|---------|--------|
| 扣款 | `platform.Debit` | `virtualBalance.Deduct`（Lua 原子） |
| 入账 | `platform.Credit` | `virtualBalance.Credit`（INCRBY） |
| 余额查询 | `platform.GetBalance` | `virtualBalance.GetBalance` |
| 游戏结算 | `platform.Settle` | 跳过，仅更新状态 |

- 两者账单都写入同一 `bill_record` 表，通过 `IsRobot` 索引区分，便于统计和审计
- 机器人虚拟余额存储在 Redis（`cashparty:robot:virtual_balance:{userID}`），与平台真实资金完全隔离
- **fail-safe 机器人检查**：`robot_checker.go` 在 redis nil 或查询出错时返回 false（按真人处理，走更严格的平台路径）

#### 4.3.6 Lua 原子性防资金凭空创造

文件：[service/lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go)

```lua
local newBalance = redis.call('INCRBY', KEYS[1], -ARGV[1])
if newBalance < 0 then
    redis.call('INCRBY', KEYS[1], ARGV[1])  -- 回滚
    return 0
end
redis.call('SADD', KEYS[2], ARGV[2])  -- 标记 dirty
return 1
```

**设计动机**（代码注释 L14-15）：
> 消除原 Go 代码三步之间的竞态。若无原子性，两线程同时读到余额 100、各扣 60，都判断 100-60=40>=0 通过，结果余额=40 但应扣 120——**凭空创造 80 资金**。

- Redis 单线程执行 Lua，"扣减→检查→回滚→标记 dirty"四步原子
- 返回 1=成功，0=余额不足（已回滚）

#### 4.3.7 余额快照的 Redis Hash（SessionPlayerTotalsKey）

文件：[lua_game.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/lua_game.go) L569-586

- `SessionPlayerTotalsKey(sessionID)` 是 Redis Hash，key=userID, value=累计收益
- 每回合 `LuaSettleRound` 时**原子累加**该 session 内每个玩家的抢红包金额和奖励金额
- 游戏结束时直接从该 Hash 读取排名，无需回溯计算
- **保证**：累加操作在 Lua 内原子完成，避免并发结算导致的数据竞争；Lua 推进 phase=SETTLED 与更新 totals 同 Lua 调用，不会出现状态已推进但 totals 未更新的情况

### 4.4 幂等性三层防线

所有关键操作都基于**三层防线**保证幂等：

#### 第一层：Redis SetNX 全局去重

- Kafka 消费端 `tryAcquire(traceID)` 用 SetNX 抢占，7天 TTL
- 保证同一 traceID 在全集群范围内只能被一个 consumer 首次处理
- fail-open 策略：Redis 故障时让流程继续，依赖 DB 层兜底

#### 第二层：DB 唯一索引 / FirstOrCreate

- `BizOrderNo` / `RoundTraceID` / `RoundID` / `ExceptionNo` / `RefundOrderNo` 唯一索引
- `grab_record` 使用 `FirstOrCreate` 按 (round_id, user_id) 查重
- 即使 SetNX 失效，数据库唯一索引也会阻止重复插入

#### 第三层：业务状态机 / BillRecord 存在性检查

| 操作 | 幂等键 | 检查方式 |
|------|--------|---------|
| `DeductForFirstRound` | roundID | `ExistsRoundSettlement` + 锁内二次检查 |
| `DeductForSystemPacket` | roundID + billType | `ExistsByRoundAndType` |
| `deductSingleUser` | roundID + billType | `ExistsByRoundAndType` |
| `SettleRound` | roundID | `status==Credited` 早返回 + 锁内检查 |
| `creditRound` | roundID + billType + userID | `GetBillByRoundTypeAndUser` 跳过已成功 |
| `settleCommission` | roundID + Commission + PlatformAccountID | `GetBillByRoundTypeAndUser` |
| `SettleReward` | roundID + SystemReward + PlatformAccountID | `GetBillByRoundTypeAndUser` |
| `creditSessionPayout` | sessionID + SessionCredit + userID | `GetBillsBySessionTypeAndUser` 检查现有 Bill 状态 |
| `SettleGame` | sessionID | 检查所有 round 的 `GameSettleStatus==Success` |
| `RetryCredit` | billID | 检查 `bill.Status==Success` |
| `ApplyForRefund` | billID | 检查 `bill.RefundStatus` + 已有 pending 退款单 |
| `ApproveRefund` / `RejectRefund` | refundOrderNo | 锁内二次检查 `status==Pending` |

**DCL 模式（双重检查锁定）**：所有"先检查后操作"都采用锁外快速检查 + 锁内安全检查，兼顾性能和正确性，防止"检查后加锁前"的并发窗口（TOCTOU）。

### 4.5 并发安全

#### 4.5.1 分布式锁的 TTL 设计

所有锁使用 `lock.WithRedisLock(ctx, redis, lockKey, ttl, func)` 封装，TTL 因操作复杂度而异：

| 锁 Key | TTL | 场景 |
|--------|-----|------|
| `SettleRoundLockKey(roundID)` | 30s | 回合结算 |
| `GameSettleLockKey(sessionID)` | 60s | 会话级结算（含平台调用） |
| `FirstRoundDeductLockKey(sessionID)` | 60s | 首回合批量扣款 |
| `BillRetryLockKey(billID)` | 30s | 单 bill 重试 |
| `RefundApplyLockKey(billID)` | 30s | 退款申请 |
| `RefundLockKey(refundOrderNo)` | 30s | 退款审批/拒绝 |
| `GameSettleRetryLockKey(sessionID, userID)` | 30s | 单玩家重试结算 |
| `scheduler:*:lock` | 60-300s | 调度器单实例执行 |

**TTL 设计原则**：操作越复杂（涉及平台调用越多）TTL 越长；60s 足够覆盖平台调用超时，避免锁过期导致并发。

#### 4.5.2 信号量限流

文件：[deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) L136-198

```go
maxConcurrent := s.maxConcurrentDeduct  // 默认 20
sem := make(chan struct{}, maxConcurrent)
for _, bill := range bills {
    wg.Add(1)
    sem <- struct{}{}   // 获取信号量
    go func(b *model.BillRecord) {
        defer wg.Done()
        defer func() { <-sem }()  // 释放信号量
        err := s.executeSingleDeduct(ctx, b, amount)
        mu.Lock()
        defer mu.Unlock()
        // ... 累加结果
    }(bill)
}
wg.Wait()
```

- 信号量（buffer=20）限制并发平台扣款调用数，避免瞬时压垮平台
- `sync.Mutex` 保护 `result` 累加（SuccessCount/FailedCount 等）
- `sync.WaitGroup` 等待所有完成

#### 4.5.3 数据库事务隔离与原子自增

- GORM `Transaction` 使用默认隔离级别，配合唯一索引和乐观状态检查弥补
- `gorm.Expr("retry_count + 1")` 使用 SQL 原子自增，避免读-改-写竞态
- `gorm.Expr("grab_count + 1")` 同样使用原子自增

### 4.6 错误分级处理

#### 4.6.1 可恢复 vs 不可恢复错误

**可恢复错误（重试）**：
- 平台入账失败（`platform.Credit` err）——网络抖动、平台临时不可用
- 处理：标记 Failed + `SetNextRetryTime` + 指数退避重试
- 依据：入账是"加钱"操作，平台通过 BizID 幂等，重试安全

**不可恢复错误（异常）**：
- 扣款失败（`platform.Debit` err）——余额不足、用户冻结
- 处理：标记 Failed + 立即创建异常记录（`CreateDebitFailedException`），不重试
- 重试超限（`retry_count >= 3`）——持续失败说明非临时问题
- 处理：创建 `ExceptionTypeCreditRetryExceed` 异常

#### 4.6.2 自动补偿 vs 人工告警

**判断核心**（[settlement_check_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_check_service.go) L49-52）：
> 在 creditRound 总是成功的新模型下，此场景说明游戏结果事件可能丢失。不自动退款（可能游戏实际已进行），创建异常记录人工审核。

**自动补偿（能确定资金归属）**：

| 场景 | 自动动作 | 触发条件 |
|------|---------|---------|
| 首回合扣款失败 | 自动申请退款 | 10min 后仍 Failed |
| FirstRoundFail 退款 | 自动 Approve | Pending 状态 |
| 入账失败 | 自动重试 | 30s 周期 |
| 会话未结算 | 强制 SettleGame | 1小时超时 |

**人工告警（无法确定资金归属）**：

| 场景 | 处理 | 原因 |
|------|------|------|
| 已扣款未结算 | 创建异常人工审核 | creditRound 总成功，此场景说明事件丢失，自动退款可能误退 |
| 入账重试超限 | 创建异常 | 3次重试仍失败，需人工介入 |
| 扣款失败 | 创建异常 | 不可恢复，需人工处理 |

**判断原则**：宁可人工也不误操作。自动退款给玩家虽简单，但若游戏实际已进行（玩家已抢红包），退款会导致平台损失。无法确定时交人工判断。

#### 4.6.3 扣款不重试 vs 入账必重试的设计动机

**扣款失败不重试**：
- 扣款是"减钱"操作，平台通常因余额不足或用户状态异常拒绝
- 重试大概率仍失败（余额不会自动增加）
- 盲目重试可能对用户造成困扰（多次扣款尝试）
- 资金安全：扣款失败=资金未动，无需补偿，只需退款给应退的玩家
- 补偿路径：`CreateDebitFailedException` → `SettlementCheckScheduler` → `ensureRefundCreated` → `RefundProcessScheduler` 自动退款

**入账失败必重试**：
- 入账是"加钱"操作，失败通常是网络/平台临时问题
- 平台通过 BizID 幂等，重试安全（不会重复加钱）
- 不重试会导致玩家资金"丢失"（已扣款未入账），违反资金不丢原则
- 指数退避（5s→10s→20s）避免压垮平台，3次失败后异常兜底

**对称性设计**：扣款失败有"退款"作为补偿路径（资金退回）；入账失败只能靠"重试"（资金必须到达）。因此入账重试是资金不丢的关键，扣款不重试是避免无效操作。

### 4.7 数据安全保障总览图

```
┌─────────────────────────────────────────────────────────────────┐
│                        数据不丢（五道防线）                       │
│  ① Kafka SetNX 抢占 + 失败释放                                    │
│  ② DB 事务回滚（DB状态+SettleRound 同事务）                       │
│  ③ 入账指数退避重试（5s→10s→20s）                                  │
│  ④ 异常记录兜底（扣款失败/重试超限/已扣未结算）                     │
│  ⑤ 余额快照 + 平台调用日志可追溯                                   │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     最终一致性（五个调度器）                      │
│  ① CreditRetryScheduler(30s) — 入账重试                          │
│  ② GameSettleRetryScheduler(30s) — 游戏结算重试                  │
│  ③ RefundProcessScheduler(1min) — 退款自动审批                    │
│  ④ SettlementCheckScheduler(5min) — 对账补偿                      │
│  ⑤ GameSettleTimeoutScheduler(5min) — 超时强制结算                │
│  + 状态机只能向前推进                                              │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     数据完整性（六重保证）                        │
│  ① DB 事务边界严格（同生共死的操作同事务）                          │
│  ② 唯一索引防重（BizOrderNo/RoundTraceID/RoundID 等）             │
│  ③ 配对账目平衡（player+platform 两条 Bill 同事务）                │
│  ④ 账目平衡公式（系统收入=玩家总扣-玩家总收入）                    │
│  ⑤ 机器人与真人资金隔离（虚拟通道+IsRobot标记）                    │
│  ⑥ Lua 原子性（防资金凭空创造）                                    │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                     幂等三层防线                                   │
│  ① Redis SetNX 全局去重（traceID，7天TTL）                        │
│  ② DB 唯一索引 / FirstOrCreate                                    │
│  ③ 业务状态机 / BillRecord 存在性检查（DCL双重检查锁定）           │
└─────────────────────────────────────────────────────────────────┘
```

---

## 5. 核心流程图

### 5.1 整体结算流程总览

```
游戏开始
   │
   ▼
┌──────────────────────────────────────────────────────────────┐
│ 开局前预检：BalanceService.CheckBalanceForReady              │
│ 计算所需费用 → 机器人查虚拟余额 / 真人查平台余额              │
└──────────────────────────────┬───────────────────────────────┘
                               │ 余额充足
                               ▼
┌──────────────────────────────────────────────────────────────┐
│ 首回合扣款：DeductService.DeductForFirstRound                │
│ 幂等检查 → 加锁 → 创建 RoundSettlement(Deducting)+N条 Bill   │
│ → executeBatchDeduct 并发扣款(信号量限流20)                   │
│ → 全成功: Deducted / 部分失败: Failed + 为成功者创建退款单    │
└──────────────────────────────┬───────────────────────────────┘
                               │
                               ▼
                      游戏进行中（多回合）
                               │
            ┌──────────────────┼──────────────────┐
            ▼                  ▼                  ▼
   ┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
   │ 后续回合扣款    │ │ 系统红包扣款    │ │ 惩罚扣款        │
   │ DeductForLater  │ │ DeductForSystem │ │ DeductPenaltyTo │
   │ Round (最小玩家)│ │ Packet          │ │ Platform        │
   └─────────────────┘ └─────────────────┘ └─────────────────┘
                               │
                               ▼ 每回合结束（Kafka 事件触发）
┌──────────────────────────────────────────────────────────────┐
│ 回合结算：SettlementService.SettleRound                      │
│ 幂等检查(status==Credited) → 加锁 → creditRound              │
│   ├─ 创建 grab Bill (Success, 仅记账)                         │
│   ├─ 创建 commission Bill (系统收入)                         │
│   └─ SettleReward: 创建系统奖励 Bill (如触发顺子/豹子)        │
│ → 统一标记 round_settlement.status = Credited                 │
└──────────────────────────────┬───────────────────────────────┘
                               │ 游戏结束（Kafka 事件触发）
                               ▼
┌──────────────────────────────────────────────────────────────┐
│ 会话级结算：GameSettleService.SettleGame                     │
│ 加锁 → 检查所有 round 是否 Credited → 标记 Settling           │
│ → 聚合 bet/payout → creditSessionPayouts                      │
│   └─ 对每个 payout>0 玩家调用 platform.Credit (真实资金移动)  │
│ → 遍历玩家 settlePlayer: platform.Settle 上报游戏结果         │
│ → 更新 GameSettleStatus = Success / Failed                   │
└──────────────────────────────────────────────────────────────┘
```

### 5.2 首回合扣款流程

```
DeductService.DeductForFirstRound(ctx, req)
        │
        ▼
┌─────────────────────────────────────────┐
│ 1. 幂等检查: ExistsRoundSettlement(roundID)│
│    若已存在 → 返回已有结果 (getExistingFirstRoundResult)│
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 2. 加锁: FirstRoundDeductLockKey(sessionID)│
│    锁内二次检查 (DCL)                     │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 3. 创建 RoundSettlement(status=Deducting)│
│    + N条 BillRecord(status=Processing)  │
│    (事务: CreateRoundSettlementAndBills) │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 4. executeBatchDeduct 并发扣款           │
│    sem := make(chan struct{}, 20)       │
│    for each player:                      │
│      go executeSingleDeduct(bill)       │
│        ├─ 机器人: virtualBalance.Deduct  │
│        └─ 真人: platform.Debit           │
│    wg.Wait()                             │
└────────────────────┬────────────────────┘
                     ▼
            ┌────────┴────────┐
            ▼                 ▼
    ┌──────────────┐   ┌──────────────┐
    │ 全部成功      │   │ 部分失败      │
    │ RoundSettle  │   │ RoundSettle  │
    │ → Deducted   │   │ → Failed     │
    └──────────────┘   │ + handleFirst│
                       │   RoundDeduct│
                       │   Failure:   │
                       │   为成功者创建│
                       │   RefundAudit│
                       └──────────────┘
```

### 5.3 回合结算流程

```
SettlementService.SettleRound(ctx, req)
        │
        ▼
┌─────────────────────────────────────────┐
│ 1. 幂等检查: 若 round_settlement.status │
│    == Credited → 直接返回 nil           │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 2. 加锁: SettleRoundLockKey(roundID)    │
│    锁内二次检查 status                   │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 3. UpdateRoundSettlementSettleInfo       │
│    (写入 sender/amount/commission 等元信息)│
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 4. creditRound: 回合级内部记账           │
│    for each player:                      │
│      ├─ 幂等: GetBillByRoundTypeAndUser  │
│      │   (roundID+GrabPacket+userID)     │
│      ├─ 已 Success → 跳过                │
│      └─ 创建 grab Bill (Success, 仅记账) │
│    settleCommission: 创建佣金 Bill        │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 5. SettleReward (如 rewardType>0)        │
│    ├─ 幂等: 检查平台 SystemReward Bill    │
│    └─ 创建 1条平台支出 Bill + N条玩家   │
│       收入 Bill (事务批量创建)           │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 6. UpdateRoundSettlementCredited         │
│    统一标记 status = Credited            │
│    (credit + reward 全部成功后才标记)    │
└─────────────────────────────────────────┘
```

### 5.4 会话级结算流程

```
GameSettleService.SettleGame(ctx, sessionID)
        │
        ▼
┌─────────────────────────────────────────┐
│ 1. 加锁: GameSettleLockKey(sessionID)   │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 2. GetAllRoundSettlementsBySession       │
│    检查所有 round 是否 Credited           │
│    若已 allSettled → 直接返回 nil        │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 3. 标记 GameSettleStatus = Settling      │
│    (所有 Bill 的 game_settle_status)     │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 4. 聚合: AggregateBetBySession          │
│         AggregatePayOutBySession         │
│    (返回 map[userID]amount)              │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 5. creditSessionPayouts                  │
│    for each player (payout>0):           │
│      creditSessionPayout(sessionID,userID)│
│        ├─ 幂等: 查现有 SESSION_CREDIT Bill│
│        ├─ Success → 跳过                 │
│        ├─ Processing/Failed → 重试        │
│        └─ 新建 Bill → executeSessionCredit│
│             ├─ 机器人: virtualBalance.Credit│
│             └─ 真人: platform.Credit     │
│    (单玩家失败仅记日志, 不中断整体)        │
└────────────────────┬────────────────────┘
                     ▼
┌─────────────────────────────────────────┐
│ 6. 遍历玩家 settlePlayer                │
│    机器人: 跳过 platform.Settle, 仅更新状态│
│    真人: platform.Settle(bet/payout/result)│
│         + 标记该玩家所有 Bill 为 settled  │
└────────────────────┬────────────────────┘
                     ▼
            ┌────────┴────────┐
            ▼                 ▼
    ┌──────────────┐   ┌──────────────┐
    │ 全部成功      │   │ 任一失败      │
    │ → Success    │   │ → Failed     │
    │              │   │ (返回 error   │
    │              │   │  触发上层重试) │
    └──────────────┘   └──────────────┘
```

### 5.5 入账重试流程（指数退避）

```
CreditRetryScheduler (每30s)
        │
        ▼
CreditRetryService.GetRetryableCredits(ctx, 100)
  查询: amount>0 AND status IN (Processing,Failed)
        AND retry_count < 3
        AND (next_retry_at IS NULL OR <= now)
        │
        ▼
   for each bill:
     if next_retry_at > now: skip  (退避等待)
     else: RetryCredit(billID)
        │
        ▼
   doRetryCredit(billID):
     ├─ 加锁: BillRetryLockKey(billID)
     ├─ 查 Bill
     ├─ 若已 Success → 直接返回
     ├─ 若 retry_count >= MaxRetryCount(3)
     │    → createException(ExceptionTypeCreditRetryExceed)
     │    → 返回 (不再重试, 转人工)
     └─ 否则 executeCredit(bill):
          ├─ 机器人: virtualBalance.Credit
          ├─ 平台账号(UserID=0): 直接标成功
          └─ 真人: platform.Credit → 更新 Bill
              ├─ 成功: Status=Success
              └─ 失败: IncrementRetryCount
                       + calculateNextRetryTime
                         delay = BaseDelay(5s) × 2^retryCount
                         封顶 MaxDelay(5min)
                       设置 next_retry_at
```

### 5.6 退款流程

```
退款状态机: None → Pending → Approved → Refunded
                              └→ Rejected

申请退款: RefundService.ApplyForRefund
   ├─ 加锁: RefundApplyLockKey(billID)
   ├─ 查 Bill, 校验 status==Success && refund_status==None
   ├─ 若已存在 pending 退款单 → 返回已有单号 (幂等)
   └─ 新建 RefundAudit(Pending) + 更新 Bill.refund_status (事务)

自动审批: RefundProcessScheduler (每1min)
   ├─ 查 status==Pending 的退款单
   └─ 若 RefundType==FirstRoundFail (首回合失败)
       → ApproveRefund (ApprovedBy=0, 系统)

人工审批: RefundService.ApproveRefund
   ├─ 加锁: RefundLockKey(refundOrderNo)
   ├─ 校验 status==Pending
   ├─ 更新为 Approved
   └─ executeRefund:
       ├─ userIDConvert → platform.Credit (退款即给玩家入账)
       ├─ 更新 callLog
       └─ UpdateRefundSuccessInTransaction
           (事务更新 RefundAudit + BillRecord)

拒绝退款: RefundService.RejectRefund
   ├─ 加锁 + 二次检查
   └─ 更新为 Rejected + 更新 Bill.refund_status
```

### 5.7 补偿对账流程

```
SettlementCheckScheduler (每5min)
        │
        ├──► CheckFirstRoundDeductFailure
        │    查询10分钟前首回合扣款失败的 RoundSettlement
        │    for each: ensureRefundCreated
        │      遍历该 round 下所有 Success 且 RefundStatus=None 的 Bill
        │      → RefundService.ApplyForRefund (自动申请退款)
        │
        └──► CheckDeductedButNotSettled(now - 5min)
             查询已扣款(Deducted)但未结算(非Credited)超过5分钟的记录
             for each: handleDeductedNotSettled
               → 创建 ExceptionRecord(ExceptionTypeDeductedNotSettled)
               (仅告警, 不自动退款 - 因 creditRound 总是成功,
                此场景说明游戏结果事件可能丢失, 自动退款可能误退)
```

---

## 6. 目录结构与模块职责

```
settlement/
├── config/                     # 配置层
│   └── config.go               # PlatformConfig 透传与默认值
├── dto/                        # 数据传输对象
│   ├── constants.go            # 所有状态码、类型码、重试参数常量
│   ├── request.go              # 请求 DTO（结算/扣款/退款/余额检查）
│   └── response.go             # 响应 DTO（带 json tag）
├── model/                      # 数据模型（GORM 表结构）
│   ├── bill.go                 # BillRecord + RoundSettlement
│   ├── exception_record.go     # ExceptionRecord
│   ├── platform_settle_log.go  # PlatformCallLog
│   └── refund.go               # RefundAudit
├── infrastructure/
│   └── persistence/
│       └── redis/
│           └── keys.go         # Redis 键定义与格式化函数
├── scheduler/                  # 调度器层
│   ├── base.go                 # BaseScheduler 基类
│   ├── credit_retry_scheduler.go
│   ├── game_settle_retry_scheduler.go
│   ├── game_settle_timeout_scheduler.go
│   ├── refund_process_scheduler.go
│   └── settlement_check_scheduler.go
├── service/                    # 服务层
│   ├── settlement_service.go      # 门面/总编排
│   ├── game_settle_service.go     # 会话级结算
│   ├── deduct_service.go          # 扣款
│   ├── bill_manager.go            # 账单 Repository
│   ├── reward_settler.go          # 奖励结算
│   ├── balance_service.go        # 余额检查
│   ├── credit_retry_service.go   # 入账重试
│   ├── refund_service.go          # 退款
│   ├── exception_manager.go      # 异常记录 Repository
│   ├── platform_call_manager.go  # 平台调用日志 Repository
│   ├── robot_checker.go           # 机器人检查（接口+实现）
│   ├── user_id_convert_service.go # 用户ID转换
│   ├── virtual_balance_service.go # 虚拟余额
│   ├── trace_id_generator.go      # 追踪ID生成
│   └── lua_scripts.go             # Lua 脚本
├── SETTLEMENT_SOLUTION.md      # 原始解决方案文档
└── SETTLEMENT_WIKI.md          # 本文档
```

### 6.1 文件分类

| 类别 | 文件 | 职责 |
|------|------|------|
| **业务编排服务** | [settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go) | 门面/总编排，统一入口 |
| | [game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) | 会话级结算（真实资金移动） |
| | [deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) | 各类扣款 |
| | [reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go) | 系统奖励结算 |
| | [refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) | 退款申请/审批/执行 |
| | [credit_retry_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go) | 入账重试（指数退避） |
| | [balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/balance_service.go) | 开局前余额检查 |
| | [settlement_check_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_check_service.go) | 补偿对账 |
| **数据访问（Repository）** | [bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) | 账单/回合结算 CRUD |
| | [exception_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/exception_manager.go) | 异常记录 CRUD |
| | [platform_call_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/platform_call_manager.go) | 平台调用日志 CRUD |
| **基础设施工具** | [trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) | 各类ID生成 |
| | [user_id_convert_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/user_id_convert_service.go) | 内部ID→平台ID转换 |
| | [virtual_balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/virtual_balance_service.go) | 机器人虚拟余额 |
| | [robot_checker.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/robot_checker.go) | 机器人识别接口+实现 |
| | [lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go) | Lua 原子脚本 |

---

## 7. 关键类与函数说明

### 7.1 SettlementService（门面/编排器）

文件：[service/settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go)

#### 结构体

```go
type SettlementService struct {
    platform       platform.Client           // 平台 API 客户端
    billMgr        *BillManager              // 账单数据访问
    redis          *cRedis.Client            // 分布式锁
    traceIDGen     *TraceIDGenerator         // ID 生成器
    deductSvc      *DeductService             // 扣款服务
    rewardSettler  *RewardSettler            // 奖励结算器
    cfg            *config.PlatformConfig    // 平台配置
    userIDConvert  *UserIDConvertService     // 用户ID转换
    gameSettleSvc  *GameSettleService        // 游戏级结算
    callMgr        *PlatformCallManager      // 平台调用日志
    robotChecker   RobotChecker              // 机器人检查（接口）
    virtualBalance *VirtualBalanceService    // 虚拟余额
}
```

#### 关键方法

| 方法 | 功能 | 返回值 |
|------|------|--------|
| `NewSettlementService(...)` | 构造函数，注入12个依赖；cfg 为 nil 时用默认配置 | `*SettlementService` |
| `SettleRound(ctx, req)` | **核心入口**：完成一轮入账（grab+commission+reward）。幂等检查→加锁→二次检查→creditRound→SettleReward→统一标记 Credited | `error` |
| `creditRound(ctx, settlement, players)` | 回合级内部记账：为 grab/commission 创建 `Success` 的 BillRecord，**不调用 platform.Credit** | `(totalSettleAmount, settleUserCount, error)` |
| `settleCommission(ctx, settlement)` | 创建佣金 BillRecord（平台账户收入） | `error` |
| `DeductPenaltyToPlatform(ctx, req)` | 惩罚扣款：配对创建 player/platform 两条 Bill（事务），真人走 platform.Debit，机器人走虚拟钱包 | `error` |
| `DistributePenaltyFromPlatform(ctx, req)` | 罚款分配：将平台罚金按人均分给多个接收者（处理整除余数） | `error` |
| `SettleGame(ctx, sessionID)` | 委托给 gameSettleSvc.SettleGame | `error` |
| `CheckBalance(ctx, userID, requiredAmount)` | 余额检查：机器人走虚拟余额，真人走 platform.GetBalance | `(balance, isSufficient, error)` |
| `GetUserBalance(ctx, userID)` | 获取用户余额（同上分流） | `(balance, error)` |
| `GetBillByTraceID` / `GetBillsByUserID` / `GetBillsByRoundID` / `GetRoundSettlement` | 查询代理方法 | 各种 |

#### 设计亮点

- **状态机一致性**：`creditRound` 只写 BillRecord，**不**在此标记 `round_settlement.status = Credited`；status 由 `SettleRound` 在 credit + reward 全部成功后统一标记。避免 reward 失败但 round 已标 Credited，导致重试时进入早返回分支、reward 永远无法补偿。
- **机器人虚拟通道**：所有涉及资金的方法都通过 `robotChecker.IsRobot` 分流。
- **配对账目**：`DeductPenaltyToPlatform` 通过 `CreateBillsPairInTransaction` 在同一事务创建 player 和 platform 两条配对 Bill，保证账目平衡。

### 7.2 GameSettleService（会话级结算）

文件：[service/game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go)

#### 结构体

```go
type GameSettleService struct {
    platform       platform.Client
    billMgr        *BillManager
    redis          *cRedis.Client
    traceIDGen     *TraceIDGenerator
    cfg            *config.PlatformConfig
    userIDConvert  *UserIDConvertService
    callMgr        *PlatformCallManager
    robotChecker   RobotChecker
    virtualBalance *VirtualBalanceService
}
```

#### 关键方法

| 方法 | 功能 |
|------|------|
| `SettleGame(ctx, sessionID)` | 游戏级结算主流程：加锁→查所有 round_settlement→检查已结算→检查所有 round 是否 Credited→标记 Settling→聚合 bet/payout→creditSessionPayouts→遍历玩家 settlePlayer→更新最终状态 |
| `settlePlayer(ctx, sessionID, userID, betAmount, payOut, ...)` | 单玩家游戏结算：机器人跳过 platform.Settle 仅更新状态；真人调用 platform.Settle 上报 game result（bet_amount/payout/result=win\|lose），标记该玩家所有 Bill 为 settled |
| `RetryPlayerSettle(ctx, sessionID, userID)` | 单玩家重试（带独立锁 GameSettleRetryLockKey） |
| `creditSessionPayouts(ctx, sessionID, payOutMap, roomID)` | 会话级入账：遍历 payOutMap，对每个 payout>0 的玩家执行 creditSessionPayout |
| `creditSessionPayout(ctx, sessionID, userID, payOut, roomID)` | 单玩家会话级入账（含幂等检查）：查现有 SESSION_CREDIT 类型 Bill，若 Success 直接返回，若 Processing/Failed 则重试，否则新建 Bill 并执行 |
| `executeSessionCredit(ctx, bill)` | 真正调用 platform.Credit：机器人走虚拟钱包；真人创建 callLog→platform.Credit→解析余额→更新 Bill 状态，失败时设置 next_retry_at（5秒后） |

#### 关键业务逻辑

**会话级入账是真实资金移动的唯一入口**。回合级 `creditRound` 仅是内部账簿记录，真正的资金入账发生在 `SettleGame → creditSessionPayouts → executeSessionCredit → platform.Credit`。扣款则早在 `DeductForFirstRound`/`DeductForLaterRound` 阶段已完成。

#### 错误处理

- `creditSessionPayouts` 中单个玩家失败仅记日志，不中断整体流程
- `SettleGame` 中若任一玩家 settle 失败，最终状态标为 `GameSettleStatusFailed`，返回 error 触发上层重试
- `executeSessionCredit` 失败时设置 `next_retry_at = now + 5s`，供 CreditRetryService 扫描重试

### 7.3 DeductService（扣款服务）

文件：[service/deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go)

#### 结构体

```go
const defaultMaxConcurrentDeduct = 20

type DeductService struct {
    platform            platform.Client
    billMgr             *BillManager
    redis               *cRedis.Client
    traceIDGen          *TraceIDGenerator
    cfg                 *config.PlatformConfig
    creditRetrySvc      *CreditRetryService
    userIDConvert       *UserIDConvertService
    callMgr             *PlatformCallManager
    maxConcurrentDeduct int   // 默认20
    robotChecker        RobotChecker
    virtualBalance      *VirtualBalanceService
}
```

#### 关键方法

| 方法 | 功能 |
|------|------|
| `DeductForFirstRound(ctx, req)` | **首回合平摊扣款**：幂等检查→加锁→二次检查→创建 RoundSettlement(Deducting)+N条 Bill(Processing)→executeBatchDeduct 并发扣款→失败则 handleFirstRoundDeductFailure 创建退款审计 |
| `executeBatchDeduct(ctx, bills, amount, roundTraceID)` | **并发批量扣款**：WaitGroup+Mutex+信号量（容量20）并发执行 executeSingleDeduct，汇总结果，全成功则更新 Deducted，否则 Failed |
| `executeSingleDeduct(ctx, bill, amount)` | **单玩家扣款**：机器人→virtualBalance.Deduct；真人→userIDConvert→platform.Debit→解析余额→更新 Bill 成功/失败，失败时调用 creditRetrySvc.CreateDebitFailedException |
| `handleFirstRoundDeductFailure(...)` | 部分失败时为成功扣款的玩家创建 RefundAudit（待退款），并更新 Bill 的 refund_status |
| `DeductForLaterRound(ctx, req)` | 后续回合扣款：由最低金额玩家承担整笔房费，委托 deductSingleUser |
| `DeductForSystemPacket(ctx, req)` | 系统发红包扣款：平台账户支出，幂等检查 ExistsByRoundAndType |
| `deductSingleUser(ctx, req, lockKey)` | 单用户扣款通用流程：幂等检查→加锁→二次检查→创建 RoundSettlement+Bill→executeSingleDeduct→更新状态 |

#### 并发安全设计

- **信号量限流**：`sem := make(chan struct{}, maxConcurrent)` 控制最大并发扣款数（默认20）
- **互斥锁保护共享结果**：`sync.Mutex` 保护 `result` 的累加
- **WaitGroup 等待所有 goroutine**：`wg.Wait()`
- **分布式锁防重**：`FirstRoundDeductLockKey(sessionID)` 防止同一会话并发扣款

### 7.4 BillManager（账单 Repository）

文件：[service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go)

#### 结构体

```go
type BillManager struct {
    db *gorm.DB
}
```

#### 方法分组

**账单 CRUD**：

| 方法 | 功能 |
|------|------|
| `CreateBill(ctx, bill)` | 单条创建 |
| `CreateBillsOnly(ctx, bills)` | 批量创建（事务），**不更新 round_settlement 状态**（供 creditRound 使用） |
| `CreateBillsInTransaction(ctx, bills)` | 批量创建（事务） |
| `CreateBillsPairInTransaction(ctx, bill1, bill2)` | 配对创建（事务，保证账目配对） |
| `CreateRoundSettlementAndBills(ctx, settlement, bills)` | 创建结算记录+账单（事务） |
| `UpdateBillStatus(ctx, billID, status, errMsg)` | 更新状态+错误信息 |
| `UpdateBillSuccess(ctx, billID, balanceBefore, balanceAfter)` | 成功时更新状态+余额快照 |
| `UpdateBillRefundStatus` / `UpdateBillExceptionID` | 关联退款/异常 |

**查询方法**：

| 方法 | 功能 |
|------|------|
| `GetBillByTraceID` / `GetBillByID` | 单条查询 |
| `GetBillByRoundTypeAndUser(ctx, roundID, billType, userID)` | 幂等性检查专用 |
| `GetBillByBatchAndUser` | 按批次+用户查询 |
| `GetBillsByTraceID` / `GetBillsByUserID` / `GetBillsByRoundID` / `GetBillsByBatchID` | 列表查询 |
| `GetBillsBySessionTypeAndUser(ctx, sessionID, billType, userID)` | 会话级 Bill 查询（用于 creditSessionPayout 幂等） |
| `ExistsByRoundAndType(ctx, roundID, billType)` | 幂等性检查 |
| `ExistsRoundSettlement(ctx, roundID)` | 回合结算记录存在性检查 |

**RoundSettlement 状态管理**：

| 方法 | 功能 |
|------|------|
| `UpdateRoundSettlementCredited` | 标记 Credited + 写入 settle_amount/count/settled_at |
| `UpdateRoundSettlementStatus` | 通用状态更新 |
| `UpdateRoundSettlementDeductSuccess` | 扣款成功计数 |
| `UpdateRoundSettlementSettleInfo` | 更新结算元信息（sender/amount/commission 等） |
| `GetRoundSettlementByRoundID` / `GetAllRoundSettlementsBySession` | 查询 |
| `UpdateGameSettleStatusBySession` / `UpdateGameSettleStatusByUser` | 游戏级结算状态 |

**聚合查询**（用于游戏结算）：

| 方法 | 功能 |
|------|------|
| `AggregateBetBySession(ctx, sessionID)` | 按玩家聚合扣款金额（amount<0，取绝对值），返回 `map[userID]abs(sum)` |
| `AggregatePayOutBySession(ctx, sessionID)` | 按玩家聚合入账金额（amount>0，排除 SESSION_CREDIT 类型避免重复），返回 `map[userID]sum` |

**重试与补偿**：

| 方法 | 功能 |
|------|------|
| `GetRetryableCredits(ctx, limit)` | 查询可重试的入账 Bill（status 为 Processing/Failed 且 retry_count < MaxRetryCount 且 next_retry_at 已到） |
| `SetNextRetryTime` / `IncrementRetryCountWithNextRetryTime` | 重试计数+下次重试时间 |
| `GetFailedFirstRoundSettlements` | 首回合扣款失败的结算记录（用于补偿退款） |
| `GetDeductedButNotSettled` | 已扣款但未结算的记录（用于异常告警） |
| `GetFailedGameSettlements` / `GetTimedOutGameSettlements` / `GetUnsettledUsersBySession` | 游戏级结算兜底查询 |

**退款审计**：

| 方法 | 功能 |
|------|------|
| `CreateRefundAudit` / `GetRefundAuditByOrderNo` / `GetRefundAuditByBillID` | 退款单 CRUD |
| `UpdateRefundAuditStatus` / `UpdateRefundAuditError` | 退款单状态更新 |
| `UpdateRefundSuccessInTransaction` | 事务内同时更新 RefundAudit 和 BillRecord 的退款状态 |

### 7.5 RewardSettler（奖励结算器）

文件：[service/reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go)

#### 结构体

```go
type RewardSettlementConfig struct {
    Enabled                  bool
    StraightRewardMultiplier float64  // 顺子奖励倍数
    LeopardRewardMultiplier  float64  // 豹子奖励倍数
}

type RewardSettler struct {
    config       *RewardSettlementConfig
    billMgr      *BillManager
    traceIDGen   *TraceIDGenerator
    robotChecker RobotChecker
}
```

#### 关键方法

| 方法 | 功能 |
|------|------|
| `CalculateRewardAmount(rewardType, totalAmount)` | 根据奖励类型计算金额：type=1 顺子（×1.0）、type=2 豹子（×10.0）、其他返回0；config.Enabled=false 时返回0 |
| `SettleReward(ctx, settlement, players)` | 系统奖励结算：幂等检查平台支出 Bill→创建1条平台支出 Bill（负数，总额=rewardAmount×playerCount）+N条玩家收入 Bill→事务批量创建 |

#### 幂等性设计

`SettleReward` 开头通过 `GetBillByRoundTypeAndUser` 检查平台账户的 `SystemReward` 类型 Bill 是否已存在且 Success，若是则直接返回 nil。防止 Kafka 重试导致 reward bills 被重复创建，与 `creditRound` 中跳过已成功 grab bill 的模式保持一致。

### 7.6 CreditRetryService（入账重试）

文件：[service/credit_retry_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go)

#### 结构体

```go
type CreditRetryConfig struct {
    MaxRetryCount   int            // 默认 dto.MaxRetryCount = 3
    BaseDelay       time.Duration  // 5s
    MaxDelay        time.Duration  // 5min
    RetryMultiplier float64        // 2.0
}

type CreditRetryService struct {
    billMgr       *BillManager
    platform      platform.Client
    redis         *cRedis.Client
    traceIDGen    *TraceIDGenerator
    cfg           *config.PlatformConfig
    retryCfg      *CreditRetryConfig
    exceptionMgr  *ExceptionManager
    userIDConvert *UserIDConvertService
    callMgr       *PlatformCallManager
}
```

#### 关键方法

| 方法 | 功能 |
|------|------|
| `GetRetryableCredits(ctx, limit)` | 委托 billMgr 查询可重试的入账 Bill |
| `RetryCredit(ctx, billID)` | 重试入口：加锁（BillRetryLockKey）→doRetryCredit |
| `doRetryCredit(ctx, billID)` | 重试核心：查 Bill→已 Success 直接返回→超过 MaxRetryCount 则 createException→否则 executeCredit，失败时计算下次重试时间并 IncrementRetryCountWithNextRetryTime |
| `executeCredit(ctx, bill)` | 执行入账：平台账户直接标成功跳过→userIDConvert→platform.Credit→解析余额→更新 Bill 状态+callLog |
| `calculateNextRetryTime(retryCount)` | **指数退避**：delay = BaseDelay × (RetryMultiplier ^ retryCount)，上限 MaxDelay |
| `createExceptionRecord(ctx, bill, exceptionType, detail)` | 创建异常记录并关联到 Bill |
| `createException(ctx, bill)` | 入账重试超限异常（ExceptionTypeCreditRetryExceed） |
| `CreateDebitFailedException(ctx, bill)` | 扣款失败异常（ExceptionTypeDebitFailed），供 DeductService 调用 |

### 7.7 RefundService（退款服务）

文件：[service/refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go)

#### 结构体

```go
type RefundService struct {
    platform      platform.Client
    billMgr       *BillManager
    redis         *cRedis.Client
    traceIDGen    *TraceIDGenerator
    db            *gorm.DB
    cfg           *config.PlatformConfig
    userIDConvert *UserIDConvertService
    callMgr       *PlatformCallManager
}
```

#### 关键方法

| 方法 | 功能 |
|------|------|
| `ApplyForRefund(ctx, req)` | **申请退款**：加锁（RefundApplyLockKey(billID)）→applyForRefundLocked |
| `applyForRefundLocked(ctx, req)` | 锁内逻辑：查 Bill→校验状态（必须 Success，不能已退/待退）→若已存在 pending 退款则返回已有单号→新建 RefundAudit+更新 Bill.refund_status（事务） |
| `ApproveRefund(ctx, req)` | **审批退款**：查退款单→校验 pending→加锁（RefundLockKey）→二次检查→更新为 Approved→executeRefund |
| `executeRefund(ctx, refund)` | 执行退款：userIDConvert→platform.Credit（退款即给玩家入账）→更新 callLog→UpdateRefundSuccessInTransaction（事务更新 RefundAudit+BillRecord） |
| `RejectRefund(ctx, req)` | **拒绝退款**：加锁→二次检查→更新为 Rejected+更新 Bill.refund_status |
| `GetRefundAuditByOrderNo` / `GetRefundsByStatus` | 查询方法 |

### 7.8 其他服务

#### BalanceService（余额服务）

文件：[service/balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/balance_service.go)

| 方法 | 功能 |
|------|------|
| `CheckBalanceForReady(ctx, req)` | 开局前余额检查：计算所需费用→机器人查虚拟余额→真人查平台余额→返回 `BalanceCheckResult` |
| `CalculateRequiredFee(roomFee, maxPlayers, maxRounds)` | 计算所需总费用：首回合=roomFee/maxPlayers，后续回合=roomFee×(maxRounds-1)，总和 |
| `CheckUserBalance(ctx, userID)` | 真人玩家余额查询：userIDConvert→platform.GetBalance→ParseAmount |

#### SettlementCheckService（补偿对账）

文件：[service/settlement_check_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_check_service.go)

```go
type SettlementCheckService struct {
    billMgr      *BillManager
    exceptionMgr *ExceptionManager
    refundSvc    *RefundService
    traceIDGen   *TraceIDGenerator
}
```

| 方法 | 功能 |
|------|------|
| `CheckFirstRoundDeductFailure(ctx)` | 查询10分钟前首回合扣款失败的记录→对每个调用 ensureRefundCreated |
| `CheckDeductedButNotSettled(ctx, since)` | 查询已扣款但未结算的记录→handleDeductedNotSettled |
| `handleDeductedNotSettled(ctx, settlement)` | 创建异常记录（ExceptionTypeDeductedNotSettled）供人工审查，**不自动退款** |
| `ensureRefundCreated(ctx, settlement)` | 遍历该 round 下所有 Success 且 RefundStatus=None 的 Bill，调用 refundSvc.ApplyForRefund 自动申请退款 |

#### RobotChecker（机器人检查）

文件：[service/robot_checker.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/robot_checker.go)

```go
type RobotChecker interface {
    IsRobot(ctx context.Context, userID int64) bool
}

type redisRobotChecker struct {
    redis *cRedis.Client
}
```

- `IsRobot(ctx, userID)` — 使用 Redis `SISMEMBER cashparty:robot:user_ids {userID}` 实现 O(1) 查询。redis 为 nil 或查询出错时返回 false（**fail-safe**，按真人处理更安全）。

#### UserIDConvertService（用户ID转换）

文件：[service/user_id_convert_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/user_id_convert_service.go)

```go
type UserService interface {
    GetUserById(ctx context.Context, id string) (*gameModel.User, error)
}

type UserIDConvertService struct {
    userSvc UserService
}
```

- `GetPlatformUserID(ctx, internalUserID)` — 将内部 int64 userID 转换为平台 string userID。**平台账户（dto.PlatformAccountID=0）直接返回错误禁止调用平台 API**。

#### VirtualBalanceService（虚拟余额）

文件：[service/virtual_balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/virtual_balance_service.go)

```go
type VirtualBalanceService struct {
    redis *cRedis.Client
}
```

| 方法 | 功能 |
|------|------|
| `Deduct(ctx, userID, amount)` | **虚拟扣款（Lua 原子）**：执行 luaDeductBalance 脚本，返回0表示余额不足，返回1表示成功 |
| `Credit(ctx, userID, amount)` | 虚拟入账：`INCRBY` 增加余额 + `SADD` 加入 dirty 集合标记需要同步到 DB |
| `GetBalance(ctx, userID)` | 查询虚拟余额：缓存未命中（goredis.Nil）返回0并告警 |

**独立实现避免循环依赖**：注释明确说明与 game 层的 `VirtualBalanceService` 共享相同 Redis key 但独立实现，因为 `game → settlement` 依赖已存在，反向不允许。

#### TraceIDGenerator（追踪ID生成器）

文件：[service/trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go)

```go
type TraceIDGenerator struct {
    idGen *idgen.SnowflakeGenerator
}
```

| 方法 | 生成的 ID 格式 |
|------|---------------|
| `GenerateRoundTraceID(sessionID, roundNo)` | `RT_{sessionID}_{roundNo}` |
| `GenerateBizOrderNo(bizType, userID)` | `{bizType}_{yyyyMMddHHmmss}_{userID}_{3位随机}` |
| `GenerateBatchID()` | `BATCH_{snowflake}` |
| `GenerateRefundOrderNo(userID)` | 复用 GenerateBizOrderNo("REFUND", userID) |
| `GenerateReconcileNo()` | `REC_{yyyyMMddHHmmss}_{4位随机}` |
| `GenerateExceptionNo()` | `EXC_{yyyyMMddHHmmss}_{4位随机}` |
| `GeneratePenaltyDeductTraceID(roomID, sessionID)` | `PENALTY_DED_{roomID}_{sessionID}` |
| `GeneratePenaltyDistTraceID(roomID, sessionID)` | `PENALTY_DIST_{roomID}_{sessionID}` |
| `ParseRoundTraceID(roundTraceID)` | 解析回 (sessionID, roundNo, err) |
| `ExtractSessionIDFromRoundTraceID(roundTraceID)` | 提取 sessionID，失败返回0 |

**可解析性**：RoundTraceID 可反向解析出 sessionID 和 roundNo，便于排障与日志关联。

#### Lua 脚本

文件：[service/lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go)

```lua
local newBalance = redis.call('INCRBY', KEYS[1], -ARGV[1])
if newBalance < 0 then
    redis.call('INCRBY', KEYS[1], ARGV[1])
    return 0
end
redis.call('SADD', KEYS[2], ARGV[2])
return 1
```

| 参数 | 含义 |
|------|------|
| `KEYS[1]` | 机器人虚拟余额 key |
| `KEYS[2]` | 脏数据集合 key |
| `ARGV[1]` | 扣减金额（正数） |
| `ARGV[2]` | userID 字符串 |
| 返回值 | `1`=成功，`0`=余额不足（已回滚） |

**原子操作消除竞态**：利用 Redis 单线程特性，将"扣减→检查→回滚→标记 dirty"四步合并为原子操作。原 Go 代码三步之间的竞态会导致"两线程并发扣减可凭空创造资金"。

#### Repository 层

**ExceptionManager**（[service/exception_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/exception_manager.go)）：

| 方法 | 功能 |
|------|------|
| `Create(ctx, exception)` | 创建异常记录 |
| `GetByID(ctx, id)` | 按 ID 查询 |
| `GetPendingExceptions(ctx, limit)` | 查询待处理异常（按 created_at 升序） |
| `UpdateStatus(ctx, id, status, handleType, remark, handledBy)` | 更新异常状态+处理信息+handled_at |

**PlatformCallManager**（[service/platform_call_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/platform_call_manager.go)）：

```go
type CallLogCreateParams struct {
    CallType   string
    BizOrderNo string
    ReqBody    interface{}
}

type CallLogUpdateParams struct {
    ID           int64
    RespBody     interface{}
    Status       int
    ErrorMessage string
}
```

| 方法 | 功能 |
|------|------|
| `CreateLog(ctx, params)` | 创建调用日志：序列化 ReqBody→创建 PlatformCallLog(status=Pending) |
| `UpdateLog(ctx, params)` | 更新日志：设置 response_time/status/error_message，retry_count+1（gorm.Expr），序列化 RespBody |
| `GetLogByID` / `GetFailedLogs` | 查询方法 |

---

## 8. 数据模型（数据库表）

结算模块涉及 5 张 MySQL 表，均使用 GORM 定义。

### 8.1 bill_record（账单记录表）

文件：[model/bill.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/bill.go)

记录每一笔扣款/入账操作。

| 字段 | 类型 | GORM tag | 说明 |
|------|------|----------|------|
| ID | int64 | primaryKey;autoIncrement | 主键 |
| RoundTraceID | string | index;size:64 | 回合追踪ID |
| BizOrderNo | string | uniqueIndex;size:64 | 业务订单号（唯一） |
| PlatformTransID | string | index;size:64 | 平台交易ID |
| BillType | int | index;not null | 账单类型 |
| DeductScene | int | default:0 | 扣款场景 |
| RoomID | int64 | index;not null | 房间ID |
| SessionID | int64 | index | 会话ID |
| RoundID | int64 | index | 回合ID |
| RoundNo | int | default:0 | 回合序号 |
| UserID | int64 | index;not null | 用户ID（0=系统） |
| BatchID | string | index;size:32 | 批次ID |
| Amount | int64 | not null | 金额（负扣正入） |
| BalanceBefore | int64 | not null;default:0 | 操作前余额 |
| BalanceAfter | int64 | not null;default:0 | 操作后余额 |
| Status | int | default:0;index | 账单状态 |
| ReconcileStatus | int | default:0;index | 对账状态 |
| RefundStatus | int | default:0;index | 退款状态 |
| RefundOrderNo | string | size:64 | 退款订单号 |
| RefundAmount | int64 | default:0 | 退款金额 |
| RefundReason | string | size:256 | 退款原因 |
| RefundAppliedAt | *time.Time | - | 退款申请时间 |
| RefundApprovedAt | *time.Time | - | 退款审批时间 |
| RefundApprovedBy | int64 | default:0 | 退款审批人 |
| RetryCount | int | default:0 | 重试次数 |
| NextRetryAt | *time.Time | - | 下次重试时间 |
| ErrorCode | string | size:32 | 错误码 |
| ErrorMessage | string | size:512 | 错误信息 |
| ExceptionID | int64 | index | 关联异常记录ID |
| GameSettleStatus | int | default:0;index | 游戏级结算标记 |
| GameSettledAt | *time.Time | index | 游戏结算时间 |
| Remark | string | size:256 | 备注 |
| IsRobot | bool | default:false;index | 是否机器人 |
| CreatedAt | time.Time | autoCreateTime | 创建时间 |
| UpdatedAt | time.Time | autoUpdateTime | 更新时间 |

**索引亮点**：`BizOrderNo` 唯一索引；`RoundTraceID`、`BillType`、`RoomID`、`SessionID`、`RoundID`、`UserID`、`BatchID`、`Status`、`ReconcileStatus`、`RefundStatus`、`ExceptionID`、`GameSettleStatus`、`GameSettledAt`、`IsRobot` 均有普通索引，便于多维度查询与重试扫描。

### 8.2 round_settlement（回合结算汇总表）

文件：[model/bill.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/bill.go)

回合级结算汇总记录。

| 字段 | 类型 | GORM tag | 说明 |
|------|------|----------|------|
| ID | int64 | primaryKey;autoIncrement | 主键 |
| RoundTraceID | string | uniqueIndex;size:64 | 回合追踪ID（唯一） |
| RoomID | int64 | index;not null | 房间ID |
| SessionID | int64 | index;not null | 会话ID |
| RoundID | int64 | uniqueIndex;not null | 回合ID（唯一） |
| RoundNo | int | not null | 回合序号 |
| DeductScene | int | not null | 扣款场景 |
| DeductAmount | int64 | default:0 | 扣款总额 |
| DeductUserCount | int | default:0 | 扣款用户数 |
| DeductSuccessCount | int | default:0 | 扣款成功数 |
| DeductedAt | *time.Time | - | 扣款完成时间 |
| SettleAmount | int64 | default:0 | 结算总额 |
| SettleUserCount | int | default:0 | 结算用户数 |
| SettleSuccessCount | int | default:0 | 结算成功数 |
| SettledAt | *time.Time | - | 结算完成时间 |
| SenderID | int64 | not null | 发送者ID |
| SenderType | string | size:20;not null | 发送者类型 |
| TotalAmount | int64 | not null | 总金额 |
| Commission | int64 | not null | 佣金 |
| PlayerCount | int | not null | 玩家数 |
| MinPlayerID | int64 | not null | 最小玩家ID |
| RewardType | int | default:0 | 奖励类型 |
| RewardAmount | int64 | default:0 | 奖励金额 |
| Status | int | default:0;index | 回合状态 |
| ReconcileStatus | int | default:0;index | 对账状态 |
| RefundStatus | int | default:0;index | 退款状态 |
| RefundReason | string | size:256 | 退款原因 |
| ErrorMessage | string | size:512 | 错误信息 |
| GameSettleStatus | int | default:0;index | 游戏级结算状态 |
| GameSettledAt | *time.Time | index | 游戏结算时间 |
| CreatedAt | time.Time | autoCreateTime | 创建时间 |
| UpdatedAt | time.Time | autoUpdateTime | 更新时间 |

`RoundTraceID` 与 `RoundID` 均唯一，保证幂等。

### 8.3 exception_record（异常记录表）

文件：[model/exception_record.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/exception_record.go)

| 字段 | 类型 | GORM tag |
|------|------|----------|
| ID | int64 | primaryKey;autoIncrement |
| ExceptionNo | string | uniqueIndex;size:32;not null |
| ExceptionType | ExceptionType | not null;index |
| BillID | int64 | index |
| RoundTraceID | string | index;size:64 |
| RoundID | int64 | index |
| BillType | int | index |
| UserID | int64 | index |
| Amount | int64 | - |
| Status | ExceptionStatus | default:0;index |
| ExceptionDetail | string | type:text |
| HandleType | HandleType | default:0 |
| HandleRemark | string | size:512 |
| HandledAt | *time.Time | - |
| HandledBy | int64 | default:0 |
| CreatedAt | time.Time | autoCreateTime |
| UpdatedAt | time.Time | autoUpdateTime |

**类型与状态常量**：

```go
type ExceptionType int
const (
    ExceptionTypeDebitFailed         = 1  // 扣款失败
    ExceptionTypeCreditRetryExceed   = 2  // 入账重试超限
    ExceptionTypeDeductedNotSettled   = 3  // 已扣未结算
)

type ExceptionStatus int
const (
    ExceptionStatusPending    = 0
    ExceptionStatusProcessing = 1
    ExceptionStatusResolved   = 2
    ExceptionStatusIgnored    = 3
)

type HandleType int
const (
    HandleTypeManual = 1
    HandleTypeRefund = 2
    HandleTypeRetry  = 3
    HandleTypeIgnore = 4
)
```

### 8.4 platform_call_log（平台调用日志表）

文件：[model/platform_settle_log.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/platform_settle_log.go)

| 字段 | 类型 | GORM tag | 说明 |
|------|------|----------|------|
| ID | int64 | primaryKey;autoIncrement | 主键 |
| CallType | string | size:20;index;not null | 调用类型（debit/credit/settle） |
| BizOrderNo | string | index;size:64;not null | 业务订单号 |
| RequestBody | string | type:text | 请求体 |
| ResponseBody | string | type:text | 响应体 |
| Status | int | default:0;index | 状态（0待处理/1成功/2失败） |
| ErrorMessage | string | size:1024 | 错误信息 |
| RetryCount | int | default:0 | 重试次数 |
| RequestTime | time.Time | not null | 请求时间 |
| ResponseTime | *time.Time | - | 响应时间 |
| CreatedAt | time.Time | autoCreateTime | - |
| UpdatedAt | time.Time | autoUpdateTime | - |

**调用类型常量**：
- `CallTypeDebit = "debit"`（扣款）
- `CallTypeCredit = "credit"`（入账）
- `CallTypeSettle = "settle"`（结算）

### 8.5 refund_audit（退款审核表）

文件：[model/refund.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/refund.go)

| 字段 | 类型 | GORM tag | 说明 |
|------|------|----------|------|
| ID | int64 | primaryKey;autoIncrement | 主键 |
| RefundOrderNo | string | uniqueIndex;size:64;not null | 退款订单号（唯一） |
| RoundTraceID | string | index;size:64 | 回合追踪ID |
| BatchID | string | index;size:32 | 批次ID |
| RoomID | int64 | not null | 房间ID |
| SessionID | int64 | not null | 会话ID |
| RoundID | int64 | default:0 | 回合ID |
| UserID | int64 | not null | 用户ID |
| BillID | int64 | not null;index | 关联账单ID |
| BillOrderNo | string | size:64;not null | 账单订单号 |
| RefundAmount | int64 | not null | 退款金额 |
| RefundReason | string | size:512;not null | 退款原因 |
| RefundType | int | not null | 退款类型 |
| Status | int | default:0;index | 退款状态 |
| AppliedAt | time.Time | not null | 申请时间 |
| AppliedBy | int64 | default:0 | 申请人 |
| ApprovedAt | *time.Time | - | 审批时间 |
| ApprovedBy | int64 | default:0 | 审批人 |
| ApproveRemark | string | size:256 | 审批备注 |
| RefundedAt | *time.Time | - | 退款完成时间 |
| PlatformTransID | string | size:64 | 平台交易ID |
| ErrorMessage | string | size:512 | 错误信息 |
| CreatedAt | time.Time | autoCreateTime | - |
| UpdatedAt | time.Time | autoUpdateTime | - |

### 8.6 表关系 ER 图

```
┌─────────────────┐         ┌──────────────────────┐
│  round_settlement│         │    bill_record       │
│─────────────────│  1    N │──────────────────────│
│ id (PK)         │◄────────│ round_id (idx)       │
│ round_id (uniq) │         │ round_trace_id (idx) │
│ round_trace_id  │◄────────│ biz_order_no (uniq)  │
│   (uniq)        │         │ user_id (idx)        │
│ session_id      │         │ batch_id (idx)       │
│ status          │         │ status (idx)         │
│ game_settle_    │         │ refund_status (idx)  │
│   status        │         │ exception_id (idx)───┼──┐
│ refund_status   │         │ game_settle_status   │  │
└─────────────────┘         │ is_robot (idx)       │  │
                            └──────────────────────┘  │
                                                      │
┌─────────────────┐                                   │
│ exception_record│                                   │
│─────────────────│◄──────────────────────────────────┘
│ id (PK)         │
│ exception_no    │
│   (uniq)        │
│ bill_id (idx)   │
│ exception_type  │
│   (idx)         │
│ status (idx)    │
└─────────────────┘

┌─────────────────┐         ┌──────────────────────┐
│  refund_audit   │         │ platform_call_log    │
│─────────────────│         │──────────────────────│
│ id (PK)         │         │ id (PK)              │
│ refund_order_no │         │ call_type (idx)      │
│   (uniq)        │         │ biz_order_no (idx)   │
│ bill_id (idx)   │         │ status (idx)         │
│ bill_order_no   │         │ request_time         │
│ status (idx)    │         │ response_time        │
│ refund_type     │         └──────────────────────┘
│ platform_trans_ │
│   id            │
└─────────────────┘
```

---

## 9. Redis 键规范

文件：[infrastructure/persistence/redis/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/infrastructure/persistence/redis/keys.go)

### 9.1 命名规范

- 统一前缀：`cashparty`
- 锁格式：`cashparty:settle:lock:<场景>:<参数>`
- 机器人：`cashparty:robot:<子项>:<参数>`
- 冒号分层，使用 `%d`/`%s` 占位符，参数顺序按业务粒度从粗到细

### 9.2 分布式锁 Key

| 常量 | 格式 | 用途 |
|------|------|------|
| `KeySettleSendPacketLock` | `cashparty:settle:lock:send_packet:%d` (roundID) | 发红包锁 |
| `KeySettleRoundLock` | `cashparty:settle:lock:round:%d` (roundID) | 回合结算锁 |
| `KeySettlePenaltyLock` | `cashparty:settle:lock:penalty:%d` (roundID) | 惩罚锁 |
| `KeyFirstRoundDeductLock` | `cashparty:settle:lock:first_round:%d` (sessionID) | 首回合扣款锁 |
| `KeyLaterRoundDeductLock` | `cashparty:settle:lock:later_round:%d` (roundID) | 后续回合扣款锁 |
| `KeySystemPacketDeductLock` | `cashparty:settle:lock:system_packet:%d` (roundID) | 系统红包扣款锁 |
| `KeyRefundLock` | `cashparty:settle:lock:refund:%s` (refundOrderNo) | 退款执行锁 |
| `KeyRefundApplyLock` | `cashparty:settle:lock:refund_apply:%d` (billID) | 退款申请锁 |
| `KeyDeductLock` | `cashparty:settle:lock:deduct:%d_%d_%d` (roundID,billType,userID) | 单笔扣款锁 |
| `KeyBillRetryLock` | `cashparty:settle:lock:bill_retry:%d` (billID) | 账单重试锁 |
| `KeyPairBillCheckLock` | `cashparty:settle:lock:pair_bill_check:%s` (roundTraceID) | 成对账单检查锁 |
| `KeyGameSettleLock` | `cashparty:settle:lock:game:%d` (sessionID) | 游戏结算锁 |
| `KeyGameSettleRetryLock` | `cashparty:settle:lock:game_retry:%d_%d` (sessionID,userID) | 游戏结算重试锁 |

每个常量都配对一个 `XxxKey(...)` 格式化函数（如 `SendPacketLockKey`、`SettleRoundLockKey` 等），通过 `fmt.Sprintf` 填充参数。

### 9.3 机器人相关 Key

| 常量 | 格式 | 用途 |
|------|------|------|
| `KeyRobotVirtualBalance` | `cashparty:robot:virtual_balance:%d` (userID) | 机器人虚拟余额 |
| `KeyRobotVirtualBalanceDirty` | `cashparty:robot:virtual_balance:dirty` | 虚拟余额脏数据集合 |
| `KeyRobotUserIDs` | `cashparty:robot:user_ids` | 机器人用户ID集合 |

> **说明**：settlement 层使用的机器人 key 与 game 层 `keys.go` 保持一致的字符串值，确保两个独立实现的 `VirtualBalanceService` 共享同一份数据。

---

## 10. 调度器机制

### 10.1 BaseScheduler 基类

文件：[scheduler/base.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/base.go)

```go
type TaskFunc func(ctx context.Context) error

type SchedulerConfig struct {
    Name         string        // 调度器名称（日志用）
    Interval     time.Duration // 执行间隔
    InitialDelay time.Duration // 启动初始延迟
    LockKey      string        // 分布式锁 Key
    LockTTL      int           // 锁 TTL（秒）
}

type BaseScheduler struct {
    config  SchedulerConfig
    task    TaskFunc
    redis   *cRedis.Client
    stopCh  chan struct{}
    mu      sync.Mutex
}
```

**运行机制**：
- `Start()`：加锁后启动 goroutine 运行 `run()`
- `run()`：先 `InitialDelay` 休眠；用 `time.NewTicker(Interval)` 创建定时器；`for-select` 在 `ticker.C` 上调用 `executeTask()`，在 `stopCh` 上退出
- `executeTask()`：通过 `lock.WithRedisLock(ctx, redis, LockKey, LockTTL, func)` 获取分布式锁后执行 `task(ctx)`。**保证多实例下只有一个实例真正执行任务**
- `Stop()`：加锁后 `close(stopCh)` 通知退出

**设计特点**：
- 所有具体调度器复用 BaseScheduler，只提供自己的 `task` 回调
- 分布式锁防止多节点重复执行
- `InitialDelay` 错峰启动，避免所有调度器同时打数据库

### 10.2 调度器总览

| 调度器 | 间隔 | 初始延迟 | LockTTL | 锁 Key | 职责 |
|--------|------|----------|---------|--------|------|
| [CreditRetryScheduler](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/credit_retry_scheduler.go) | 30s | 10s | 60s | `scheduler:credit_retry:lock` | 入账失败重试（指数退避，最多3次） |
| [GameSettleRetryScheduler](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/game_settle_retry_scheduler.go) | 30s | 15s | 60s | `scheduler:game_settle_retry:lock` | 游戏级结算失败重试（逐玩家） |
| [GameSettleTimeoutScheduler](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/game_settle_timeout_scheduler.go) | 5min | 1min | 300s | `scheduler:game_settle_timeout:lock` | 超1小时未结算强制结算 |
| [RefundProcessScheduler](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/refund_process_scheduler.go) | 1min | 30s | 120s | `scheduler:refund_process:lock` | 首回合失败退款自动审批 |
| [SettlementCheckScheduler](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/settlement_check_scheduler.go) | 5min | 1min | 300s | `scheduler:settlement_check:lock` | 结算完整性检查（扣款未结算、首回合失败） |

**设计说明**：
- 调度器命名规范：`scheduler:<name>:lock`
- `InitialDelay` 故意错开（10s/15s/30s/1min），避免多个调度器同时启动打 DB
- `LockTTL` 普遍大于 `Interval`，保证单次任务执行期间锁不被抢占

### 10.3 各调度器执行逻辑

#### CreditRetryScheduler

```
execute(ctx):
  1. creditRetry.GetRetryableCredits(ctx, 100)  // 最多100条
  2. for each bill:
       if bill.NextRetryAt != nil && > now: skip  (退避等待)
       else: creditRetry.RetryCredit(ctx, bill.ID)
         失败仅记日志, 不中断循环
```

#### GameSettleRetryScheduler

```
execute(ctx):
  1. billMgr.GetFailedGameSettlements(ctx, 100)  // 游戏级结算失败的 sessionID
  2. for each sessionID:
       users := billMgr.GetUnsettledUsersBySession(ctx, sessionID)
       for each userID:
         gameSettleSvc.RetryPlayerSettle(ctx, sessionID, userID)  // 带独立锁
       if allSuccess && len(users) > 0:
         billMgr.UpdateGameSettleStatusBySession(ctx, sessionID, Success)
```

#### GameSettleTimeoutScheduler

```
execute(ctx):
  1. billMgr.GetTimedOutGameSettlements(ctx, 1*time.Hour, 100)
     // 所有回合已入账但游戏级结算超过1小时未完成的 sessionID
  2. for each sessionID:
       gameSettleSvc.SettleGame(ctx, sessionID)  // 强制结算
```

#### RefundProcessScheduler

```
execute(ctx):
  1. billMgr.GetRefundsByStatus(ctx, RefundStatusPending, 100, 0)
  2. for each refund:
       if refund.RefundType == RefundTypeFirstRoundFail:
         refundSvc.ApproveRefund(ctx, &RefundApproveRequest{
           RefundOrderNo, ApprovedBy: 0  // 0=系统
         })
       // 其他类型(RefundTypeOther)不自动处理, 需人工审批
```

#### SettlementCheckScheduler

```
execute(ctx):
  1. settlementCheck.CheckFirstRoundDeductFailure(ctx)
  2. settlementCheck.CheckDeductedButNotSettled(ctx, now.Add(-5*time.Minute))
```

---

## 11. 配置与常量

### 11.1 配置层

文件：[config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/config/config.go)

```go
type PlatformConfig = config.PlatformConfig  // 类型别名, 复用 common 层
```

| 函数 | 功能 |
|------|------|
| `DefaultPlatformConfig()` | 返回默认配置：Provider="mock"、GameCode="redpacket"、GameName="redpacket"、Currency="MXN"（墨西哥比索） |
| `FromCommonConfig(cfg)` | 从 common 层配置转换为本模块配置；nil 时返回默认配置 |

> 配置层极薄，仅做透传/兜底，真正的平台配置结构定义在 `common/config` 包。

### 11.2 常量定义

文件：[dto/constants.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/constants.go)

#### 账单类型 BillType

| 常量 | 值 | 含义 |
|------|----|------|
| `BillTypeFirstRoundDeduct` | 2 | 首回合扣款 |
| `BillTypeGrabPacket` | 3 | 抢红包（入账） |
| `BillTypeLaterRoundDeduct` | 4 | 后续回合扣款 |
| `BillTypeCommission` | 7 | 佣金（系统记录） |
| `BillTypePenaltyIncome` | 8 | 惩罚扣款 |
| `BillTypeSystemPacket` | 9 | 系统红包 |
| `BillTypePenaltyDistribute` | 10 | 惩罚分配 |
| `BillTypeSystemReward` | 11 | 系统奖励 |
| `BillTypeSessionCredit` | 12 | 会话入账 |

> 注：BillType=1（发红包）在文档中存在，constants.go 中未定义（直接使用数字）。

#### 账单状态 BillStatus

| 常量 | 值 | 含义 |
|------|----|------|
| `BillStatusProcessing` | 0 | 处理中 |
| `BillStatusSuccess` | 1 | 成功 |
| `BillStatusFailed` | 2 | 失败 |
| `BillStatusRefunded` | 3 | 已退款 |

#### 回合结算状态 RoundStatus

| 常量 | 值 | 含义 |
|------|----|------|
| `RoundStatusDeducting` | 0 | 扣款中 |
| `RoundStatusDeducted` | 1 | 已扣款 |
| `RoundStatusSettling` | 2 | 结算中 |
| `RoundStatusSuccess` | 3 | 成功 |
| `RoundStatusPartial` | 4 | 部分成功 |
| `RoundStatusFailed` | 5 | 失败 |
| `RoundStatusCredited` | 6 | 已入账 |

#### 退款状态 RefundStatus

| 常量 | 值 | 含义 |
|------|----|------|
| `RefundStatusNone` | 0 | 无 |
| `RefundStatusPending` | 1 | 待处理 |
| `RefundStatusApproved` | 2 | 已批准 |
| `RefundStatusRefunded` | 3 | 已退款 |
| `RefundStatusRejected` | 4 | 已拒绝 |

#### 退款类型 RefundType

| 常量 | 值 | 含义 |
|------|----|------|
| `RefundTypeFirstRoundFail` | 1 | 首回合失败（自动审批） |
| `RefundTypeOther` | 2 | 其他（人工审批） |

#### 扣款场景 DeductScene

| 常量 | 值 | 含义 |
|------|----|------|
| `DeductSceneFirstRoundShare` | 1 | 首回合分摊 |
| `DeductSceneLaterRoundMin` | 2 | 后续回合最小玩家扣 |
| `DeductSceneSystemPacket` | 3 | 系统红包扣 |

#### 游戏级结算状态 GameSettleStatus

| 常量 | 值 | 含义 |
|------|----|------|
| `GameSettleStatusNone` | 0 | 无 |
| `GameSettleStatusSettling` | 1 | 结算中 |
| `GameSettleStatusSuccess` | 2 | 成功 |
| `GameSettleStatusFailed` | 3 | 失败 |

#### 系统与重试常量

| 常量 | 类型 | 值 | 说明 |
|------|------|----|------|
| `PlatformAccountID` | int64 | 0 | 系统账号 |
| `MaxRetryCount` | int | 3 | 最大重试次数 |
| `PenaltyRoundID` | int64 | 0 | 惩罚回合ID |
| `CreditRetryBaseDelay` | time.Duration | 5s | 重试基础延迟 |

#### 对账状态/类型/范围

| 常量 | 值 |
|------|----|
| `ReconcileStatusPending` / `Success` / `Abnormal` | 0 / 1 / 2 |
| `ReconcileTypeScheduled` / `Abnormal` / `Manual` | 1 / 2 / 3 |
| `ReconcileScopeSession` / `Round` / `Bill` | 1 / 2 / 3 |

---

## 12. 依赖关系

### 12.1 服务层依赖图

```
SettlementService（门面）
├── BillManager
├── TraceIDGenerator
├── DeductService
│   ├── CreditRetryService
│   │   ├── BillManager
│   │   ├── ExceptionManager
│   │   ├── PlatformCallManager
│   │   └── UserIDConvertService
│   ├── UserIDConvertService
│   ├── PlatformCallManager
│   └── VirtualBalanceService
├── RewardSettler
│   ├── BillManager
│   ├── TraceIDGenerator
│   └── RobotChecker
├── GameSettleService
│   ├── BillManager
│   ├── PlatformCallManager
│   ├── UserIDConvertService
│   ├── VirtualBalanceService
│   └── RobotChecker
├── UserIDConvertService → UserService(interface)
├── PlatformCallManager
├── RobotChecker(interface) → redisRobotChecker
└── VirtualBalanceService → luaDeductBalance

RefundService（独立链路）
├── BillManager
├── PlatformCallManager
├── UserIDConvertService
└── TraceIDGenerator

BalanceService（独立链路）
├── UserIDConvertService
├── VirtualBalanceService
└── RobotChecker

SettlementCheckService（补偿链路）
├── BillManager
├── ExceptionManager
├── RefundService
└── TraceIDGenerator
```

### 12.2 调度器 → service 依赖映射

| 调度器 | 依赖的 service |
|--------|----------------|
| CreditRetryScheduler | CreditRetryService |
| GameSettleRetryScheduler | BillManager, GameSettleService |
| GameSettleTimeoutScheduler | BillManager, GameSettleService |
| RefundProcessScheduler | RefundService, BillManager |
| SettlementCheckScheduler | SettlementCheckService |

所有调度器均通过 `NewBaseScheduler` 复用 [base.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/base.go) 的运行框架，差异仅在 config 与 task 回调。

### 12.3 外部依赖

| 依赖包 | 用途 |
|--------|------|
| `api/platform` | 平台 API 客户端（Debit/Credit/GetBalance/Settle） |
| `common/lock` | `WithRedisLock` 分布式锁工具 |
| `common/logger` | 日志 |
| `common/redis` | Redis 客户端封装 |
| `common/idgen` | 雪花 ID 生成器 |
| `common/config` | PlatformConfig 结构定义 |
| `settlement/config` | PlatformConfig 透传 |
| `settlement/dto` | DTO 与常量 |
| `settlement/model` | 数据模型 |
| `settlement/infrastructure/persistence/redis` | Redis key 生成函数 |
| `game/model` | User 模型（通过 UserService 接口） |
| `github.com/redis/go-redis/v9` | goredis Nil 错误判断 |
| `gorm.io/gorm` | ORM |

### 12.4 数据流链路

```
请求 DTO (dto/request.go)
        │
        ▼
service 层 (BillManager / RefundService / ...)
        │
        ▼
写入 model 层
  (bill_record / round_settlement / refund_audit
   / exception_record / platform_call_log)
        │
        ▼  redis keys.go 定义的锁保证并发安全
        │
        ▼
scheduler 层 周期性扫描 model 表
        │
        ▼  调用 service 兜底重试 / 退款 / 异常处理
```

---

## 13. 设计模式

| 设计模式 | 应用位置 | 说明 |
|---------|---------|------|
| **门面模式（Facade）** | SettlementService | 作为统一入口，内部委托给 DeductService/GameSettleService/RewardSettler 等 |
| **Repository 模式** | BillManager、ExceptionManager、PlatformCallManager | 封装所有数据库操作，业务层不直接接触 db |
| **策略模式** | RobotChecker 接口、UserService 接口 | 可替换的机器人识别和用户查询实现 |
| **依赖注入** | 所有 New* 构造函数 | 通过构造函数注入所有依赖，便于测试和替换 |
| **模板方法** | executeSingleDeduct / executeSessionCredit / executeCredit | 相同结构：准备→调用平台→更新状态→记录日志 |
| **指数退避重试** | CreditRetryService.calculateNextRetryTime | BaseDelay × 2^retryCount，封顶 MaxDelay |
| **补偿模式** | SettlementCheckService | 定时扫描异常状态并补偿（自动退款或创建异常） |
| **日志切面/审计** | PlatformCallManager + callLog 模式 | 所有平台调用前后记录日志 |
| **脏标记模式** | VirtualBalanceService.Credit 的 SADD dirty | 标记需要异步同步到 DB 的数据 |
| **双重检查锁定（DCL）** | 所有 WithRedisLock 内的二次状态检查 | 锁外检查+锁内二次检查防并发 |
| **信号量限流** | DeductService.executeBatchDeduct 的 sem channel | 控制最大并发扣款数 |
| **状态机** | RoundSettlement.Status、BillRecord.Status、RefundAudit.Status | 明确的状态流转 |
| **原子操作（Lua）** | luaDeductBalance | 消除多步 Redis 操作的竞态 |

---

## 14. 幂等性与并发安全

### 14.1 幂等性设计

所有关键操作都基于业务唯一键做前置检查 + Redis 分布式锁内二次检查（DCL 模式）。

| 操作 | 幂等键 | 检查方式 |
|------|--------|---------|
| `DeductForFirstRound` | roundID | `ExistsRoundSettlement` + 锁内二次检查 |
| `DeductForSystemPacket` | roundID + billType | `ExistsByRoundAndType` |
| `deductSingleUser` | roundID + billType | `ExistsByRoundAndType` |
| `SettleRound` | roundID | `status==Credited` 早返回 + 锁内检查 |
| `creditRound` | roundID + billType + userID | `GetBillByRoundTypeAndUser` 跳过已成功 |
| `settleCommission` | roundID + Commission + PlatformAccountID | `GetBillByRoundTypeAndUser` |
| `SettleReward` | roundID + SystemReward + PlatformAccountID | `GetBillByRoundTypeAndUser` |
| `creditSessionPayout` | sessionID + SessionCredit + userID | `GetBillsBySessionTypeAndUser` 检查现有 Bill 状态 |
| `SettleGame` | sessionID | 检查所有 round 的 GameSettleStatus==Success |
| `RetryCredit` | billID | 检查 bill.Status==Success |
| `ApplyForRefund` | billID | 检查 bill.RefundStatus + 已有 pending 退款单 |
| `ApproveRefund` / `RejectRefund` | refundOrderNo | 锁内二次检查 status==Pending |

**核心原则**：基于业务唯一键（roundID/sessionID/billID）做前置检查 + Redis 分布式锁内二次检查（DCL 模式），保证 Kafka 重试等场景下的幂等性。

### 14.2 并发安全设计

1. **Redis 分布式锁**（`lock.WithRedisLock`）：所有关键写操作都加锁，TTL 一般30-60秒
   - `SettleRoundLockKey(roundID)`
   - `FirstRoundDeductLockKey(sessionID)`
   - `GameSettleLockKey(sessionID)` / `GameSettleRetryLockKey(sessionID, userID)`
   - `SystemPacketDeductLockKey` / `LaterRoundDeductLockKey`
   - `BillRetryLockKey(billID)`
   - `RefundApplyLockKey(billID)` / `RefundLockKey(refundOrderNo)`

2. **双重检查锁定（DCL）**：锁外检查 + 锁内二次检查，避免并发请求重复执行

3. **信号量限流**：`executeBatchDeduct` 使用 `chan struct{}` 容量20控制并发扣款数

4. **互斥锁保护共享状态**：`executeBatchDeduct` 使用 `sync.Mutex` 保护 `result` 累加

5. **WaitGroup 同步**：`executeBatchDeduct` 使用 `sync.WaitGroup` 等待所有 goroutine 完成

6. **Lua 脚本原子性**：`luaDeductBalance` 利用 Redis 单线程特性保证"扣减→检查→回滚→标记 dirty"原子执行

7. **数据库事务**：所有多表写操作通过 `db.Transaction` 保证 ACID
   - `CreateBillsPairInTransaction`
   - `CreateRoundSettlementAndBills`
   - `UpdateRefundSuccessInTransaction`
   - 等

---

## 15. 错误处理与重试策略

### 15.1 错误处理策略

1. **错误包装**：统一使用 `fmt.Errorf("...failed: %w", err)` 保留错误链
2. **状态回写**：所有失败都会更新 `BillRecord.Status=Failed` + `error_message`
3. **分级处理**：
   - 平台调用失败 → 更新 Bill Failed + 更新 callLog Failed + 设置 `next_retry_at`
   - 重试超限 → 创建 `ExceptionRecord` 供人工处理
   - 扣款失败 → 创建 `RefundAudit` 自动退款
4. **余额解析容错**：platform 返回成功但 ParseAmount 失败时，标记 Bill 为 Success 但 balance=0，避免资金状态不一致（仅记日志）
5. **部分失败隔离**：`executeBatchDeduct` 中单玩家失败不影响其他玩家；`creditSessionPayouts` 中单玩家失败仅记日志不中断
6. **fail-safe 机器人检查**：`robotChecker` 在 redis nil 或出错时返回 false（按真人处理，更安全）

### 15.2 重试策略

#### 入账重试（指数退避）

```
CreditRetryConfig {
    MaxRetryCount:   3
    BaseDelay:       5s
    MaxDelay:        5min
    RetryMultiplier: 2.0
}

退避序列: 5s → 10s → 20s
计算公式: delay = min(BaseDelay × 2^retryCount, MaxDelay)
```

- 重试成功 → `Status=Success`
- 重试失败 → `retry_count++`，设置 `next_retry_at`
- `retry_count >= 3` → 创建异常记录（`ExceptionTypeCreditRetryExceed`），不再重试

#### 扣款策略

- **扣款失败不重试**：扣款失败直接标记异常（`ExceptionTypeDebitFailed`），由人工或退款流程处理，避免重复扣款风险
- 首回合部分失败 → 为成功扣款的玩家自动创建 `RefundAudit`

### 15.3 补偿对账

| 检查项 | 触发条件 | 处理方式 |
|--------|----------|----------|
| 首回合扣款失败 | 10分钟前 RoundSettlement.Status=Failed | 自动申请退款 |
| 已扣款未结算 | Deducted 状态超过5分钟未变 Credited | 创建异常记录（人工处理） |
| 入账重试超限 | retry_count >= 3 | 创建异常记录（人工处理） |
| 游戏结算失败 | GameSettleStatus=Failed | 逐玩家重试 |
| 游戏结算超时 | 所有回合 Credited 但游戏结算超1小时 | 强制结算 |

---

## 16. 状态机

### 16.1 BillRecord 状态机

```
                ┌─────────────┐
                │ Processing  │
                │   (0)       │
                └──────┬──────┘
                       │
            ┌──────────┴──────────┐
            ▼                     ▼
    ┌───────────────┐     ┌───────────────┐
    │   Success     │     │    Failed     │
    │   (1)         │     │    (2)        │
    └───────┬───────┘     └───────────────┘
            │
            │ ApplyForRefund + ApproveRefund
            ▼
    ┌───────────────┐
    │   Refunded    │
    │   (3)         │
    └───────────────┘
```

### 16.2 RoundSettlement 状态机

```
┌────────────┐  扣款完成   ┌────────────┐  SettleRound  ┌────────────┐
│ Deducting  │ ─────────► │ Deducted   │ ────────────► │ Credited   │
│   (0)      │            │   (1)      │               │   (6)      │
└────────────┘            └────────────┘               └────────────┘
                                │
                                │ 部分失败
                                ▼
                          ┌────────────┐
                          │  Failed    │
                          │   (5)      │
                          └────────────┘

中间状态: Settling(2) / Success(3) / Partial(4)
```

### 16.3 RefundAudit 状态机

```
                ┌─────────────┐
                │   Pending   │
                │   (1)       │
                └──────┬──────┘
                       │
            ┌──────────┴──────────┐
            ▼                     ▼
    ┌───────────────┐     ┌───────────────┐
    │   Approved    │     │   Rejected    │
    │   (2)         │     │   (4)         │
    └───────┬───────┘     └───────────────┘
            │ executeRefund
            ▼
    ┌───────────────┐
    │   Refunded    │
    │   (3)         │
    └───────────────┘
```

### 16.4 GameSettleStatus 状态机

```
┌────────┐  SettleGame  ┌──────────┐  全部成功  ┌─────────┐
│  None  │ ───────────► │ Settling │ ────────► │ Success │
│  (0)   │              │   (1)    │           │  (2)    │
└────────┘              └────┬─────┘           └─────────┘
                             │
                             │ 任一失败
                             ▼
                        ┌──────────┐
                        │  Failed  │
                        │   (3)    │
                        └──────────┘
```

---

## 17. 集成与上下游调用

### 17.1 部署与启动

结算模块**没有独立进程**，而是作为 `game-service` 的内部模块同进程部署。

#### 初始化流程

文件：[game/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go)

```
NewApplicationWithConfig(cfg)
  ├─ 创建 platformClient (platform.NewClient)
  ├─ 创建 billMgr (NewBillManager)
  ├─ 创建 traceIDGen (NewTraceIDGenerator)
  ├─ 创建 platformCfg (FromCommonConfig)
  ├─ 创建 userIDConvert (NewUserIDConvertService)
  ├─ 创建 exceptionMgr (NewExceptionManager)
  ├─ 创建 callMgr (NewPlatformCallManager)
  ├─ 创建 creditRetrySvc (NewCreditRetryService)
  ├─ 创建 robotChecker (NewRobotChecker)
  ├─ 创建 settlementVirtualBalance (NewVirtualBalanceService)
  ├─ 创建 deductSvc (NewDeductService)
  ├─ 创建 refundSvc (NewRefundService)
  ├─ 创建 rewardSettler (NewRewardSettler)
  ├─ 创建 gameSettleSvc (NewGameSettleService)
  ├─ 创建 settlementSvc (NewSettlementService)
  └─ NewContainer(...) → container.InitAppServices()
        ├─ 创建 BalanceService
        ├─ 创建 RoomAppService (注入 SettlementSvc, BalanceService)
        ├─ 创建 GameAppService (注入 SettlementSvc, DeductSvc, RefundSvc, rewardSettler)
        ├─ 创建 SeatAppService (注入 SettlementSvc, BalanceService)
        ├─ 创建 PenaltyService (注入 SettlementSvc)
        └─ initSettlementSchedulers()  // 创建5个调度器
```

#### 启动调度器

```
Application.Start(ctx) → Container.StartSchedulers()
  ├─ TimeoutScheduler.Start()
  ├─ CreditRetryScheduler.Start()
  ├─ RefundProcessScheduler.Start()
  ├─ SettlementCheckScheduler.Start()
  ├─ GameSettleRetryScheduler.Start()
  ├─ GameSettleTimeoutScheduler.Start()
  └─ (机器人调度器, 若启用)
```

### 17.2 上游调用方

结算服务被 `game/application` 层的多个服务调用：

| 调用方 | 调用的结算方法 | 触发场景 |
|--------|---------------|----------|
| [GameAppService](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go) | `DeductService.DeductForFirstRound` | 开局前首回合扣款 |
| [GameAppService](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go) | `SettlementService.DistributePenaltyFromPlatform` | 惩罚分配 |
| [PenaltyService](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/penalty_service.go) | `SettlementService.DeductPenaltyToPlatform` | 惩罚扣款 |
| [SeatAppService](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/seat_app_service.go) | `BalanceService.CheckBalanceForReady` | 开局前余额检查 |
| [GameEventConsumer](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) | `SettlementService.SettleRound` | Kafka `RoundSettle` 事件 |
| [GameEventConsumer](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) | `SettlementService.SettleGame` | Kafka `SessionEnd` 事件 |

### 17.3 Kafka 事件驱动

结算模块的 `SettleRound` 和 `SettleGame` 主要通过 Kafka 事件触发：

文件：[game/infrastructure/messaging/game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go)

#### 事件处理流程

```
Kafka TopicGameEvents 消息
        │
        ▼
GameEventConsumer.HandleEvent(ctx, msg)
  ├─ tryAcquire(traceID)  // SetNX 原子抢占, 防重复处理
  │
  ├─ GameEventRoundSettle → handleRoundSettle
  │    ├─ 事务内: 更新 Round 状态 + 创建 grab_record + 更新 session_player
  │    ├─ 组装 RoundSettleRequest
  │    └─ settlementService.SettleRound(ctx, settleReq)
  │         失败 → return err 触发事务回滚
  │         (重试依赖 SettleRound 内部幂等 + grab_record 的 FirstOrCreate)
  │
  └─ GameEventSessionEnd → handleSessionEnd
       ├─ 幂等检查: session 是否已 Completed
       ├─ 事务内: 更新 session 状态 + 更新 session_player.total_profit
       └─ settlementService.SettleGame(ctx, sessionID)
            失败 → return err 触发事务回滚
            (重试依赖 session 幂等检查 + SettleGame 内部幂等)
```

#### 抢占与释放

- `tryAcquire`：用 `SetNX` 原子抢占事件处理权（key=`cashparty:game_event_processed:{traceID}`，TTL=7天）
- `releaseAcquire`：处理失败时 `Del` 释放，让 Kafka 重试能重新进入
- **fail-open 策略**：Redis 不可用时返回 true（让业务侧幂等兜底）

---

## 18. 接口与 DTO 参考

### 18.1 请求 DTO

文件：[dto/request.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/request.go)

| 结构体 | 用途 | 关键字段 |
|--------|------|---------|
| `SendPacketRequest` | 发红包请求 | RoomID, SessionID, RoundID, UserID, SenderType, RoomFee, Commission |
| `RoundSettleRequest` | **回合结算请求（核心入口）** | RoomID, SessionID, RoundID, RoundNo, SenderID, SenderType, TotalAmount, Commission, RoomFeePerPlayer, MinPlayerID, Players, RewardType, RewardAmount |
| `PlayerSettleInfo` | 玩家结算信息 | UserID, Amount, IsMin |
| `PenaltyDeductRequest` | 惩罚扣款请求 | RoomID, SessionID, RoundNo, UserID, Amount, PenaltyType |
| `PenaltyDistributeRequest` | 惩罚分配请求 | RoomID, SessionID, RoundID, Amount, Recipients, Reason |
| `FirstRoundDeductRequest` | 首回合扣款请求 | RoomID, SessionID, RoundID, RoundNo, RoomFeePerPlayer, Players |
| `PlayerDeductInfo` | 玩家扣款信息 | UserID, Nickname |
| `LaterRoundDeductRequest` | 后续回合扣款请求 | RoomID, SessionID, RoundID, RoundNo, RoomFee, MinPlayerID, RoundTraceID |
| `SystemPacketDeductRequest` | 系统红包扣款请求 | RoomID, SessionID, RoundID, RoundNo, TotalAmount, RoundTraceID, Reason |
| `SingleDeductRequest` | 单笔扣款请求 | RoomID, SessionID, RoundID, RoundNo, UserID, Amount, BillType, DeductScene, RoundTraceID, Remark |
| `RefundApplyRequest` | 退款申请 | BillID, RefundAmount, RefundReason, RefundType, AppliedBy |
| `RefundApproveRequest` | 退款审批 | RefundOrderNo, ApprovedBy, Remark |
| `RefundRejectRequest` | 退款拒绝 | RefundOrderNo, RejectedBy, Remark |
| `BalanceCheckRequest` | 余额检查 | UserID, RoomFee, MaxPlayers, MaxRounds |
| `GameSettleRequest` | 游戏结算请求 | RoomID, SessionID |

### 18.2 响应 DTO

文件：[dto/response.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/response.go)

所有响应结构体均带 `json` tag。

| 结构体 | 用途 | 关键字段 |
|--------|------|---------|
| `FirstRoundDeductResult` | 首回合扣款结果 | BatchID, AllSuccess, SuccessCount, FailedCount, SuccessPlayers, FailedPlayers |
| `FailedPlayerInfo` | 失败玩家信息 | UserID, ErrorCode, ErrorMsg |
| `RefundResult` | 退款结果 | RefundOrderNo, Status, RefundedAt, ErrorMsg |
| `BillQueryResult` | 账单查询结果 | Total, List |
| `BillInfo` | 账单详情 | ID, RoundTraceID, BizOrderNo, BillType, DeductScene, RoomID, SessionID, RoundID, RoundNo, UserID, Amount, BalanceBefore, BalanceAfter, Status, RefundStatus, RefundAmount, RefundOrderNo, ErrorMessage, Remark, CreatedAt, UpdatedAt |
| `RoundSettlementInfo` | 回合结算详情 | ID, RoundTraceID, RoomID, SessionID, RoundID, RoundNo, DeductScene, DeductAmount, DeductUserCount, DeductSuccessCount, DeductedAt, SettleAmount, SettleUserCount, SettleSuccessCount, SettledAt, Status, RefundStatus, RefundReason, ErrorMessage, CreatedAt, UpdatedAt |
| `RefundAuditInfo` | 退款审核详情 | ID, RefundOrderNo, RoundTraceID, BatchID, RoomID, SessionID, RoundID, UserID, BillID, BillOrderNo, RefundAmount, RefundReason, RefundType, Status, AppliedAt, AppliedBy, ApprovedAt, ApprovedBy, ApproveRemark, RefundedAt, PlatformTransID, ErrorMessage, CreatedAt, UpdatedAt |
| `BalanceCheckResult` | 单用户余额检查结果 | UserID, Balance, RequiredFee, IsSufficient |
| `BatchBalanceCheckResult` | 批量余额检查结果 | AllSufficient, Results |
| `GameSettleInfo` | 游戏结算总览 | SessionID, RoomID, TotalRounds, SettleStatus, SettledAt, Players |
| `GamePlayerSettleInfo` | 玩家游戏结算明细 | BetAmount, PayOut, NetAmount, GameResult, Status |

---

## 附录：关键文件索引

| 文件 | 路径 |
|------|------|
| 门面服务 | [service/settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go) |
| 会话级结算 | [service/game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) |
| 扣款服务 | [service/deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) |
| 账单 Repository | [service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) |
| 奖励结算 | [service/reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go) |
| 余额服务 | [service/balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/balance_service.go) |
| 入账重试 | [service/credit_retry_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go) |
| 退款服务 | [service/refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) |
| 异常管理 | [service/exception_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/exception_manager.go) |
| 平台调用日志 | [service/platform_call_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/platform_call_manager.go) |
| 机器人检查 | [service/robot_checker.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/robot_checker.go) |
| 用户ID转换 | [service/user_id_convert_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/user_id_convert_service.go) |
| 虚拟余额 | [service/virtual_balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/virtual_balance_service.go) |
| ID生成器 | [service/trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) |
| Lua脚本 | [service/lua_scripts.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/lua_scripts.go) |
| 补偿对账 | [service/settlement_check_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_check_service.go) |
| 调度器基类 | [scheduler/base.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/base.go) |
| 配置 | [config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/config/config.go) |
| 常量 | [dto/constants.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/constants.go) |
| 请求DTO | [dto/request.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/request.go) |
| 响应DTO | [dto/response.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/response.go) |
| 账单模型 | [model/bill.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/bill.go) |
| 异常记录模型 | [model/exception_record.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/exception_record.go) |
| 平台日志模型 | [model/platform_settle_log.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/platform_settle_log.go) |
| 退款模型 | [model/refund.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/model/refund.go) |
| Redis键 | [infrastructure/persistence/redis/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/infrastructure/persistence/redis/keys.go) |
| 集成入口 | [game/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) |
| 集成容器 | [game/bootstrap/container.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/container.go) |
| 事件消费者 | [game/infrastructure/messaging/game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go) |
| 原始解决方案 | [SETTLEMENT_SOLUTION.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/SETTLEMENT_SOLUTION.md) |

---

> 本文档基于结算模块源代码分析生成，覆盖模块定位、整体架构、设计思想、核心流程图、模块职责、关键类与函数、数据模型、Redis 键规范、调度器机制、配置常量、依赖关系、设计模式、幂等与并发、错误处理、状态机、集成调用、DTO 参考等关键信息。所有文件引用均为可点击链接，便于直接跳转到源码位置进行核对。
