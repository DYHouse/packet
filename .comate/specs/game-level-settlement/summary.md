# 重构完成总结：回合级扣款 + 回合级入账 + 游戏级结算

## 重构目标

将结算逻辑从"每回合调用 platform.Settle(/credit_n_settle) 同时完成入账+结算"拆分为：
- **回合级入账**：调用 platform.Credit(/credit)，只加钱
- **游戏级结算**：10 回合结束后调用 platform.Settle(/settle)，只上报 bet_amount/payout/result，不移动资金

## 变更文件清单

### 新增文件
| 文件 | 说明 |
|---|---|
| `service/game_settle_service.go` | 游戏级结算核心逻辑（聚合 BillRecord + 调用 platform.Settle） |
| `scheduler/game_settle_timeout_scheduler.go` | 游戏结算超时调度器（5min 间隔） |
| `scheduler/game_settle_retry_scheduler.go` | 游戏结算重试调度器（30s 间隔） |

### 修改文件
| 文件 | 变更内容 |
|---|---|
| `api/platform/gamingpanda_client.go` | Settle 端点从 `/credit_n_settle` 改为 `/settle` |
| `api/platform/mock_client.go` | Settle 实现改为仅记录游戏结果，不修改余额 |
| `dto/constants.go` | 新增 RoundStatusCredited=6, GameSettleStatus, BillGameSettleStatus 枚举 |
| `dto/request.go` | 新增 GameSettleRequest |
| `dto/response.go` | 新增 GameSettleInfo, GamePlayerSettleInfo |
| `model/bill.go` | BillRecord 增加 GameSettleStatus 字段；RoundSettlement 增加 GameSettleStatus + GameSettledAt |
| `infrastructure/persistence/redis/keys.go` | 新增 GameSettleLockKey；移除 KeySettlementDone 和 SettlementDoneKey |
| `service/settlement_service.go` | 核心重构：SettleRound 改为 CreditRound(调用 Credit)；executeCredit 替换为 executeCreditOnly；DistributePenaltyFromPlatform 改用 Credit；新增 checkAndSettleGame/SettleGame |
| `service/credit_retry_service.go` | executeCredit 中 GrabPacket/SystemReward 分支的 platform.Settle 改为统一的 platform.Credit |
| `service/reward_settler.go` | 奖励入账从 platform.Settle 改为 platform.Credit |
| `service/bill_manager.go` | 新增 AggregateBetBySession, AggregatePayOutBySession, UpdateGameSettleStatusByUser, GetUnsettledUsersBySession, GetAllRoundSettlementsBySession, UpdateGameSettleStatusBySession, GetPendingGameSettlements, GetFailedGameSettlements, GetTimedOutGameSettlements |

## 核心逻辑变更

### 回合结束流程
```
旧: Deduct → Settle(/credit_n_settle, 含bet拼凑逻辑) → 完成
新: Deduct → Credit(/credit, 只加钱) → 检查是否触发游戏结算
```

### 游戏结算流程（新增）
```
所有回合 Credited → 聚合 BillRecord(按 SessionID+UserID) →
  bet_amount = 扣款Bill总和, payout = 入账Bill总和 →
  platform.Settle(/settle, 只上报不移动资金) → 标记 GameSettleStatus
```

### platform.Settle 语义变更
```
旧: POST /credit_n_settle → balance = balance - betAmount + payout（入账+结算）
新: POST /settle → 仅记录游戏结果，不修改余额（只结算不入账）
```

### 关键设计决策
- 不新建数据库表，通过 RoundSettlement.GameSettleStatus 和 BillRecord.GameSettleStatus 跟踪游戏级结算状态
- 游戏结算数据运行时聚合（按 SessionID 从 BillRecord 聚合），不持久化中间结果
- 只新增一个 Redis 分布式锁（GameSettleLockKey），幂等判断走数据库
