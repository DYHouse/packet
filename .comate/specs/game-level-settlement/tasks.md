# 重构任务计划：回合级扣款 + 回合级入账 + 游戏级结算

- [x] Task 1: 修改 platform 层 — Settle 端点从 /credit_n_settle 改为 /settle
    - 1.1: gamingpanda_client.go 中 Settle 端点从 `/credit_n_settle` 改为 `/settle`
    - 1.2: mock_client.go 中 Settle 实现改为仅记录游戏结果，不修改余额

- [x] Task 2: 新增枚举常量 — dto/constants.go
    - 2.1: 新增 RoundStatusCredited = 6
    - 2.2: 新增 GameSettleStatus 枚举（0=None, 1=Settling, 2=Success, 3=Failed）
    - 2.3: 新增 BillGameSettleStatus 枚举（0=None, 1=Settled）

- [x] Task 3: 模型变更 — RoundSettlement / BillRecord 增加字段
    - 3.1: RoundSettlement 增加 GameSettleStatus int 和 GameSettledAt *time.Time 字段
    - 3.2: BillRecord 增加 GameSettleStatus int 字段

- [x] Task 4: 新增 Redis Key — infrastructure/persistence/redis/keys.go
    - 4.1: 新增 GameSettleLockKey(sessionID) 函数，返回 `cashparty:settle:lock:game:{sessionID}`

- [x] Task 5: 新增 DTO — dto/request.go / dto/response.go
    - 5.1: request.go 新增 GameSettleRequest 结构
    - 5.2: response.go 新增 GameSettleInfo 响应结构

- [x] Task 6: BillManager 增加聚合查询方法 — service/bill_manager.go
    - 6.1: 新增 AggregateBetBySession(sessionID) — 按 SessionID 聚合扣款金额，返回 map[userID]int64
    - 6.2: 新增 AggregatePayOutBySession(sessionID) — 按 SessionID 聚合入账金额，返回 map[userID]int64
    - 6.3: 新增 UpdateGameSettleStatusByUser(sessionID, userID, status) — 按玩家标记结算状态
    - 6.4: 新增 GetUnsettledUsersBySession(sessionID) — 查询游戏中未结算的玩家列表

- [x] Task 7: 重构 SettlementService — 回合级入账拆分 + 游戏级结算
    - 7.1: 新增 CreditRound 方法 — 替代原 executeRoundSettle，对每个玩家调用 platform.Credit() 而非 platform.Settle()
    - 7.2: 新增 executeCreditOnly 方法 — 构造 CreditRequest 调用 platform.Credit()，只入账不含游戏元数据
    - 7.3: 修改 SettleRound 方法 — 调用 CreditRound 替代原 executeRoundSettle，回合完成后将 RoundSettlement 状态设为 Credited(6)，检查是否触发游戏结算
    - 7.4: 新增 SettleGame 方法 — 游戏级结算：聚合 BillRecord → 逐玩家调用 platform.Settle(/settle) → 标记 GameSettleStatus
    - 7.5: 删除原 executeCredit 方法（回合级 bet_amount/payout 拼凑逻辑）
    - 7.6: 修改 settleCommission — 移入 CreditRound 流程，仍为纯记账
    - 7.7: 修改 DeductPenaltyToPlatform / DistributePenaltyFromPlatform — 分发罚金改为调用 platform.Credit() 而非 platform.Settle()

- [x] Task 8: 修改 DeductService — service/deduct_service.go
    - 8.1: 确保扣款Bill写入 SessionID（已有字段，检查确认）

- [x] Task 9: 修改 CreditRetryService — service/credit_retry_service.go
    - 9.1: RetryCredit 中将 platform.Settle 改为 platform.Credit
    - 9.2: 构造 CreditRequest 替代 SettleRequest

- [x] Task 10: 修改 RewardSettler — service/reward_settler.go
    - 10.1: 奖励入账改为调用 platform.Credit() 而非 platform.Settle()

- [x] Task 11: 新增 GameSettleService — service/game_settle_service.go
    - 11.1: 实现 SettleGame 核心逻辑：聚合 → 逐玩家调用 platform.Settle(/settle) → 更新状态
    - 11.2: 实现 retryPlayerSettle 方法：重试单个玩家的游戏级结算

- [x] Task 12: 新增调度器
    - 12.1: 新增 GameSettleTimeoutScheduler — 扫描超时未结算游戏，触发强制结算
    - 12.2: 新增 GameSettleRetryScheduler — 扫描结算失败的游戏，重试未结算玩家

- [x] Task 13: 适配现有调度器
    - 13.1: CreditRetryScheduler — 适配 Credit 重试（非 Settle 重试）
    - 13.2: ExceptionHandleScheduler — 异常处理适配游戏级结算逻辑
    - 13.3: PairBillCheckScheduler — 对账逻辑适配游戏级
    - 13.4: SettlementCheckScheduler — 检查逻辑适配游戏级

- [x] Task 14: 清理旧 Redis Key — infrastructure/persistence/redis/keys.go
    - 14.1: 移除 SettleRoundLockKey（回合级 Settle 锁，不再需要）
    - 14.2: 移除 SettlementDoneKey（回合级 Settle 完成标记，不再需要）
