# Saga 事务编排重构方案

> 本文针对 `settlement/service` 下"散弹枪式"的补偿/重试/退款逻辑,给出基于 **Orchestration Saga** 的统一事务编排重构方案。属于 [CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md) 中 D-07 的细化。

## 目录

- [一、背景:当前事务实现现状](#一背景当前事务实现现状)
- [二、Saga vs TCC:概念对比](#二saga-vs-tcc概念对比)
- [三、本项目场景分析](#三本项目场景分析)
- [四、选型结论:Orchestration Saga](#四选型结论orchestration-saga)
- [五、详细实现方案](#五详细实现方案)
- [六、核心代码示例](#六核心代码示例)
- [七、迁移路线](#七迁移路线)
- [八、风险与边界](#八风险与边界)

---

## 一、背景:当前事务实现现状

### 1.1 资金链路全貌

经审查 [settlement_service.go](../settlement/service/settlement_service.go) / [deduct_service.go](../settlement/service/deduct_service.go) / [refund_service.go](../settlement/service/refund_service.go) / [credit_retry_service.go](../settlement/service/credit_retry_service.go) / [game_settle_service.go](../settlement/service/game_settle_service.go),资金流转分 5 个长流程:

| 流程 | 入口 | 关键步骤 | 失败处理位置 |
|------|------|---------|------------|
| 首回合批量扣款 | `DeductService.DeductForFirstRound` | 创建 round_settlement+bills → 批量 `platform.Debit` → 部分失败触发退款 | `handleFirstRoundDeductFailure`(deduct_service.go:283) |
| 后续/系统扣款 | `DeductService.DeductForLaterRound` / `DeductForSystemPacket` | 创建 round_settlement+bills → 单人 `platform.Debit` | `CreateDebitFailedException`(credit_retry_service.go:216) |
| 回合结算 | `SettlementService.SettleRound` | creditRound(写账) → `SettleReward` → 标记 Credited | if-err 上抛触发 Kafka 重试(settlement_service.go:104, 112, 119) |
| 会话结算 | `GameSettleService.SettleGame` | 聚合账单 → `creditSessionPayouts` → `platform.Settle` 每玩家 | `UpdateGameSettleStatus` Failed + 日志(game_settle_service.go:148-158) |
| 退款 | `RefundService.ApplyForRefund` → `ApproveRefund` → `executeRefund` | 审批 → `platform.Credit` | `UpdateRefundAuditError`(refund_service.go:159, 183) |
| 入账重试 | `CreditRetryService.RetryCredit` | 指数退避重试 `platform.Credit` | `createException`(credit_retry_service.go:211) |

### 1.2 当前模式:分散的手工补偿(非 Saga 非 TCC)

**特征**:

1. **局部 MySQL 事务**:[bill_manager.go](../settlement/service/bill_manager.go) 的 `CreateRoundSettlementAndBills` / `CreateBillsPairInTransaction` / `CreateBillsInTransaction` / `UpdateRefundSuccessInTransaction` 只覆盖单步写库
2. **分布式锁防并发**:`lock.WithRedisLock` 包裹每个流程入口
3. **失败处理散落**:每个 service 自己 if-err 处理,有的创建 RefundAudit、有的创建 ExceptionRecord、有的只 log 一行
4. **重试策略各自实现**:`CreditRetryService` 用指数退避;`GameSettleService.RetryPlayerSettle` 直接重试无退避;`SettleRound` 靠 Kafka 消费者重投
5. **幂等性靠业务字段**:如 `ExistsByRoundAndType` / `GetBillByRoundTypeAndUser` 检查,无统一幂等键管理
6. **无全局状态机**:流程进度无法查询,只知道单步 bill/refund_audit 状态,不知道"这个 session 的结算卡在哪一步"

### 1.3 痛点

- **D-07(已识别)**:补偿逻辑散弹枪式,无统一事务编排抽象
- **可观测性差**:结算卡住时无法快速定位"卡在哪一步"
- **补偿重复实现**:`handleFirstRoundDeductFailure` 与 `RefundService.ApplyForRefund` 做的事高度重叠(都是为已扣款 bill 创建 RefundAudit)
- **重试策略不统一**:5 个流程各有一套重试语义
- **测试难**:见 [CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md) D-02,核心资金逻辑零测试保护

---

## 二、Saga vs TCC:概念对比

### 2.1 核心机制

| 维度 | Saga | TCC |
|------|------|-----|
| **操作模型** | 正向操作 + 反向补偿(Try→...→Compensate) | 三阶段:Try(资源预留)→ Confirm(确认)→ Cancel(释放) |
| **一致性** | 最终一致(中间状态可见) | 最终一致(中间状态被 Try 资源锁定隐藏) |
| **隔离性** | 弱(中间态可被读到) | 较强(Try 阶段冻结资源,业务不可见) |
| **参与者要求** | 每步支持正向 + 反向两个接口 | 每步支持 Try / Confirm / Cancel 三个接口 |
| **失败回滚** | 已执行的正向操作逐个反向补偿 | 调用 Cancel 释放 Try 阶段冻结的资源 |
| **适用场景** | 长流程、外部系统参与、资源无法冻结 | 短事务、内部系统、可冻结资源(库存/额度) |
| **实现复杂度** | 中(状态机 + 补偿函数) | 高(每参与者三接口 + 资源冻结逻辑) |
| **典型例子** | 订单流程(支付+发货+通知) | 库存扣减、账户转账 |

### 2.2 子类型

**Saga** 有两种实现风格:

| 风格 | 协调方式 | 优点 | 缺点 |
|------|---------|------|------|
| **Choreography(编排式)** | 事件驱动,各服务订阅事件自治响应 | 完全解耦,适合微服务 | 事件环路、流程难追踪、易死循环 |
| **Orchestration(协调式)** | 中央协调器驱动状态机,显式调用各步 | 流程清晰、可观测、易调试 | 中心化,协调器单点 |

**TCC** 通常无子类型,但 Confirm/Cancel 必须幂等且快速。

### 2.3 失败处理对比(以"首回合扣款"为例)

**Saga 模式**:
```
Step1: Debit 用户A 成功
Step2: Debit 用户B 失败 → 触发补偿
  Compensate Step1: Credit 用户A(退款)
最终: 用户A 收到退款,用户B 未扣款
```

**TCC 模式**:
```
Try1: 冻结用户A 余额 100
Try2: 冻结用户B 余额 100 → 失败
Cancel1: 解冻用户A 余额 100
最终: 用户A 余额不变(业务不可见中间态),用户B 未扣款
```

**关键差异**:TCC 要求"冻结"语义,平台若不支持"冻结余额"接口则无法实现 TCC。

---

## 三、本项目场景分析

### 3.1 资金链路特征

经 [deduct_service.go:200-281](../settlement/service/deduct_service.go) 与 [game_settle_service.go:164-234](../settlement/service/game_settle_service.go) 审查,关键事实:

1. **外部平台接口只支持 Debit / Credit / Settle / GetBalance 四个动作**([platform/client.go](../api/platform/client.go))
   - **不支持 Try(冻结)/Confirm(确认)/Cancel(解冻) 三阶段语义**
   - 资金一旦 Debit 成功,只能靠反向 Credit 退回,无法"解冻"

2. **业务流程长**:首回合扣款 → 抢红包(游戏侧) → 回合结算 → 会话结算,横跨 game 与 settlement 两个限界上下文

3. **已是 in-process 单体**:settlement 被 game 进程内嵌(见 [B-02](./CODE_ARCHITECTURE_REFACTOR_PLAN.md#b-02)),跨服务通信实为进程内调用

4. **现有补偿能力**:
   - RefundAudit 表 + RefundService 已实现"扣款退款"补偿
   - ExceptionRecord 表 + ExceptionManager 已实现"异常登记"
   - CreditRetryScheduler 已实现异步指数退避重试
   - BillRecord 的 `retry_count` / `next_retry_at` / `status` 字段已支持状态追踪

5. **幂等性已部分落地**:每个 bill 有 `biz_order_no` 作为幂等键,platform 端按 biz_id 去重

### 3.2 关键约束(来自 project_memory)

- **资金安全硬约束**:虚拟余额扣减必须 Lua 原子化
- **数据一致性**:事务操作失败必须返回错误触发回滚
- **Settlement 不能 import game**

### 3.3 关键判断点

**TCC 不可行的根本原因**:

- TCC 要求每个参与者提供 Try/Confirm/Cancel 三接口。**外部 platform 只提供 Debit/Credit 两接口,且 Debit 是"真扣款"而非"冻结"**,无法改造为 Try 语义
- 改 TCC 意味着要么自己实现"冻结层"(在 settlement 与 platform 之间加一层本地账户,记录冻结额度),要么放弃接入外部平台 — 成本极高且偏离业务

**Saga 可行的关键依据**:

- 反向补偿 = `platform.Credit`,已落地于 [refund_service.go:156-205](../settlement/service/refund_service.go)
- 已有 RefundAudit/ExceptionRecord 表作为补偿日志,扩展成本低
- 已有异步重试调度器,只需把分散的补偿整合为 Saga 协调器

---

## 四、选型结论:Orchestration Saga

### 4.1 选型

**Orchestration Saga(协调式 Saga)**

### 4.2 选型依据

| 决策点 | Saga | TCC | 结论 |
|--------|------|-----|------|
| 外部平台接口支持 | ✅ 正向+反向双接口(Debit/Credit) | ❌ 仅两接口,无 Try/Confirm/Cancel | **Saga** |
| 改造成本 | 中(整合现有补偿) | 高(需自建冻结层) | **Saga** |
| 业务流程长度 | 长流程适合 Saga | 短流程才适合 TCC | **Saga** |
| 隔离性要求 | 弱(中间态可见可接受,业务允许"已扣款待退款") | 强(资金不能被中间态污染) | 业务允许弱隔离 → **Saga** |
| 现有代码基础 | RefundService/ExceptionManager 可复用 | 需推倒重来 | **Saga** |
| Choreography vs Orchestration | — | — | 单进程内显式调用比事件环路清晰 → **Orchestration** |

### 4.3 为什么选 Orchestration 而非 Choreography

| 因素 | Orchestration 优势 |
|------|-------------------|
| 单进程内调用 | 无需事件分发,显式调用更高效 |
| 流程线性 | 扣款→结算→会话结算 是线性流程,适合状态机驱动 |
| 可观测性 | 中央协调器可记录全局进度,易查"卡在哪一步" |
| 已有 BillManager | 扩展为 Saga 状态表成本低 |
| 避免 Choreography 死循环 | 单体内事件环路风险高 |
| 可调试 | 状态机显式,易单元测试 |

---

## 五、详细实现方案

### 5.1 总体架构

```
┌─────────────────────────────────────────────────────┐
│  Game Event Consumer (Kafka)                        │
│  ─────────────────────────────────                  │
│  解析事件 → 调用 Saga 协调器入口                     │
└──────────────────────┬──────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────┐
│  SagaOrchestrator (新增,中央协调器)                 │
│  ─────────────────────────────────                  │
│  • 创建 Saga 实例(持久化 saga_instance 表)          │
│  • 驱动状态机: 执行下一步 / 失败时反向补偿           │
│  • 幂等控制(基于 saga_id + step_no)                 │
│  • 重试调度(复用现有 BaseScheduler)                │
└──────┬──────────────────────────────────────────────┘
       │ 调用
       ▼
┌─────────────────────────────────────────────────────┐
│  SagaStep 接口实现(包装现有 service)                │
│  ─────────────────────────────────                  │
│  • DeductStep (包装 DeductService)                  │
│  • CreditRoundStep (包装 SettlementService)         │
│  • SettleRewardStep (包装 RewardSettler)             │
│  • SessionSettleStep (包装 GameSettleService)       │
│  每步实现 Execute() + Compensate()                  │
└──────┬──────────────────────────────────────────────┘
       │ 复用
       ▼
┌─────────────────────────────────────────────────────┐
│  现有基础设施(复用,不改)                           │
│  ─────────────────────────────────                  │
│  BillManager / RefundService / ExceptionManager    │
│  CreditRetryScheduler / BaseScheduler              │
└─────────────────────────────────────────────────────┘
```

### 5.2 数据模型

新增两张表(与现有 BillRecord/RefundAudit/ExceptionRecord 解耦):

#### 表 1:`saga_instance`(Saga 实例)

```sql
CREATE TABLE saga_instance (
    id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
    saga_id         VARCHAR(64)  NOT NULL UNIQUE COMMENT '业务唯一 ID,如 settle_round_{roundID}',
    saga_type       VARCHAR(32) NOT NULL COMMENT 'Saga 类型,如 SETTLE_ROUND / SETTLE_GAME',
    business_key    VARCHAR(128) NOT NULL COMMENT '业务键,如 round_id / session_id',
    status          TINYINT     NOT NULL COMMENT '0=RUNNING 1=COMPLETED 2=COMPENSATING 3=FAILED 4=COMPENSATED',
    current_step    INT         NOT NULL DEFAULT 0 COMMENT '当前执行到第几步(0-based)',
    payload         JSON        COMMENT 'Saga 输入参数(序列化的 dto.RoundSettleRequest 等)',
    result          JSON        COMMENT '最终结果',
    error_message   TEXT        COMMENT '失败原因',
    retry_count     INT         NOT NULL DEFAULT 0,
    next_retry_at   DATETIME,
    created_at      DATETIME    NOT NULL,
    updated_at      DATETIME    NOT NULL,
    INDEX idx_status_retry (status, next_retry_at),
    INDEX idx_business (saga_type, business_key)
) ENGINE=InnoDB COMMENT='Saga 事务实例';
```

#### 表 2:`saga_step_log`(步骤执行日志)

```sql
CREATE TABLE saga_step_log (
    id              BIGINT      PRIMARY KEY AUTO_INCREMENT,
    saga_id         VARCHAR(64) NOT NULL,
    step_no         INT         NOT NULL COMMENT '步骤序号',
    step_name       VARCHAR(64) NOT NULL COMMENT '步骤名,如 DEDUCT_FIRST_ROUND',
    action          VARCHAR(16) NOT NULL COMMENT 'EXECUTE / COMPENSATE',
    status          TINYINT     NOT NULL COMMENT '0=STARTED 1=SUCCESS 2=FAILED',
    request         JSON        COMMENT '步骤入参',
    response        JSON        COMMENT '步骤返回',
    error_message   TEXT,
    started_at      DATETIME    NOT NULL,
    finished_at     DATETIME,
    UNIQUE KEY uk_saga_step_action (saga_id, step_no, action) COMMENT '幂等性保证',
    INDEX idx_saga (saga_id)
) ENGINE=InnoDB COMMENT='Saga 步骤执行日志';
```

**设计要点**:

- `saga_id` 唯一,如 `settle_round_{roundID}` —— 同一业务重入时返回已有 saga,实现**幂等**
- `saga_step_log` 的 `UNIQUE(saga_id, step_no, action)` —— 防止同一步骤重复执行(幂等)
- `status` 五态机:`RUNNING → COMPLETED` 或 `RUNNING → COMPENSATING → COMPENSATED` 或 `COMPENSATING → FAILED`
- `next_retry_at` —— 复用现有 `BaseScheduler` 扫描重试

### 5.3 核心接口定义

新建 `settlement/domain/saga/` 目录:

```
settlement/domain/saga/
├── saga.go          # Saga 实例与状态枚举
├── step.go          # SagaStep 接口
├── definition.go    # SagaDefinition(步骤编排)
└── orchestrator.go  # Orchestrator 接口
```

**SagaStep 接口**(每个业务步骤实现):

```go
package saga

import "context"

// Action 标识是执行还是补偿
type Action string

const (
    ActionExecute    Action = "EXECUTE"
    ActionCompensate Action = "COMPENSATE"
)

// StepContext 传递给每个步骤
type StepContext struct {
    SagaID      string
    StepNo      int
    Payload     []byte        // saga_instance.payload
    StepResult  []byte        // 上一步的输出(链式传递)
}

// SagaStep 每个业务步骤实现此接口
type SagaStep interface {
    // Name 步骤唯一名(用于日志/调试)
    Name() string

    // Execute 正向执行。返回的 result 会被记录到 step_log 并传给下一步
    Execute(ctx context.Context, sctx *StepContext) (result []byte, err error)

    // Compensate 反向补偿。仅当 status=SUCCESS 的步骤才需要补偿
    Compensate(ctx context.Context, sctx *StepContext) error

    // Retryable 是否可重试(网络错误等可重试,业务规则错误不可重试)
    Retryable(err error) bool

    // CompensateOnFailure 失败时是否触发补偿(某些步骤如只读查询无需补偿)
    CompensateOnFailure() bool
}
```

**SagaDefinition**(步骤编排):

```go
package saga

// SagaDefinition 描述一类 Saga 的步骤序列
type SagaDefinition struct {
    SagaType string
    Steps    []SagaStep
}

// IsFinalStep 判断是否最后一步
func (d *SagaDefinition) IsFinalStep(stepNo int) bool {
    return stepNo >= len(d.Steps)-1
}
```

**Orchestrator 接口**:

```go
package saga

import "context"

// Orchestrator 中央协调器
type Orchestrator interface {
    // Start 启动一个新 Saga(若 saga_id 已存在则直接返回已有实例,实现幂等)
    Start(ctx context.Context, sagaID string, def *SagaDefinition, payload []byte) (*Instance, error)

    // Resume 恢复执行(被调度器唤起时调用)
    Resume(ctx context.Context, sagaID string) error

    // GetStatus 查询 Saga 状态(可观测性)
    GetStatus(ctx context.Context, sagaID string) (*Instance, error)

    // Compensate 触发手动补偿(运维用)
    Compensate(ctx context.Context, sagaID string) error
}
```

### 5.4 状态机流转

```
                    ┌──────────────────────────────────────────┐
                    │                                          │
                    ▼                                          │ retry
              ┌──────────┐                              ┌──────┴──────┐
   Start ───► │ RUNNING  │ ── step success until ───► │  RUNNING     │
              └────┬─────┘     final step               └──────────────┘
                   │                                           │
                   │ all steps done                            │ step failed &
                   │                                           │ retryable & retry_count < max
                   ▼                                           │
              ┌───────────┐                                     │
              │ COMPLETED  │                                     │
              └───────────┘                                     │
                                                                │
                   ┌─────────────────────┐                      │
                   │                     │                      │
                   │  step failed &      │                      │
                   │  retry exhausted ◄───┴──── step failed & ───┘
                   │                     │      not retryable
                   ▼                     │
              ┌──────────────┐            │
              │ COMPENSATING │ ◄──────────┘
              └──────┬───────┘
                     │
                     │ all compensations success
                     ▼
              ┌───────────────┐
              │ COMPENSATED    │
              └───────────────┘
                     │
                     │ compensation failed
                     ▼
              ┌───────────┐
              │  FAILED    │ ──► 创建 ExceptionRecord,人工介入
              └───────────┘
```

### 5.5 关键流程:首回合扣款 Saga 示例

将 [DeductService.DeductForFirstRound](../settlement/service/deduct_service.go) 改造为 Saga:

| step_no | step_name | Execute | Compensate |
|---------|-----------|---------|------------|
| 0 | CREATE_ROUND_SETTLEMENT | 创建 round_settlement + bills(processing) | 删除 round_settlement(或标 ABORTED) |
| 1 | DEDUCT_ALL_PLAYERS | 批量 `platform.Debit`(并发) | 对已成功 bill 调 `RefundService.ApplyForRefund` |
| 2 | UPDATE_SETTLEMENT_DEDUCTED | 标记 round_settlement 为 Deducted | 无(纯状态更新,无需补偿) |

**Saga 启动入口**(替换现有 `DeductForFirstRound`):

```go
func (s *DeductService) DeductForFirstRound(ctx context.Context, req *dto.FirstRoundDeductRequest) (*dto.FirstRoundDeductResult, error) {
    sagaID := fmt.Sprintf("deduct_first_round_%d", req.RoundID)

    def := &saga.SagaDefinition{
        SagaType: "DEDUCT_FIRST_ROUND",
        Steps: []saga.SagaStep{
            NewCreateRoundSettlementStep(s.billMgr, s.traceIDGen, req),
            NewDeductAllPlayersStep(s),                              // 复用现有 executeSingleDeduct
            NewUpdateSettlementDeductedStep(s.billMgr),
        },
    }

    payload, _ := json.Marshal(req)
    inst, err := s.orchestrator.Start(ctx, sagaID, def, payload)
    if err != nil {
        return nil, err
    }

    // 查询结果构造 dto 返回
    return s.buildFirstRoundResultFromSaga(inst)
}
```

**DeductAllPlayersStep 的 Compensate 实现**(替换现有 `handleFirstRoundDeductFailure`):

```go
func (s *DeductAllPlayersStep) Compensate(ctx context.Context, sctx *saga.StepContext) error {
    // 从 step_log 取 Execute 阶段记录的成功/失败玩家列表
    stepLog, err := s.sagaLogRepo.GetStepLog(ctx, sctx.SagaID, sctx.StepNo, saga.ActionExecute)
    if err != nil {
        return err
    }

    var execResult DeductBatchResult
    json.Unmarshal(stepLog.Response, &execResult)

    // 对所有已成功扣款的玩家发起退款(复用 RefundService)
    for _, successUserID := range execResult.SuccessPlayers {
        bill, _ := s.billMgr.GetBillByBatchAndUser(ctx, execResult.BatchID, successUserID)
        if bill == nil || bill.Status != dto.BillStatusSuccess {
            continue
        }
        _, err := s.refundService.ApplyForRefund(ctx, &dto.RefundApplyRequest{
            BillID:       bill.ID,
            RefundAmount: -bill.Amount,
            RefundReason: "首回合扣款 Saga 补偿",
            RefundType:   dto.RefundTypeFirstRoundFail,
        })
        if err != nil {
            return err  // 任一补偿失败则 Saga 转 FAILED
        }
    }
    return nil
}
```

**收益对比**:

| 项目 | 改造前(deduct_service.go:283-319) | 改造后 |
|------|-----------------------------------|--------|
| 补偿触发 | 手工 if-err 调用 handleFirstRoundDeductFailure | 状态机自动驱动 |
| 补偿幂等 | 无保护,可能重复退款 | step_log 唯一键保证 |
| 补偿状态 | 散在 bill.refund_status / refund_audit | saga_instance.status 集中可见 |
| 失败定位 | 需查多张表 | 查 saga_instance + saga_step_log |

### 5.6 复用现有补偿器

现有组件直接作为 SagaStep 的内部依赖,**不重写**:

| 现有组件 | 在 Saga 中的角色 |
|---------|------------------|
| [RefundService](../settlement/service/refund_service.go) | 反向补偿执行器(DeductStep 的 Compensate 调用) |
| [ExceptionManager](../settlement/service/exception_manager.go) | Saga FAILED 时登记异常 |
| [CreditRetryScheduler](../settlement/scheduler/credit_retry_scheduler.go) | 改造为扫描 saga_instance.status=RUNNING & next_retry_at < now |
| [BaseScheduler](../settlement/scheduler/base.go) | 直接复用 |
| BillManager 的各种 Transaction 方法 | 作为 SagaStep.Execute 的实现 |

### 5.7 幂等性设计

**三层幂等**:

1. **Saga 级**:`saga_id` 唯一,`Start` 时若已存在直接返回已有实例
2. **步骤级**:`saga_step_log` 的 `UNIQUE(saga_id, step_no, action)` 防止同一步骤重复执行
3. **业务级**:每个 `platform.Debit` / `Credit` 调用带 `biz_id`(已是现有实现),platform 端按 biz_id 去重

**重入场景**:
- Kafka 重投 → `Start` 返回已有 saga_instance → `Resume` 跳过已成功的步骤,从中断点继续
- 调度器重试 → `Resume` 同上

### 5.8 可观测性增强

新增查询能力(运维与监控):

```go
// 查询某 session 的结算进度
GET /admin/saga/settle_game/{sessionID}
返回: saga_instance.status / current_step / 各 step_log 状态

// 查询所有卡住的 Saga
GET /admin/saga/stuck?status=RUNNING&older_than=10m
返回: 卡在 RUNNING 状态超过 10 分钟的 Saga 列表

// 手动触发补偿
POST /admin/saga/{sagaID}/compensate
```

---

## 六、核心代码示例

### 6.1 SagaOrchestrator 实现骨架

```go
package saga

import (
    "context"
    "encoding/json"
    "fmt"
    "time"
)

type OrchestratorImpl struct {
    repo        SagaRepository       // 持久化 saga_instance / saga_step_log
    retryPolicy RetryPolicy          // 重试策略(可配置)
}

func (o *OrchestratorImpl) Start(ctx context.Context, sagaID string, def *SagaDefinition, payload []byte) (*Instance, error) {
    // 幂等:若已存在则直接返回
    if existing, _ := o.repo.GetInstance(ctx, sagaID); existing != nil {
        return existing, nil
    }

    inst := &Instance{
        SagaID:     sagaID,
        SagaType:   def.SagaType,
        Status:     StatusRunning,
        Payload:    payload,
        CreatedAt:  time.Now(),
        UpdatedAt:  time.Now(),
    }
    if err := o.repo.CreateInstance(ctx, inst); err != nil {
        return nil, err
    }

    return inst, o.Resume(ctx, sagaID)
}

func (o *OrchestratorImpl) Resume(ctx context.Context, sagaID string) error {
    inst, err := o.repo.GetInstance(ctx, sagaID)
    if err != nil {
        return err
    }
    if inst.Status != StatusRunning {
        return nil  // 已终态
    }

    def := o.registry.Get(inst.SagaType)
    if def == nil {
        return fmt.Errorf("saga definition not found: %s", inst.SagaType)
    }

    // 从 current_step 继续
    for stepNo := inst.CurrentStep; stepNo < len(def.Steps); stepNo++ {
        step := def.Steps[stepNo]
        sctx := &StepContext{
            SagaID: sagaID,
            StepNo: stepNo,
            Payload: inst.Payload,
        }

        // 幂等:检查该步骤是否已成功执行
        if log, _ := o.repo.GetStepLog(ctx, sagaID, stepNo, ActionExecute); log != nil && log.Status == StepStatusSuccess {
            sctx.StepResult = log.Response
            continue
        }

        // 记录步骤开始
        stepLog := &StepLog{
            SagaID:    sagaID,
            StepNo:    stepNo,
            StepName:  step.Name(),
            Action:    ActionExecute,
            Status:    StepStatusStarted,
            StartedAt: time.Now(),
        }
        _ = o.repo.CreateStepLog(ctx, stepLog)

        // 执行
        result, err := step.Execute(ctx, sctx)
        if err != nil {
            stepLog.ErrorMessage = err.Error()
            stepLog.Status = StepStatusFailed
            _ = o.repo.UpdateStepLog(ctx, stepLog)

            if !step.Retryable(err) || !o.retryPolicy.CanRetry(inst.RetryCount) {
                // 触发补偿
                return o.compensate(ctx, inst, def, stepNo)
            }

            // 可重试:更新 next_retry_at,等调度器唤起
            inst.NextRetryAt = o.retryPolicy.NextRetry(inst.RetryCount)
            inst.RetryCount++
            _ = o.repo.UpdateInstance(ctx, inst)
            return nil
        }

        // 步骤成功
        stepLog.Status = StepStatusSuccess
        stepLog.Response = result
        stepLog.FinishedAt = time.Now()
        _ = o.repo.UpdateStepLog(ctx, stepLog)

        inst.CurrentStep = stepNo + 1
        inst.UpdatedAt = time.Now()
        _ = o.repo.UpdateInstance(ctx, inst)
    }

    // 全部成功
    inst.Status = StatusCompleted
    inst.UpdatedAt = time.Now()
    return o.repo.UpdateInstance(ctx, inst)
}

func (o *OrchestratorImpl) compensate(ctx context.Context, inst *Instance, def *SagaDefinition, failedStepNo int) error {
    inst.Status = StatusCompensating
    _ = o.repo.UpdateInstance(ctx, inst)

    // 反向遍历已成功的步骤,调用 Compensate
    for stepNo := failedStepNo - 1; stepNo >= 0; stepNo-- {
        step := def.Steps[stepNo]
        if !step.CompensateOnFailure() {
            continue
        }

        // 幂等:检查该步骤是否已补偿
        if log, _ := o.repo.GetStepLog(ctx, inst.SagaID, stepNo, ActionCompensate); log != nil && log.Status == StepStatusSuccess {
            continue
        }

        sctx := &StepContext{
            SagaID:  inst.SagaID,
            StepNo:  stepNo,
            Payload: inst.Payload,
        }

        stepLog := &StepLog{
            SagaID:    inst.SagaID,
            StepNo:    stepNo,
            StepName:  step.Name(),
            Action:    ActionCompensate,
            Status:    StepStatusStarted,
            StartedAt: time.Now(),
        }
        _ = o.repo.CreateStepLog(ctx, stepLog)

        if err := step.Compensate(ctx, sctx); err != nil {
            stepLog.ErrorMessage = err.Error()
            stepLog.Status = StepStatusFailed
            _ = o.repo.UpdateStepLog(ctx, stepLog)

            // 补偿失败 → Saga 转 FAILED,登记异常
            inst.Status = StatusFailed
            inst.ErrorMessage = fmt.Sprintf("compensate step %d failed: %v", stepNo, err)
            _ = o.repo.UpdateInstance(ctx, inst)
            return err
        }

        stepLog.Status = StepStatusSuccess
        stepLog.FinishedAt = time.Now()
        _ = o.repo.UpdateStepLog(ctx, stepLog)
    }

    inst.Status = StatusCompensated
    inst.UpdatedAt = time.Now()
    return o.repo.UpdateInstance(ctx, inst)
}
```

### 6.2 RetryPolicy(统一重试策略)

替换现有 [credit_retry_service.go:181-188](../settlement/service/credit_retry_service.go) 的 `calculateNextRetryTime`:

```go
package saga

type RetryPolicy struct {
    MaxRetryCount   int
    BaseDelay       time.Duration
    MaxDelay        time.Duration
    RetryMultiplier float64
}

func DefaultRetryPolicy() *RetryPolicy {
    return &RetryPolicy{
        MaxRetryCount:   5,
        BaseDelay:       5 * time.Second,
        MaxDelay:        5 * time.Minute,
        RetryMultiplier: 2.0,
    }
}

func (p *RetryPolicy) CanRetry(retryCount int) bool {
    return retryCount < p.MaxRetryCount
}

func (p *RetryPolicy) NextRetry(retryCount int) time.Time {
    delay := time.Duration(float64(p.BaseDelay) *
        math.Pow(p.RetryMultiplier, float64(retryCount)))
    if delay > p.MaxDelay {
        delay = p.MaxDelay
    }
    return time.Now().Add(delay)
}

// Retryable 判断错误是否可重试
func (p *RetryPolicy) Retryable(err error) bool {
    var pe *platform.PlatformError
    if errors.As(err, &pe) {
        // 网络错误/超时/5xx 可重试,4xx 业务错误不可重试
        return pe.IsTransient()
    }
    return false
}
```

### 6.3 SagaDefinition 注册表

```go
package saga

type Registry struct {
    definitions map[string]*SagaDefinition
}

func (r *Registry) Register(def *SagaDefinition) {
    r.definitions[def.SagaType] = def
}

func (r *Registry) Get(sagaType string) *SagaDefinition {
    return r.definitions[sagaType]
}

// 在 settlement/bootstrap 中注册所有 Saga 定义
func RegisterAllSagas(reg *Registry, deps *Dependencies) {
    reg.Register(&SagaDefinition{
        SagaType: "DEDUCT_FIRST_ROUND",
        Steps: []SagaStep{
            NewCreateRoundSettlementStep(deps.BillMgr, deps.TraceIDGen),
            NewDeductAllPlayersStep(deps.DeductSvc),
            NewUpdateSettlementDeductedStep(deps.BillMgr),
        },
    })

    reg.Register(&SagaDefinition{
        SagaType: "SETTLE_ROUND",
        Steps: []SagaStep{
            NewUpdateSettleInfoStep(deps.BillMgr),
            NewCreditRoundStep(deps.SettlementSvc),
            NewSettleRewardStep(deps.RewardSettler),
            NewMarkRoundCreditedStep(deps.BillMgr),
        },
    })

    reg.Register(&SagaDefinition{
        SagaType: "SETTLE_GAME",
        Steps: []SagaStep{
            NewAggregateBillsStep(deps.BillMgr),
            NewCreditSessionPayoutsStep(deps.GameSettleSvc),
            NewSettleEachPlayerStep(deps.GameSettleSvc),
            NewMarkGameSettledStep(deps.BillMgr),
        },
    })
}
```

---

## 七、迁移路线

> 原则:**先建框架,逐流程迁移,新老并存**。不一次性重写。

### 阶段 1:基础设施搭建(P0,先行)

- [ ] 新建 `settlement/domain/saga/` 包,实现 `SagaStep` / `SagaDefinition` / `Orchestrator` 接口
- [ ] 新建 `settlement/infrastructure/persistence/mysql/saga_repository.go`,实现 `SagaRepository`
- [ ] 创建 `saga_instance` / `saga_step_log` 表的 migration
- [ ] 实现 `OrchestratorImpl` + `RetryPolicy`
- [ ] 单元测试:用 fake steps 验证状态机正确性(无需连真实 DB)

### 阶段 2:首回合扣款 Saga 试点(P1)

选择 `DeductForFirstRound` 作为试点,因为:
- 流程短(3 步)
- 补偿逻辑现成(已有 `handleFirstRoundDeductFailure` 与 `RefundService`)
- 失败影响可控(扣款未完成,游戏未开始)

- [ ] 实现 3 个 SagaStep:`CreateRoundSettlementStep` / `DeductAllPlayersStep` / `UpdateSettlementDeductedStep`
- [ ] 改造 `DeductService.DeductForFirstRound` 为调用 `orchestrator.Start`
- [ ] 改造 `CreditRetryScheduler` 增加扫描 `saga_instance.status=RUNNING & next_retry_at < now` 的逻辑(或新建 SagaRetryScheduler)
- [ ] 对比测试:对比新旧实现的扣款/退款结果一致性

### 阶段 3:回合结算与会话结算迁移(P1-P2)

- [ ] 迁移 `SettlementService.SettleRound`(4 步:UpdateSettleInfo → CreditRound → SettleReward → MarkCredited)
- [ ] 迁移 `GameSettleService.SettleGame`(4 步)
- [ ] 统一 `SettleGame` 中的 `creditSessionPayouts` 与 `settlePlayer` 重试为 Saga 重试机制
- [ ] 移除 `GameSettleService.RetryPlayerSettle`,由 Saga 统一处理

### 阶段 4:退款与异常流程接入(P2)

- [ ] 退款流程 `RefundService.ApplyForRefund` → `ApproveRefund` → `executeRefund` 改造为 Saga(3 步)
- [ ] Saga FAILED 时自动调用 `ExceptionManager.Create`(替换 `CreditRetryService.createException`)
- [ ] 提供管理端查询接口(`/admin/saga/{sagaID}`)

### 阶段 5:清理冗余(P2-P3)

- [ ] 移除 `DeductService.handleFirstRoundDeductFailure`(逻辑已被 Saga Compensate 替代)
- [ ] 移除 `CreditRetryService.calculateNextRetryTime`(被 `RetryPolicy.NextRetry` 替代)
- [ ] `BillRecord` 的 `retry_count` / `next_retry_at` 字段保留(单步重试用),但全局重试由 Saga 接管
- [ ] 整合 [CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md) 的 D-07:Manager 改名 Repository 下沉

### 阶段 6:边界扩展(P3,可选)

- [ ] 若未来 settlement 拆为独立服务(见 [B-02](./CODE_ARCHITECTURE_REFACTOR_PLAN.md#b-02)),Orchestrator 可天然支持跨服务 Saga —— 只需把 SagaStep 的 Execute/Compensate 改为 gRPC 调用
- [ ] 与 game 侧的抢红包 Lua 脚本协作可通过定义 `game` 端的 SagaStep 适配器(进程内调用)

---

## 八、风险与边界

### 8.1 风险

| 风险 | 应对 |
|------|------|
| Saga 框架引入新复杂度 | 阶段 1 单元测试覆盖状态机;阶段 2 试点后评估再推广 |
| `saga_instance` 表成为热点 | 加 `idx_status_retry` 索引;调度器分批扫描;终态数据定期归档 |
| 迁移期间新老逻辑并存导致行为不一致 | `DeductForFirstRound` 入口单一,迁移后老代码下线,不长期并存 |
| 补偿操作本身失败(如 platform.Credit 也失败) | Saga 转 FAILED + 登记 ExceptionRecord + 告警人工介入(已是现有模式) |
| 中间状态可见性问题(Saga 弱隔离) | 业务已接受(现有实现也是弱隔离),不引入新风险 |

### 8.2 不适用 Saga 的场景(边界)

以下场景保持现有实现,**不强行套 Saga**:

1. **单步 MySQL 事务**:`CreateBillsPairInTransaction` / `UpdateRefundSuccessInTransaction` 等 — 本就是 ACID,无需 Saga
2. **Redis Lua 原子操作**:虚拟余额扣减(已用 Lua 保证原子性,见 project_memory 约束)
3. **只读查询**:`GetBillsByUserID` / `CheckBalance` 等
4. **机器人虚拟通道**:不走 platform,无外部依赖,无需补偿

### 8.3 与现有约束的兼容性

| 约束 | 兼容性 |
|------|--------|
| `cashparty:` Redis key 前缀 | Saga 用 MySQL 表,不涉及 Redis key;分布式锁仍走 `lock.WithRedisLock` |
| Settlement 不 import game | Saga 定义在 `settlement/domain/saga/`,不依赖 game |
| 虚拟余额 Lua 原子化 | Saga 不改变虚拟余额扣减的 Lua 实现,只在 `DeductAllPlayersStep` 内部调用 |
| 事务失败返回 error 触发回滚 | SagaStep.Execute 失败上抛 error 触发补偿,符合现有模式 |

### 8.4 性能考量

- **单流程增加 2 张表的写入开销**:saga_instance + saga_step_log,每步 ~2 次 INSERT
- **对比**:现有实现每步也已写 BillRecord + 可能写 RefundAudit/ExceptionRecord,增量开销 < 20%
- **收益**:统一重试/补偿/可观测,降低运维成本与定位时间,值得

---

## 附录:与 D-07 的关系

本文是 [CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md) 中 **D-07** 的细化实现方案:

> **D-07 [中] settlement 补偿/重试/检查逻辑散弹枪式,缺统一事务编排抽象**
> 解决方案:抽出 `TransactionOrchestrator` / `SagaCoordinator` 接口...

本文给出了具体的:
- 选型依据(Saga vs TCC → 选 Orchestration Saga)
- 数据模型(saga_instance / saga_step_log 表)
- 接口定义(SagaStep / SagaDefinition / Orchestrator)
- 状态机设计
- 代码骨架
- 6 阶段迁移路线

实施时建议与 [CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md) 的**阶段 3(settlement 分层补齐)**结合:先把 BillManager 等下沉为 Repository(A-02),再在其上搭建 Saga 框架,避免在脏的 service 层上叠加新抽象。

---

**文档版本**:v1.0
**前置依赖**:[CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md) 阶段 0/1 完成
**实施顺序**:本文阶段 1-2 可与 CODE_ARCHITECTURE_REFACTOR_PLAN 阶段 1-2 并行
