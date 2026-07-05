# 事务实现 Review 与重构方案（TRANSACTION_REFACTOR_PLAN.md）

> 基于 `backend/` 全量代码（171 个 Go 文件）事务实现的深度 review 编制。覆盖 11 处真实事务调用点、1 处死代码抽象层、4 处伪事务、9 处状态机 UPDATE、4 处 RPC-DB 一致性场景。本文档专注于事务（Transaction）维度，分布式锁维度见 [LOCK_TX_REFACTOR_PLAN.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/LOCK_TX_REFACTOR_PLAN.md)。

---

## 目录

1. [执行摘要](#1-执行摘要)
2. [当前事务实现盘点](#2-当前事务实现盘点)
3. [问题清单](#3-问题清单)
4. [事务最佳实践](#4-事务最佳实践)
5. [架构设计：事务应该放在哪一层](#5-架构设计事务应该放在哪一层)
6. [目标架构](#6-目标架构)
7. [分阶段重构计划](#7-分阶段重构计划)
8. [关键改造细节](#8-关键改造细节)
9. [决策记录](#9-决策记录)
10. [风险与缓解](#10-风险与缓解)
11. [验证清单](#11-验证清单)
12. [附录：与成熟框架对比](#12-附录与成熟框架对比)

---

## 1. 执行摘要

### 1.1 现状结论

| 维度 | 评价 | 说明 |
|---|---|---|
| 事务抽象层 | ❌ 死代码 | `domain.Transaction` / `WithTransaction` 已实现但全仓零调用 |
| 事务边界划分 | ⚠️ 不一致 | `BillManager` 与 `GameEventConsumer` 直接持有 `*gorm.DB` 开事务，违反 CODING_STANDARD §8.2 |
| 跨表原子写 | ✅ 基本正确 | 6 处 `db.Transaction` 包裹跨表写，事务粒度合理 |
| 跨服务调用 | ⚠️ 已拆分但注释不规范 | `handleRoundSettle` / `handleSessionEnd` 已将 `SettleRound` / `SettleGame` 移出主事务（注释明确说明），但缺乏统一的"事务边界划分"规约 |
| 乐观锁 | ✅ 已基本补齐 | 9 处状态机 UPDATE 方法均带 `WHERE status = ?` 条件并检查 `RowsAffected` |
| RPC-DB 一致性 | ❌ 缺失中间态 | `executeRefund` / `executeSingleDeduct` 等无 Processing 中间态，RPC 成功+DB 失败时存在重复调用风险 |
| 事务传播 | ❌ 不存在 | 无 RequiresNew / Nested / Supports 等传播行为 |
| 嵌套事务 | ⚠️ 未使用 | GORM savepoint 能力未被显式使用，`InTransaction` 后缀方法存在但未嵌套调用 |

### 1.2 核心问题

1. **事务抽象层是死代码**：`game/domain/db_repository.go` 定义了 `Transaction` 接口和 `WithTransaction` 方法，`game/infrastructure/persistence/mysql/db_repository.go` 实现了 `GormTransactionImpl`，但全仓零调用。所有事务都绕过抽象直接通过 `*gorm.DB.Transaction(...)` 开启。
2. **Service / Consumer 层直接持有 `*gorm.DB`**：`BillManager`（settlement/service/bill_manager.go:13-15）和 `GameEventConsumer`（game/infrastructure/messaging/game_event_consumer.go:32-38）直接持有 `*gorm.DB` 字段并开事务，违反 CODING_STANDARD §8.2"事务通过 `domain.Transaction.Execute` 调用，禁止 service 直接持有 `*gorm.DB` 开事务"。
3. **RPC-DB 一致性缺失**：4 处 RPC 调用（`executeRefund` / `executeSingleDeduct` / `creditSessionPayout` / `settlePlayer`）无 Processing 中间态，RPC 成功但本地 DB 失败时无法判断是否已调用 RPC，重试可能重复扣款/入账。
4. **应用层跨多 Repository 调用无原子性保证**：`GameAppService.initRoundAndDeduct` 调用 4 个 `RoundDBRepo` 方法各自独立提交，依赖补偿（`updateRoundFailed`）和幂等保证最终一致，但补偿失败时数据不一致。

### 1.3 推荐方案

- **激活事务抽象层**：将 `BillManager` 和 `GameEventConsumer` 的事务调用迁移到 `dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error { ... })`。
- **引入 RPC-DB 一致性中间态**：所有 RPC 调用前先置 Processing（带乐观锁），RPC 成功后置终态，失败后置 Failed，重试时查平台侧状态。
- **明确事务边界规约**：事务边界放在 **Application 层**（用例编排层），Repository 层提供原子操作，Service 层提供业务逻辑但不持有 `*gorm.DB`。
- **保留"事务外调用 + Kafka 重试 + 幂等"模式**：跨服务调用不在主事务内，依赖幂等保证最终一致性。

---

## 2. 当前事务实现盘点

### 2.1 事务抽象层（死代码）

**接口定义** - [game/domain/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/db_repository.go):

```go
// 行 55-60：事务内子 repo 聚合视图
type Transaction interface {
    RoomDBRepo() RoomDBRepository
    SessionDBRepo() SessionDBRepository
    UserDBRepo() UserDBRepository
    RoundDBRepo() RoundDBRepository
}

// 行 173-181：顶层仓储聚合
type DBRepository interface {
    RoomDBRepo() RoomDBRepository
    SessionDBRepo() SessionDBRepository
    UserDBRepo() UserDBRepository
    RoomConfigDBRepo() RoomConfigDBRepository
    RoundDBRepo() RoundDBRepository
    HistoryDBRepo() HistoryDBRepository
    WithTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
```

**实现** - [game/infrastructure/persistence/mysql/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/db_repository.go):

```go
// 行 42-47
func (r *DBRepositoryImpl) WithTransaction(ctx context.Context, fn func(tx domain.Transaction) error) error {
    return r.db.WithContext(ctx).Transaction(func(gormTx *gorm.DB) error {
        tx := NewGormTransaction(gormTx)
        return fn(tx)
    })
}

// 行 51-72：事务内子 repo 聚合，构造时一次性初始化
type GormTransactionImpl struct {
    db          *gorm.DB
    roomRepo    domain.RoomDBRepository
    sessionRepo domain.SessionDBRepository
    userRepo    domain.UserDBRepository
    roundRepo   domain.RoundDBRepository
}
```

**关键缺陷**：通过全仓 grep `dbRepo.WithTransaction` / `.WithTransaction(ctx` 验证，**抽象层在业务代码中零调用**。所有实际事务都绕过抽象，直接通过 `*gorm.DB.Transaction(...)` 开启。

### 2.2 真实事务调用点（11 处）

| # | 文件:行号 | 方法 | 边界 | 乐观锁 | 错误包装 |
|---|---|---|---|---|---|
| 1 | `game/infrastructure/persistence/mysql/session_repository.go:119` | `BatchUpdateSessionPlayerStats` | repo 内批量 | ❌ | ❌ |
| 2 | `settlement/service/bill_manager.go:200` | `CreateBillsInTransaction` | 批量创建 bills | - | ✅ |
| 3 | `settlement/service/bill_manager.go:211` | `CreateRoundSettlementAndBills` | 创建 settlement + bills | - | ✅ |
| 4 | `settlement/service/bill_manager.go:225` | `CreateBillsPairInTransaction` | 创建配对 bill | - | ✅ |
| 5 | `settlement/service/bill_manager.go:367` | `UpdateRefundSuccessInTransaction` | 更新 refund_audit + bill | ✅ | ✅ |
| 6 | `settlement/service/bill_manager.go:441` | `CreateRefundAuditAndUpdateBillRefundStatus` | 创建 refund_audit + 更新 bill | ✅ | ✅ |
| 7 | `settlement/service/bill_manager.go:487` | `RejectRefund` | 更新 refund_audit + bill | ✅ | ✅ |
| 8 | `game/infrastructure/messaging/game_event_consumer.go:168` | `handleSessionStart` | 创建 session + players | - | ✅ |
| 9 | `game/infrastructure/messaging/game_event_consumer.go:223` | `handlePacketCreated` | 更新 round + 创建 packets | - | ✅ |
| 10 | `game/infrastructure/messaging/game_event_consumer.go:291` | `handleRoundSettle` | 更新 round + grab_records + session_players + special_reward | - | ✅ |
| 11 | `game/infrastructure/messaging/game_event_consumer.go:458` | `handleSessionEnd` | 更新 session + session_players | - | ✅ |

### 2.3 直接持有 `*gorm.DB` 的位置

| 文件 | 行号 | 持有者 | 违反规约 |
|---|---|---|---|
| `settlement/service/bill_manager.go` | 13-15 | `BillManager.db *gorm.DB` | §8.2、§15.4 |
| `game/infrastructure/messaging/game_event_consumer.go` | 32-38 | `GameEventConsumer.db *gorm.DB` | §8.2 |

**说明**：`RefundService`、`DeductService`、`SettlementService`、`GameSettleService` 已通过 `BillManager` 间接访问 DB，不直接持有 `*gorm.DB`（P0-4 已修复）。但 `BillManager` 自身仍直接持有，且 `GameEventConsumer` 作为 infrastructure 层组件也直接持有。

### 2.4 事务边界划分方式

| 层 | 是否开事务 | 模式 |
|---|---|---|
| `application`（GameAppService / GrabService / PenaltyService） | ❌ | 通过 Redis 锁 + Lua 脚本 + 异步事件保证一致性 |
| `settlement/service`（SettlementService / RefundService / DeductService / GameSettleService） | ❌ | 通过 Redis 锁 + 状态机 + 幂等检查保证最终一致性 |
| `settlement/service/BillManager` | ✅ 6 处 | `db.Transaction(func(tx *gorm.DB) error { ... })` 自管理事务 |
| `game/infrastructure/messaging/GameEventConsumer` | ✅ 4 处 | `c.db.Transaction(...)` 直接持有 `*gorm.DB` |
| `game/infrastructure/persistence/mysql/*Repository` | ✅ 1 处 | 仅 `BatchUpdateSessionPlayerStats` 内部开事务 |
| `game/domain` + `db_repository.go` | 死代码 | `WithTransaction` 零调用 |

### 2.5 一致性保障机制（非 DB 事务）

由于业务流程大量依赖最终一致性而非 ACID 事务，需要明确以下机制：

1. **Redis 分布式锁**（`common/lock.WithRedisLock`）：14 处业务锁 + 6 处 scheduler 锁，防并发
2. **GORM 乐观锁**：`WHERE id = ? AND status = ?` 模式，9 处状态机 UPDATE 均已补齐
3. **幂等检查**：基于 `BizOrderNo` / `RoundTraceID` / `RefundOrderNo` 等确定性键 + DB 唯一索引
4. **状态机推进**：`Pending → Processing → Success/Failed/Refunded` 等单向推进（但部分流程缺 Processing 中间态）
5. **Kafka 重试**：消费失败时释放抢占锁让 Kafka 重新投递
6. **Redis Lua 脚本**：抢红包、发红包、结算等高频操作完全用 Lua 保证原子性
7. **补偿事务**：`updateRoundFailed` / `handleFirstRoundDeductFailure` 等显式补偿

### 2.6 跨服务调用事务边界

`game_event_consumer.go` 中 `handleRoundSettle` / `handleSessionEnd` 采用"事务外调用 + Kafka 重试 + 幂等"模式：

```go
// game_event_consumer.go:291-425 handleRoundSettle
if err := c.db.Transaction(func(tx *gorm.DB) error {
    // 主事务：更新 round、grab_records、session_players、special_reward
    return nil
}); err != nil {
    return err
}
// SettleRound 在主事务之外调用：主事务已提交 round 数据，
// SettleRound 创建平台账 bill。若 SettleRound 失败，Kafka 重试重新投递事件；
// SettleRound 内部幂等（RoundStatusCredited 早返回 + GetBillByRoundTypeAndUser 跳过已创建 bill）。
if err := c.settlementService.SettleRound(ctx, settleReq); err != nil {
    return fmt.Errorf("settle round failed: %w", err)
}
```

**评价**：该模式正确（跨服务调用不在主事务内），但缺乏统一的"事务边界划分"规约文档化，新开发者难以判断何时该用事务、何时该用最终一致性。

### 2.7 `InTransaction` 模式（预留扩展点）

`BillManager` 提供两个 `InTransaction` 后缀方法，接受外部 `tx *gorm.DB`，允许调用方在已存在的事务内复用：

- `CreateRefundAuditAndUpdateBillRefundStatusInTransaction`（行 415-436）
- `RejectRefundInTransaction`（行 449-482）

```go
// 自管理事务版本（service 层调用）
func (m *BillManager) RejectRefund(ctx context.Context, ...) error {
    return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        return m.RejectRefundInTransaction(ctx, tx, ...)
    })
}

// InTransaction 版本（外层事务内调用）
func (m *BillManager) RejectRefundInTransaction(ctx context.Context, tx *gorm.DB, ...) error {
    // 直接使用传入的 tx，不再开新事务
}
```

**现状**：当前没有调用方在 `db.Transaction` 内部再调用这些 `InTransaction` 方法。这是预留的扩展点，用于未来需要跨多个 Manager 的原子操作。

---

## 3. 问题清单

### 3.1 P0（资金安全，必须修复）

| ID | 问题 | 位置 | 影响 |
|---|---|---|---|
| TX-P0-1 | 事务抽象层是死代码，全仓零调用 | `game/domain/db_repository.go`、`game/infrastructure/persistence/mysql/db_repository.go` | §8.2 规约形同虚设；事务边界治理无统一入口；新开发者难以判断事务该放哪一层 |
| TX-P0-2 | `BillManager` 直接持有 `*gorm.DB` 开事务 | `settlement/service/bill_manager.go:13-15` | 违反 §8.2、§15.4；BillManager 既是数据访问层又开事务，职责混乱；难以 mock 测试 |
| TX-P0-3 | `GameEventConsumer` 直接持有 `*gorm.DB` 开事务 | `game/infrastructure/messaging/game_event_consumer.go:32-38` | 违反 §8.2；infrastructure 层直接操作 DB 而非通过 Repository 抽象 |
| TX-P0-4 | RPC-DB 一致性缺失 Processing 中间态 | `refund_service.go:executeRefund`、`deduct_service.go:executeSingleDeduct`、`game_settle_service.go:creditSessionPayout/settlePlayer` | RPC 成功 + DB 失败时无本地标记，重试可能重复扣款/入账 |
| TX-P0-5 | `session_repository.go:119` repo 内开事务无乐观锁 | `game/infrastructure/persistence/mysql/session_repository.go:114-139` | `BatchUpdateSessionPlayerStats` 批量 UPDATE 无 `WHERE status = ?` 条件，并发可覆盖 |

### 3.2 P1（一致性，近期修复）

| ID | 问题 | 位置 |
|---|---|---|
| TX-P1-1 | 应用层跨多 Repository 调用无原子性 | `game/application/game_app_service.go:initRoundAndDeduct`（4 个 RoundDBRepo 方法各自独立提交） |
| TX-P1-2 | `Transaction` 接口缺 `HistoryDBRepo` / `RoomConfigDBRepo` / `BillRepo` | `game/domain/db_repository.go:55-60`（事务内无法访问全部子 repo） |
| TX-P1-3 | `settlement` 模块无独立 `bootstrap` 包 | settlement 服务装配混在 `game/bootstrap/`，事务抽象层无法注入到 settlement service |
| TX-P1-4 | 缺乏事务超时控制 | 所有 `db.Transaction` 无 ctx 超时，长事务可能阻塞连接池 |
| TX-P1-5 | `BatchUpdateSessionPlayerStats` 在 Repository 内开事务 | `session_repository.go:114` 违反"事务边界在 Application/Service 层"原则 |

### 3.3 P2（架构治理，中期重构）

| ID | 问题 | 位置 |
|---|---|---|
| TX-P2-1 | 无事务传播行为配置 | 无法表达 Required / RequiresNew / Nested 等语义 |
| TX-P2-2 | 无事务隔离级别配置 | 全部使用默认隔离级别（RR），无法针对场景优化 |
| TX-P2-3 | 无事务监控 metrics | 事务耗时、成功率、回滚率无采集 |
| TX-P2-4 | `InTransaction` 模式未被使用 | 预留的 tx 复用能力闲置，未来跨 Manager 原子操作需重新设计 |
| TX-P2-5 | 缺乏事务边界划分的统一规约文档 | 新开发者难以判断何时用事务、何时用最终一致性 |

### 3.4 P3（细节优化）

| ID | 问题 |
|---|---|
| TX-P3-1 | 无嵌套事务（GORM savepoint 未显式使用） |
| TX-P3-2 | 无分布式事务支持（XA / TCC / SAGA）—— 当前业务不需要 |
| TX-P3-3 | 事务内日志缺失（无事务 ID 关联日志） |

---

## 4. 事务最佳实践

### 4.1 事务边界划分原则

#### 4.1.1 事务应该放在哪一层？

**业界共识**（DDD + Clean Architecture）：

| 层 | 是否开事务 | 理由 |
|---|---|---|
| **Presentation**（handler / controller） | ❌ | 只负责协议转换，不含业务逻辑 |
| **Application**（用例编排 / use case） | ✅ **推荐** | 跨多个 Repository 的原子操作在此编排，事务边界最自然 |
| **Domain**（领域服务 / 实体） | ❌（但有例外） | 领域服务不含技术细节；但聚合根的原子操作可由 Application 层包裹事务 |
| **Infrastructure / Repository** | ❌（除批量操作） | 单个 Repository 操作应是原子的（单条 SQL 已自动原子）；批量操作可内部开事务但需谨慎 |
| **Consumer / Scheduler** | ⚠️ 间接 | 应通过调用 Application 层 service 间接受益于事务，而非直接开事务 |

**推荐**：**事务边界放在 Application 层**（用例编排层），通过 `dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error { ... })` 调用。

#### 4.1.2 事务粒度原则

1. **短事务原则**（MUST）：事务应尽可能短，避免长事务持有锁过久。事务内禁止：
   - 调用外部 RPC / HTTP 接口
   - 调用 Kafka Producer（应事务提交后发送）
   - 执行耗时计算
   - 等待用户输入

2. **跨表原子写原则**（MUST）：跨多个表的写操作必须在同一事务内。

3. **跨服务调用移出事务原则**（MUST）：跨服务调用（RPC、HTTP）必须移出主事务，依赖幂等 + 重试保证最终一致性。

4. **读操作移出事务原则**（SHOULD）：事务内只包含必须原子的写操作，读操作尽量在事务外。

### 4.2 事务抽象层设计原则

#### 4.2.1 Unit of Work 模式（推荐）

```go
// domain/transaction.go
type Transaction interface {
    // 事务内的子 repo 聚合视图
    BillRepo() BillRepository
    RoundRepo() RoundRepository
    SessionRepo() SessionRepository
    // ... 全部子 repo
}

type DBRepository interface {
    // 顶层聚合访问（非事务场景）
    BillRepo() BillRepository
    RoundRepo() RoundRepository
    // ...
    // 事务入口
    WithTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
```

**优点**：
- 事务边界显式（`WithTransaction` 调用即事务边界）
- 子 repo 自动使用事务 `*gorm.DB`，无需手动传递
- 接口隔离：业务代码依赖 `domain.Transaction` 接口，不依赖 `*gorm.DB`

#### 4.2.2 Spring `@Transactional` 装饰器模式（不推荐用于 Go）

Go 无注解，需用装饰器或中间件实现，增加复杂度。当前项目不采用。

#### 4.2.3 函数式事务（当前 GORM 模式）

```go
db.Transaction(func(tx *gorm.DB) error {
    // 业务逻辑
    return nil
})
```

**问题**：`tx *gorm.DB` 泄漏到业务层，违反依赖倒置；无法 mock；事务边界分散在调用点。

### 4.3 乐观锁 vs 悲观锁

#### 4.3.1 乐观锁（推荐）

适用场景：状态机推进、并发冲突较少、读多写少。

```go
result := tx.Model(&BillRecord{}).
    Where("id = ? AND status = ?", billID, fromStatus).
    Updates(updates)
if result.RowsAffected == 0 {
    // 已被其他事务处理，视为幂等成功
    return nil
}
```

**优点**：无锁等待、性能高、自动重试友好。
**缺点**：冲突时需重试（但本项目通过幂等设计已规避）。

#### 4.3.2 悲观锁（`SELECT ... FOR UPDATE`）

适用场景：资金扣减、库存扣减等强一致性场景。

```go
tx.Clauses(clause.Locking{Strength: "UPDATE"}).
    Where("id = ?", billID).
    First(&bill)
```

**当前项目不采用**：资金操作通过 RPC 调用平台侧，本地 DB 只记录状态，无需悲观锁。

### 4.4 RPC-DB 一致性模式

#### 4.4.1 Processing 中间态模式（推荐）

```
1. 本地置 Processing（带乐观锁 WHERE status = ?）
2. 调 RPC（BizID 确定性 → 平台侧幂等）
3a. RPC 成功 → 本地置 Success
3b. RPC 失败 → 本地置 Failed
4. 重试时若已是 Processing → 先查平台侧状态
    - 平台已成功 → 本地置 Success
    - 平台未成功 → 重新调 RPC
```

**适用场景**：本地 DB + 远程 RPC 的混合操作。

#### 4.4.2 Saga 模式（不推荐用于当前项目）

适用场景：跨多个服务的长事务。实现复杂，需补偿事务。当前业务通过幂等 + 重试已满足需求。

#### 4.4.3 TCC 模式（不推荐用于当前项目）

适用场景：强一致性跨服务事务。需 Try / Confirm / Cancel 三个接口。当前平台侧 RPC 不支持 TCC。

### 4.5 幂等性设计原则

#### 4.5.1 三层防线（MUST）

1. **Redis SetNX 抢占**：防止并发重复处理
2. **DB 唯一索引**：幂等键（`BizOrderNo` / `RoundTraceID` / `RefundOrderNo`）必须有唯一索引
3. **状态机检查**：进入逻辑后先查现有状态，已是终态则直接返回 `nil`

#### 4.5.2 幂等键确定性生成（MUST）

```go
// ✅ 正确：基于业务上下文确定性生成
BizOrderNo := fmt.Sprintf("%s_%d_%d", roundTraceID, billType, userID)

// ❌ 错误：时间戳 + 随机数，重试时无法复现
ExceptionNo := fmt.Sprintf("EXC_%d_%d", time.Now().Unix(), rand.Intn(10000))
```

---

## 5. 架构设计：事务应该放在哪一层

### 5.1 推荐架构：事务边界在 Application 层

```
┌─────────────────────────────────────────────────────────────┐
│ Presentation Layer (gRPC handler / HTTP handler)            │
│   - 协议转换、参数校验                                       │
│   - 不开事务                                                 │
└────────────────────────┬────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────┐
│ Application Layer (GameAppService / SettlementAppService)   │
│   - 用例编排：跨多个 Repository 的原子操作                   │
│   - ★ 事务边界在此 ★                                        │
│   - dbRepo.WithTransaction(ctx, func(tx Transaction) {     │
│         tx.BillRepo().Create(...)                           │
│         tx.RoundRepo().Update(...)                          │
│     })                                                      │
│   - 跨服务调用移出事务（事务提交后调用）                      │
└────────────────────────┬────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────┐
│ Domain Layer (Service / Entity)                             │
│   - 业务逻辑：状态机校验、业务规则                           │
│   - 不开事务（事务由 Application 层管理）                    │
│   - 依赖 Repository 接口，不依赖 *gorm.DB                    │
└────────────────────────┬────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────┐
│ Infrastructure Layer (Repository 实现 / Consumer)           │
│   - GORM Repository 实现 domain.Repository 接口             │
│   - 单条操作自动原子（GORM 内置）                            │
│   - 批量操作可接受外部 tx 参数（InTransaction 模式）         │
│   - Consumer 调用 Application 层 service，不直接开事务       │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 各层职责明确

| 层 | 事务职责 | 实现方式 |
|---|---|---|
| Presentation | 不开事务 | 只做协议转换 |
| Application | **开事务** | `dbRepo.WithTransaction(ctx, func(tx) { ... })` |
| Domain Service | 不开事务 | 纯业务逻辑，依赖 Repository 接口 |
| Repository（单条） | 自动原子 | GORM 单条 Create/Update/Delete 已原子 |
| Repository（批量） | 接受外部 tx | `InTransaction(ctx, tx, ...)` 方法 |
| Consumer | 不开事务 | 调用 Application 层 service |
| Scheduler | 不开事务 | 调用 Application 层 service |

### 5.3 事务边界划分规约

| 场景 | 是否开事务 | 模式 |
|---|---|---|
| 单表单条写 | ❌ | GORM 自动原子 |
| 单表批量写 | ✅ | `WithTransaction` 包裹 |
| 跨表原子写（同服务） | ✅ | `WithTransaction` 包裹 |
| 跨多 Repository 写（同服务） | ✅ | `WithTransaction` 包裹，事务内通过 `tx.XxxRepo()` 访问 |
| 跨服务调用（RPC） | ❌ | 事务外调用 + 幂等 + 重试 |
| 跨服务调用 + 本地写 | ❌ | 本地写事务提交后调 RPC；RPC 失败靠重试 + 幂等 |
| 读操作 | ❌ | 事务外执行 |
| 状态机推进 | ❌ | 乐观锁 `WHERE status = ?` 已保证原子 |

### 5.4 settlement 模块特殊设计

settlement 模块的 `BillManager` 当前同时承担"Repository 实现"和"事务管理"双重职责，需拆分：

**Before**（当前）：
```go
// BillManager 既是 Repository 又开事务
type BillManager struct {
    db *gorm.DB
}
func (m *BillManager) CreateRoundSettlementAndBills(ctx, settlement, bills) error {
    return m.db.Transaction(func(tx *gorm.DB) error { ... })
}
```

**After**（目标）：
```go
// BillRepository 接口（domain 层）
type BillRepository interface {
    Create(ctx context.Context, bill *model.BillRecord) error
    CreateInTransaction(ctx context.Context, tx Transaction, bill *model.BillRecord) error
    UpdateStatus(ctx context.Context, billID int64, fromStatus, toStatus int) error
    // ...
}

// Application 层开事务
func (s *SettlementAppService) SettleRound(ctx context.Context, req *dto.RoundSettleRequest) error {
    return s.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        if err := tx.BillRepo().CreateInTransaction(ctx, tx, bill); err != nil { ... }
        if err := tx.RoundRepo().UpdateInTransaction(ctx, tx, round); err != nil { ... }
        return nil
    })
}
```

---

## 6. 目标架构

### 6.1 事务抽象层设计

**目标**：激活 `domain.Transaction` 接口，扩展为覆盖全部子 repo 的统一事务入口。

```go
// game/domain/transaction.go（新增独立文件）
package domain

import "context"

// Transaction 事务内子 repo 聚合视图
type Transaction interface {
    BillRepo() BillRepository
    RoundRepo() RoundRepository
    SessionRepo() SessionRepository
    RoomRepo() RoomRepository
    UserRepo() UserRepository
    RoomConfigRepo() RoomConfigRepository
    HistoryRepo() HistoryRepository
    RefundRepo() RefundRepository
    // 所有子 repo
}

// TransactionManager 事务管理器接口
type TransactionManager interface {
    // WithTransaction 在事务内执行 fn，fn 返回 error 则回滚
    WithTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
```

### 6.2 实现层

```go
// game/infrastructure/persistence/mysql/transaction.go
type GormTransaction struct {
    db          *gorm.DB
    billRepo    domain.BillRepository
    roundRepo   domain.RoundRepository
    sessionRepo domain.SessionRepository
    // ... 全部子 repo，构造时 eager init
}

func NewGormTransaction(db *gorm.DB) *GormTransaction {
    return &GormTransaction{
        db:          db,
        billRepo:    NewGormBillRepository(db),
        roundRepo:   NewGormRoundRepository(db),
        sessionRepo: NewGormSessionRepository(db),
        // ...
    }
}

// 各子 repo 方法接受 tx 参数
func (t *GormTransaction) BillRepo() domain.BillRepository { return t.billRepo }
```

### 6.3 事务上下文传递

子 Repository 实现需要支持"事务上下文"——即方法内使用事务的 `*gorm.DB` 而非自有的 `*gorm.DB`。

**方案 A**（推荐）：Repository 方法接受 `tx` 参数

```go
type BillRepository interface {
    Create(ctx context.Context, bill *model.BillRecord) error  // 非事务
    CreateInTx(ctx context.Context, tx domain.Transaction, bill *model.BillRecord) error  // 事务内
}
```

**方案 B**：Repository 持有 `*gorm.DB`，事务内通过 `tx.BillRepo()` 获取事务版本

```go
type GormTransaction struct {
    db *gorm.DB
}

func (t *GormTransaction) BillRepo() domain.BillRepository {
    return NewGormBillRepository(t.db)  // 用事务的 db
}
```

**推荐方案 B**：更简洁，业务代码无感知，事务边界只在 Application 层显式。

### 6.4 Consumer 层改造

```go
// Before（当前）
type GameEventConsumer struct {
    db *gorm.DB  // ❌ 直接持有
}

func (c *GameEventConsumer) handleRoundSettle(ctx, msg) error {
    return c.db.Transaction(func(tx *gorm.DB) error { ... })
}

// After（目标）
type GameEventConsumer struct {
    dbRepo domain.DBRepository  // ✅ 通过抽象
    settlementAppService SettlementAppService  // ✅ 调用 Application 层
}

func (c *GameEventConsumer) handleRoundSettle(ctx, msg) error {
    // 主事务：更新 round、grab_records、session_players
    if err := c.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        if err := tx.RoundRepo().Update(ctx, round); err != nil { ... }
        if err := tx.GrabRecordRepo().BatchCreate(ctx, records); err != nil { ... }
        return nil
    }); err != nil {
        return err
    }
    // 跨服务调用移出事务
    return c.settlementAppService.SettleRound(ctx, settleReq)
}
```

### 6.5 BillManager 改造

```go
// Before（当前）
type BillManager struct {
    db *gorm.DB  // ❌
}

// After（目标）
type BillRepository interface {
    Create(ctx context.Context, bill *model.BillRecord) error
    GetByID(ctx context.Context, billID int64) (*model.BillRecord, error)
    UpdateStatus(ctx context.Context, billID int64, fromStatus, toStatus int) error
    // ...
}

// BillManager 不再直接持有 db，改为依赖 BillRepository 接口
type RefundService struct {
    billRepo domain.BillRepository  // ✅ 接口依赖
    dbRepo   domain.DBRepository    // ✅ 事务入口
}

func (s *RefundService) RejectRefund(ctx context.Context, refundID int64) error {
    return s.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        if err := tx.RefundRepo().UpdateStatus(ctx, refundID, ...); err != nil { ... }
        if err := tx.BillRepo().UpdateRefundStatus(ctx, billID, ...); err != nil { ... }
        return nil
    })
}
```

---

## 7. 分阶段重构计划

### Phase 1：激活事务抽象层（P0，2-3 天）

**任务**：
1. 扩展 `domain.Transaction` 接口，覆盖全部子 repo（BillRepo / RefundRepo / HistoryRepo / RoomConfigRepo）
2. 修改 `GormTransactionImpl`，eager init 全部子 repo
3. 新增 `domain.TransactionManager` 接口
4. 在 `bootstrap/container.go` 注入 `DBRepository`（已包含 `WithTransaction`）

**验证**：
- `go build ./...`
- `go vet ./...`
- `gofmt -l`

### Phase 2：BillManager 下沉为 Repository（P0，2-3 天）

**任务**：
1. 将 `BillManager` 拆分为：
   - `BillRepository` 接口（domain 层）：单条操作 + `InTransaction` 版本
   - `gormBillRepository` 实现（infrastructure 层）
   - `RefundRepository` 接口 + 实现
2. `RefundService` / `DeductService` / `SettlementService` / `GameSettleService` 改为依赖 `BillRepository` 接口 + `DBRepository`（事务入口）
3. 跨表原子写（如 `CreateRoundSettlementAndBills`）改为 Application 层通过 `WithTransaction` 编排

**验证**：
- `go build ./settlement/...`
- grep 确认 `BillManager` 无 `*gorm.DB` 字段
- grep 确认 `billRepo.WithTransaction` 有调用

### Phase 3：GameEventConsumer 改造（P0，2 天）

**任务**：
1. `GameEventConsumer` 移除 `db *gorm.DB` 字段，改为 `dbRepo domain.DBRepository`
2. 4 处 `c.db.Transaction(...)` 改为 `c.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error { ... })`
3. 事务内 DB 操作通过 `tx.RoundRepo()` / `tx.SessionRepo()` 等访问
4. 跨服务调用（`SettleRound` / `SettleGame`）保持事务外调用

**验证**：
- `go build ./game/infrastructure/messaging/...`
- grep 确认 `c.db.Transaction` 无调用
- grep 确认 `c.dbRepo.WithTransaction` 有调用

### Phase 4：RPC-DB 一致性中间态（P0，2-3 天）

**任务**：
1. `executeRefund`：调 RPC 前置 refund_audit 为 Processing（带乐观锁），RPC 成功后置 Refunded，失败置 Rejected
2. `executeSingleDeduct`：调 RPC 前置 bill 为 Processing，RPC 成功后置 Success，失败置 Failed
3. `creditSessionPayout` / `settlePlayer`：同上
4. 重试时若已是 Processing，先查平台侧状态（待平台 `QueryStatus` 接口可用后实施）

**验证**：
- `go build ./settlement/...`
- grep 确认所有 RPC 调用前都有 Processing 置位

### Phase 5：session_repository 改造（P1，1 天）

**任务**：
1. `BatchUpdateSessionPlayerStats` 从 Repository 内部事务迁移到 Application 层
2. 或保留在 Repository 但接受外部 `tx` 参数（`InTransaction` 模式）
3. 补充乐观锁条件（`WHERE session_id = ? AND user_id = ?`）

**验证**：
- `go build ./game/infrastructure/persistence/mysql/...`
- grep 确认 Repository 内无 `db.Transaction` 直接调用

### Phase 6：settlement 模块 bootstrap 包（P1，1-2 天）

**任务**：
1. 新增 `settlement/bootstrap/` 包
2. 将 settlement 服务装配从 `game/bootstrap/` 独立
3. 注入 `DBRepository` 到 settlement service

**验证**：
- `go build ./settlement/...`
- settlement 服务启动正常

### Phase 7：事务超时与监控（P2，1 天）

**任务**：
1. `WithTransaction` 增加 ctx 超时（默认 30s，可配置）
2. 采集事务 metrics：耗时、成功率、回滚率
3. 长事务告警（> 5s）

**验证**：
- 配置文件有事务超时配置
- metrics 上报正常

### Phase 8：规约文档化（P2，半天）

**任务**：
1. 在 `CODING_STANDARD.md` §8.2 补充"事务边界划分规约"详细说明
2. 更新本文档为最终版本
3. 团队培训

---

## 8. 关键改造细节

### 8.1 激活事务抽象层

```go
// game/domain/transaction.go（新增）
package domain

import "context"

// Transaction 事务内子 repo 聚合视图
type Transaction interface {
    BillRepo() BillRepository
    RoundRepo() RoundRepository
    SessionRepo() SessionRepository
    RoomRepo() RoomRepository
    UserRepo() UserRepository
    RoomConfigRepo() RoomConfigRepository
    HistoryRepo() HistoryRepository
    RefundRepo() RefundRepository
    GrabRecordRepo() GrabRecordRepository
    SpecialRewardRepo() SpecialRewardRepository
}

// DBRepository 顶层仓储聚合
type DBRepository interface {
    // 非事务访问
    BillRepo() BillRepository
    RoundRepo() RoundRepository
    // ...
    // 事务入口
    WithTransaction(ctx context.Context, fn func(tx Transaction) error) error
}
```

### 8.2 BillManager 改造为 BillRepository

```go
// Before（settlement/service/bill_manager.go:13-15）
type BillManager struct {
    db *gorm.DB
}

func (m *BillManager) CreateRoundSettlementAndBills(ctx context.Context, settlement *model.RoundSettlement, bills []*model.BillRecord) error {
    return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        if err := tx.Create(settlement).Error; err != nil { ... }
        for _, bill := range bills {
            if err := tx.Create(bill).Error; err != nil { ... }
        }
        return nil
    })
}

// After
// settlement/domain/bill_repository.go（接口）
type BillRepository interface {
    Create(ctx context.Context, bill *model.BillRecord) error
    BatchCreate(ctx context.Context, bills []*model.BillRecord) error
    GetByID(ctx context.Context, billID int64) (*model.BillRecord, error)
    UpdateStatus(ctx context.Context, billID int64, fromStatus, toStatus int, errMsg string) error
    UpdateSuccess(ctx context.Context, billID int64, fromStatus int, balanceBefore, balanceAfter int64) error
    UpdateRefundStatus(ctx context.Context, billID int64, fromRefundStatus, toRefundStatus int, refundOrderNo string) error
    ExistsByRoundAndType(ctx context.Context, roundID int64, billType int) (bool, error)
    GetByRoundTypeAndUser(ctx context.Context, roundID int64, billType int, userID int64) (*model.BillRecord, error)
    // ...
}

// settlement/infrastructure/persistence/mysql/bill_repository.go（实现）
type gormBillRepository struct {
    db *gorm.DB
}

func NewGormBillRepository(db *gorm.DB) domain.BillRepository {
    return &gormBillRepository{db: db}
}

func (r *gormBillRepository) Create(ctx context.Context, bill *model.BillRecord) error {
    if err := r.db.WithContext(ctx).Create(bill).Error; err != nil {
        return fmt.Errorf("create bill failed: %w", err)
    }
    return nil
}

// Application 层开事务
func (s *SettlementAppService) CreateRoundSettlementAndBills(ctx context.Context, settlement *model.RoundSettlement, bills []*model.BillRecord) error {
    return s.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        if err := tx.RoundSettlementRepo().Create(ctx, settlement); err != nil {
            return fmt.Errorf("create round settlement failed: %w", err)
        }
        if err := tx.BillRepo().BatchCreate(ctx, bills); err != nil {
            return fmt.Errorf("create bills failed: %w", err)
        }
        return nil
    })
}
```

### 8.3 GameEventConsumer 改造

```go
// Before（game/infrastructure/messaging/game_event_consumer.go:32-38）
type GameEventConsumer struct {
    db                  *gorm.DB
    redis               *cRedis.Client
    settlementService   *settlementService.SettlementService
    // ...
}

func (c *GameEventConsumer) handleRoundSettle(ctx context.Context, msg *GameEventMessage) error {
    return c.db.Transaction(func(tx *gorm.DB) error {
        // 更新 round
        if err := tx.Model(&model.Round{}).Where("round_id = ?", ...).Updates(...).Error; err != nil { ... }
        // 创建 grab_records
        for _, r := range data.Results {
            if err := tx.Where(...).Assign(...).FirstOrCreate(...).Error; err != nil { ... }
        }
        // 调用 SettleRound ❌ 伪事务（已修复：移出主事务）
        return c.settlementService.SettleRound(ctx, settleReq)
    })
}

// After
type GameEventConsumer struct {
    dbRepo               domain.DBRepository
    redis                *cRedis.Client
    settlementAppService SettlementAppService
    // ...
}

func (c *GameEventConsumer) handleRoundSettle(ctx context.Context, msg *GameEventMessage) error {
    // 1. 主事务：更新 round、grab_records、session_players、special_reward
    if err := c.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        if err := tx.RoundRepo().UpdateStatus(ctx, roundID, RoundStatusEnded); err != nil {
            return fmt.Errorf("update round failed: %w", err)
        }
        if err := tx.GrabRecordRepo().BatchCreate(ctx, records); err != nil {
            return fmt.Errorf("create grab records failed: %w", err)
        }
        if err := tx.SessionPlayerRepo().BatchUpdateStats(ctx, sessionID, stats); err != nil {
            return fmt.Errorf("update session players failed: %w", err)
        }
        if data.RewardType > 0 {
            if err := tx.SpecialRewardRepo().Create(ctx, reward); err != nil { ... }
        }
        return nil
    }); err != nil {
        return err
    }
    // 2. 跨服务调用移出事务
    return c.settlementAppService.SettleRound(ctx, settleReq)
}
```

### 8.4 RPC-DB 一致性中间态（executeRefund 示例）

```go
// Before（refund_service.go:executeRefund）
func (s *RefundService) executeRefund(ctx context.Context, refund *model.RefundAudit) error {
    if refund.Status == dto.RefundStatusRefunded { return nil }
    // 直接调 RPC，无 Processing 中间态
    creditResult, err := s.platform.Credit(ctx, creditReq)
    if err != nil {
        if retryErr := s.billMgr.UpdateRefundAuditToPendingForRetry(...); retryErr != nil { ... }
        return fmt.Errorf("platform refund failed: %w", err)
    }
    return s.billMgr.UpdateRefundSuccessInTransaction(ctx, refund.ID, dto.RefundStatusProcessing, dto.BillStatusSuccess, platformTransID, time.Now())
}

// After
func (s *RefundService) executeRefund(ctx context.Context, refund *model.RefundAudit) error {
    // 1. 幂等检查：已 Refunded 直接返回
    if refund.Status == dto.RefundStatusRefunded { return nil }

    // 2. 重试时若已是 Processing，先查平台侧状态（待平台 QueryStatus 接口可用后实施）
    if refund.Status == dto.RefundStatusProcessing {
        // TODO: 平台 QueryStatus 接口可用后，先查平台侧状态
        // settled, err := s.platform.QueryStatus(ctx, refund.RefundOrderNo)
        // if settled { return s.billRepo.UpdateRefundSuccessInTransaction(...) }
    }

    // 3. 本地置 Processing（带乐观锁 WHERE status = 'Approved'）
    if err := s.billRepo.UpdateRefundAuditToProcessing(ctx, refund.ID, dto.RefundStatusApproved); err != nil {
        return fmt.Errorf("update refund audit to processing failed: %w", err)
    }

    // 4. 调 RPC（BizID 确定性 → 平台侧幂等）
    creditResult, err := s.platform.Credit(ctx, creditReq)
    if err != nil {
        // RPC 失败 → 置回 Pending 允许重试
        if retryErr := s.billRepo.UpdateRefundAuditToPendingForRetry(ctx, refund.ID, dto.RefundStatusProcessing, err.Error()); retryErr != nil {
            logger.Error("update refund audit to pending for retry failed", "refund_id", refund.ID, "error", retryErr)
        }
        return fmt.Errorf("platform refund failed: %w", err)
    }

    // 5. RPC 成功 → 置 Refunded（事务内更新 refund_audit + bill）
    return s.dbRepo.WithTransaction(ctx, func(tx domain.Transaction) error {
        if err := tx.RefundRepo().UpdateToRefunded(ctx, refund.ID, dto.RefundStatusProcessing, platformTransID, time.Now()); err != nil {
            return fmt.Errorf("update refund to refunded failed: %w", err)
        }
        if err := tx.BillRepo().UpdateToRefunded(ctx, refund.BillID, dto.BillStatusSuccess); err != nil {
            return fmt.Errorf("update bill to refunded failed: %w", err)
        }
        return nil
    })
}
```

### 8.5 事务超时控制

```go
// WithTransaction 增加超时控制
func (r *DBRepositoryImpl) WithTransaction(ctx context.Context, fn func(tx domain.Transaction) error) error {
    // 派生带超时的 ctx（默认 30s，可配置）
    txCtx, cancel := context.WithTimeout(ctx, r.txTimeout)
    defer cancel()
    return r.db.WithContext(txCtx).Transaction(func(gormTx *gorm.DB) error {
        tx := NewGormTransaction(gormTx)
        return fn(tx)
    })
}
```

---

## 9. 决策记录

### D1：激活还是删除事务抽象层？

**决策**：激活。

**理由**：
- 当前"死代码"状态最差，既误导又增加维护负担
- 激活后可统一治理事务边界、传播、超时、乐观锁
- 删除则 §8.2 规约形同虚设
- DDD 分层要求 Application 层通过抽象开事务，不直接依赖 `*gorm.DB`

### D2：事务边界放在哪一层？

**决策**：Application 层（用例编排层）。

**理由**：
- Application 层是跨多个 Repository 的原子操作编排点，事务边界最自然
- Repository 层单条操作已自动原子，批量操作可接受外部 tx
- Domain Service 层不持有技术细节，不应开事务
- Consumer / Scheduler 应通过调用 Application 层间接受益于事务

### D3：BillManager 改造为 Repository 还是保留？

**决策**：拆分为 `BillRepository` 接口 + `gormBillRepository` 实现。

**理由**：
- `BillManager` 当前同时承担"Repository 实现"和"事务管理"双重职责，违反单一职责
- 拆分后事务边界上移到 Application 层，BillRepository 只提供原子操作
- 接口化便于 mock 测试

### D4：RPC-DB 一致性用 Processing 中间态还是 Saga？

**决策**：Processing 中间态。

**理由**：
- Saga 模式实现复杂，需补偿事务
- Processing 中间态 + 平台侧 BizID 幂等已能满足当前业务
- 重试时查平台侧状态可覆盖 RPC 成功 + DB 失败的窗口

### D5：事务传播行为是否实现？

**决策**：暂不实现。

**理由**：
- 当前业务无嵌套事务需求
- `InTransaction` 模式（接受外部 tx）已预留扩展点
- Spring 风格的 Propagation RequiresNew / Nested 等增加复杂度，收益不明显

### D6：事务超时是否实现？

**决策**：实现，默认 30s。

**理由**：
- 防止长事务阻塞连接池
- 与 GORM `Transaction` 配合 ctx 超时即可实现
- 超时时间可配置

### D7：跨服务调用如何在事务边界内处理？

**决策**：移出主事务，依赖幂等 + 重试。

**理由**：
- 跨服务调用不能回滚，主事务内调用会产生"伪事务"
- `SettleRound` / `SettleGame` 内部已有完整的事务 + 幂等 + 锁保护
- 拆分后事务边界清晰

### D8：settlement 模块是否新增独立 bootstrap 包？

**决策**：新增。

**理由**：
- §2.1 DDD 分层要求
- settlement 服务的装配应独立于 game 服务
- 便于 settlement 服务独立部署（未来微服务化）

---

## 10. 风险与缓解

### 10.1 风险：事务抽象激活可能影响性能

**缓解**：
- 抽象层仅包装 GORM `Transaction`，无额外开销
- 子 repo eager init，无懒加载
- 压测验证

### 10.2 风险：BillManager 拆分可能引入新 bug

**缓解**：
- 分阶段改造，每个 service 独立测试
- 保留 `InTransaction` 后缀方法作为过渡
- 增加单元测试覆盖

### 10.3 风险：RPC-DB 中间态改造可能引入新 bug

**缓解**：
- 分阶段改造，每个 service 独立测试
- 保留对账调度器作为兜底
- 增加单元测试覆盖 Processing 状态转换

### 10.4 风险：GameEventConsumer 改造影响消息消费

**缓解**：
- 改造前后的消息处理逻辑保持一致
- 增加集成测试覆盖
- 灰度发布验证

### 10.5 风险：事务超时可能导致业务中断

**缓解**：
- 默认超时 30s，覆盖 99.9% 业务场景
- 超时时间可配置，针对不同场景调整
- 监控超时事件

---

## 11. 验证清单

### 11.1 编译与格式

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `gofmt -l` 无输出

### 11.2 事务抽象层

- [ ] `domain.Transaction` 接口覆盖全部子 repo
- [ ] `GormTransactionImpl` eager init 全部子 repo
- [ ] grep `dbRepo.WithTransaction` 在业务代码中有调用
- [ ] grep `s.db.Transaction(` 在 service 层为 0
- [ ] grep `c.db.Transaction(` 在 consumer 层为 0

### 11.3 BillManager 改造

- [ ] `BillManager` 无 `*gorm.DB` 字段
- [ ] `BillRepository` 接口定义在 domain 层
- [ ] `gormBillRepository` 实现在 infrastructure 层
- [ ] `RefundService` / `DeductService` 依赖 `BillRepository` 接口

### 11.4 GameEventConsumer 改造

- [ ] `GameEventConsumer` 无 `db *gorm.DB` 字段
- [ ] `GameEventConsumer` 持有 `dbRepo domain.DBRepository`
- [ ] 4 处事务调用通过 `c.dbRepo.WithTransaction`

### 11.5 RPC-DB 一致性

- [ ] `executeRefund` 有 Processing 中间态
- [ ] `executeSingleDeduct` 有 Processing 中间态
- [ ] `creditSessionPayout` 有 Processing 中间态
- [ ] `settlePlayer` 有 Processing 中间态
- [ ] 所有 RPC 调用前都有 Processing 置位

### 11.6 事务边界

- [ ] `handleRoundSettle` 的 `SettleRound` 调用在主事务外
- [ ] `handleSessionEnd` 的 `SettleGame` 调用在主事务外
- [ ] `BatchUpdateSessionPlayerStats` 不在 Repository 内开事务

### 11.7 事务超时

- [ ] `WithTransaction` 支持 ctx 超时
- [ ] 默认超时 30s
- [ ] 超时时间可配置

### 11.8 settlement bootstrap

- [ ] `settlement/bootstrap/` 包存在
- [ ] settlement 服务独立装配
- [ ] settlement 服务启动正常

---

## 12. 附录：与成熟框架对比

### 12.1 事务框架对比

| 特性 | 当前实现 | 重构后 | Spring TX | Java JPA | 差距 |
|---|---|---|---|---|---|
| 统一抽象 | ❌ 死代码 | ✅ 激活 | ✅ | ✅ | 对齐 |
| 事务边界 | 散落在 BillManager / Consumer | Application 层 | Service 层 | Service 层 | 对齐 |
| 传播行为 | ❌ | ❌（D5 决策不做） | ✅（7 种） | ✅ | 仍有差距 |
| 隔离级别配置 | ❌ | ❌ | ✅ | ✅ | 仍有差距 |
| 超时控制 | ❌ | ✅ 30s | ✅ | ✅ | 对齐 |
| 乐观锁 | ✅ 9 处 | ✅ 12 处 | ✅（@Version） | ✅（@Version） | 对齐 |
| RPC-DB 一致性 | ❌ | ✅ Processing | - | - | 项目特有 |
| 嵌套事务 | ❌ | ❌（savepoint 未用） | ✅ | ✅ | 仍有差距 |
| Metrics | ❌ | ✅ | ✅ | ✅ | 对齐 |

### 12.2 业界最佳实践对照

| 最佳实践 | 当前状态 | 重构后 |
|---|---|---|
| 事务边界在 Application 层 | ❌ | ✅ |
| 跨服务调用移出事务 | ✅（已拆分） | ✅ |
| 短事务原则 | ✅ | ✅ |
| 乐观锁优先 | ✅ | ✅ |
| 幂等三层防线 | ✅ | ✅ |
| RPC-DB Processing 中间态 | ❌ | ✅ |
| 事务超时控制 | ❌ | ✅ |
| 事务 metrics 采集 | ❌ | ✅ |
| 事务边界文档化 | ❌ | ✅ |

---

**文档版本**：v1
**编写日期**：2026-07-05
**基于 review**：11 个事务调用点、1 处死代码抽象层、4 处 RPC-DB 一致性场景、9 处状态机 UPDATE、171 个 Go 文件全量分析
**关联文档**：[CODING_STANDARD.md §8、§18](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md)、[LOCK_TX_REFACTOR_PLAN.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/LOCK_TX_REFACTOR_PLAN.md)、[BACKEND_AUDIT_REPORT.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/BACKEND_AUDIT_REPORT.md)