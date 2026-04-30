# Settlement Code Review - Data Consistency & Cleanup

## Overview

对结算模块进行全面代码审查，重点关注数据一致性问题和无用代码清理。

---

## Critical Data Consistency Issues

### 1. 平台扣款/入账成功但 parseAmount 失败导致状态不一致（资金风险）

**文件**: `service/deduct_service.go:220-231`

`executeSingleDeduct` 中，若 `platform.Debit` 成功但 `platform.ParseAmount` 失败：
- Bill 被标记为 `Success`（balanceBefore=0, balanceAfter=0）
- 但函数返回 error
- 调用方 `executeBatchDeduct` 将此视为**扣款失败**
- RoundSettlement 被标记为 `Failed`
- 后续触发退款流程 → **玩家被扣款 + 又被退款 = 资金漏洞**

**修复方案**: parseAmount 失败时不应返回 error（资金已动），应只记录 warning，让流程继续。

**文件**: `service/settlement_service.go:295-299`

`executeCreditOnly` 中，若 `platform.Credit` 成功但 `parseAmount` 失败：
- Bill **未被**标记为 Success
- 只设置了 `nextRetryTime`
- CreditRetryScheduler 重试时会再次调用 Credit → **重复入账 = 资金漏洞**

**修复方案**: 平台 Credit 已成功时，应将 Bill 标记为 Success（balanceAfter=0），不触发重试。

**文件**: `service/settlement_service.go:348-351`

`DeductPenaltyToPlatform` 中同样的问题：Debit 成功但 parseAmount 失败，Bill 仍为 Processing，平台收入 Bill 未创建 → 玩家被扣款但系统无记录。

### 2. DeductForFirstRound 幂等性检查无效（可导致重复扣款）

**文件**: `service/deduct_service.go:57-61`

```go
batchID := s.traceIDGen.GenerateBatchID() // 每次生成新的随机 ID
if exists, _ := s.billMgr.ExistsBatchDeduct(ctx, req.SessionID, batchID); exists {
    // 永远不会进入此分支，因为 batchID 是新生成的
```

`batchID` 是每次调用新产生的雪花 ID，`ExistsBatchDeduct` 永远返回 false。Redis 锁过期后再次调用会创建重复账单和扣款。

**修复方案**: 幂等性检查应基于 `sessionID + roundNo`（或 `roundID`），而非随机生成的 batchID。

### 3. ApproveRefund 缺少锁内二次状态检查（可导致重复退款）

**文件**: `service/refund_service.go:122-142`

`ApproveRefund` 在获取锁**之前**检查状态为 Pending，获取锁后**没有**重新检查状态。对比 `RejectRefund`（line 189-196）在锁内做了二次检查。

并发调用 `ApproveRefund` 可能导致同一笔退款被执行两次 → **重复退款 = 资金漏洞**。

**修复方案**: 在锁内重新查询 RefundAudit 并验证状态仍为 Pending。

### 4. RoundSettlement 和 BillRecord 创建不在同一事务

**文件**: `service/deduct_service.go:88-115`

`DeductForFirstRound` 中：
1. 创建 RoundSettlement（line 88）
2. 创建 Bills（line 113，在事务中）

若 Bills 创建失败，RoundSettlement 成为孤儿记录（无对应 Bill，状态永远为 Deducting）。

**文件**: `service/deduct_service.go:448-470`

`deductSingleUser` 同样的问题：RoundSettlement 和 Bill 不在同一事务中。

**修复方案**: 将 RoundSettlement 和 Bill 的创建放入同一个数据库事务。

### 5. creditRound 中两个状态更新不原子（可导致回合卡住）

**文件**: `service/settlement_service.go:215-220`

```go
s.billMgr.UpdateRoundSettlementSuccess(ctx, ...)   // 设为 Success
s.billMgr.UpdateRoundSettlementStatus(ctx, ..., dto.RoundStatusCredited, "") // 设为 Credited
```

两次独立 DB 更新。若第二次失败，Round 停留在 Success 状态（而非 Credited），游戏级结算永远不会被触发。

**修复方案**: 将 Success 状态信息合并到 Credited 状态更新中，只做一次 DB 操作。

### 6. DeductPenaltyToPlatform 玩家扣款和平台收入不在同一事务

**文件**: `service/settlement_service.go:304-375`

玩家扣款 Bill 成功后，才创建平台收入 Bill（line 357-372）。若中间崩溃，玩家已被扣款但平台收入未记录 → 账目不平。

**修复方案**: 将两个 Bill 的创建放在同一个事务中（平台收入 Bill 可以先创建为 Processing，扣款成功后一起更新）。

---

## Important Data Consistency Issues

### 7. RewardSettler 的 BizOrderNo 不含时间戳/随机因子

**文件**: `service/reward_settler.go:78, 97`

```go
BizOrderNo: fmt.Sprintf("REWARD_OUT_%d", settlement.RoundID)
BizOrderNo: fmt.Sprintf("REWARD_IN_%d_%d", settlement.RoundID, player.UserID)
```

BizOrderNo 是唯一索引。如果 SettleReward 部分失败后重试，会因唯一索引冲突而无法重新创建。这虽然防了重复，但也阻断了正常重试。

**修复方案**: 使用 `traceIDGen.GenerateBizOrderNo()` 生成带时间戳和随机因子的 BizOrderNo。

### 8. RefundService.GetRefundsByStatus 忽略 offset 参数

**文件**: `service/refund_service.go:213-215`

```go
func (s *RefundService) GetRefundsByStatus(ctx context.Context, status int, limit, offset int) ([]*model.RefundAudit, error) {
    return s.billMgr.GetRefundsByStatus(ctx, status, limit) // offset 被忽略
}
```

**修复方案**: 传递 offset 给 billMgr 或移除参数。

### 9. PairBillCheckScheduler 时间间隔写法不规范

**文件**: `scheduler/pair_bill_check_scheduler.go:18-19`

```go
Interval:     60 * 1000 * 1000000,  // = 60秒，但写法晦涩
InitialDelay: 30 * 1000 * 1000000,  // = 30秒
```

应使用 `time.Minute` 和 `30 * time.Second`。

---

## Unused Code to Delete

### 1. SettlementService.executeDeduct（死代码）

**文件**: `service/settlement_service.go:64-100`

从未被调用。与 `DeductService.executeSingleDeduct` 功能重复。

### 2. DeductService.executeSingleCredit（死代码）

**文件**: `service/deduct_service.go:307-341`

从未被调用。与 `SettlementService.executeCreditOnly` 和 `CreditRetryService.executeCredit` 功能重复。

### 3. SettlementService.checkAndSettleGame（死代码）

**文件**: `service/settlement_service.go:436-459`

从未被调用。游戏级结算由 scheduler 触发 `GameSettleService.SettleGame`。

### 4. BillManager.GetProcessingBills（死代码）

**文件**: `service/bill_manager.go:131-138`

从未被调用。查询逻辑已被 `GetRetryableCredits` 替代。

### 5. BillManager.GetBillByTraceID（设计缺陷，建议保留但修改语义）

**文件**: `service/bill_manager.go:52-59`

`GetBillByTraceID` 通过 `round_trace_id` 查询单条 Bill，但一个 round_trace_id 对应多条 Bill。此方法名具有误导性。目前仅 `SettlementService.GetBillByTraceID` 调用，但同样存在语义问题。

---

## Data Flow Summary

```
发红包扣款 → DeductService.DeductForFirstRound (幂等性缺陷)
                              ↓
                   RoundSettlement + Bill (非事务)
                              ↓
                   executeBatchDeduct (parseAmount 失败风险)
                              ↓
回合结算 → SettlementService.SettleRound
                              ↓
           creditRound (两个状态更新不原子)
                              ↓
           executeCreditOnly (parseAmount 失败导致重试重复入账)
                              ↓
游戏级结算 → GameSettleService.SettleGame
                              ↓
惩罚扣款 → SettlementService.DeductPenaltyToPlatform (非事务)
                              ↓
退款 → RefundService (ApproveRefund 缺二次检查)
```

---

## Expected Outcomes

1. 修复 parseAmount 失败时的状态处理，防止资金漏洞
2. 修复幂等性检查逻辑，防止重复扣款
3. 修复 ApproveRefund 锁内二次检查，防止重复退款
4. 关键操作使用事务保证原子性
5. 清理所有无用代码
6. 统一时间间隔写法
