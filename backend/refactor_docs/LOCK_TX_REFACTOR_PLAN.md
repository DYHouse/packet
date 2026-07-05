# 分布式锁与事务重构方案（LOCK_TX_REFACTOR_PLAN.md）

> 基于 `backend/common/lock`、`settlement/service`、`game/application`、`game/infrastructure/messaging`、`common/scheduler` 等模块的全面代码 review 编制。覆盖 26 个分布式锁调用点、12 个事务调用点、15 个幂等检查点、3 个已实现乐观锁、9 个缺失乐观锁、2 个 Kafka 消费者幂等锁、2 个 Robot 独立锁实现。

---

## 目录

1. [当前实现盘点](#1-当前实现盘点)
2. [问题清单](#2-问题清单)
3. [整体设计思路](#3-整体设计思路)
4. [文件结构](#4-文件结构)
5. [规约（DL-1~DL-12、TX-1~TX-12）](#5-规约)
6. [分阶段实施计划](#6-分阶段实施计划)
7. [关键修复细节](#7-关键修复细节)
8. [决策记录](#8-决策记录)
9. [风险与缓解](#9-风险与缓解)
10. [验证清单](#10-验证清单)

---

## 1. 当前实现盘点

### 1.1 分布式锁框架（common/lock/distributed_lock.go）

**能力清单**：

| 能力 | 状态 |
|---|---|
| 加锁（SetNX，基于 redsync） | ✅ |
| 解锁（GET+DEL Lua，redsync 内部） | ⚠️ 间接 |
| TTL 设置 | ✅（默认 30s） |
| Watchdog 自动续期 | ✅（间隔 TTL/3） |
| 重试（3 次 × 100ms） | ✅ |
| 可重入 | ❌ |
| 公平锁 | ❌ |
| 嵌套锁 | ❌（同 key 嵌套会死锁） |
| RedLock（多节点 Quorum） | ❌ |
| panic recover（fn 内） | ❌ |
| ctx 监听（watchdog） | ❌ |
| 加锁失败原因区分 | ❌ |

**高阶函数**：
- `WithLock(ctx, key, opts, fn)`：基础包装
- `WithRedisLock(ctx, client, key, expirySeconds, fn)`：自动初始化 redsync + 固定参数

### 1.2 分布式锁调用点（26 处）

#### 业务服务层（14 处，全部走 `WithRedisLock`）

| # | 调用点 | Key 模式 | TTL | 锁外预检 | 锁内双检 | 规约 |
|---|---|---|---|---|---|---|
| 1 | `settlement/service/deduct_service.go:76` DeductForFirstRound | `cashparty:settle:lock:first_round:%d` | 60s | ✅ | ✅ | ✅ |
| 2 | `settlement/service/deduct_service.go:484` deductSingleUser | `cashparty:settle:lock:later_round:%d` | 30s | ✅ | ✅ | ✅ |
| 3 | `settlement/service/deduct_service.go:432` DeductForSystemPacket | `cashparty:settle:lock:system_packet:%d` | 30s | ✅ | ✅ | ✅ |
| 4 | `settlement/service/game_settle_service.go:61` SettleGame | `cashparty:settle:lock:game:%d` | 60s | **❌** | ✅ | ❌ §9.2 |
| 5 | `settlement/service/game_settle_service.go:262` RetryPlayerSettle | `cashparty:settle:lock:game_retry:%d:%d` | 30s | - | ✅ | ⚠️ |
| 6 | `settlement/service/refund_service.go:58` ApplyForRefund | `cashparty:settle:lock:refund_apply:%d` | 30s | - | ✅ | ✅ |
| 7 | `settlement/service/refund_service.go:137` ApproveRefund | `cashparty:settle:lock:refund:%s` | 30s | ✅ | ✅ | ✅ |
| 8 | `settlement/service/refund_service.go:222` RejectRefund | `cashparty:settle:lock:refund:%s` | 30s | ✅ | ✅ | ✅ |
| 9 | `settlement/service/credit_retry_service.go:79` RetryCredit | `cashparty:settle:lock:bill_retry:%d` | 30s | - | ✅ | ✅ |
| 10 | `settlement/service/settlement_service.go:74` SettleRound | `cashparty:settle:lock:round:%d` | 30s | ✅ | ✅ | ✅ |
| 11 | `game/application/game_app_service.go:181` SendPacket | `cashparty:lock:send_packet:%s:%s` | 10s | - | ✅ | ✅ |
| 12 | `game/application/game_app_service.go:610` OnSendTimeout | `cashparty:lock:send_packet:%s:%s` | 10s | - | ✅ | ✅ |
| 13 | `game/application/game_app_service.go:712` OnReplaceTimeout | `cashparty:lock:replace_timeout:%s:%s` | 30s | - | ✅ | ✅ |
| 14 | `game/application/game_app_service.go:888` settleRound | `cashparty:lock:settle:%s:%s` | 30s | - | ✅（Lua code==2） | ✅ |

#### Scheduler 层（6 处，走 `BaseScheduler.executeTask`）

| # | Scheduler | LockKey | LockTTL |
|---|---|---|---|
| 15 | CreditRetryScheduler | `cashparty:scheduler:credit_retry:lock` | 60s |
| 16 | SettlementCheckScheduler | `cashparty:scheduler:settlement_check:lock` | 300s |
| 17 | RefundProcessScheduler | `cashparty:scheduler:refund_process:lock` | 120s |
| 18 | GameSettleRetryScheduler | `cashparty:scheduler:game_settle_retry:lock` | 60s |
| 19 | GameSettleTimeoutScheduler | `cashparty:scheduler:game_settle_timeout:lock` | 300s |
| 20 | VirtualBalanceSyncScheduler | `cashparty:scheduler:virtual_balance_sync:lock` | interval+5s |

**豁免**：TimeoutScheduler（ZRem 原子性，SCH-12 豁免）、RobotSchedulerService（per-room 锁限流）

#### Robot 独立锁（2 处，不走 redsync）

| # | 调用点 | Key | TTL | Token | 释放 |
|---|---|---|---|---|---|
| 21 | `RobotSchedulerRedis.AcquireRoomAssignLock` | `cashparty:robot:room_assign:%s` | 30s | **❌ value=1** | **❌ 调用方从不 Release** |
| 22 | `RobotSchedulerRedis.AcquireAssignLock` | `cashparty:robot:assign:%d` | 10s | ✅ uuid | ✅ Lua GET==token DEL |

#### Kafka 消费者幂等锁（2 处，不走 redsync）

| # | 调用点 | Key | TTL | Token | 释放 |
|---|---|---|---|---|---|
| 23 | `RoomEventConsumer.tryAcquire` | `cashparty:room:event:processed:%s` | 7d | **❌ value="1"** | **❌ 裸 Del** |
| 24 | `GameEventConsumer.tryAcquire` | `cashparty:game:event:processed:%s` | 7d | **❌ value=1** | **❌ 裸 Del** |

### 1.3 事务抽象层

**接口**：`game/domain/db_repository.go:55-60` 的 `domain.Transaction` 与 `domain.DBRepository.WithTransaction`

**实现**：`game/infrastructure/persistence/mysql/db_repository.go:42-72` 的 `DBRepositoryImpl.WithTransaction` 与 `GormTransactionImpl`

**能力**：
- Begin/Commit/Rollback：间接（GORM `Transaction(func(tx *gorm.DB) error)`）
- ctx 传递：✅
- panic recover + Rollback：✅（GORM 内置）
- 嵌套事务：仅 GORM savepoint
- 传播行为：❌
- 隔离级别配置：❌
- 超时控制：❌
- 跨资源一致性：❌

**关键缺陷**：抽象层是**死代码**，全仓零调用。所有事务绕过抽象直接 `db.Transaction(...)`。

### 1.4 事务调用点（12 处）

| # | 文件 | 边界 | 乐观锁 | 错误包装 | 规约 |
|---|---|---|---|---|---|
| 1 | `session_repository.go:119` BatchUpdateSessionPlayerStats | repo 内批量 | ❌ | ❌ `return err` | §4.3/§8.3 |
| 2 | `game_event_consumer.go:152` handleSessionStart | 创建 session + players | - | ✅ `%w` | ✅ |
| 3 | `game_event_consumer.go:207` handlePacketCreated | 更新 round + 创建 packets | - | ✅ | ✅ |
| 4 | `game_event_consumer.go:275` handleRoundSettle | 更新 round + grab_records + session_players + special_reward + **调用 SettleRound** | - | ✅ | **❌ 伪事务**（SettleRound 用独立 db 句柄） |
| 5 | `game_event_consumer.go:452` handleSessionEnd | 更新 session + session_player + **调用 SettleGame** | - | ✅ | **❌ 伪事务**（同上） |
| 6 | `refund_service.go:106` applyForRefundLocked | 创建 refund_audit + 更新 bill.refund_status | ❌ | ✅ | §8.2/§8.3 |
| 7 | `bill_manager.go:177` CreateBillsInTransaction | 批量创建 bills | - | ❌ `return err` | §4.3/§15.8 |
| 8 | `bill_manager.go:188` CreateRoundSettlementAndBills | 创建 round_settlement + bills | - | ❌ | §4.3 |
| 9 | `bill_manager.go:202` CreateBillsPairInTransaction | 创建配对 bill | - | ❌ | §4.3 |
| 10 | `bill_manager.go:284` UpdateRefundSuccessInTransaction | 更新 refund_audit + bill | **❌ 无 status 条件** | ❌ | §4.3/§8.3 |
| 11 | `bill_manager.go:443` CreateBillsOnly | 批量创建 bills（与 #7 重复） | - | ❌ | §4.3/§15.8 |

### 1.5 幂等性实现

**幂等键**：
- `BizOrderNo`：`{roundTraceID}_{billType}_{userID}`（确定性，DB 唯一索引）
- `RoundTraceID`：`RT_{sessionID}_{roundNo}` 等（确定性）
- `RefundOrderNo`：`REFUND_{billID}`（确定性）
- `ExceptionNo`：`EXC_{timestamp}_{random4}`（**非确定性**，违反 §9.3 精神）

**幂等检查点**（15 处）：
- SettleRound / creditRound / settleCommission / SettleReward：✅ 查 Success bill 跳过
- DeductForFirstRound / DeductForSystemPacket / deductSingleUser：✅ 锁外+锁内双检
- DeductPenaltyToPlatform：**❌ 仅查 Success，缺复合唯一索引**
- ApplyForRefund / ApproveRefund / RejectRefund：✅
- SettleGame：**❌ 缺锁外预检**
- settlePlayer / creditSessionPayout / RetryCredit：✅
- Kafka consumer handleSessionStart / handlePacketCreated / handleRoundSettle / handleSessionEnd：✅

### 1.6 乐观锁实现

**已实现（3 处）**：
- `UpdateRoundSettlementCredited`：`WHERE round_trace_id = ? AND status != ?`
- `UpdateRoundSettlementStatus`：同上
- `UpdateRefundAuditStatus`：`WHERE id = ? AND status = ?`

**缺失（9 处）**：
- `UpdateBillStatus` / `UpdateBillSuccess` / `UpdateBillRefundStatus`
- `UpdateGameSettleStatusByUser` / `UpdateGameSettleStatusBySession`
- `UpdateRoundSettlementDeductSuccess` / `UpdateRoundSettlementSettleInfo`
- `UpdateRefundAuditError` / `UpdateRefundSuccessInTransaction`

---

## 2. 问题清单

### 2.1 P0（资金安全，必须修复）

| ID | 问题 | 位置 | 影响 |
|---|---|---|---|
| L-P0-1 | Kafka 消费者幂等锁无 token + 裸 Del 释放 | `room_event_consumer.go:164`、`game_event_consumer.go:515` | TTL 过期 + 重新抢占时误删他人锁，破坏幂等性，导致重复扣款/结算 |
| L-P0-2 | `AcquireRoomAssignLock` 无 token + 调用方从不 Release | `robot_scheduler.go:77`、`robot_scheduler_service.go:190` | 同 room 30s 内只能分配一次机器人；无 token 无法安全释放 |
| TX-P0-1 | `executeRefund` / `executeSingleDeduct` / `creditSessionPayout` / `settlePlayer` 缺 RPC-DB 一致性中间态 | `refund_service.go:157`、`deduct_service.go:213`、`game_settle_service.go:175,311` | RPC 成功 + DB 失败时无本地标记，重试可能重复扣款/入账 |
| TX-P0-2 | `executeSessionCredit` 重试 flat 5s + 不递增 retry_count | `game_settle_service.go:391` | 违反 §9.5；`CreditRetryScheduler` 无限重试同一 bill |
| TX-P0-3 | `handleRoundSettle` / `handleSessionEnd` 伪事务 | `game_event_consumer.go:275,452` | 事务内调用 SettleRound/SettleGame 用独立 db 句柄，主事务回滚不回滚 bill |
| TX-P0-4 | 9 个 UPDATE 方法缺乐观锁 | `bill_manager.go` 9 处 | 并发可覆盖状态、重复退款、重复结算 |

### 2.2 P1（一致性，近期修复）

| ID | 问题 | 位置 |
|---|---|---|
| L-P1-1 | `SettleGame` 缺锁外预检 | `game_settle_service.go:59-81` |
| L-P1-2 | `WithLock` 内 fn panic 无 recover | `common/lock/distributed_lock.go:143` |
| L-P1-3 | `InitLocker` 用 sync.Mutex + nil 检查，非 sync.Once | `common/lock/distributed_lock.go:22` |
| L-P1-4 | watchdog 不监听 ctx.Done | `common/lock/distributed_lock.go:115` |
| L-P1-5 | `WithRedisLock` 每次检查 nil + 隐式初始化 | `common/lock/distributed_lock.go:152` |
| L-P1-6 | 命名违规：`InitLocker`/`Obtain`/`WithLock` | `common/lock/distributed_lock.go` |
| TX-P1-1 | `DeductPenaltyToPlatform` 幂等检查不完整 + 缺复合唯一索引 | `settlement_service.go:220` |
| TX-P1-2 | `handleFirstRoundDeductFailure` 孤儿 refund_audit 风险 | `deduct_service.go:299` |
| TX-P1-3 | `RejectRefund` 非原子（refund_audit 与 bill 不在同一 tx） | `refund_service.go:211` |
| TX-P1-4 | `if exists, _ :=` 错误吞没（6 处） | `deduct_service.go:69,78,414,420,467,472` |
| TX-P1-5 | `BillManager` 5 处 `return err` 未包装 | `bill_manager.go` |

### 2.3 P2（架构治理，中期重构）

| ID | 问题 | 位置 |
|---|---|---|
| L-P2-1 | 业务锁 TTL 全部硬编码 | 14 个调用点 |
| L-P2-2 | `VirtualBalanceSyncScheduler` LockTTL 计算脆弱 | `virtual_balance_sync.go:31` |
| L-P2-3 | `LockKeyRoom` 死代码常量 | `common/lock/distributed_lock.go:37` |
| L-P2-4 | `ReconcileLock` 死代码 | `rediskeys/keys.go:47` |
| L-P2-5 | `SendPacketLockKey` 命名冲突 | settlement vs game |
| TX-P2-1 | 事务抽象层是死代码（全仓零调用） | `game/domain/db_repository.go` |
| TX-P2-2 | `CreateBillsInTransaction` 与 `CreateBillsOnly` 重复 | `bill_manager.go:176,442` |
| TX-P2-3 | `ExceptionNo` 非确定性生成 | `trace_id_generator.go:44` |
| TX-P2-4 | `settlement/` 缺 `bootstrap/` 包 | `settlement/` 目录 |
| TX-P2-5 | `Transaction` 接口缺 HistoryDBRepo / RoomConfigDBRepo | `game/domain/db_repository.go:55` |

### 2.4 P3（细节优化）

| ID | 问题 |
|---|---|
| L-P3-1 | 无可重入锁（外层锁→内层同 key 锁会死锁） |
| L-P3-2 | 无 RedLock（单点 Redis 故障时双写不一致） |
| L-P3-3 | 无加锁失败原因区分（"已被持有" vs "Redis 异常"） |
| L-P3-4 | `Exists` 系列命名不一致（`ExistsByRoundAndType` vs `ExistsRoundSettlement`） |

---

## 3. 整体设计思路

### 3.1 锁框架设计

**核心原则**：保留 `redsync` 作为底层实现（已稳定，无需重造轮子），但在其上构建项目自有的锁抽象层，解决 token 不可见、ctx 不监听、panic 不 recover、命名不规范等问题。

**分层**：
```
业务层（service / application / consumer）
        ↓ 调用
高阶函数 WithRedisLock(ctx, lockMgr, key, opts, fn)
        ↓ 调用
LockManager.Acquire / Release / WithLock
        ↓ 委托
redsync.Mutex（SetNX + Lua 释放）
        ↓
Redis
```

**关键改进**：
1. **LockManager 取代全局 redsyncClient**：依赖注入，避免全局单例 + sync.Once
2. **token 透明化**：`Acquire` 返回 `*Lock`（含 token 字段），调用方可读取
3. **watchdog 监听 ctx**：`Acquire(ctx, ...)` 的 ctx 传给 watchdog goroutine
4. **WithLock 内 panic recover**：转 error，避免击穿上层
5. **加锁失败原因区分**：返回哨兵 error `ErrLockNotAcquired`
6. **配置外部化**：`LockConfig` 结构体，TTL 从 YAML 读取

### 3.2 Kafka 消费者幂等锁设计

**核心原则**：复用 Robot 已有的 `robot_lock.lua.go` Lua 脚本（`if GET key == token then DEL key end`），统一 token + Lua 释放模式。

**改造**：
```go
// tryAcquire 返回 (bool, token, error)
token := uuid.New().String()
ok, err := redis.SetNX(ctx, key, token, 7*24h).Result()
return ok, token, err

// releaseAcquire 用 Lua
scripts.ReleaseAssignLock.Run(ctx, redis, []string{key}, token)
```

**Lua 脚本迁移**：从 `game/infrastructure/persistence/redis/scripts/robot_lock.lua.go` 迁移到 `common/redis/scripts/release_lock.lua.go`，作为项目共享脚本。

### 3.3 Robot RoomAssignLock 设计

**核心原则**：补齐 `ReleaseRoomAssignLock` + token 校验，与 `AcquireAssignLock` 模式对齐。

```go
// AcquireRoomAssignLock 返回 (bool, token, error)
token := uuid.New().String()
ok, err := redis.SetNX(ctx, key, token, ttl).Result()
return ok, token, err

// ReleaseRoomAssignLock 用 Lua
scripts.ReleaseAssignLock.Run(ctx, redis, []string{key}, token)

// 调用方
locked, lockToken, err := s.robotSchedulerRedis.AcquireRoomAssignLock(ctx, room.RoomID, ttl)
if err != nil || !locked { return nil }
defer s.robotSchedulerRedis.ReleaseRoomAssignLock(ctx, room.RoomID, lockToken)
```

### 3.4 事务抽象层设计

**核心决策**：**激活事务抽象层**（选项 A），删除"死代码"状态。

**理由**：
- 当前所有事务绕过抽象，§8.2 规约 100% 违反
- 抽象层存在但无人用，既误导又增加维护负担
- 激活后可统一治理事务边界、传播、超时、乐观锁、监控

**改造路径**：
1. `BillManager` 接受 `domain.Transaction` 或 `tx *gorm.DB` 参数
2. 所有 service 通过 `dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error { ... })` 调用
3. 事务内所有 DB 操作通过 `tx.BillRepo()` / `tx.RoundRepo()` 等子 repo
4. `game_event_consumer.handleRoundSettle` / `handleSessionEnd` 的 `SettleRound` / `SettleGame` 调用移出主事务（移入独立事务或后续 Kafka 事件触发），消除"伪事务"

### 3.5 RPC-DB 一致性设计

**核心原则**：引入 **Processing 中间态** + **平台侧状态查询**。

**模式**：
```
1. 本地置 Processing（带乐观锁 WHERE status = ?）
2. 调 RPC（BizID 确定性 → 平台侧幂等）
3a. RPC 成功 → 本地置 Success
3b. RPC 失败 → 本地置 Failed
4. 重试时若已是 Processing → 先查平台侧状态（用 BizID）
    - 平台已成功 → 本地置 Success
    - 平台未成功 → 重新调 RPC
```

**应用场景**：
- `executeRefund`：refund_audit 状态 Pending → Processing → Refunded
- `executeSingleDeduct`：bill 状态（无）→ Processing → Success/Failed
- `creditSessionPayout`：bill 状态（无）→ Processing → Success/Failed
- `settlePlayer`：bill 状态（无）→ Processing → Success/Failed

### 3.6 乐观锁补齐设计

**模式**：所有状态机 UPDATE 操作 MUST 包含 `WHERE status = ?` 或 `WHERE status != ?` 条件，并检查 `RowsAffected`。

**示例**：
```go
// Before
result := tx.Model(&BillRecord{}).Where("id = ?", billID).Updates(updates)

// After
result := tx.Model(&BillRecord{}).
    Where("id = ? AND status IN ?", billID, []string{StatusProcessing, StatusFailed}).
    Updates(updates)
if result.RowsAffected == 0 {
    return nil // 幂等成功或状态不匹配
}
```

### 3.7 伪事务修复设计

**选项 A（推荐）**：将 `SettleRound` / `SettleGame` 调用移出主事务。

```go
// Before（伪事务）
func (c *GameEventConsumer) handleRoundSettle(ctx, msg) error {
    return c.db.Transaction(ctx, func(tx *gorm.DB) error {
        // 更新 round、grab_records、session_players
        // 调用 SettleRound（用独立 db 句柄）
    })
}

// After（真事务）
func (c *GameEventConsumer) handleRoundSettle(ctx, msg) error {
    // 1. 主事务：更新 round、grab_records、session_players
    if err := c.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        return tx.RoundRepo().Update(...)
    }); err != nil {
        return err
    }
    // 2. 独立调用 SettleRound（内部有自己的事务和幂等）
    return c.settlementService.SettleRound(ctx, ...)
}
```

**理由**：
- `SettleRound` / `SettleGame` 内部已有完整的事务 + 幂等 + 锁保护
- 主事务只需保证 round / grab_records / session_players 的一致性
- 拆分后事务边界清晰，无需"伪事务"注释

---

## 4. 文件结构

### 4.1 新增文件

```
backend/
├── common/
│   ├── lock/
│   │   ├── distributed_lock.go      # 重构（保留）
│   │   ├── manager.go               # 新增：LockManager（DI 替代全局单例）
│   │   ├── options.go               # 新增：LockOptions + LockConfig
│   │   ├── errors.go                # 新增：ErrLockNotAcquired 等哨兵 error
│   │   ├── manager_test.go          # 新增：单元测试
│   │   └── scripts/
│   │       └── release_lock.lua.go  # 新增：统一 Lua 释放脚本（从 robot_lock 迁移）
│   ├── redis/
│   │   └── scripts/
│   │       └── registry.go          # 新增：Lua 脚本注册表（统一管理）
│   └── config/
│       └── types.go                 # 修改：新增 LockConfig
├── game/
│   ├── domain/
│   │   └── db_repository.go         # 修改：Transaction 接口扩展（如保留）
│   ├── infrastructure/
│   │   ├── messaging/
│   │   │   ├── room_event_consumer.go   # 修改：tryAcquire + token + Lua
│   │   │   └── game_event_consumer.go   # 修改：同上
│   │   └── persistence/
│   │       ├── mysql/
│   │       │   └── db_repository.go     # 修改：WithTransaction 激活或删除
│   │       └── redis/
│   │           └── robot_scheduler.go   # 修改：AcquireRoomAssignLock + Release
│   └── application/
│       └── robot_scheduler_service.go   # 修改：defer ReleaseRoomAssignLock
├── settlement/
│   ├── service/
│   │   ├── bill_manager.go          # 修改：9 处乐观锁 + 错误包装 + 去重
│   │   ├── refund_service.go        # 修改：Processing 中间态 + 事务下沉
│   │   ├── deduct_service.go        # 修改：Processing 中间态 + 错误检查
│   │   ├── game_settle_service.go   # 修改：锁外预检 + 指数退避 + Processing
│   │   ├── settlement_service.go    # 修改：复合唯一索引幂等
│   │   └── trace_id_generator.go    # 修改：ExceptionNo 确定性
│   └── bootstrap/                   # 新增：settlement 独立装配
│       └── container.go
├── game/
│   ├── infrastructure/
│   │   └── messaging/
│   │       └── game_event_consumer.go   # 修改：移除伪事务
│   └── bootstrap/
│       └── container.go             # 修改：注入 LockManager
└── config/
    └── game.yaml                    # 修改：新增 lock 配置段
```

### 4.2 删除文件

- 无（保留 `common/lock/distributed_lock.go` 作为 redsync 封装层）

### 4.3 修改文件总览

| 文件 | 修改类型 | 涉及问题 |
|---|---|---|
| `common/lock/distributed_lock.go` | 重构 | L-P1-2~L-P1-6 |
| `common/lock/manager.go` | 新增 | L-P1-3~L-P1-5 |
| `common/lock/options.go` | 新增 | L-P2-1 |
| `common/lock/errors.go` | 新增 | L-P3-3 |
| `common/lock/scripts/release_lock.lua.go` | 新增 | L-P0-1 |
| `game/infrastructure/messaging/room_event_consumer.go` | 修改 | L-P0-1 |
| `game/infrastructure/messaging/game_event_consumer.go` | 修改 | L-P0-1, TX-P0-3 |
| `game/infrastructure/persistence/redis/robot_scheduler.go` | 修改 | L-P0-2 |
| `game/application/robot_scheduler_service.go` | 修改 | L-P0-2 |
| `settlement/service/bill_manager.go` | 修改 | TX-P0-4, TX-P1-5, TX-P2-2 |
| `settlement/service/refund_service.go` | 修改 | TX-P0-1, TX-P1-3 |
| `settlement/service/deduct_service.go` | 修改 | TX-P0-1, TX-P1-4 |
| `settlement/service/game_settle_service.go` | 修改 | TX-P0-1, TX-P0-2, L-P1-1 |
| `settlement/service/settlement_service.go` | 修改 | TX-P1-1 |
| `settlement/service/trace_id_generator.go` | 修改 | TX-P2-3 |
| `game/domain/db_repository.go` | 修改 | TX-P2-1 |
| `game/infrastructure/persistence/mysql/db_repository.go` | 修改 | TX-P2-1 |
| `game/bootstrap/container.go` | 修改 | 注入 LockManager |
| `common/config/types.go` | 修改 | L-P2-1 |
| `config/game.yaml` | 修改 | L-P2-1 |
| `CODING_STANDARD.md` | 修改 | 新增 §18 锁与事务规约 |

---

## 5. 规约

### 5.1 分布式锁规约（DL-1~DL-12）

#### DL-1：统一 LockManager 注入（MUST）

所有锁操作 MUST 通过 `LockManager` 实例调用，**禁止**直接使用全局 `redsyncClient`。

```go
// ✅ 正确
type Service struct {
    lockMgr *lock.LockManager
}
s.lockMgr.WithLock(ctx, key, opts, fn)

// ❌ 错误
lock.WithRedisLock(ctx, redis, key, ttl, fn) // 全局单例
```

**理由**：依赖注入便于测试与生命周期管理；避免全局单例的并发初始化问题。

#### DL-2：token 必须用随机 UUID（MUST）

所有锁的 value MUST 是 `uuid.New().String()`，**禁止**用 `"1"`、roomID、userID 等业务字段。

**理由**：token 用于释放锁时的持有者校验，防止误删他人锁。

#### DL-3：释放锁必须用 Lua 脚本（MUST）

释放锁 MUST 用 `if GET key == token then DEL key end` Lua 脚本，**禁止**裸 `Del`。

```go
// ✅ 正确
scripts.ReleaseAssignLock.Run(ctx, redis, []string{key}, token)

// ❌ 错误
redis.Del(ctx, key)
```

**理由**：GET + DEL 两步操作非原子，TTL 过期后可能误删他人锁。

#### DL-4：watchdog 必须监听 ctx（MUST）

watchdog goroutine MUST 同时监听 `ctx.Done()` 与 `watchdogStop` 通道。

```go
select {
case <-ctx.Done():
    return
case <-l.watchdogStop:
    return
case <-ticker.C:
    l.mutex.Extend()
}
```

**理由**：ctx 取消时 watchdog 应立即退出，避免无限续期。

#### DL-5：WithLock 必须recover panic（MUST）

`WithLock` 内 MUST recover fn 的 panic 并转为 error。

```go
defer func() {
    if r := recover(); r != nil {
        err = fmt.Errorf("panic in lock fn: %v", r)
    }
}()
```

**理由**：避免 panic 击穿上层；保证锁被释放。

#### DL-6：加锁失败必须区分原因（MUST）

加锁失败 MUST 返回哨兵 error 区分"锁已被持有"与"Redis 异常"。

```go
var ErrLockNotAcquired = errors.New("lock not acquired")
var ErrRedisUnavailable = errors.New("redis unavailable")
```

**理由**：调用方可根据原因决定是否重试。

#### DL-7：锁 TTL 必须配置化（MUST）

业务锁 TTL MUST 从配置文件读取，**禁止**硬编码。

```yaml
lock:
  deduct_first_round_ttl: 60s
  deduct_later_round_ttl: 30s
  settle_round_ttl: 30s
  # ...
```

**理由**：TTL 散落在调用点无法统一治理。

#### DL-8：InitLocker 必须用 sync.Once（MUST）

单例初始化 MUST 用 `sync.Once`，**禁止**`sync.Mutex` + nil 检查。

**理由**：§4.4 规约；`sync.Mutex` + nil 检查非幂等。

#### DL-9：锁 key 必须在 common/rediskeys 定义（MUST）

所有锁 key 常量 MUST 定义在 `common/rediskeys/keys.go`，**禁止**在 `common/lock` 或业务包内定义。

**理由**：§7.1 单一真相源。

#### DL-10：Lua 脚本必须在 common/redis/scripts 注册（MUST）

所有锁相关 Lua 脚本 MUST 在 `common/redis/scripts/registry.go` 注册，**禁止**散落在业务包。

**理由**：脚本统一管理，避免重复定义。

#### DL-11：死锁避免（MUST）

同一 key 的 `WithLock` MUST NOT 嵌套调用（会死锁）。如需嵌套，MUST 使用可重入锁（未来实现）。

**理由**：redsync 默认非可重入。

#### DL-12：锁释放失败不阻断业务（SHOULD）

`Release` 失败时 SHOULD 记日志但不返回 error 给业务，**禁止**因释放失败回滚已成功的业务。

**理由**：锁会因 TTL 自然过期；业务已成功不应因锁释放失败而回滚。

### 5.2 事务规约（TX-1~TX-12）

#### TX-1：事务必须通过抽象层调用（MUST）

所有事务 MUST 通过 `dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error { ... })` 调用，**禁止**service 直接持有 `*gorm.DB` 开事务。

```go
// ✅ 正确
err := s.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
    return tx.BillRepo().Create(ctx, bill)
})

// ❌ 错误
err := s.db.Transaction(func(tx *gorm.DB) error { ... })
```

**理由**：§8.2 规约；统一事务边界治理。

#### TX-2：状态机 UPDATE 必须含乐观锁（MUST）

所有状态机 UPDATE 操作 MUST 包含 `WHERE status = ?` 或 `WHERE status != ?` 条件，并检查 `RowsAffected`。

```go
result := tx.Model(&BillRecord{}).
    Where("id = ? AND status IN ?", billID, allowedStates).
    Updates(updates)
if result.RowsAffected == 0 {
    return nil // 幂等成功或状态不匹配
}
```

**理由**：§8.3 规约；防止并发覆盖状态。

#### TX-3：RPC-DB 操作必须引入 Processing 中间态（MUST）

涉及 RPC + DB 写的操作 MUST 引入 Processing 中间态：

1. 本地置 Processing（带乐观锁）
2. 调 RPC（BizID 确定性）
3a. RPC 成功 → 本地置 Success
3b. RPC 失败 → 本地置 Failed
4. 重试时若已是 Processing → 先查平台侧状态

**理由**：消除 RPC 成功 + DB 失败时的"无本地标记"窗口。

#### TX-4：事务内禁止调用外部 DB 写（MUST）

事务内 MUST NOT 调用使用独立 `*gorm.DB` 句柄的 DB 写操作。如需调用外部 service，MUST 将其移出主事务。

**理由**：避免"伪事务"（主事务回滚不回滚外部 DB 写）。

#### TX-5：重试必须指数退避 + 递增 retry_count（MUST）

所有重试 MUST 使用指数退避（`baseDelay * 2^retryCount`，封顶 maxDelay）并递增 `retry_count`。

```go
nextDelay := baseDelay * time.Duration(math.Pow(2, float64(retryCount)))
if nextDelay > maxDelay {
    nextDelay = maxDelay
}
bill.RetryCount++
bill.NextRetryAt = time.Now().Add(nextDelay)
```

**理由**：§9.5 规约；flat 重试会压垮下游。

#### TX-6：幂等键必须确定性生成（MUST）

所有幂等键（BizOrderNo / RoundTraceID / RefundOrderNo / ExceptionNo）MUST 基于业务上下文确定性生成，**禁止**用时间戳 + 随机数。

**理由**：§9.3 规约；确定性键允许重试复现。

#### TX-7：锁外预检 + 锁内双检（MUST）

所有"创建型"业务操作（Settle / Deduct / Refund）MUST 锁外预检 + 锁内双检。

```go
// 锁外预检
exists, err := s.Exists(ctx, key)
if err != nil { return err }
if exists { return nil }

// 加锁
err = s.lockMgr.WithLock(ctx, lockKey, opts, func() error {
    // 锁内双检
    exists, err := s.Exists(ctx, key)
    if err != nil { return err }
    if exists { return nil }
    // 业务逻辑
})
```

**理由**：§9.2 规约；减少锁竞争。

#### TX-8：错误必须包装（MUST）

所有事务方法 MUST 用 `fmt.Errorf("xxx failed: %w", err)` 包装错误，**禁止**裸 `return err`。

**理由**：§4.3 规约；保留调用栈。

#### TX-9：错误禁止吞没（MUST）

`if exists, _ :=` 形式的错误吞没 MUST 改为显式检查 error。

**理由**：§4.4 规约；Redis/DB 异常时误判为"不存在"导致重复创建。

#### TX-10：重复代码必须消除（MUST）

字节级重复的方法 MUST 合并，如 `CreateBillsInTransaction` 与 `CreateBillsOnly`。

**理由**：§15.8 规约。

#### TX-11：复合唯一索引必须建立（MUST）

`BillRecord` 表 MUST 建立 `(round_trace_id, bill_type, user_id)` 复合唯一索引。

**理由**：兜底防止并发创建重复 bill。

#### TX-12：settlement 模块必须有 bootstrap 包（SHOULD）

`settlement/` 模块 SHOULD 有独立的 `bootstrap/` 包，避免装配逻辑混在 `game/bootstrap/`。

**理由**：§2.1 DDD 分层。

---

## 6. 分阶段实施计划

### Phase 1：锁框架重构（P0+P1，1-2 天）

**任务**：
1. 新建 `common/lock/manager.go`：`LockManager` 结构体，注入 `*redsync.Redsync`
2. 新建 `common/lock/options.go`：`LockOptions` + `LockConfig`
3. 新建 `common/lock/errors.go`：`ErrLockNotAcquired` / `ErrRedisUnavailable`
4. 新建 `common/lock/scripts/release_lock.lua.go`：迁移 `robot_lock.lua.go` 的 Lua 脚本
5. 重构 `common/lock/distributed_lock.go`：
   - `InitLocker` 改用 `sync.Once`
   - `WithLock` 加 panic recover
   - watchdog 监听 ctx
   - 重命名 `Obtain` → `NewLock`，`WithLock` 保留
6. 新增 `common/lock/manager_test.go`：单元测试

**验证**：
- `go build ./common/lock/...`
- `go vet ./common/lock/...`
- `gofmt -l common/lock/`
- `go test ./common/lock/...`

### Phase 2：Kafka 消费者幂等锁修复（P0，1 天）

**任务**：
1. 修改 `room_event_consumer.go`：
   - `tryAcquire` 返回 `(bool, token, error)`，token 用 `uuid.New().String()`
   - `releaseAcquire` 用 Lua 脚本
2. 修改 `game_event_consumer.go`：同上
3. 修改调用方：保存 token，失败时用 token 释放

**验证**：
- `go build ./game/infrastructure/messaging/...`
- grep 确认无裸 `Del` 释放锁

### Phase 3：Robot RoomAssignLock 修复（P0，1 天）

**任务**：
1. 修改 `robot_scheduler.go`：
   - `AcquireRoomAssignLock` 返回 `(bool, token, error)`，value 用 uuid
   - 新增 `ReleaseRoomAssignLock(ctx, roomID, token) error`，用 Lua 脚本
2. 修改 `robot_scheduler_service.go:190`：
   - 接收 token
   - `defer ReleaseRoomAssignLock(ctx, room.RoomID, lockToken)`

**验证**：
- `go build ./game/...`
- grep 确认 `AcquireRoomAssignLock` 调用方都有 `defer Release`

### Phase 4：乐观锁补齐（P0，1-2 天）

**任务**：
1. 修改 `bill_manager.go` 9 个 UPDATE 方法：
   - `UpdateBillStatus`：加 `WHERE status IN ?`
   - `UpdateBillSuccess`：加 `WHERE status = ?`
   - `UpdateBillRefundStatus`：加 `WHERE refund_status = ?`
   - `UpdateGameSettleStatusByUser`：加 `WHERE settle_status = ?`
   - `UpdateGameSettleStatusBySession`：加 `WHERE settle_status = ?`
   - `UpdateRoundSettlementDeductSuccess`：加 `WHERE deduct_success = false`
   - `UpdateRoundSettlementSettleInfo`：加 `WHERE settle_info IS NULL`
   - `UpdateRefundAuditError`：加 `WHERE status = ?`
   - `UpdateRefundSuccessInTransaction`：加 `WHERE status = ?`（refund_audit + bill）
2. 新增 `BillRecord` 的 `(round_trace_id, bill_type, user_id)` 复合唯一索引 migration

**验证**：
- `go build ./settlement/...`
- grep 确认 9 个方法都有 `WHERE` 条件

### Phase 5：RPC-DB 一致性中间态（P0，2-3 天）

**任务**：
1. 修改 `refund_service.go`：
   - `executeRefund`：调 RPC 前置 refund_audit 为 Processing（带乐观锁 `WHERE status = 'Approved'`）
   - RPC 成功后置 Refunded，失败置 Rejected
   - 重试时若已是 Processing，先查平台侧状态
2. 修改 `deduct_service.go`：
   - `executeSingleDeduct`：调 RPC 前置 bill 为 Processing
   - RPC 成功后置 Success，失败置 Failed
3. 修改 `game_settle_service.go`：
   - `creditSessionPayout`：同上
   - `settlePlayer`：同上
   - `executeSessionCredit`：改用 `IncrementRetryCountWithNextRetryTime` + 指数退避

**验证**：
- `go build ./settlement/...`
- grep 确认所有 RPC 调用前都有 Processing 置位

### Phase 6：伪事务修复 + 事务抽象激活（P0+P2，2-3 天）

**任务**：
1. 修改 `game_event_consumer.go`：
   - `handleRoundSettle`：将 `SettleRound` 调用移出主事务
   - `handleSessionEnd`：将 `SettleGame` 调用移出主事务
2. 激活事务抽象层：
   - `BillManager` 接受 `domain.Transaction` 或 tx 参数
   - 所有 service 通过 `dbRepo.WithTransaction` 调用
3. 修改 `session_repository.go:119`：迁移到 `dbRepo.WithTransaction`
4. 修改 `refund_service.go:106`：迁移到 `dbRepo.WithTransaction`

**验证**：
- `go build ./...`
- grep 确认无 `s.db.Transaction(` 直接调用
- grep 确认 `dbRepo.WithTransaction` 有调用

### Phase 7：其他 P1 修复（P1，1-2 天）

**任务**：
1. `SettleGame` 补锁外预检
2. `DeductPenaltyToPlatform` 幂等检查扩展
3. `handleFirstRoundDeductFailure`：`CreateRefundAudit` + `UpdateBillRefundStatus` 同一 tx
4. `RejectRefund`：`UpdateRefundAuditStatus` + `UpdateBillRefundStatus` 同一 tx
5. `BillManager` 5 处 `return err` 改为 `fmt.Errorf("...: %w", err)`
6. `deduct_service.go` 6 处 `if exists, _ :=` 改为显式检查 error
7. `ExceptionNo` 改为确定性生成（`EXC_{billID}_{exceptionType}`）
8. 删除 `CreateBillsOnly` 或 `CreateBillsInTransaction`（保留一个）
9. 删除 `LockKeyRoom` 死代码
10. 删除 `ReconcileLock` 死代码

**验证**：
- `go build ./...`
- `go vet ./...`
- `gofmt -l`

### Phase 8：配置外部化 + 规约更新（P2，1 天）

**任务**：
1. 新增 `common/config/types.go` 的 `LockConfig` 结构体
2. 修改 14 个业务锁调用点：从配置读取 TTL
3. 更新 `config/game.yaml`：新增 `lock` 配置段
4. 更新 `CODING_STANDARD.md`：新增 §18 锁与事务规约（DL-1~DL-12、TX-1~TX-12）

**验证**：
- `go build ./...`
- `gofmt -l`
- grep 确认无硬编码 TTL

### Phase 9：全局验证（P3，半天）

**任务**：
1. `go build ./...`
2. `go vet ./...`
3. `gofmt -l`
4. `go test ./common/lock/...`
5. grep 验证：
   - 无裸 `Del` 释放锁
   - 无 `value="1"` 锁值
   - 无 `s.db.Transaction(` 直接调用
   - 9 个 UPDATE 方法都有乐观锁
   - 无 `if exists, _ :=` 错误吞没
   - 无 `return err` 未包装（BillManager）

---

## 7. 关键修复细节

### 7.1 Kafka 消费者 tryAcquire 修复

```go
// Before（room_event_consumer.go:148）
func (c *RoomEventConsumer) tryAcquire(ctx context.Context, eventID string) (bool, error) {
    key := rediskeys.RoomEventProcessedKey(eventID)
    ok, err := c.redis.SetNX(ctx, key, "1", 7*24*time.Hour).Result()
    if err != nil {
        return false, err
    }
    return ok, nil
}

func (c *RoomEventConsumer) releaseAcquire(ctx context.Context, eventID string) {
    key := rediskeys.RoomEventProcessedKey(eventID)
    c.redis.Del(ctx, key)  // ❌ 裸 Del
}

// After
func (c *RoomEventConsumer) tryAcquire(ctx context.Context, eventID string) (bool, string, error) {
    key := rediskeys.RoomEventProcessedKey(eventID)
    token := uuid.New().String()
    ok, err := c.redis.SetNX(ctx, key, token, 7*24*time.Hour).Result()
    if err != nil {
        return false, "", err
    }
    return ok, token, nil
}

func (c *RoomEventConsumer) releaseAcquire(ctx context.Context, eventID, token string) {
    key := rediskeys.RoomEventProcessedKey(eventID)
    scripts.ReleaseLock.Run(ctx, c.redis, []string{key}, token)
}

// 调用方
acquired, token, err := c.tryAcquire(ctx, eventID)
if err != nil {
    return err // fail-closed，触发 Kafka 重试
}
if !acquired {
    return nil // 已处理
}
defer func() {
    if shouldRelease {
        c.releaseAcquire(ctx, eventID, token)
    }
}()
```

### 7.2 AcquireRoomAssignLock 修复

```go
// Before（robot_scheduler.go:77）
func (r *RobotSchedulerRedis) AcquireRoomAssignLock(ctx context.Context, roomID string, ttl time.Duration) (bool, error) {
    key := rediskeys.RobotRoomAssignLockKey(roomID)
    ok, err := r.redis.SetNX(ctx, key, "1", ttl).Result()
    if err != nil {
        return false, err
    }
    return ok, nil
}

// After
func (r *RobotSchedulerRedis) AcquireRoomAssignLock(ctx context.Context, roomID string, ttl time.Duration) (bool, string, error) {
    key := rediskeys.RobotRoomAssignLockKey(roomID)
    token := uuid.New().String()
    ok, err := r.redis.SetNX(ctx, key, token, ttl).Result()
    if err != nil {
        return false, "", err
    }
    return ok, token, nil
}

func (r *RobotSchedulerRedis) ReleaseRoomAssignLock(ctx context.Context, roomID, token string) error {
    key := rediskeys.RobotRoomAssignLockKey(roomID)
    return scripts.ReleaseLock.Run(ctx, r.redis, []string{key}, token).Err()
}

// 调用方（robot_scheduler_service.go:190）
locked, lockToken, err := s.robotSchedulerRedis.AcquireRoomAssignLock(ctx, room.RoomID, ttl)
if err != nil || !locked {
    return nil
}
defer s.robotSchedulerRedis.ReleaseRoomAssignLock(ctx, room.RoomID, lockToken)
```

### 7.3 RPC-DB 一致性中间态（executeRefund 示例）

```go
// Before（refund_service.go:157）
func (s *RefundService) executeRefund(ctx context.Context, refund *RefundAudit) error {
    // 直接调 RPC，无中间态
    err := s.platform.Credit(ctx, refund.BizOrderNo, refund.Amount)
    if err != nil {
        return err
    }
    // RPC 成功后更新本地
    return s.billMgr.UpdateRefundSuccessInTransaction(ctx, refund.ID, refund.BillID)
}

// After
func (s *RefundService) executeRefund(ctx context.Context, refund *RefundAudit) error {
    // 1. 重试时若已是 Processing，先查平台侧状态
    if refund.Status == RefundStatusProcessing {
        settled, err := s.platform.QueryStatus(ctx, refund.BizOrderNo)
        if err != nil {
            return err
        }
        if settled {
            return s.billMgr.UpdateRefundSuccessInTransaction(ctx, refund.ID, refund.BillID)
        }
        // 平台未成功，继续重试 RPC
    }

    // 2. 本地置 Processing（带乐观锁 WHERE status = 'Approved'）
    err := s.billMgr.UpdateRefundAuditStatus(ctx, refund.ID, RefundStatusApproved, RefundStatusProcessing)
    if err != nil {
        return err
    }

    // 3. 调 RPC（BizID 确定性 → 平台侧幂等）
    err = s.platform.Credit(ctx, refund.BizOrderNo, refund.Amount)
    if err != nil {
        // RPC 失败 → 置 Rejected（带乐观锁）
        _ = s.billMgr.UpdateRefundAuditStatus(ctx, refund.ID, RefundStatusProcessing, RefundStatusRejected)
        return err
    }

    // 4. RPC 成功 → 置 Refunded
    return s.billMgr.UpdateRefundSuccessInTransaction(ctx, refund.ID, refund.BillID)
}
```

### 7.4 乐观锁补齐（UpdateBillStatus 示例）

```go
// Before（bill_manager.go:24）
func (m *BillManager) UpdateBillStatus(ctx context.Context, billID int64, status string) error {
    return m.db.WithContext(ctx).Model(&BillRecord{}).
        Where("id = ?", billID).
        Update("status", status).Error
}

// After
func (m *BillManager) UpdateBillStatus(ctx context.Context, billID int64, fromStatus, toStatus string) error {
    result := m.db.WithContext(ctx).Model(&BillRecord{}).
        Where("id = ? AND status = ?", billID, fromStatus).
        Update("status", toStatus)
    if result.Error != nil {
        return fmt.Errorf("update bill status failed: %w", result.Error)
    }
    if result.RowsAffected == 0 {
        return nil // 幂等成功或状态不匹配
    }
    return nil
}
```

### 7.5 伪事务修复（handleRoundSettle 示例）

```go
// Before（game_event_consumer.go:275）
func (c *GameEventConsumer) handleRoundSettle(ctx context.Context, msg *GameEventMessage) error {
    return c.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        // 更新 round
        // 创建 grab_records
        // 更新 session_players
        // 创建 special_reward
        // 调用 SettleRound（用独立 db 句柄）❌ 伪事务
        return c.settlementService.SettleRound(ctx, ...)
    })
}

// After
func (c *GameEventConsumer) handleRoundSettle(ctx context.Context, msg *GameEventMessage) error {
    // 1. 主事务：更新 round、grab_records、session_players、special_reward
    err := c.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        if err := tx.RoundRepo().Update(ctx, round); err != nil {
            return fmt.Errorf("update round failed: %w", err)
        }
        if err := tx.GrabRecordRepo().BatchCreate(ctx, records); err != nil {
            return fmt.Errorf("create grab records failed: %w", err)
        }
        // ...
        return nil
    })
    if err != nil {
        return err
    }

    // 2. 独立调用 SettleRound（内部有自己的事务 + 幂等 + 锁）
    return c.settlementService.SettleRound(ctx, ...)
}
```

### 7.6 WithLock panic recover

```go
// Before（distributed_lock.go:143）
func WithLock(ctx context.Context, key string, opts *LockOptions, fn func() error) error {
    lock, err := Obtain(ctx, key, opts)
    if err != nil {
        return err
    }
    defer lock.Release(ctx)
    return fn()
}

// After
func WithLock(ctx context.Context, key string, opts *LockOptions, fn func() error) (err error) {
    lock, err := NewLock(ctx, key, opts)
    if err != nil {
        return err
    }
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("panic in lock fn: %v", r)
        }
        _ = lock.Release(ctx) // 锁释放失败不阻断业务
    }()
    return fn()
}
```

### 7.7 watchdog 监听 ctx

```go
// Before（distributed_lock.go:115）
func (l *Lock) startWatchdog(interval time.Duration) {
    l.wg.Add(1)
    go func() {
        defer l.wg.Done()
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                if ok, err := l.mutex.Extend(); !ok || err != nil {
                    return
                }
            case <-l.watchdogStop:
                return
            }
        }
    }()
}

// After
func (l *Lock) startWatchdog(ctx context.Context, interval time.Duration) {
    l.wg.Add(1)
    go func() {
        defer l.wg.Done()
        ticker := time.NewTicker(interval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-l.watchdogStop:
                return
            case <-ticker.C:
                if ok, err := l.mutex.Extend(); !ok || err != nil {
                    return
                }
            }
        }
    }()
}
```

---

## 8. 决策记录

### D1：保留 redsync 还是自持 token + Lua？

**决策**：保留 redsync 作为底层实现。

**理由**：
- redsync 已稳定，内部已实现 token + Lua 释放
- 自持 Lua 脚本会增加维护负担
- redsync 的 watchdog 机制成熟

**例外**：Kafka 消费者幂等锁与 Robot RoomAssignLock 不走 redsync（用 SetNX + Lua），因为它们是"一次性抢占"而非"持有型锁"。

### D2：激活还是删除事务抽象层？

**决策**：激活。

**理由**：
- 当前"死代码"状态最差，既误导又增加维护负担
- 激活后可统一治理事务边界、传播、超时、乐观锁
- 删除则 §8.2 规约形同虚设

### D3：伪事务修复选 A 还是 B？

**决策**：选项 A（移出主事务）。

**理由**：
- `SettleRound` / `SettleGame` 内部已有完整的事务 + 幂等 + 锁保护
- 主事务只需保证 round / grab_records / session_players 的一致性
- 拆分后事务边界清晰，无需"伪事务"注释

### D4：RPC-DB 一致性用 Processing 中间态还是 Saga？

**决策**：Processing 中间态。

**理由**：
- Saga 模式实现复杂，需补偿事务
- Processing 中间态 + 平台侧 BizID 幂等已能满足当前业务
- 重试时查平台侧状态可覆盖 RPC 成功 + DB 失败的窗口

### D5：复合唯一索引建在哪些字段？

**决策**：`BillRecord` 表建 `(round_trace_id, bill_type, user_id)` 复合唯一索引。

**理由**：
- `BizOrderNo` 单列唯一索引能挡住完全相同的 bill
- 但挡不住"同 traceID+type+user 但不同 status"的并发插入
- 复合唯一索引作为兜底，防止并发创建重复 bill

### D6：LockManager 是全局单例还是 DI？

**决策**：DI。

**理由**：
- 全局单例的 sync.Once 初始化在测试中不便
- DI 便于 mock 与生命周期管理
- 与项目其他 Manager（如 AsyncTaskRunner）模式一致

### D7：ExceptionNo 改为确定性生成还是保留时间戳+随机？

**决策**：改为确定性。

**理由**：
- §9.3 规约要求确定性
- `EXC_{billID}_{exceptionType}` 可保证唯一性（同一 bill 同一类型异常只创建一次）
- 重试时可复现

### D8：settlement 模块是否新增 bootstrap 包？

**决策**：新增（P2 优先级）。

**理由**：
- §2.1 DDD 分层要求
- 避免 game 和 settlement 的依赖关系纠缠
- settlement 服务装配独立治理

---

## 9. 风险与缓解

### 9.1 风险：RPC-DB 中间态改造可能引入新 bug

**缓解**：
- 分阶段改造，每个 service 独立测试
- 保留对账调度器作为兜底
- 增加单元测试覆盖 Processing 状态转换

### 9.2 风险：事务抽象激活可能影响性能

**缓解**：
- 抽象层仅包装 GORM `Transaction`，无额外开销
- 子 repo eager init，无懒加载
- 压测验证

### 9.3 风险：乐观锁补齐可能导致 RowsAffected==0 误判

**缓解**：
- `RowsAffected==0` 返回 nil（幂等成功），不返回 error
- 增加 audit log 记录状态不匹配情况
- 监控 RowsAffected==0 的频率

### 9.4 风险：Lua 脚本迁移可能引入不一致

**缓解**：
- 迁移后保留原 `robot_lock.lua.go` 作为 re-export
- grep 确认所有调用点都用新脚本
- 单元测试验证 Lua 脚本行为

### 9.5 风险：伪事务修复可能改变业务行为

**缓解**：
- `SettleRound` / `SettleGame` 内部已有幂等保证
- 主事务回滚后 Kafka 重试时，`SettleRound` 幂等跳过已创建 bill
- 增加集成测试覆盖

---

## 10. 验证清单

### 10.1 编译与格式

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `gofmt -l` 无输出

### 10.2 锁相关

- [ ] grep `redis.Del(ctx, .*lock` 无裸 Del 释放锁
- [ ] grep `SetNX(ctx, key, "1"` 无 value="1" 锁值
- [ ] grep `SetNX(ctx, key, 1,` 无 value=1 锁值
- [ ] grep `AcquireRoomAssignLock` 调用方都有 `defer Release`
- [ ] grep `redsyncClient ==` 无全局单例检查
- [ ] grep `sync.Mutex` + nil 检查在 lock 包内为 0
- [ ] grep `LockKeyRoom` 死代码已删除
- [ ] grep `ReconcileLock` 死代码已删除
- [ ] `common/lock/manager_test.go` 测试通过

### 10.3 事务相关

- [ ] grep `s.db.Transaction(` 无直接调用（service 层）
- [ ] grep `c.db.Transaction(` 无直接调用（consumer 层）
- [ ] grep `dbRepo.WithTransaction` 有调用
- [ ] grep `if exists, _ :=` 在 deduct_service 为 0
- [ ] grep `return err$` 在 bill_manager 为 0（全部 `%w` 包装）
- [ ] grep `CreateBillsOnly` 与 `CreateBillsInTransaction` 仅保留一个

### 10.4 乐观锁相关

- [ ] `UpdateBillStatus` 有 `WHERE status = ?`
- [ ] `UpdateBillSuccess` 有 `WHERE status = ?`
- [ ] `UpdateBillRefundStatus` 有 `WHERE refund_status = ?`
- [ ] `UpdateGameSettleStatusByUser` 有 `WHERE settle_status = ?`
- [ ] `UpdateGameSettleStatusBySession` 有 `WHERE settle_status = ?`
- [ ] `UpdateRoundSettlementDeductSuccess` 有 `WHERE deduct_success = ?`
- [ ] `UpdateRoundSettlementSettleInfo` 有 `WHERE settle_info IS NULL`
- [ ] `UpdateRefundAuditError` 有 `WHERE status = ?`
- [ ] `UpdateRefundSuccessInTransaction` 有 `WHERE status = ?`（refund_audit + bill）

### 10.5 幂等相关

- [ ] `executeRefund` 有 Processing 中间态
- [ ] `executeSingleDeduct` 有 Processing 中间态
- [ ] `creditSessionPayout` 有 Processing 中间态
- [ ] `settlePlayer` 有 Processing 中间态
- [ ] `executeSessionCredit` 用 `IncrementRetryCountWithNextRetryTime` + 指数退避
- [ ] `SettleGame` 有锁外预检
- [ ] `DeductPenaltyToPlatform` 幂等检查扩展
- [ ] `ExceptionNo` 确定性生成

### 10.6 伪事务相关

- [ ] `handleRoundSettle` 的 `SettleRound` 调用在主事务外
- [ ] `handleSessionEnd` 的 `SettleGame` 调用在主事务外

### 10.7 配置相关

- [ ] `config/game.yaml` 有 `lock` 配置段
- [ ] 14 个业务锁 TTL 从配置读取
- [ ] grep `expirySeconds` 在 service 层为 0（全部从配置读）

### 10.8 规约相关

- [ ] `CODING_STANDARD.md` 新增 §18 锁与事务规约
- [ ] §18 包含 DL-1~DL-12 共 12 条锁规约
- [ ] §18 包含 TX-1~TX-12 共 12 条事务规约

---

## 附录：与成熟框架对比

### A.1 锁框架对比

| 特性 | 当前实现 | 重构后 | Redisson | 差距 |
|---|---|---|---|---|
| token + Lua 释放 | ⚠️ redsync 内部 | ✅ 自持脚本 | ✅ | 对齐 |
| watchdog | ✅ | ✅ + ctx 监听 | ✅ | 对齐 |
| panic recover | ❌ | ✅ | - | 对齐 |
| 加锁失败区分 | ❌ | ✅ 哨兵 error | ✅ | 对齐 |
| 可重入 | ❌ | ❌（D7 决策不做） | ✅ | 仍有差距 |
| 公平锁 | ❌ | ❌ | ✅ | 仍有差距 |
| RedLock | ❌ | ❌ | ✅ | 仍有差距 |

### A.2 事务框架对比

| 特性 | 当前实现 | 重构后 | Spring TX | 差距 |
|---|---|---|---|---|
| 统一抽象 | ❌ 死代码 | ✅ 激活 | ✅ | 对齐 |
| 传播行为 | ❌ | ❌ | ✅ | 仍有差距 |
| 隔离级别配置 | ❌ | ❌ | ✅ | 仍有差距 |
| 超时控制 | ❌ | ❌ | ✅ | 仍有差距 |
| 乐观锁 | 3 处 | 12 处 | - | 大幅改善 |
| RPC-DB 一致性 | ❌ | ✅ Processing | - | 对齐（项目特有） |

---

**文档版本**：v1
**编写日期**：2026-07-04
**基于 review**：26 个锁调用点、12 个事务调用点、15 个幂等检查点、12 个乐观锁检查点
