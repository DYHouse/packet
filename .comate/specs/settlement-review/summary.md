# Settlement Code Review - Summary

## 修改文件列表

| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `settlement/service/deduct_service.go` | 修复+清理 | parseAmount 失败处理、幂等性修复、事务合并、删除 executeSingleCredit |
| `settlement/service/settlement_service.go` | 修复+清理 | parseAmount 失败处理、creditRound 原子更新、惩罚扣款事务、删除 executeDeduct/checkAndSettleGame |
| `settlement/service/credit_retry_service.go` | 修复 | parseAmount 失败时标记 Success 而非触发重试 |
| `settlement/service/refund_service.go` | 修复 | ApproveRefund 锁内二次状态检查、GetRefundsByStatus 传递 offset |
| `settlement/service/bill_manager.go` | 修复+清理 | 新增 CreateRoundSettlementAndBills/CreateBillsPairInTransaction/UpdateRoundSettlementCredited，GetRefundsByStatus 增加 offset，删除 GetProcessingBills |
| `settlement/service/reward_settler.go` | 修复 | BizOrderNo 使用 traceIDGen 生成，注入 traceIDGen 依赖 |
| `settlement/scheduler/pair_bill_check_scheduler.go` | 修复 | 时间间隔改用 time.Minute/time.Second |
| `settlement/scheduler/refund_process_scheduler.go` | 适配 | GetRefundsByStatus 调用增加 offset 参数 |
| `game/bootstrap/container.go` | 适配 | NewRewardSettler 调用增加 traceIDGen 参数 |
| `game/bootstrap/app.go` | 适配 | NewRewardSettler 调用增加 traceIDGen 参数 |

## 修复的关键数据一致性问题

### 1. parseAmount 失败导致资金风险（最高优先级）

**问题**: 平台 Debit/Credit 成功但 parseAmount 失败时，原代码返回 error，导致：
- 扣款场景：Bill 被标记 Success 但流程视为失败 → 触发退款 → 玩家被扣款又退款（资金漏洞）
- 入账场景：Bill 未标记 Success → 重试时再次 Credit → 重复入账（资金漏洞）

**修复**: parseAmount 失败时标记 Bill 为 Success（balanceAfter=0），不返回 error，仅记录 warning。涉及 4 个文件的 5 处修改。

### 2. DeductForFirstRound 幂等性检查无效（高优先级）

**问题**: batchID 每次随机生成，ExistsBatchDeduct 永远返回 false，Redis 锁过期后可重复扣款。

**修复**: 改用 `ExistsRoundSettlement(roundID)` 做幂等检查，锁内二次检查，并新增 `getExistingFirstRoundResult` 方法查询已有结果。

### 3. ApproveRefund 并发重复退款（高优先级）

**问题**: 锁外检查状态后获取锁，锁内未二次检查，并发可能导致同一笔退款执行两次。

**修复**: 锁内重新查询 RefundAudit 并验证状态仍为 Pending，若已变更则直接返回。

### 4. 关键操作事务原子性（中高优先级）

**问题**: 
- DeductForFirstRound/deductSingleUser/DeductForSystemPacket：RoundSettlement 和 Bills 不在同一事务，中间失败产生孤儿记录
- creditRound：两次独立 DB 更新，第二次失败导致回合卡住
- DeductPenaltyToPlatform：玩家扣款和平台收入不在同一事务

**修复**: 
- 新增 `CreateRoundSettlementAndBills` 和 `CreateBillsPairInTransaction` 方法
- 新增 `UpdateRoundSettlementCredited` 合并 Success+Credited 为一次原子更新
- 惩罚扣款场景先在事务中创建两个 Bill，再调用平台扣款

## 清理的无用代码

| 方法 | 文件 | 原因 |
|------|------|------|
| `SettlementService.executeDeduct` | settlement_service.go | 从未被调用，与 DeductService.executeSingleDeduct 重复 |
| `DeductService.executeSingleCredit` | deduct_service.go | 从未被调用，与 SettlementService.executeCreditOnly 重复 |
| `SettlementService.checkAndSettleGame` | settlement_service.go | 从未被调用，游戏级结算由 scheduler 触发 |
| `BillManager.GetProcessingBills` | bill_manager.go | 从未被调用，已被 GetRetryableCredits 替代 |

## 其他修复

- **RewardSettler BizOrderNo**: 从固定格式 `REWARD_OUT_{roundID}` 改为 `traceIDGen.GenerateBizOrderNo()` 生成，避免唯一索引冲突阻断重试
- **GetRefundsByStatus offset**: 修复 offset 参数被忽略的问题
- **PairBillCheckScheduler 时间写法**: `60 * 1000 * 1000000` 改为 `time.Minute`

## 编译验证

- `go build ./settlement/...` — 通过
- `go build ./game/...` — 通过
- `go build ./...` — 唯一错误在 scripts/ 目录（已有的多个 main 冲突），与本次修改无关
