# Settlement Code Review - Data Consistency Fix & Cleanup Tasks

- [x] Task 1: 修复 parseAmount 失败导致的状态不一致（资金风险）
    - 1.1: 修复 `deduct_service.go` executeSingleDeduct 中 Debit 成功但 parseAmount 失败的处理：不返回 error，仅 log warning，让流程正常继续
    - 1.2: 修复 `settlement_service.go` executeCreditOnly 中 Credit 成功但 parseAmount 失败的处理：标记 Bill 为 Success（balanceAfter=0），不触发重试
    - 1.3: 修复 `settlement_service.go` executeDeduct 中 Debit 成功但 parseAmount 失败的处理：同 1.1 逻辑（此方法为死代码，将在 Task 5 删除，此处暂不修复）
    - 1.4: 修复 `credit_retry_service.go` executeCredit 中 Credit 成功但 parseAmount 失败的处理：同 1.2 逻辑，标记 Bill 为 Success 而非返回 error
    - 1.5: 修复 `settlement_service.go` DeductPenaltyToPlatform 中 Debit 成功但 parseAmount 失败的处理：标记 Bill 为 Success 并继续创建平台收入 Bill

- [x] Task 2: 修复 DeductForFirstRound 幂等性检查
    - 2.1: 在 BillManager 中添加 ExistsRoundSettlement 方法（按 sessionID + roundNo 或 roundID 检查），替换基于随机 batchID 的幂等检查
    - 2.2: 修改 DeductService.DeductForFirstRound，使用 roundID 做幂等性判断而非 batchID
    - 2.3: 若已有 RoundSettlement 存在，查询已有 Bills 并返回结果

- [x] Task 3: 修复 ApproveRefund 缺少锁内二次状态检查
    - 3.1: 在 `refund_service.go` ApproveRefund 的锁内重新查询 RefundAudit 并验证状态仍为 Pending

- [x] Task 4: 修复关键操作的事务原子性
    - 4.1: 修复 `deduct_service.go` DeductForFirstRound：将 RoundSettlement 和 Bills 的创建放入同一个数据库事务
    - 4.2: 修复 `deduct_service.go` deductSingleUser：将 RoundSettlement 和 Bill 的创建放入同一个数据库事务
    - 4.3: 修复 `settlement_service.go` creditRound：合并 UpdateRoundSettlementSuccess 和 UpdateRoundSettlementStatus 为一次原子更新
    - 4.4: 修复 `settlement_service.go` DeductPenaltyToPlatform：将玩家扣款 Bill 和平台收入 Bill 放入同一事务

- [x] Task 5: 清理无用代码
    - 5.1: 删除 `settlement_service.go` 中的 executeDeduct 方法（line 64-100，从未被调用）
    - 5.2: 删除 `deduct_service.go` 中的 executeSingleCredit 方法（line 307-341，从未被调用）
    - 5.3: 删除 `settlement_service.go` 中的 checkAndSettleGame 方法（line 436-459，从未被调用）
    - 5.4: 删除 `bill_manager.go` 中的 GetProcessingBills 方法（line 131-138，从未被调用）

- [x] Task 6: 修复其他中低优先级问题
    - 6.1: 修复 `reward_settler.go` 中 BizOrderNo 使用 traceIDGen.GenerateBizOrderNo() 生成，而非固定格式
    - 6.2: 修复 `refund_service.go` GetRefundsByStatus 传递 offset 参数给 billMgr
    - 6.3: 修复 `pair_bill_check_scheduler.go` 时间间隔写法，使用 time.Minute 和 time.Second 替代纳秒硬编码
