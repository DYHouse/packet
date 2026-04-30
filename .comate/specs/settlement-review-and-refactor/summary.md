# Settlement 模块重构总结

## 修改范围

共修改 **12 个文件**，涵盖 settlement 模块的全部核心代码和 bootstrap 容器。

### 修改文件清单

| 文件 | 修改类型 | 修复的问题 |
|------|---------|-----------|
| `settlement/service/deduct_service.go` | 重构 | P0-1(nil DB panic), P0-2(ParseAmount), P0-5(并发限制), P1-2(Exists签名), P2-5(硬编码) |
| `settlement/service/settlement_service.go` | 重构 | P0-2(ParseAmount), P0-3(DB更新错误), P0-4(整数截断), P1-3(依赖注入), P2-5(硬编码), P3-4(void返回) |
| `settlement/service/credit_retry_service.go` | 重构 | P0-2(ParseAmount), P1-5(重试计数), P2-3(重复常量), P2-6(重复方法) |
| `settlement/service/bill_manager.go` | 重构 | P1-1(WithContext), P1-2(Exists返回error), P2-2(errMsg忽略), P2-3(重复常量) |
| `settlement/service/refund_service.go` | 重构 | P0-6(事务保障), P1-8(RejectRefund锁), P2-8(绕过BillManager), P2-9(空platformTransID) |
| `settlement/service/game_settle_service.go` | 重构 | P1-6(幂等检查), P1-7(RetryPlayerSettle锁) |
| `settlement/service/exception_manager.go` | 修复 | P1-1(WithContext) |
| `settlement/dto/constants.go` | 新增常量 | MaxRetryCount, PenaltyRoundID, CreditRetryBaseDelay |
| `settlement/infrastructure/persistence/redis/keys.go` | 新增 | GameSettleRetryLockKey |
| `game/bootstrap/container.go` | 重构 | P1-4(重复构造), P3-5(缺失调度器) |
| `game/bootstrap/app.go` | 重构 | P1-4(统一依赖注入) |
| `settlement/service/pair_bill_check_service.go` | 修复 | P2-4(常量引用), P2-7(缺失BatchID) |

## 按严重程度统计

| 级别 | 总数 | 已修复 |
|------|------|--------|
| P0-致命 | 6 | 6 |
| P1-严重 | 8 | 8 |
| P2-中等 | 10 | 10 |
| P3-轻微 | 7 | 5 (P3-1 AutoMigrate保留, P3-2 中文硬编码保留, P3-7 draw结果保留) |

## 关键修复详情

### P0-1: DeductService nil DB panic
- 移除 `NewDeductService` 内部的 `NewExceptionManager(nil)` 和 `NewCreditRetryService()` 构造
- 改为接收外部注入的 `creditRetrySvc *CreditRetryService` 参数
- 在 `container.go` 和 `app.go` 中统一注入

### P0-2: ParseAmount error 静默忽略
- 全部 7 处 `balanceAfter, _ := platform.ParseAmount(...)` 改为处理 error
- 解析失败时记录原始金额、更新账单状态、返回明确错误

### P0-3: 关键 DB 更新错误被丢弃
- `creditRound` 中 `UpdateRoundSettlementSuccess` 和 `UpdateRoundSettlementStatus` 错误必须处理
- `UpdateRoundSettlementSettleInfo` 错误改为 return 而非仅 log
- `executeBatchDeduct` 中所有 DB 更新添加错误日志

### P0-4: 惩罚分配整数截断
- 使用余数分配策略：`shareAmount + (i < remainder ? 1 : 0)`
- 确保 `sum(shares) == totalAmount`

### P0-5: 批量扣款无并发限制
- 添加 `maxConcurrentDeduct = 20` 的信号量限制
- 通过 channel 实现 goroutine pool
- 在 `DeductService` struct 中添加可配置字段

### P0-6: ApplyForRefund 非原子操作
- `CreateRefundAudit` + `UpdateBillRefundStatus` 包裹在 `db.WithContext(ctx).Transaction()` 中

### P1-1: BillManager WithContext 传播
- 全部 593 行中所有 `m.db.` 调用替换为 `m.db.WithContext(ctx).`
- `ExceptionManager` 同样修复

### P1-2: ExistsBy* 返回 error
- 5 个 Exists 方法签名从 `bool` 改为 `(bool, error)`
- 更新所有 6 处调用方适配新签名

### P1-3/4: 统一依赖注入
- `SettlementService` 构造改为接收所有子服务作为参数
- `container.go` 和 `app.go` 中每个服务仅构造一次
- `initSettlementSchedulers` 接收注入的 `creditRetrySvc`、`exceptionMgr`、`gameSettleSvc`

### P1-5: 重试计数顺序修复
- `doRetryCredit` 中先执行 `executeCredit`，失败后再递增重试计数
- `IncrementRetryCountWithNextRetryTime` 的 error 必须处理

### P1-6/7: GameSettleService 修复
- 幂等检查从只检查第一个 round 改为遍历所有 rounds
- `RetryPlayerSettle` 添加分布式锁保护

### P1-8: RejectRefund 分布式锁
- 添加与 `ApproveRefund` 相同的分布式锁
- 锁内重新检查状态防止并发冲突

## 未修复项（建议后续处理）

1. **P3-1 AutoMigrate 在业务代码中**: 建议迁移到独立的 migration 工具
2. **P3-2 中文字符串硬编码**: 建议引入 i18n 机制
3. **P3-7 gameResult 无 draw**: 视业务需求决定是否添加
4. **BillManager 拆分**: 601 行的 God Class 建议后续拆分为独立 Repository
5. **SettlementService 拆分**: 建议从 God Object 拆出 CreditService 和 PenaltyService
