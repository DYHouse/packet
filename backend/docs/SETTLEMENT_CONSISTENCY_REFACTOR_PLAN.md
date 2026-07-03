# 结算服务一致性补强方案

> **决策结论**:不引入 Saga 框架,不引入本地消息表。当前结算架构本质已是"轻量级 Saga"(BillRecord 作状态机 + Scheduler 作重试协调器 + RefundService 作补偿事务)。平台已支持 BizOrderNo 幂等,重试天然安全。仅需补强 4 个 P1 级 Scheduler 覆盖盲区,即可覆盖所有数据一致性问题。

## 目录

- [一、当前结算架构设计思路(深度剖析)](#一当前结算架构设计思路深度剖析)
- [二、为什么说当前已是 Saga 等价物](#二为什么说当前已是-saga-等价物)
- [三、资金链路三层模型](#三资金链路三层模型)
- [四、不引入 Saga 的判断依据](#四不引入-saga-的判断依据)
- [五、当前架构的 4 个数据问题](#五当前架构的-4-个数据问题)
- [六、补强方案(不引入 Saga,不引入本地消息表)](#六补强方案不引入-saga不引入本地消息表)
- [七、事件传递层:保持原样 + 重试](#七事件传递层保持原样--重试)
- [八、幂等性修复](#八幂等性修复)
- [九、事务边界修复](#九事务边界修复)
- [十、迁移路线](#十迁移路线)
- [十一、风险与边界](#十一风险与边界)
- [十二、总结](#十二总结)

---

## 一、当前结算架构设计思路(深度剖析)

### 1.1 整体架构概览

当前结算服务采用**"状态机 + 定时调度 + 幂等重试"**架构,核心设计如下:

```
┌─ 触发层(Kafka 事件)─────────────────────────────┐
│  game_event_consumer 收到事件后调用:               │
│    handleSessionStart → 创建 GameSession            │
│    handlePacketCreated → 创建 Round                 │
│    handleRoundSettle  → SettlementService.SettleRound │
│    handleSessionEnd   → GameSettleService.SettleGame   │
└──────────────────────────────────────────────────────┘
                       │
                       ▼
┌─ 业务执行层(三类业务)──────────────────────────────┐
│  扣款层(DeductService):                           │
│    DeductForFirstRound / DeductForLaterRound         │
│    调 platform.Debit(真扣钱)                       │
│                                                      │
│  回合结算层(SettlementService.SettleRound):        │
│    creditRound + settleCommission + SettleReward      │
│    仅写 BillRecord(内部账本,不动真实钱)            │
│                                                      │
│  会话结算层(GameSettleService.SettleGame):         │
│    creditSessionPayouts + settlePlayer              │
│    调 platform.Credit(真给钱)+ platform.Settle(上报)│
└──────────────────────────────────────────────────────┘
                       │
                       ▼
┌─ 兜底层(5 个 Scheduler)───────────────────────────┐
│  CreditRetryScheduler(30s):扫描 amount>0 卡住的 Bill │
│  GameSettleRetryScheduler(30s):扫描 Failed 会话结算  │
│  GameSettleTimeoutScheduler(5min):扫描超时未结算    │
│  RefundProcessScheduler(1min):自动审批退款           │
│  SettlementCheckScheduler(5min):对账检查            │
└──────────────────────────────────────────────────────┘
```

### 1.2 状态机字段设计

当前用 **3 个独立的状态字段** 编排整个流程,这是核心设计思路:

| 实体 | 字段 | 状态值 | 写入时机 |
|------|------|--------|---------|
| RoundSettlement | `Status` | 0=Created → 1=Deducted → 2=Settling(未用) → 3=Credited → 4=Failed | [deduct_service.go:185](file:///e:/demo/party/packet/backend/settlement/service/deduct_service.go) / [settlement_service.go:118](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go) |
| RoundSettlement | `GameSettleStatus` | 0=None → 1=Settling → 2=Success → 3=Failed | [game_settle_service.go:91,148,150](file:///e:/demo/party/packet/backend/settlement/service/game_settle_service.go) |
| BillRecord | `Status` | 0=Processing → 1=Success → 2=Failed → 3=Refunded | 各 service 内 |
| BillRecord | `RefundStatus` | 0=None → 1=Pending → 2=Approved → 3=Success → 4=Rejected | [refund_service.go](file:///e:/demo/party/packet/backend/settlement/service/refund_service.go) |

**设计思想**:每个状态字段对应一个独立子流程,字段之间解耦:
- `RoundSettlement.Status` 跟踪"扣款 → 回合结算"链路
- `GameSettleStatus` 跟踪"会话结算"链路(独立于回合结算)
- `BillRecord.Status` 跟踪单笔资金调用(最细粒度)
- `BillRecord.RefundStatus` 跟踪退款流程(独立于 Bill 主状态)

### 1.3 幂等性设计(核心保障)

每个关键写操作都有幂等检查,这是当前架构能稳定运行的基石:

| 操作 | 幂等检查 | 代码位置 |
|------|---------|---------|
| 创建 grab bill | `GetBillByRoundTypeAndUser(BillTypeGrab)` | [settlement_service.go:147-155](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go) |
| 创建 commission bill | `GetBillByRoundTypeAndUser(BillTypeCommission)` | [settlement_service.go:185-190](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go) |
| 创建 reward bill | `GetBillByRoundTypeAndUser(BillTypeSystemReward)` | [reward_settler.go:69-72](file:///e:/demo/party/packet/backend/settlement/service/reward_settler.go) |
| 创建 session credit bill | `GetBillsBySessionTypeAndUser(BillTypeSessionCredit)` | [game_settle_service.go:289-301](file:///e:/demo/party/packet/backend/settlement/service/game_settle_service.go) |
| 创建退款申请 | `GetRefundByBillID` | [refund_service.go](file:///e:/demo/party/packet/backend/settlement/service/refund_service.go) |

**幂等检查的意义**:即使流程崩溃后被 Scheduler 重试,也不会重复创建 Bill / 重复退款。这让"重试"成为安全的兜底机制。

### 1.4 平台 BizOrderNo 幂等(关键前置保障)

**平台已支持基于 BizOrderNo 的幂等去重**。这是整个重试机制能安全运行的根基:

| 调用 | 幂等机制 | 场景 |
|------|---------|------|
| `platform.Debit` | 相同 BizOrderNo → 平台返回相同结果 | Debit 成功但 UpdateBillSuccess 失败 → Scheduler 重调 → 平台幂等返回 |
| `platform.Credit` | 相同 BizOrderNo → 平台返回相同结果 | Credit 成功但 UpdateBillSuccess 失败 → Scheduler 重调 → 平台幂等返回 |
| `platform.Settle` | 相同 BizOrderNo → 平台返回相同结果 | Settle 失败 → GameSettleRetryScheduler 重调 → 平台幂等返回 |

**这意味着**:`platform.Debit/Credit` 成功但本地 `UpdateBillSuccess` 失败时,Scheduler 重调 platform 是**安全的**(不会重复扣款/入账)。这消除了"钱已动但记录没跟上"的资金风险。

### 1.5 补偿事务设计(RefundService)

当前用退款机制实现补偿事务,而非显式的 Saga Compensate:

```
扣款成功 → 触发结算
        ↓ 失败
        handleFirstRoundDeductFailure([deduct_service.go:283-319](file:///e:/demo/party/packet/backend/settlement/service/deduct_service.go))
        ↓
        对已成功扣款的玩家创建 RefundAudit(status=Pending)
        ↓
        RefundProcessScheduler(1min)自动审批 Pending 退款
        ↓
        ApproveRefund → executeRefund → 调 platform.Credit 反向退款
```

**设计要点**:
- 扣款与退款是**异步解耦**的(扣款当下不立即退款,而是创建退款申请)
- 退款走独立的 `RefundAudit` 表,有自己的状态机(Pending → Approved → Success)
- 退款 Bill 的 `RefundStatus` 字段独立于主 `Status`,避免相互干扰

### 1.6 重试调度器分工设计

5 个 Scheduler 各司其职,分层兜底:

```
┌─ 单笔资金调用级(最细粒度)──────────────────────────┐
│  CreditRetryScheduler(30s):                          │
│    扫 amount>0 AND status IN(Processing, Failed)      │
│    重试 platform.Credit(依赖 BizOrderNo 幂等)         │
└──────────────────────────────────────────────────────┘
                       │
                       ▼
┌─ 业务流程级(会话结算)──────────────────────────────┐
│  GameSettleRetryScheduler(30s):                      │
│    扫 GameSettleStatus=Failed 的 session               │
│    对未结算玩家调 RetryPlayerSettle                     │
│  GameSettleTimeoutScheduler(5min):                   │
│    扫 GameSettleStatus=None 且超 1h 的 session         │
│    调 SettleGame 启动结算                              │
└──────────────────────────────────────────────────────┘
                       │
                       ▼
┌─ 补偿事务级(退款)──────────────────────────────────┐
│  RefundProcessScheduler(1min):                       │
│    扫 RefundAudit.status=Pending 且类型=FirstRoundFail │
│    自动 ApproveRefund                                  │
└──────────────────────────────────────────────────────┘
                       │
                       ▼
┌─ 最终对账级(人工兜底)─────────────────────────────┐
│  SettlementCheckScheduler(5min):                     │
│    CheckFirstRoundDeductFailure:10min 后扫描 failed    │
│      的首回合扣款,自动创建退款                         │
│    CheckDeductedButNotSettled:扫描已扣款未结算的 round  │
│      创建 ExceptionRecord(人工介入)                    │
└──────────────────────────────────────────────────────┘
```

---

## 二、为什么说当前已是 Saga 等价物

### 2.1 Saga 概念对照

Saga 模式的核心要素与当前代码的对应关系:

| Saga 概念 | 经典 Saga 实现 | 当前代码实现 |
|----------|--------------|------------|
| **本地消息表** | outbox 表保证外部调用与本地记录原子 | `BillRecord` 表(BillStatus 作状态机)+ `RefundAudit` 表 |
| **正向操作(Execute)** | Saga Step.Execute | `SettleRound` / `creditRound` / `executeSessionCredit` |
| **补偿操作(Compensate)** | Saga Step.Compensate | `RefundService.ApplyForRefund` + `executeRefund` |
| **重试策略** | RetryPolicy | `CreditRetryScheduler` + `GameSettleRetryScheduler`(指数退避) |
| **状态机** | saga_instance.status | `RoundSettlement.Status` + `GameSettleStatus` + `BillRecord.Status` |
| **幂等保障** | saga_step_log 唯一键 | `GetBillByRoundTypeAndUser` 检查 + 平台 BizOrderNo 幂等 |
| **协调器** | Orchestrator | 5 个 Scheduler(分布式协调) |

### 2.2 关键等价性论证

**等价性 1:补偿事务 = 退款机制**

Saga 的补偿事务是"反向操作抵消正向操作"。当前系统的"扣款 → 退款"正是这个模式:
- 正向:`platform.Debit`(扣钱)
- 补偿:`platform.Credit`(退款,反向给钱)
- 协调:RefundProcessScheduler 自动审批 Pending 退款

**等价性 2:本地消息表 = BillRecord**

BillRecord 表本质上就是一个"本地消息表":
- 每个 Bill 记录一次"应该发生的资金调用"
- BillStatus 作状态机(Processing → Success / Failed)
- 卡在 Processing 的 Bill 由 Scheduler 重试(等价于 outbox Publisher)
- 平台 BizOrderNo 幂等保证重试安全(等价于 outbox 的 at-least-once 语义)

**等价性 3:幂等保障 = saga_step_log**

Saga 用 `UNIQUE(saga_id, step_no, action)` 保证 step 幂等。当前用 `GetBillByRoundTypeAndUser` 检查是否已存在,语义等价。

### 2.3 当前方案 vs 显式 Saga 的差异

| 维度 | 当前方案 | 显式 Saga |
|------|---------|----------|
| 协调方式 | 分布式(多个 Scheduler 各扫各的) | 集中式(Orchestrator 驱动状态机) |
| 状态查询 | 需 join 多张表 | 单表查 saga_instance |
| 补偿触发 | 异步(Scheduler 定时扫) | 同步(Step 失败立即 Compensate) |
| 代码量 | 已存在 | 需新增 ~1000 行框架代码 |
| 学习成本 | 团队熟悉现有模式 | 需学习 Saga 概念 |

**核心差异**:当前是"事件驱动 + 定时扫描"的分布式协调,Saga 是"中央协调器 + 同步状态机"。两者语义等价,但当前方案已落地运行。

---

## 三、资金链路三层模型

理解当前设计的关键:资金链路分三层,每层"是否动真实钱"决定了需要的一致性强度。

```
┌─ 第一层:扣款(动真钱)─────────────────────────────┐
│  DeductService.DeductForFirstRound / LaterRound      │
│  ↓                                                   │
│  platform.Debit(外部 HTTP,不可回滚)                  │
│  ↓                                                   │
│  BillRecord.Status = Success                        │
│                                                      │
│  风险:platform 成功但 DB 更新失败 → Bill 卡 Processing │
│  保障:平台 BizOrderNo 幂等 → Scheduler 重调安全      │
└──────────────────────────────────────────────────────┘
                       │
                       ▼
┌─ 第二层:回合结算(不动真钱,纯内部账本)────────────┐
│  SettlementService.SettleRound                      │
│  ↓                                                   │
│  creditRound(创建 grab+commission Bill)             │
│  SettleReward(创建 reward Bill)                     │
│  UpdateRoundSettlementCredited                      │
│                                                      │
│  风险:账本部分缺失(如 commission 失败被吞)        │
│  保障:纯 DB 操作,幂等重试即可收敛                   │
│  不需要:Saga(纯 DB 操作本身 ACID)                  │
└──────────────────────────────────────────────────────┘
                       │
                       ▼
┌─ 第三层:会话入账(动真钱)─────────────────────────┐
│  GameSettleService.SettleGame                       │
│  ↓                                                   │
│  creditSessionPayouts → platform.Credit(真给钱)      │
│  settlePlayer → platform.Settle(仅上报,不动钱)      │
│  ↓                                                   │
│  GameSettleStatus = Success                          │
│                                                      │
│  风险:platform 成功但 DB 更新失败 → Bill 卡 Processing │
│  保障:平台 BizOrderNo 幂等 → Scheduler 重调安全      │
└──────────────────────────────────────────────────────┘
```

### 三层模型的核心洞察

| 层级 | 是否动真钱 | 失败后果 | 一致性机制 |
|------|----------|---------|----------|
| 扣款层 | ✅ 是 | 钱已扣没记录 | **平台 BizOrderNo 幂等 + Scheduler 重试** |
| 回合结算层 | ❌ 否 | 内部账本不一致 | DB 事务 + 幂等重试(已够) |
| 会话入账层 | ✅ 是 | 钱已给没记录 | **平台 BizOrderNo 幂等 + Scheduler 重试** |

**关键判断**:第二层(SettleRound)不需要 Saga,因为它是纯 DB 操作,失败靠幂等重试即可收敛。Saga 编排纯 DB 操作纯属过度设计。

**关键保障**:第一层和第三层动真钱,失败风险靠**平台 BizOrderNo 幂等**消除(已确认支持)。Scheduler 重调 platform 是安全的,不会重复扣款/入账。

---

## 四、不引入 Saga 的判断依据

### 4.1 资金链路是单向链式,无需原子提交

Saga 适用于"多个独立服务必须原子提交"的场景。但当前资金链路是**单向链式**:

```
扣款 →(异步)→ 内部记账 →(异步)→ 会话入账
```

每跳之间是**异步解耦**的:
- 扣款成功后,红包异步发出(不需要原子)
- 内部记账失败,不影响已扣款(靠幂等重试)
- 会话入账失败,不影响内部记账(靠重试 + 退款)

不存在"必须同时成功/失败"的跨服务事务,因此不需要 Saga 的原子提交能力。

### 4.2 补偿事务已由 RefundService 实现

Saga 的补偿事务(COMPENSATING)在当前系统中由 RefundService 承担:
- 扣款失败 → `handleFirstRoundDeductFailure` 创建退款申请
- 退款申请 → `RefundProcessScheduler` 自动审批
- 审批后 → `executeRefund` 调 `platform.Credit` 反向退款

这套机制已经覆盖了"扣款需要回滚"的场景,无需 Saga 的 Compensate 接口。

### 4.3 平台 BizOrderNo 幂等已确认

**平台已支持基于 BizOrderNo 的幂等去重**。这是整个重试机制能安全运行的根基。这意味着:
- `platform.Debit` 成功但 `UpdateBillSuccess` 失败 → Bill 卡 Processing → Scheduler 重调 → 平台幂等返回相同结果 → 本地状态修正
- 无需本地消息表保证"platform 调用与 DB 原子"(平台幂等已保证重试安全)
- 无需 Saga 的 Compensate(重试即可收敛,不需要反向操作)

### 4.4 4 个 P1 级问题都靠"改几行代码"解决

研究发现的 4 个数据问题(见 §五),全部可通过"补 Scheduler 扫描条件"或"改 err 处理"解决,无需引入 Saga 框架。详见 §六。

---

## 五、当前架构的 4 个数据问题

### P1-1:GameSettleStatus=Settling 永久卡死

| 项 | 内容 |
|----|------|
| **触发条件** | `SettleGame` 在 [game_settle_service.go:91](file:///e:/demo/party/packet/backend/settlement/service/game_settle_service.go) 置 Settling 后,于 `creditSessionPayouts` / `settlePlayer` 循环中进程崩溃 |
| **代码位置** | 扫描条件 [bill_manager.go:489](file:///e:/demo/party/packet/backend/settlement/infrastructure/persistence/mysql/bill_manager.go)(只扫 Failed)+ [bill_manager.go:501](file:///e:/demo/party/packet/backend/settlement/infrastructure/persistence/mysql/bill_manager.go)(只扫 None) |
| **数据后果** | 会话级入账永久停滞;玩家可能已部分入账,但状态既非成功也非失败,无法自动收敛 |
| **当前兜底** | **无**,需人工改库 |
| **严重度** | P1(流程卡死 + 中间态不一致) |
| **修复方式** | `GameSettleTimeoutScheduler` 扫描条件加 `Settling`(见 §六) |

### P1-2:commission bill 创建失败被吞且永不重试

| 项 | 内容 |
|----|------|
| **触发条件** | `settleCommission` 的 `CreateBill` 失败 |
| **代码位置** | [settlement_service.go:132-136](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go)(只 log 不 return)+ [settlement_service.go:83-84](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go)(重试时 status=Credited 早返回) |
| **数据后果** | 系统佣金账目永久缺失(UserID=0 内部账,不影响真实资金,但账目不平) |
| **当前兜底** | 无 |
| **严重度** | P1(账目不一致) |
| **修复方式** | `logger.Error` 改为 `return err`(见 §六) |

### P1-3:扣款类 Bill(amount<0)卡 Processing 无 Scheduler 扫

| 项 | 内容 |
|----|------|
| **触发条件** | `platform.Debit` 成功但 `UpdateBillSuccess` 失败,Bill 留在 Processing(amount<0) |
| **代码位置** | [bill_manager.go:309](file:///e:/demo/party/packet/backend/settlement/infrastructure/persistence/mysql/bill_manager.go) `GetRetryableCredits` 条件 `amount > 0`,**排除扣款类** |
| **数据后果** | 玩家已被平台扣款,但本地 bill 状态滞后;`CreateDebitFailedException` 只在 Debit 失败时触发,成功但状态未更新时**无异常** |
| **当前兜底** | 无(需人工核对平台对账单) |
| **严重度** | P1(平台已扣款但本地账本不一致) |
| **修复方式** | 扩展 CreditRetryScheduler 去掉 `amount>0` 限制(见 §六) |

**注**:平台已支持 BizOrderNo 幂等,所以 Bill 卡 Processing 时重调 `platform.Debit` 是安全的(平台返回相同结果),本地状态可收敛。

### P1-4:RefundAudit Approved 后 executeRefund 失败永不重试

| 项 | 内容 |
|----|------|
| **触发条件** | `executeRefund` 调 `platform.Credit` 失败 |
| **代码位置** | [refund_service.go:182-191](file:///e:/demo/party/packet/backend/settlement/service/refund_service.go)(只 `UpdateRefundAuditError`,status 仍 Approved)+ [refund_process_scheduler.go:41](file:///e:/demo/party/packet/backend/settlement/scheduler/refund_process_scheduler.go)(只扫 Pending) |
| **数据后果** | 退款永久卡在 Approved,玩家应退未退 |
| **当前兜底** | 无 |
| **严重度** | P1(应退未退,需人工) |
| **修复方式** | `RefundProcessScheduler` 增扫 `Approved`(见 §六) |

**注**:平台已支持 BizOrderNo 幂等,所以重新调 `executeRefund` 是安全的(不会重复退款)。

---

## 六、补强方案(不引入 Saga,不引入本地消息表)

### 6.1 补强总览

| 修复项 | 解决问题 | 改动量 | 位置 |
|--------|---------|--------|------|
| `GameSettleTimeoutScheduler` 扫描条件加 `Settling` | P1-1 | 改 1 行 SQL | bill_manager.go |
| `creditRound` 中 `settleCommission` 失败上抛 err | P1-2 | 改 3 行 | settlement_service.go |
| `CreditRetryScheduler` 去掉 `amount>0` 限制(或新增 DebitRetryScheduler) | P1-3 | 改 1 行 SQL | bill_manager.go |
| `RefundProcessScheduler` 增扫 `Approved` | P1-4 | 改 SQL + 加重试逻辑 | refund_process_scheduler.go |

### 6.2 P1-1 修复:GameSettleTimeoutScheduler 加扫 Settling

[bill_manager.go:501](file:///e:/demo/party/packet/backend/settlement/infrastructure/persistence/mysql/bill_manager.go):

```go
// 改造前:
func (m *BillManager) GetGameSettleTimeoutSessions(ctx context.Context, before time.Time, limit int) ([]*model.RoundSettlement, error) {
    var settlements []*model.RoundSettlement
    err := m.db.WithContext(ctx).
        Where("status = ? AND game_settle_status = ? AND updated_at < ?",
            dto.RoundStatusCredited, dto.GameSettleStatusNone, before).
        Order("updated_at ASC").Limit(limit).Find(&settlements).Error
    return settlements, err
}

// 改造后:扫描条件加 Settling(覆盖崩溃在 SettleGame 中途的场景)
func (m *BillManager) GetGameSettleTimeoutSessions(ctx context.Context, before time.Time, limit int) ([]*model.RoundSettlement, error) {
    var settlements []*model.RoundSettlement
    err := m.db.WithContext(ctx).
        Where("status = ? AND game_settle_status IN (?) AND updated_at < ?",
            dto.RoundStatusCredited,
            []int{dto.GameSettleStatusNone, dto.GameSettleStatusSettling},
            before).
        Order("updated_at ASC").Limit(limit).Find(&settlements).Error
    return settlements, err
}
```

**配合调整**:`GameSettleTimeoutScheduler` 扫到 Settling 状态时,先调 `SettleGame`,SettleGame 内部的幂等检查会跳过已入账玩家(因 `GetBillsBySessionTypeAndUser` 检查),只补未入账的。

### 6.3 P1-2 修复:commission 失败上抛 err

[settlement_service.go:132-136](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go):

```go
// 改造前:
if settlement.Commission > 0 {
    if err := s.settleCommission(ctx, settlement); err != nil {
        logger.Error("settle commission failed", "round_id", settlement.RoundID, "error", err)
        // 仅 log,继续执行 → commission 永久缺失
    }
}

// 改造后:上抛 err,触发 SettleRound 整体失败
// 失败后 status 不升 Credited,Kafka 重试时重新进入 SettleRound
// creditRound 内的幂等检查会跳过已创建的 grab bill,只重试 commission
if settlement.Commission > 0 {
    if err := s.settleCommission(ctx, settlement); err != nil {
        return 0, 0, fmt.Errorf("settle commission failed: %w", err)
    }
}
```

**幂等保障**:`settleCommission` 内部已有 `GetBillByRoundTypeAndUser(BillTypeCommission)` 检查([settlement_service.go:185-190](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go)),重试时不会重复创建 commission bill。

### 6.4 P1-3 修复:CreditRetryScheduler 覆盖扣款类 Bill

**方案选择**:有两种实现方式,推荐方案 A(改动最小)。

**方案 A:扩展 CreditRetryScheduler 去掉 amount>0 限制(推荐)**

[bill_manager.go:308-310](file:///e:/demo/party/packet/backend/settlement/infrastructure/persistence/mysql/bill_manager.go):

```go
// 改造前:
func (m *BillManager) GetRetryableCredits(ctx context.Context, limit int) ([]*model.BillRecord, error) {
    var bills []*model.BillRecord
    err := m.db.WithContext(ctx).
        Where("amount > 0 AND status IN (?) AND retry_count < ?",
            []int{dto.BillStatusProcessing, dto.BillStatusFailed}, dto.MaxRetryCount).
        Order("updated_at ASC").Limit(limit).Find(&bills).Error
    return bills, err
}

// 改造后:去掉 amount > 0 限制,同时覆盖扣款类(amount<0)和入账类(amount>0)
func (m *BillManager) GetRetryableBills(ctx context.Context, limit int) ([]*model.BillRecord, error) {
    var bills []*model.BillRecord
    err := m.db.WithContext(ctx).
        Where("status IN (?) AND retry_count < ?",
            []int{dto.BillStatusProcessing, dto.BillStatusFailed}, dto.MaxRetryCount).
        Order("updated_at ASC").Limit(limit).Find(&bills).Error
    return bills, err
}
```

**CreditRetryScheduler 执行逻辑调整**:

```go
// CreditRetryScheduler 处理卡住的 Bill(扣款类 + 入账类)
func (s *CreditRetryScheduler) execute(ctx context.Context) error {
    bills, err := s.billMgr.GetRetryableBills(ctx, 100)  // 改名 + 去 amount 限制
    if err != nil {
        return err
    }

    for _, bill := range bills {
        if err := s.retryBill(ctx, bill); err != nil {
            logger.Error("retry bill failed", "bill_id", bill.ID, "error", err)
        }
    }
    return nil
}

func (s *CreditRetryScheduler) retryBill(ctx context.Context, bill *model.BillRecord) error {
    // 根据 BillType 决定调用哪个 platform 方法
    // 关键:平台 BizOrderNo 幂等保证重调安全
    switch bill.BillType {
    case dto.BillTypeDebit, dto.BillTypeFirstRoundDeduct, dto.BillTypeLaterRoundDeduct:
        // 扣款类:重调 platform.Debit(平台幂等返回相同结果)
        return s.retryDebit(ctx, bill)
    case dto.BillTypeSessionCredit, dto.BillTypeGrab, dto.BillTypeCommission, dto.BillTypeSystemReward:
        // 入账类:重调 platform.Credit(平台幂等返回相同结果)
        return s.retryCredit(ctx, bill)
    default:
        return fmt.Errorf("unknown bill type: %d", bill.BillType)
    }
}

func (s *CreditRetryScheduler) retryDebit(ctx context.Context, bill *model.BillRecord) error {
    // 重新调 platform.Debit(平台 BizOrderNo 幂等)
    // 平台若已扣款,返回相同结果 → 本地 UpdateBillSuccess 修正状态
    // 平台若未扣款(如上次实际失败),重新扣款
    // ...
    return nil
}

func (s *CreditRetryScheduler) retryCredit(ctx context.Context, bill *model.BillRecord) error {
    // 重新调 platform.Credit(平台 BizOrderNo 幂等)
    // 逻辑与现有 CreditRetryScheduler 重试入账类 Bill 相同
    // ...
    return nil
}
```

**关键**:平台 BizOrderNo 幂等保证重调安全。无论是扣款类(amount<0)还是入账类(amount>0),重调 platform 都不会重复扣款/入账。

**方案 B:新增独立的 DebitRetryScheduler(可选)**

若希望扣款类与入账类重试逻辑完全隔离,可新增独立的 `DebitRetryScheduler`,扫描条件 `amount<0 AND status IN(Processing, Failed)`,单独调 `platform.Debit`。代码隔离更清晰,但多一个 Scheduler。

### 6.5 P1-4 修复:RefundProcessScheduler 增扫 Approved

[refund_process_scheduler.go:41](file:///e:/demo/party/packet/backend/settlement/scheduler/refund_process_scheduler.go):

```go
// 改造后:同时扫描 Pending 和 Approved
// Pending:自动审批(ApproveRefund)
// Approved:executeRefund 失败的,重新调 executeRefund(平台 BizOrderNo 幂等保证安全)
pendingRefunds, err := s.billMgr.GetRefundsByStatus(ctx, dto.RefundStatusPending, 100, 0)
if err != nil {
    logger.Error("get pending refunds failed", "error", err)
}
for _, refund := range pendingRefunds {
    if err := s.refundSvc.ApproveRefund(ctx, refund.ID); err != nil {
        logger.Error("approve refund failed", "refund_id", refund.ID, "error", err)
    }
}

approvedRefunds, err := s.billMgr.GetRefundsByStatus(ctx, dto.RefundStatusApproved, 100, 0)
if err != nil {
    logger.Error("get approved refunds failed", "error", err)
}
for _, refund := range approvedRefunds {
    // 重新调 executeRefund(幂等,平台基于 BizOrderNo 去重)
    if err := s.refundSvc.RetryExecuteRefund(ctx, refund.ID); err != nil {
        logger.Error("retry execute refund failed", "refund_id", refund.ID, "error", err)
    }
}
```

**RefundService 新增方法**:

```go
// RetryExecuteRefund 重试执行退款(幂等,基于 BizOrderNo)
// 平台已支持 BizOrderNo 幂等,重调安全(不会重复退款)
func (s *RefundService) RetryExecuteRefund(ctx context.Context, refundID int64) error {
    refund, err := s.billMgr.GetRefundByID(ctx, refundID)
    if err != nil {
        return err
    }
    if refund.Status != dto.RefundStatusApproved {
        return nil  // 状态已变化,跳过
    }
    // 重新调 executeRefund,内部幂等(平台基于 BizOrderNo 去重)
    return s.executeRefund(ctx, refund)
}
```

### 6.6 补强后的 Scheduler 覆盖矩阵

| 场景 | 补强前 | 补强后 |
|------|--------|--------|
| 扣款类 Bill 卡 Processing(amount<0) | ❌ 无 | ✅ CreditRetryScheduler 去 amount 限制 |
| 入账类 Bill 卡 Processing(amount>0) | ✅ CreditRetryScheduler | ✅ 同前 |
| SettleRound 内部失败(commission 被吞) | ❌ 无 | ✅ commission 上抛 err + 幂等重试 |
| SettleRound 完全没跑 | ✅ SettlementCheckScheduler(人工) | ✅ 同前 |
| SettleGame 卡在 Settling | ❌ 无 | ✅ GameSettleTimeoutScheduler 加扫 Settling |
| SettleGame 完全没跑 | ✅ GameSettleTimeoutScheduler | ✅ 同前 |
| platform.Settle 失败 | ✅ GameSettleRetryScheduler | ✅ 同前 |
| 退款 Pending → Approved | ✅ RefundProcessScheduler | ✅ 同前 |
| 退款 Approved → Success(executeRefund 失败) | ❌ 无 | ✅ RefundProcessScheduler 增扫 Approved |
| 扣款失败 → 退款 | ✅ SettlementCheckScheduler | ✅ 同前 |

---

## 七、事件传递层:保持原样 + 重试

### 7.1 改动原则

**保持现有架构不变**,只做两个改动:
1. 删除 `go func()`,改为同步调用
2. 增加重试(2-3 次,指数退避)

不引入 outbox 表、不引入对账调度器。丢失靠最终对账(SettlementCheckScheduler)兜底。

### 7.2 Publisher 增加重试

[game_event_publisher.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_publisher.go):

```go
func (p *GameEventPublisher) publishWithRetry(ctx context.Context, event *domain.GameEvent) error {
    data, err := json.Marshal(event)
    if err != nil {
        return fmt.Errorf("marshal event failed: %w", err)
    }
    key := fmt.Sprintf("%s_%s", event.RoomID, event.SessionID)

    var lastErr error
    for i := 0; i < 3; i++ {
        if i > 0 {
            delay := time.Duration(i*i) * 500 * time.Millisecond  // 0.5s, 2s
            select {
            case <-time.After(delay):
            case <-ctx.Done():
                return ctx.Err()
            }
        }
        if err := p.producer.Send(ctx, kafka.TopicGameEvents, []byte(key), data); err != nil {
            lastErr = err
            logger.Warn("publish retry", "attempt", i+1, "event_type", event.EventType, "error", err)
            continue
        }
        return nil
    }
    return lastErr
}
```

### 7.3 4 个调用点:删除 go func,改同步

[game_app_service.go](file:///e:/demo/party/packet/backend/game/application/game_app_service.go) 4 个调用点统一改造:

```go
// 改造后:同步调用 + 重试,失败仅 log(不 return err,Redis 状态已变更不可回滚)
if err := s.eventPublisher.PublishRoundSettle(ctx, event); err != nil {
    logger.Error("publish round settle event failed after retries",
        "round_id", roundID, "error", err)
    // 不 return err:Lua 已提交,业务不可回滚
    // 丢失的事件靠最终对账(SettlementCheckScheduler)兜底
}
```

### 7.4 Consumer 修复:失败不 commit

[common/kafka/consumer.go:105-117](file:///e:/demo/party/packet/backend/common/kafka/consumer.go):

```go
// 改造后:失败不 commit,Kafka 自动重投(at-least-once)
if err := c.processMessage(ctx, msg); err != nil {
    logger.Error("kafka process message failed, NOT committing",
        "topic", c.topic, "error", err)
    time.Sleep(5 * time.Second)  // 限制重投频率
    continue
}

if err := c.reader.CommitMessages(ctx, msg); err != nil {
    logger.Error("kafka commit failed", "error", err)
}
```

**毒消息防护**(用 Redis 计数,超阈值 commit 跳过 + 告警):

```go
retryKey := fmt.Sprintf("cashparty:kafka:retry:%s_%d", msg.Topic, msg.Offset)
retryCount, _ := c.redis.Incr(ctx, retryKey).Result()
if retryCount == 1 {
    c.redis.Expire(ctx, retryKey, 1*time.Hour)
}
if retryCount > 50 {
    logger.Error("kafka message exhausted retries, committing to skip",
        "topic", msg.Topic, "offset", msg.Offset, "retry_count", retryCount)
    c.redis.Del(ctx, retryKey)
    c.reader.CommitMessages(ctx, msg)
    continue
}
```

### 7.5 tryAcquire 废弃

[game_event_consumer.go:449-475](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) 的 `tryAcquire` / `releaseAcquire` 被 consumer 侧业务幂等替代:

```go
// 改造后:删除 tryAcquire/releaseAcquire
// 幂等由 consumer handler 内的业务检查保证:
//   - handleSessionStart: FirstOrCreate
//   - handlePacketCreated: RoundStatus 更新条件
//   - handleRoundSettle: SettleRound 内 RoundStatusCredited 检查
//   - handleSessionEnd: SessionStatusCompleted 检查
```

---

## 八、幂等性修复

### 8.1 SessionPlayer 统计改幂等

[game_event_consumer.go:274-298](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go):

```go
// 改造后:先检查 grab_record 是否已存在(幂等标记)
for _, r := range data.Results {
    userID := parseInt64(r.UserID)
    var count int64
    tx.Model(&model.RoundGrabRecord{}).
        Where("round_id = ? AND user_id = ?", parseInt64(event.RoundID), userID).
        Count(&count)

    if count == 0 {
        // 首次处理,累加统计
        tx.Model(&model.SessionPlayer{}).
            Where("session_id = ? AND user_id = ?", sessionIDInt64, userID).
            Updates(map[string]interface{}{
                "grab_count": gorm.Expr("grab_count + 1"),
                "total_grab":  gorm.Expr("total_grab + ?", r.Amount),
            })
    }
    // count > 0 表示重试,跳过累加
}
```

### 8.2 BizOrderNo 确定性生成

[trace_id_generator.go:24-28](file:///e:/demo/party/packet/backend/settlement/service/trace_id_generator.go):

```go
// 改造后:基于业务确定性参数生成,确保重试生成相同 BizOrderNo
// 这是平台 BizOrderNo 幂等能生效的前提
func (g *TraceIDGenerator) GenerateBizOrderNo(bizType string, userID int64, bizKey int64) string {
    return fmt.Sprintf("%s_%d_%d", bizType, bizKey, userID)
}
```

所有调用点需同步修改,传入业务键(`roundID` 或 `sessionID`)。这是平台 BizOrderNo 幂等能生效的前提——若每次重试生成不同 BizOrderNo,平台无法识别为重复请求。

### 8.3 BillManager 增加 tx 版本

为支持 §九 的事务边界修复,`BillManager` 需增加 `WithTx` 版本方法:

```go
// 新增 tx 版本(用传入的 tx)
func (m *BillManager) UpdateBillSuccessWithTx(tx *gorm.DB, billID int64, balanceBefore, balanceAfter int64) error {
    return tx.Model(&model.BillRecord{}).
        Where("id = ?", billID).
        Updates(map[string]any{
            "status":         dto.BillStatusSuccess,
            "balance_before": balanceBefore,
            "balance_after":  balanceAfter,
        }).Error
}
```

---

## 九、事务边界修复

### 9.1 SettleRound 接受 tx 参数

[settlement_service.go:67](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go):

```go
// 改造后:
func (s *SettlementService) SettleRound(ctx context.Context, tx *gorm.DB, req *dto.RoundSettleRequest) error {
    // 内部用 tx 写库,与 consumer 的 db.Transaction 闭合
}
```

### 9.2 consumer 调用改造

[game_event_consumer.go:358](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go):

```go
// 改造后:
if err := c.settlementService.SettleRound(ctx, tx, settleReq); err != nil {
```

[game_event_consumer.go:435](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) SettleGame 同理改造。

这样 settlement 表的写入与 game 表的写入在同一事务内,回滚时一起回滚。

---

## 十、迁移路线

### 阶段 1:Consumer 修复 + 事件重试(P0,先行)

- [ ] [common/kafka/consumer.go](file:///e:/demo/party/packet/backend/common/kafka/consumer.go) 改为失败不 commit + 毒消息防护
- [ ] `StartOffset` 改为 `FirstOffset`
- [ ] [game_event_publisher.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_publisher.go) 增加 `publishWithRetry`
- [ ] [game_app_service.go](file:///e:/demo/party/packet/backend/game/application/game_app_service.go) 4 个调用点删除 `go func()`,改同步 + 重试
- [ ] 删除 `tryAcquire` / `releaseAcquire`

### 阶段 2:幂等性修复(P0)

- [ ] [game_event_consumer.go:274-298](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) SessionPlayer 统计改幂等
- [ ] [trace_id_generator.go:24-28](file:///e:/demo/party/packet/backend/settlement/service/trace_id_generator.go) BizOrderNo 改确定性生成(平台幂等生效的前提)
- [ ] 所有 `GenerateBizOrderNo` 调用点同步修改

### 阶段 3:Scheduler 补强(P1,本文档核心)

- [ ] **P1-1**:[bill_manager.go:501](file:///e:/demo/party/packet/backend/settlement/infrastructure/persistence/mysql/bill_manager.go) `GetGameSettleTimeoutSessions` 扫描条件加 `Settling`
- [ ] **P1-2**:[settlement_service.go:132-136](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go) `settleCommission` 失败改上抛 err
- [ ] **P1-3**:[bill_manager.go:308-310](file:///e:/demo/party/packet/backend/settlement/infrastructure/persistence/mysql/bill_manager.go) `GetRetryableCredits` 去 `amount>0` 限制(改名为 `GetRetryableBills`),CreditRetryScheduler 增加 `retryDebit` 分支
- [ ] **P1-4**:[refund_process_scheduler.go:41](file:///e:/demo/party/packet/backend/settlement/scheduler/refund_process_scheduler.go) 增扫 `Approved` 状态 + RefundService 新增 `RetryExecuteRefund`

### 阶段 4:事务边界修复(P0)

- [ ] `SettlementService.SettleRound` 增加 `tx *gorm.DB` 参数
- [ ] `SettlementService.SettleGame` 增加 `tx *gorm.DB` 参数
- [ ] [game_event_consumer.go:358,435](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) 传 tx
- [ ] `BillManager` 增加 `WithTx` 版本方法

---

## 十一、风险与边界

### 11.1 风险

| 风险 | 应对 |
|------|------|
| 事件传递层无 outbox,仍有丢失风险 | 同步重试覆盖 99.9%;剩余靠 SettlementCheckScheduler 对账 |
| `SettleRound(tx, ...)` 改造影响面大 | BillManager 方法增加 tx 参数是渐进式 |
| CreditRetryScheduler 去 amount 限制后,扣款类重试逻辑需新增 | 复用平台 BizOrderNo 幂等,重调安全;新增 `retryDebit` 分支 |
| 平台 BizOrderNo 幂等性未来变化 | 定期对账(平台对账单 vs 本地 BillRecord) |

### 11.2 不适用的场景

- **Redis Lua 虚拟余额扣减**:已原子,不改
- **结算记账**(`creditRound` / `SettleReward`):纯 DB 操作,本身 ACID
- **只读查询**(`CheckBalance` / `GetBillsByUserID`)
- **机器人虚拟通道**:不走 platform,无外部依赖

### 11.3 与 Saga 方案的对比

| 维度 | 本方案(补强现有) | Saga 方案 |
|------|----------------|----------|
| 改动量 | ~200 行(4 处补强 + 事件重试 + 事务边界) | ~1500 行(Saga 框架 + 5 个 Saga 定义) |
| 学习成本 | 团队已熟悉现有模式 | 需学习 Saga 概念 |
| 状态查询 | 需 join 多张表 | 单表查 saga_instance |
| 补偿触发 | 异步(Scheduler 定时扫) | 同步(Step 失败立即 Compensate) |
| 资金安全 | 同等(都依赖平台 BizOrderNo 幂等) | 同等 |
| 风险 | 低(增量改动) | 高(重写核心流程) |
| 可观测性 | 状态分散,需聚合查询 | 集中,易查进度 |

### 11.4 与本地消息表方案的对比

| 维度 | 本方案(不引入本地消息表) | 本地消息表方案 |
|------|------------------------|-------------|
| 平台调用成功 + DB 更新失败 | Bill 卡 Processing → Scheduler 重调(平台幂等返回) | outbox 卡 PLATFORM_NOT_CALLED → Scheduler 重调(平台幂等返回) |
| 最终一致性保障 | 平台 BizOrderNo 幂等 | 平台 BizOrderNo 幂等(相同) |
| 是否能少调 platform | 每次都要重调 | 若 response 存成功,用存的补更新(少调一次) |
| 新增表 | 无 | `platform_call_outbox` |
| 改动量 | ~200 行 | ~500 行 |

**关键洞察**:本地消息表只是"少调几次 platform"的优化,不是根本保障。平台 BizOrderNo 幂等才是真正的一致性保障。既然平台已支持幂等,Scheduler 重调 platform 是安全的,本地消息表的边际价值很低。

### 11.5 性能影响

| 操作 | 延迟增量 | 可接受 |
|------|---------|--------|
| 事件同步重试(每个事件) | 0-4.5s(仅失败时) | ✅ 结算异步路径 |
| CreditRetryScheduler 扫描范围扩大 | 后台,无影响 | ✅ |
| 补强后的 Scheduler 扫描 | 后台,无影响 | ✅ |

### 11.6 与现有约束兼容

| 约束 | 兼容性 |
|------|--------|
| `cashparty:` Redis key 前缀 | 毒消息计数用 `cashparty:kafka:retry:` 前缀 |
| Settlement 不 import game | 所有补强在 settlement 包内 |
| Lua 原子化 | 不改 Lua,事件传递层仍"Lua 后发 Kafka" |
| 事务失败返回 error 触发回滚 | settleCommission 失败上抛 err 触发 SettleRound 失败,符合现有模式 |

---

## 十二、总结

### 核心判断

**不引入 Saga,不引入本地消息表**。当前结算架构已是"轻量级 Saga"等价物:
- BillRecord 作本地消息表(BillStatus 作状态机)
- 5 个 Scheduler 作分布式协调器
- RefundService 作补偿事务
- 幂等检查作 step_log 唯一键
- **平台 BizOrderNo 幂等作最终一致性保障**

### 真正需要做的

1. **Scheduler 补强**(§六):解决 P1-1/P1-2/P1-3/P1-4(4 个覆盖盲区)
2. **事件传递重试**(§七):降低事件丢失概率
3. **幂等性修复**(§八):保证重试安全(BizOrderNo 确定性是前提)
4. **事务边界修复**(§九):保证 SettleRound 与 consumer tx 闭合

### 不需要做的

- ❌ Saga 框架(saga_instance / saga_step_log / Orchestrator)
- ❌ 5 个 Saga 定义
- ❌ SagaRetryScheduler
- ❌ 本地消息表(platform_call_outbox 表)
- ❌ Outbox 调度器
- ❌ 复杂的 outbox 对账调度器

### 资金安全的核心保障

资金安全**不依赖 Saga,也不依赖本地消息表**,而是依赖:

1. **平台 BizOrderNo 幂等**:保证重试不重复扣款/入账(已确认支持)
2. **BillRecord 状态机**:跟踪每笔资金调用的状态(已存在)
3. **Scheduler 重试**:卡在 Processing 的 Bill 由 Scheduler 重调 platform(已存在,需补强覆盖范围)
4. **幂等检查**:每个关键写操作都有 `GetBillByRoundTypeAndUser` 检查(已存在)
5. **settlement 表对账**:`SettlementCheckScheduler` 扫描 RoundSettlement.Status,与事件传递层解耦(已存在)
6. **退款补偿**:RefundService 实现补偿事务(已存在,需补强 Approved 状态重试)

这六层保障中,Saga 只能替代其中第 2-3 层(且不如现有方案直接),本地消息表只是第 3 层的微优化。因此引入两者的边际收益都极低。

### 改动量总览

| 阶段 | 改动量 | 改动内容 |
|------|--------|---------|
| 阶段 1:Consumer 修复 + 事件重试 | ~80 行 | consumer 失败不 commit + publisher 重试 + 删 tryAcquire |
| 阶段 2:幂等性修复 | ~30 行 | SessionPlayer 幂等 + BizOrderNo 确定性 |
| 阶段 3:Scheduler 补强 | ~60 行 | 4 处扫描条件/err 处理修改 |
| 阶段 4:事务边界修复 | ~30 行 | SettleRound/SettleGame 加 tx 参数 |
| **总计** | **~200 行** | **无新表,无新框架,纯增量补强** |

---

**文档版本**:v2.0(确认平台 BizOrderNo 幂等后,删除本地消息表章节,方案大幅简化)
**前置文档**:[CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md)(架构总览)
**关联文档**:[OUTBOX_SAGA_REFACTOR_PLAN.md](./OUTBOX_SAGA_REFACTOR_PLAN.md)(v3.0,已被本文档替代,保留作历史参考)
