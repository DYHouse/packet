# Settlement 模块代码审查问题修复与重构任务计划

- [x] Task 1: 修复 DeductService nil DB 导致的 panic（P0-1）
    - 1.1: 修改 `NewDeductService` 签名，增加 `exceptionMgr *ExceptionManager` 参数
    - 1.2: 移除 `deduct_service.go` 内部的 `NewExceptionManager(nil)` 和 `NewCreditRetryService(...)` 构造
    - 1.3: 修改 `NewDeductService` 签名，增加 `creditRetrySvc *CreditRetryService` 参数
    - 1.4: 更新 `container.go` 中 `NewDeductService` 调用处，注入正确的依赖实例

- [x] Task 2: 修复 ParseAmount error 被静默忽略问题（P0-2）
    - 2.1: 修复 `settlement_service.go` 中 4 处 `balanceAfter, _ := platform.ParseAmount(...)` 错误处理
    - 2.2: 修复 `deduct_service.go` 中 2 处 `balanceAfter, _ := platform.ParseAmount(...)` 错误处理
    - 2.3: 修复 `credit_retry_service.go` 中 1 处 `balanceAfter, _ := platform.ParseAmount(...)` 错误处理
    - 2.4: 统一错误处理模式：解析失败时更新账单为 Failed 状态并记录原始金额

- [x] Task 3: 修复关键 DB 更新错误被丢弃问题（P0-3）
    - 3.1: 修复 `settlement_service.go:213-219` 中 `UpdateRoundSettlementSuccess` 和 `UpdateRoundSettlementStatus` 错误未处理
    - 3.2: 修复 `settlement_service.go:123-124` 中 `UpdateRoundSettlementSettleInfo` 错误仅 log 不 return
    - 3.3: 修复 `settlement_service.go:219` 中 `UpdateRoundSettlementStatus` 错误被丢弃
    - 3.4: 对平台已成功但本地状态更新失败的情况，创建异常记录用于人工介入

- [x] Task 4: 修复惩罚分配整数除法截断导致资金丢失（P0-4）
    - 4.1: 修改 `DistributePenaltyFromPlatform` 中 `shareAmount` 计算逻辑，处理余数分配
    - 4.2: 确保前 N 个接收者分配余数，保证 `sum(shares) == totalAmount`

- [x] Task 5: 批量扣款添加并发限制（P0-5）
    - 5.1: 在 `deduct_service.go` 的 `executeBatchDeduct` 中添加可配置的信号量限制（默认 20）
    - 5.2: 在 `config.go` 中添加 `MaxConcurrentDeduct` 配置项
    - 5.3: 确保信号量在 goroutine 正确释放（含 panic 场景）

- [x] Task 6: RefundService.ApplyForRefund 添加事务保障（P0-6）
    - 6.1: 将 `CreateRefundAudit` + `UpdateBillRefundStatus` 包裹在 DB 事务中
    - 6.2: 确保 `RefundService` 持有 `*gorm.DB` 引用用于创建事务

- [x] Task 7: BillManager 所有方法添加 WithContext(ctx) 传播（P1-1）
    - 7.1: 逐方法将 `m.db.Model(...)` 替换为 `m.db.WithContext(ctx).Model(...)`
    - 7.2: 逐方法将 `m.db.Where(...)` 替换为 `m.db.WithContext(ctx).Where(...)`
    - 7.3: 逐方法将 `m.db.Create(...)` 替换为 `m.db.WithContext(ctx).Create(...)`
    - 7.4: 对所有 593 行中的 DB 调用统一添加 WithContext

- [x] Task 8: ExistsBy* 方法返回 (bool, error)（P1-2）
    - 8.1: 修改 `ExistsByRoundAndType` 返回 `(bool, error)`
    - 8.2: 修改 `ExistsByBizOrderNo` 返回 `(bool, error)`
    - 8.3: 修改 `ExistsByRoundTraceID` 返回 `(bool, error)`
    - 8.4: 修改 `ExistsBatchDeduct` 返回 `(bool, error)`
    - 8.5: 修改 `ExistsByBillIDAndType` 返回 `(bool, error)`
    - 8.6: 更新所有调用方适配新签名

- [x] Task 9: 统一依赖注入，消除 Container 中重复构造（P1-3, P1-4）
    - 9.1: 在 `container.go` 中统一构造所有 settlement 子服务实例（单例）
    - 9.2: `SettlementService` 构造改为接收注入的子服务，不再内部创建
    - 9.3: 确保 `DeductService`、`RefundService`、`RewardSettler`、`CreditRetryService`、`GameSettleService` 各仅构造一次
    - 9.4: 删除 `SettlementService` 内部的子服务构造代码

- [x] Task 10: 修复 CreditRetryService 重试计数问题（P1-5）
    - 10.1: 将 `IncrementRetryCountWithNextRetryTime` 移到 `executeCredit` 之后
    - 10.2: 处理 `IncrementRetryCountWithNextRetryTime` 的 error 返回值
    - 10.3: 确保重试计数与实际重试执行一致

- [x] Task 11: 修复 GameSettleService 幂等检查和锁问题（P1-6, P1-7）
    - 11.1: 修改 `SettleGame` 幂等检查遍历所有 rounds，而非只检查 `settlements[0]`
    - 11.2: 为 `RetryPlayerSettle` 添加分布式锁保护
    - 11.3: 在 `redis/keys.go` 中添加 game settle retry 的锁 key 定义

- [x] Task 12: 修复 RefundService.RejectRefund 缺少分布式锁（P1-8）
    - 12.1: 为 `RejectRefund` 添加与 `ApproveRefund` 相同的分布式锁保护
    - 12.2: 使用 `RefundLockKey(refundOrderNo)` 作为锁 key

- [x] Task 13: 统一 MaxRetryCount 常量（P2-3, P2-4）
    - 13.1: 删除 `bill_manager.go:12` 的 `MaxRetryCount` 常量
    - 13.2: 统一使用 `dto/constants.go` 中定义的常量或 `CreditRetryConfig.MaxRetryCount`
    - 13.3: 更新 `pair_bill_check_service.go` 中的引用

- [x] Task 14: 修复 BillManager.UpdateRoundSettlementStatus 忽略 errMsg 参数（P2-2）

- [x] Task 15: 修复 pair_bill_check_service 缺失 BatchID（P2-7）

- [x] Task 16: 修复 RefundService 中 executeRefund 空 platformTransID 和绕过 BillManager（P2-8, P2-9）

- [x] Task 17: 替换硬编码魔法值为配置（P2-5）

- [x] Task 18: 合并 CreditRetryService 中重复的异常创建方法（P2-6）

- [x] Task 19: 修复惩罚分配错误只 log 不返回（P3-3, P3-4）

- [x] Task 20: 注册缺失的调度器（P3-5, P3-6）
