# Settlement 模块代码审查与重构方案

## 一、审查概览

对 `backend/settlement` 模块全部 24 个 Go 源文件进行系统化审查，按严重程度分为 **P0-致命 / P1-严重 / P2-中等 / P3-轻微** 四级，共识别出 **6 个 P0、8 个 P1、10 个 P2、7 个 P3** 问题。

---

## 二、问题清单

### P0 - 致命（资金安全 / 数据一致性）

#### P0-1: DeductService 内部 CreditRetryService 使用 nil DB，运行时 panic

- **文件**: `service/deduct_service.go:40-41`
- **现状**: `NewDeductService` 内部自建 `NewExceptionManager(nil)` + `NewCreditRetryService(...)`，传入的 `*gorm.DB` 为 `nil`
- **影响**: 当首回合扣款有玩家失败时，调用 `CreditRetryService.CreateDebitFailedException` → `ExceptionManager.Create` → **nil 指针 dereference panic**，整个请求崩溃
- **根因**: `SettlementService` 和 `DeductService` 各自独立创建了 `CreditRetryService` 实例，DeductService 构造时没有接收 `db` 参数

#### P0-2: platform.ParseAmount 错误被全局静默忽略，余额数据可能为 0

- **文件**: `service/settlement_service.go:97,289,337,474` / `service/deduct_service.go:204,311` / `service/credit_retry_service.go:136`
- **现状**: 所有 `balanceAfter, _ := platform.ParseAmount(...)` 均忽略 error
- **影响**: 若金额解析失败，`balanceAfter = 0` 被写入 `BillRecord.BalanceAfter`，在金融系统中意味着 **审计数据被静默篡改**，账单记录的余额与平台真实余额不一致
- **影响范围**: 涉及扣款、入账、惩罚分配、系统奖励等所有资金流操作

#### P0-3: 关键 DB 更新错误被丢弃，可能导致资金双重操作

- **文件**: `service/settlement_service.go:213-219`
- **现状**: `creditRound` 中 `UpdateRoundSettlementSuccess` 和 `UpdateRoundSettlementStatus` 的错误均被完全忽略
- **影响**: 平台侧已入账成功，但本地 RoundSettlement 状态未更新。重试调度器将再次触发入账 → **双重入账**，造成资金损失
- **同类**: `settlement_service.go:123-124` 仅 log 不 return；`deduct_service.go` 中部分更新错误也被丢弃

#### P0-4: 惩罚分配整数除法截断，资金丢失

- **文件**: `service/settlement_service.go:383`
- **现状**: `shareAmount := req.Amount / int64(len(req.Recipients))` 整数除法直接截断
- **影响**: `Amount=100, Recipients=3` → 每人 33，总分配 99，**1 单位资金永久丢失**
- **金融合规**: 财务系统不允许资金凭空消失

#### P0-5: 并发扣款无 goroutine 限制，可能导致平台限流 / 级联故障

- **文件**: `service/deduct_service.go:138-160`
- **现状**: `executeBatchDeduct` 为每个玩家启动一个 goroutine，无并发限制
- **影响**: 1000 个玩家 = 1000 个并发平台 API 请求 → 触发平台限流 → 大面积扣款失败 → 批量退款 → 系统雪崩

#### P0-6: RefundService.ApplyForRefund 非原子操作，数据不一致

- **文件**: `service/refund_service.go:102-108`
- **现状**: `CreateRefundAudit` + `UpdateBillRefundStatus` 不在同一事务中
- **影响**: 若第二步失败，存在孤立 RefundAudit（status=Pending）但 Bill.RefundStatus 未更新的状态，后续调度器处理时会重复创建退款或误处理

---

### P1 - 严重（业务正确性 / 可靠性）

#### P1-1: BillManager 几乎所有方法未传递 ctx 到 GORM

- **文件**: `service/bill_manager.go` 全文（593 行，几乎所有 DB 调用）
- **现状**: 方法签名接受 `ctx context.Context`，但内部用 `m.db.Model(...)` 而非 `m.db.WithContext(ctx).Model(...)`
- **影响**: 请求超时/取消不会传播到 DB 层，已废弃的请求仍可能在 DB 上执行写操作；分布式追踪无法关联

#### P1-2: ExistsBy* 方法吞掉 DB 错误，返回 false（不存在），TOCTOU 竞态

- **文件**: `service/bill_manager.go:72-86, 101-107, 354-377`
- **现状**: `ExistsByRoundAndType` 等方法在 DB 错误时返回 `false`（含义"不存在"），调用方据此创建新记录
- **影响**: DB 故障时大量重复账单被创建；与后续 Create 操作构成 TOCTOU 竞态条件

#### P1-3: SettlementService 是 God Object，构造器创建 7 个内部依赖

- **文件**: `service/settlement_service.go:33-68`
- **现状**: `NewSettlementService` 内部 `NewDeductService` + `NewRefundService` + `NewRewardSettler` + `NewCreditRetryService` + `NewGameSettleService` + `NewPlatformCallManager` + `NewUserIDConvertService`
- **影响**: 无法单独测试任何子服务；与 P0-1 联动（DeductService 内部的 CreditRetryService 实例与 SettlementService 的实例不同，前者用 nil DB）

#### P1-4: Container 中服务重复构造

- **文件**: `game/bootstrap/container.go:101-227`
- **现状**: `DeductService`、`RefundService`、`RewardSettler`、`CreditRetryService` 各被独立构造了一次，而 `SettlementService` 内部又各自构造一次。两个 `CreditRetryService` 实例持有不同的 `ExceptionManager`
- **影响**: 同一业务概念存在两个独立实例，状态不共享，行为不一致

#### P1-5: CreditRetryService 先递增重试计数再执行，错误被忽略

- **文件**: `service/credit_retry_service.go:95-98`
- **现状**: `IncrementRetryCountWithNextRetryTime` 在 `executeCredit` 之前调用，且其 error 被完全忽略
- **影响**: 若 DB 更新成功但 executeCredit 失败，retry_count 已递增但实际未重试，浪费重试次数；若 DB 更新失败，retry_count 未递增但代码以为已递增

#### P1-6: GameSettleService 幂等检查只看第一个 round

- **文件**: `service/game_settle_service.go:66`
- **现状**: `if settlements[0].GameSettleStatus == dto.GameSettleStatusSuccess` 只检查第一个
- **影响**: 若第一个 round 已 settle 但后续 round 未 settle，整个 GameSettle 被跳过，玩家丢失游戏结算

#### P1-7: RetryPlayerSettle 无分布式锁

- **文件**: `service/game_settle_service.go:210-233`
- **现状**: 与 `SettleGame` 不同，retry 路径没有分布式锁保护
- **影响**: 并发 retry 可能对同一玩家重复调用平台 settle 接口

#### P1-8: RefundService.RejectRefund 无分布式锁

- **文件**: `service/refund_service.go:163-180`
- **现状**: `ApproveRefund` 有锁，但 `RejectRefund` 没有锁
- **影响**: 并发 approve + reject 可能导致退款既被批准又被拒绝

---

### P2 - 中等（代码质量 / 可维护性）

#### P2-1: BillManager 违反 SRP，管理三种独立领域实体

- **文件**: `service/bill_manager.go`
- **现状**: 单个 struct 601 行，管理 `BillRecord` + `RoundSettlement` + `RefundAudit`
- **建议**: 拆分为 `BillRepository`、`RoundSettlementRepository`、`RefundAuditRepository`

#### P2-2: UpdateRoundSettlementStatus 忽略 errMsg 参数

- **文件**: `service/bill_manager.go:121-125`
- **现状**: `errMsg` 参数被接受但未用于更新 `error_message` 字段

#### P2-3: MaxRetryCount 常量重复定义

- **文件**: `service/bill_manager.go:12` 和 `service/credit_retry_service.go:28`
- **现状**: 两处 `MaxRetryCount = 3`，可漂移不一致

#### P2-4: credit_retry_service 和 pair_bill_check_service 引用不同的 MaxRetryCount

- **文件**: `service/pair_bill_check_service.go:86` 引用 bill_manager 的常量
- **现状**: PairBillCheckService 使用 `MaxRetryCount`（来自 bill_manager），CreditRetryService 使用 `CreditRetryConfig.MaxRetryCount`

#### P2-5: 硬编码的魔法值

| 位置 | 值 | 应该 |
|------|------|------|
| `settlement_service.go:321` | `RoundID: "0"` | 使用常量或从请求获取 |
| `settlement_service.go:266,285` | `5*time.Second` 重试延迟 | 使用 CreditRetryConfig |
| `refund_service.go:54,124` | Lock TTL `30` | 可配置化 |
| `game_settle_service.go:168` | `Multiplier: "1"` | 可配置化 |

#### P2-6: credit_retry_service 中 createException 和 CreateDebitFailedException 逻辑重复

- **文件**: `service/credit_retry_service.go:149-190`
- **现状**: 两个方法几乎相同，仅 ExceptionType 和 ExceptionDetail 不同
- **建议**: 合并为一个参数化方法

#### P2-7: pair_bill_check_service 创建缺失 credit bill 时未复制 BatchID

- **文件**: `service/pair_bill_check_service.go:94-116`
- **现状**: 新建的 credit bill 没有从 debit bill 复制 `BatchID` 字段
- **影响**: 成对账单之间缺少关联，对账和审计追踪断裂

#### P2-8: RefundService.GetRefundsByStatus 绕过 BillManager 直接访问 DB

- **文件**: `service/refund_service.go:186-195`
- **现状**: 用 `s.db` 而非 `billMgr` 查询，破坏了数据访问层封装

#### P2-9: executeRefund 传入空 platformTransID

- **文件**: `service/refund_service.go:160`
- **现状**: `UpdateRefundSuccessInTransaction(ctx, refund.ID, "", time.Now())`
- **影响**: 退款成功后未记录平台交易 ID，无法与平台侧对账

#### P2-10: game_settle_service 中 callMgr 的 CreateLog/UpdateLog 错误被忽略

- **文件**: `service/game_settle_service.go:175,184-188,199-203`
- **影响**: 平台调用审计日志可能静默丢失

---

### P3 - 轻微（代码规范 / 最佳实践）

#### P3-1: BillManager 内 AutoMigrate 不应在业务代码中

- **文件**: `service/bill_manager.go:179-186`
- **建议**: 使用迁移工具管理 schema 变更

#### P3-2: 中文字符串硬编码

- **文件**: `service/credit_retry_service.go:160,181`
- **现状**: `"入账重试超限"`、`"扣款失败"` 硬编码在代码中

#### P3-3: 惩罚分配错误只 log 不返回

- **文件**: `service/settlement_service.go:399-410`
- **现状**: `DistributePenaltyFromPlatform` 始终返回 `nil`，调用方无法知道部分分配失败

#### P3-4: checkAndSettleGame 返回 void，错误无法传播

- **文件**: `service/settlement_service.go:414-437`

#### P3-5: GameSettleRetryScheduler 和 GameSettleTimeoutScheduler 已定义但未在 Container 中注册

- **文件**: `scheduler/game_settle_retry_scheduler.go` 和 `scheduler/game_settle_timeout_scheduler.go`
- **现状**: 两个调度器存在但从未实例化启动

#### P3-6: Scheduler Manager 注册机制未使用

- **文件**: `scheduler/manager.go`
- **现状**: 定义了注册接口但无调用方

#### P3-7: game_settle_service 中 gameResult 只有 win/lose，无 draw

- **文件**: `service/game_settle_service.go:151-154`

---

## 三、架构层面问题总结

### 3.1 依赖注入混乱

```
Container
  ├── DeductService (独立构造, 含内部 CreditRetryService + ExceptionManager(nil))
  ├── RefundService (独立构造)
  ├── RewardSettler (独立构造)
  ├── SettlementService (内部又构造了一遍 DeductService/RefundService/RewardSettler/CreditRetryService)
  └── initSettlementSchedulers
        └── CreditRetryService (第三次构造)
```

**问题**: `CreditRetryService` 被构造了 3 次，`RefundService` 被构造了 2 次，`DeductService` 被构造了 2 次。每个实例内部状态独立，尤其是 `DeductService` 内部的 `ExceptionManager(nil)` 会导致 panic。

### 3.2 职责划分不清

- `BillManager` 既是 Repository 又包含业务逻辑（AutoMigrate、聚合计算），且管理三个不相关领域
- `SettlementService` 是 God Object，直接包含扣款、入账、惩罚、对账等所有逻辑
- `CreditRetryService` 既负责重试执行又负责异常创建，职责混合

### 3.3 事务保障缺失

- 跨表操作（CreateRefundAudit + UpdateBillRefundStatus）不在事务中
- 跨服务操作（平台 API 调用 + 本地 DB 更新）无补偿机制
- `ExistsBy*` + `Create` 之间无原子性保障

---

## 四、重构方案

### 4.1 重构目标

1. **消除 P0 问题**：确保资金安全和数据一致性
2. **引入依赖注入**：统一服务实例生命周期，消除重复构造
3. **拆分职责**：遵循 SRP，每个 struct/函数只做一件事
4. **增强事务保障**：关键操作使用 DB 事务 + 补偿机制
5. **统一错误处理**：不允许静默丢弃金融操作的 error

### 4.2 目录结构重构

```
settlement/
├── config/
│   └── config.go                    # 统一配置（含 retry、lock TTL 等）
├── dto/
│   ├── constants.go                 # 常量定义（合并去重 MaxRetryCount）
│   ├── request.go
│   └── response.go
├── model/
│   ├── bill.go
│   ├── round_settlement.go          # 从 bill.go 拆出
│   ├── refund.go
│   └── exception_record.go
├── repository/                      # 新增：纯数据访问层
│   ├── bill_repository.go           # BillRecord 的 CRUD
│   ├── settlement_repository.go     # RoundSettlement 的 CRUD
│   ├── refund_repository.go         # RefundAudit 的 CRUD
│   └── exception_repository.go      # ExceptionRecord 的 CRUD
├── infrastructure/
│   └── persistence/
│       └── redis/
│           └── keys.go
├── service/
│   ├── settlement_service.go        # 门面，只做编排
│   ├── deduct_service.go            # 扣款（接收注入的依赖）
│   ├── credit_service.go            # 入账（从 settlement_service 拆出）
│   ├── refund_service.go
│   ├── game_settle_service.go
│   ├── credit_retry_service.go      # 只负责重试，不创建异常
│   ├── exception_service.go         # 异常创建/处理（独立服务）
│   ├── pair_bill_check_service.go
│   ├── settlement_check_service.go
│   ├── reward_settler.go
│   ├── balance_service.go
│   ├── penalty_service.go           # 新增：惩罚相关逻辑从 settlement_service 拆出
│   ├── trace_id_generator.go
│   └── user_id_convert_service.go
└── scheduler/
    ├── base.go
    ├── manager.go                   # 统一管理所有调度器
    ├── credit_retry_scheduler.go
    ├── pair_bill_check_scheduler.go
    ├── settlement_check_scheduler.go
    ├── refund_process_scheduler.go
    ├── exception_handle_scheduler.go
    ├── game_settle_retry_scheduler.go
    └── game_settle_timeout_scheduler.go
```

### 4.3 核心重构项

#### 重构项 1: 统一依赖注入，消除重复构造

**原则**: 每个服务只构造一次，通过 Container 注入

```go
// container.go 重构后
type SettlementContainer struct {
    // 基础设施
    DB             *gorm.DB
    Redis          *redis.Client
    PlatformClient platform.Client
    
    // Repository 层
    BillRepo       *repository.BillRepository
    SettlementRepo *repository.SettlementRepository
    RefundRepo     *repository.RefundRepository
    ExceptionRepo  *repository.ExceptionRepository
    
    // 公共服务
    TraceIDGen     *service.TraceIDGenerator
    UserIDConvert  *service.UserIDConvertService
    PlatformCfg    *config.PlatformConfig
    
    // 业务服务（单例）
    ExceptionSvc   *service.ExceptionService
    CreditRetrySvc *service.CreditRetryService
    DeductSvc      *service.DeductService
    CreditSvc      *service.CreditService
    RefundSvc      *service.RefundService
    RewardSettler  *service.RewardSettler
    GameSettleSvc  *service.GameSettleService
    PenaltySvc     *service.PenaltyService
    BalanceSvc     *service.BalanceService
    
    // 门面
    SettlementSvc  *service.SettlementService
}
```

#### 重构项 2: 拆分 BillManager 为独立 Repository

```go
// repository/bill_repository.go
type BillRepository struct {
    db *gorm.DB
}

func (r *BillRepository) WithContext(ctx context.Context) *gorm.DB {
    return r.db.WithContext(ctx)
}

// 所有方法必须使用 WithContext(ctx)
func (r *BillRepository) Create(ctx context.Context, bill *model.BillRecord) error {
    return r.WithContext(ctx).Create(bill).Error
}

// ExistsBy* 返回 (bool, error)，不再吞掉 DB 错误
func (r *BillRepository) ExistsByBizOrderNo(ctx context.Context, bizOrderNo string) (bool, error) {
    var count int64
    err := r.WithContext(ctx).Model(&model.BillRecord{}).
        Where("biz_order_no = ?", bizOrderNo).Count(&count).Error
    return count > 0, err
}

// 批量创建使用事务
func (r *BillRepository) CreateBatch(ctx context.Context, bills []*model.BillRecord) error {
    return r.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        return tx.CreateInBatches(bills, 100).Error
    })
}
```

#### 重构项 3: 错误处理规范化

**规则**: 金融操作中不允许 `_ =` 忽略 error

```go
// Before (P0-2):
balanceAfter, _ := platform.ParseAmount(result.Data.Balance.Amount)

// After:
balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
if err != nil {
    logger.Error("parse balance amount failed", "raw", result.Data.Balance.Amount, "error", err)
    // 记录原始值用于后续人工审计
    _ = s.billMgr.UpdateBillFailed(ctx, bill.ID, "PARSE_AMOUNT_FAILED", 
        fmt.Sprintf("raw_amount=%s", result.Data.Balance.Amount))
    return fmt.Errorf("parse balance amount failed: %w", err)
}
```

```go
// Before (P0-3): 关键状态更新错误被忽略
s.billMgr.UpdateRoundSettlementSuccess(ctx, ...)
s.billMgr.UpdateRoundSettlementStatus(ctx, ...)

// After: 错误必须处理
if err := s.settlementRepo.UpdateSuccess(ctx, ...); err != nil {
    // 平台已入账但本地状态更新失败，这是严重异常
    // 记录到异常表用于人工介入
    s.exceptionSvc.CreateSystemException(ctx, &dto.CreateExceptionRequest{
        Type:    dto.ExceptionTypeStateSyncFailed,
        Detail:  fmt.Sprintf("round %s credited on platform but local status update failed: %v", roundTraceID, err),
    })
    return fmt.Errorf("update round settlement success failed after platform credit: %w", err)
}
```

#### 重构项 4: 惩罚分配金额精度修复

```go
// Before (P0-4):
shareAmount := req.Amount / int64(len(req.Recipients))

// After:
recipientCount := int64(len(req.Recipients))
shareAmount := req.Amount / recipientCount
remainder := req.Amount % recipientCount

for i, recipient := range req.Recipients {
    amount := shareAmount
    if int64(i) < remainder {
        amount++ // 前 remainder 个接收者多分 1 单位
    }
    // ... 创建入账账单
}
```

#### 重构项 5: 并发扣款限流

```go
// Before (P0-5): 无限制并发
for _, bill := range bills {
    wg.Add(1)
    go func(b *model.BillRecord) { ... }(bill)
}

// After: 使用 worker pool 限制并发度
const maxConcurrentDeduct = 20 // 可配置

sem := make(chan struct{}, maxConcurrentDeduct)
var wg sync.WaitGroup
var mu sync.Mutex
var successCount int
var failedBills []*model.BillRecord

for _, bill := range bills {
    wg.Add(1)
    sem <- struct{}{} // 获取信号量
    go func(b *model.BillRecord) {
        defer wg.Done()
        defer func() { <-sem }() // 释放信号量
        if err := s.executeSingleDeduct(ctx, b, amount); err != nil {
            mu.Lock()
            failedBills = append(failedBills, b)
            mu.Unlock()
        } else {
            mu.Lock()
            successCount++
            mu.Unlock()
        }
    }(bill)
}
wg.Wait()
```

#### 重构项 6: RefundService.ApplyForRefund 事务保障

```go
// Before (P0-6): 非原子操作
s.billMgr.CreateRefundAudit(ctx, refundAudit)
s.billMgr.UpdateBillRefundStatus(ctx, bill.ID, ...)

// After: 在事务中执行
func (s *RefundService) ApplyForRefund(ctx context.Context, req *dto.ApplyRefundRequest) (string, error) {
    var refundOrderNo string
    err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        // 在事务内创建退款记录
        if err := tx.Create(refundAudit).Error; err != nil {
            return err
        }
        // 在事务内更新账单退款状态
        if err := tx.Model(&model.BillRecord{}).
            Where("id = ?", bill.ID).
            Update("refund_status", dto.RefundStatusPending).Error; err != nil {
            return err
        }
        refundOrderNo = refundAudit.RefundOrderNo
        return nil
    })
    return refundOrderNo, err
}
```

#### 重构项 7: SettlementService 从 God Object 拆为编排门面

```go
// 重构后的 SettlementService 只做编排，不包含具体逻辑
type SettlementService struct {
    deductSvc   *DeductService    // 注入，非内部创建
    creditSvc   *CreditService    // 新拆出
    refundSvc   *RefundService    // 注入
    rewardSettler *RewardSettler  // 注入
    penaltySvc  *PenaltyService   // 新拆出
    gameSettleSvc *GameSettleService // 注入
    billRepo    *repository.BillRepository
}

func (s *SettlementService) SettleRound(ctx context.Context, req *dto.SettleRoundRequest) error {
    // 1. 幂等检查（增强：覆盖所有终态）
    // 2. 加锁
    // 3. 委托 creditSvc.creditRound() 处理入账
    // 4. 委托 rewardSettler 处理奖励
    // 5. 更新状态（错误必须处理）
    // 6. 委托 gameSettleSvc 检查游戏结算
}
```

#### 重构项 8: CreditRetryService 职责拆分

```go
// 重构前：重试执行 + 异常创建混合
type CreditRetryService struct { ... }
func (s *CreditRetryService) RetryCredit(...) { ... }  // 重试
func (s *CreditRetryService) createException(...) { ... }  // 创建异常
func (s *CreditRetryService) CreateDebitFailedException(...) { ... }  // 创建异常

// 重构后：只负责重试执行
type CreditRetryService struct {
    billRepo       *repository.BillRepository
    platformClient platform.Client
    retryCfg       config.CreditRetryConfig  // 外部可配置
}

func (s *CreditRetryService) RetryCredit(ctx context.Context, bill *model.BillRecord) error {
    // 只负责：递增重试计数 → 执行平台入账 → 更新状态
    // 超限判断由调用方（scheduler）负责
}
```

### 4.4 重构执行顺序（依赖关系）

```
阶段 1: 紧急修复（P0 问题，不改结构）
  ├─ 1.1 修复 DeductService nil DB panic → 改为接收注入的 ExceptionManager
  ├─ 1.2 处理 ParseAmount error → 不再忽略
  ├─ 1.3 处理关键 DB 更新 error → 不再忽略
  ├─ 1.4 修复惩罚分配整数截断
  ├─ 1.5 批量扣款添加并发限制
  └─ 1.6 Refund ApplyForRefund 添加事务

阶段 2: 统一依赖注入
  ├─ 2.1 创建 SettlementContainer
  ├─ 2.2 所有服务改为接收注入的依赖
  ├─ 2.3 消除 SettlementService 内部的子服务构造
  └─ 2.4 消除 Container 中的重复构造

阶段 3: 拆分 Repository
  ├─ 3.1 拆分 BillManager → BillRepo + SettlementRepo + RefundRepo + ExceptionRepo
  ├─ 3.2 所有 Repository 方法添加 WithContext(ctx)
  ├─ 3.3 ExistsBy* 方法返回 (bool, error)
  └─ 3.4 删除 BillManager 中的 AutoMigrate

阶段 4: 服务职责拆分
  ├─ 4.1 从 SettlementService 拆出 CreditService
  ├─ 4.2 从 SettlementService 拆出 PenaltyService
  ├─ 4.3 CreditRetryService 移除异常创建职责
  ├─ 4.4 合并 createException + CreateDebitFailedException
  └─ 4.5 GameSettleService 修复幂等检查 + 添加锁

阶段 5: 代码质量清理
  ├─ 5.1 统一 MaxRetryCount 常量
  ├─ 5.2 替换硬编码魔法值为配置
  ├─ 5.3 修复 UpdateRoundSettlementStatus 忽略 errMsg
  ├─ 5.4 修复 pair_bill_check 缺失 BatchID
  ├─ 5.5 修复 executeRefund 空 platformTransID
  ├─ 5.6 修复 RefundService.RejectRefund 添加锁
  └─ 5.7 注册缺失的 scheduler + 启用 manager
```

---

## 五、预期成果

| 维度 | 重构前 | 重构后 |
|------|--------|--------|
| P0 缺陷数 | 6 | 0 |
| P1 缺陷数 | 8 | ≤1 |
| 服务重复构造 | 3 个 CreditRetryService、2 个 RefundService | 每个服务仅 1 个实例 |
| BillManager 行数 | 601 | 拆分后每个 Repo ≤150 行 |
| ctx 传播率 | ~5% | 100% |
| 金融操作 error 忽略率 | ~80% | 0% |
| 批量操作并发控制 | 无 | 可配置 worker pool |
| 事务保障 | 部分 | 所有多表操作在事务中 |
