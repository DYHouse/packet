# Settlement 模块逆向分析与重构设计文档

> 本文档基于对 Settlement 模块源码的完整逆向阅读生成。**代码是唯一事实来源**，所有结论均可追溯到具体代码位置。未参考任何既有设计文档，未凭经验猜测业务。

---

## 目录

1. [Settlement 模块概览](#1-settlement-模块概览)
2. [功能清单（完整）](#2-功能清单完整)
3. [核心业务流程](#3-核心业务流程)
4. [调用链分析](#4-调用链分析)
5. [状态流分析](#5-状态流分析)
6. [数据流分析](#6-数据流分析)
7. [事件流分析](#7-事件流分析)
8. [核心对象分析](#8-核心对象分析)
9. [当前优秀设计（保留理由）](#9-当前优秀设计保留理由)
10. [当前存在问题（原因+风险+建议）](#10-当前存在问题原因风险建议)
11. [Code Smell 清单](#11-code-smell-清单)
12. [可删除代码清单（附理由与影响分析）](#12-可删除代码清单附理由与影响分析)
13. [重构目标](#13-重构目标)
14. [推荐整体架构](#14-推荐整体架构)
15. [推荐目录结构](#15-推荐目录结构)
16. [推荐模块划分](#16-推荐模块划分)
17. [推荐对象设计](#17-推荐对象设计)
18. [推荐业务流程](#18-推荐业务流程)
19. [分阶段重构计划](#19-分阶段重构计划phase-1--phase-n)
20. [重构风险评估](#20-重构风险评估)
21. [最终建议](#21-最终建议)

---

## 1. Settlement 模块概览

### 1.1 模块定位

Settlement 模块是"红包游戏"后台的**结算子系统**，负责所有涉及资金流动的账务处理，包括：玩家扣款、抢红包入账、佣金、系统奖励、会话级派奖、整局上报平台、罚扣、退款、失败重试与对账。

**关键事实**：Settlement **不是独立部署的服务**，而是**嵌入在 game 二进制中**的子模块。

证据：
- 不存在 `cmd/settlement/main.go`；`cmd/game/main.go` 仅调用 `bootstrap.Run()`。
- `game/bootstrap/app.go` 的 `NewApplicationWithConfig` 负责构建全部 settlement Repository / Service / AppService 并注入 game 容器。
- `game/bootstrap/container.go` 持有 `SettleAppSvc`、`RoundSettleSvc`、`PenaltySettlementSvc`、`BalanceQuerySvc`、`DeductSvc`、`RefundSvc`、`BalanceService`、`VirtualBalanceService` 等 settlement 字段。
- 5 个 settlement scheduler 通过 `container.go:initSettlementSchedulers()` 注册到 game 的 `SchedulerRegistry`，与 game scheduler 一同启停。

### 1.2 目录结构（现状）

```
settlement/
├── application/                  # 用例编排层（AppService facade）
│   ├── doc.go                    # 包文档 + 事务编排策略说明
│   ├── settle_app_service.go     # SettleAppService（结算用例入口，9 方法）
│   ├── refund_app_service.go     # RefundAppService（退款用例入口，5 方法）— 死代码
│   └── scheduler_app_service.go  # SchedulerAppService（5 个调度器统一入口）
├── config/
│   └── config.go                 # PlatformConfig / LockConfig 别名与默认工厂
├── domain/                       # 领域层（聚合根 + 领域服务接口 + 仓储接口）
│   ├── bill.go                   # BillRecord 聚合根 + 状态枚举
│   ├── round_settlement.go       # RoundSettlement 聚合根 + 状态枚举
│   ├── refund.go                 # RefundAudit 聚合根 + 状态枚举
│   ├── exception.go              # ExceptionRecord 聚合根（枚举 type alias 自 model）
│   ├── errors.go                 # 领域错误（NotFound / InvalidStatus）
│   ├── platform_user.go          # PlatformUser 值对象
│   ├── user_service.go           # UserService 领域服务接口（game 实现）
│   ├── virtual_balance_service.go# VirtualBalanceService + RobotAccountStore 接口
│   └── repository/
│       ├── transaction.go        # Transaction / DBRepository（Unit of Work）
│       ├── bill_repository.go    # BillRepository 接口（20 方法）
│       ├── round_settlement_repository.go  # 13 方法
│       ├── refund_audit_repository.go      # 9 方法
│       ├── exception_repository.go         # 1 方法（仅 Create）
│       ├── platform_call_log_repository.go # 4 方法
│       └── settlement_query_repository.go  # 4 方法（只读聚合，CQRS 读模型）
├── dto/                          # 数据传输对象
│   ├── constants.go              # 流程控制常量 + 从 domain 重导出的兼容别名
│   ├── platform_call.go          # CallLogCreateParams / CallLogUpdateParams
│   ├── request.go                # 15 个请求 DTO（无 json tag）
│   └── response.go               # 10 个响应 DTO（部分带 json tag）
├── model/                        # GORM 持久化模型
│   ├── bill.go                   # BillRecord + RoundSettlement（表 bill_record / round_settlement）
│   ├── exception_record.go       # ExceptionRecord + 枚举类型定义（表 exception_record）
│   ├── platform_settle_log.go    # PlatformCallLog + 状态常量（表 platform_call_log）
│   └── refund.go                 # RefundAudit（表 refund_audit）
├── service/                      # 领域服务层（业务逻辑承载）
│   ├── doc.go                    # 包文档 + 7 业务域分组
│   ├── deduct_service.go         # 扣款服务（首轮/后续轮/系统红包）
│   ├── round_settle_service.go   # 回合结算服务
│   ├── game_settle_reporting_service.go  # 整局上报服务
│   ├── session_payout_service.go # 会话级派奖服务
│   ├── credit_retry_service.go   # 入账重试服务
│   ├── refund_service.go         # 退款服务
│   ├── penalty_settlement_service.go     # 罚扣服务
│   ├── reward_settler.go         # 系统奖励结算器
│   ├── balance_service.go        # 余额校验服务（含 CheckBalanceForReady）
│   ├── balance_query_service.go  # 余额查询服务
│   ├── settlement_check_service.go       # 对账检查服务
│   ├── robot_checker.go          # 机器人识别服务（Redis SISMEMBER）
│   ├── user_id_convert_service.go        # 内部 ID → 平台 ID 转换
│   ├── trace_id_generator.go     # 业务号生成器（确定性 + 雪花）
│   └── *_test.go / mocks_test.go # 测试与桩件
├── scheduler/                    # 薄壳调度器（仅持有 base + schedulerApp + 配置）
│   ├── credit_retry_scheduler.go
│   ├── game_settle_retry_scheduler.go
│   ├── game_settle_timeout_scheduler.go
│   ├── refund_process_scheduler.go
│   └── settlement_check_scheduler.go
└── infrastructure/
    └── persistence/
        ├── mysql/                # MySQL 仓储实现
        │   ├── db_repository.go          # dbRepositoryImpl + gormTransactionImpl（Unit of Work 实现）
        │   ├── bill_repository.go         # billRepository（20 方法实现）
        │   ├── round_settlement_repository.go
        │   ├── refund_audit_repository.go
        │   ├── exception_repository.go   # ExceptionRepositoryImpl（仅 Create）
        │   ├── platform_call_log_repository.go
        │   ├── settlement_query_repository.go
        │   └── migrations/
        │       ├── add_bill_record_compound_unique_index.sql    # v1
        │       └── add_bill_record_compound_unique_index_v2.sql # v2（替换 v1）
        └── redis/
            ├── virtual_balance_repository.go   # 虚拟余额仓储（实现 VirtualBalanceService）
            └── scripts/
                ├── registry.go                 # Lua 脚本注册
                ├── virtual_balance.lua.go      # luaDeductBalance + luaCreditBalance
                └── virtual_balance_test.go
```

### 1.3 技术栈与外部依赖

| 类别 | 依赖 |
|---|---|
| ORM | gorm |
| 缓存 | Redis（go-redis）+ miniredis（测试） |
| 分布式锁 | `common/lock.WithRedisLock`（Redis + Lua 释放） |
| ID 生成 | `common/idgen`（snowflake 封装，带时钟回拨处理）+ `crypto/rand`（对账号随机） |
| 平台 RPC | `api/platform.Client`（GetBalance / Debit / Credit / Settle） |
| 调度器 | `common/scheduler`（BaseScheduler + Registry） |
| 日志 | `common/logger` |
| Trace | `common/trace.TraceIDGenerator` |

### 1.4 数据库表

| 表名 | 模型 | 用途 | 唯一索引 |
|---|---|---|---|
| `bill_record` | `model.BillRecord` | 账单流水（扣款/入账/退款/佣金/奖励） | `idx_round_trace_bill_user(round_trace_id, bill_type, user_id)` + `biz_order_no` 唯一 |
| `round_settlement` | `model.RoundSettlement` | 单回合结算汇总 | `round_trace_id` 唯一 + `round_id` 唯一 |
| `refund_audit` | `model.RefundAudit` | 退款申请审批流 | `refund_order_no` 唯一 |
| `exception_record` | `model.ExceptionRecord` | 异常记录（人工对账） | `exception_no` 唯一 |
| `platform_call_log` | `model.PlatformCallLog` | 平台调用审计日志 | 无唯一索引 |

### 1.5 Redis Key

| Key 模式 | 类型 | 用途 |
|---|---|---|
| `cashparty:robot:virtual_balance:{userID}` | string(int64) | 机器人虚拟余额 |
| `cashparty:robot:virtual_balance:dirty` | set | 脏 userID 集合（待同步 DB） |
| `cashparty:robot:user_ids` | set | 机器人 ID 集合（SISMEMBER 判定） |
| `cashparty:scheduler:{credit_retry,game_settle_retry,game_settle_timeout,refund_process,settlement_check}:lock` | string | 调度器分布式锁 |
| `cashparty:settle:round:{roundID}:lock` 等 10 个业务锁 | string | 业务操作分布式锁 |

### 1.6 Lua 脚本

| 脚本名 | 作用 | 返回值 |
|---|---|---|
| `deduct_balance` | 原子扣减虚拟余额（INCRBY 负值 + 余额检查 + 回滚 + 标记 dirty） | `1`=成功 / `0`=余额不足 |
| `credit_balance` | 原子入账虚拟余额（INCRBY 正值 + 标记 dirty） | `1` |

均通过 `cRedis.NewScript` 注册，KEYS 与 ARGV 由 Go 侧传入，符合规约（禁止内联 `redis.Eval`）。

### 1.7 平台 RPC 调用

| RPC | 调用方 Service | 业务用途 |
|---|---|---|
| `GetBalance` | BalanceQueryService、BalanceService | 查询用户余额 |
| `Debit` | DeductService、PenaltySettlementService | 真人扣款 |
| `Credit` | CreditRetryService、RefundService、SessionPayoutService | 真人入账/退款 |
| `Settle` | GameSettleReportingService | 整局结果上报 |

机器人不调平台 RPC，改走 `VirtualBalanceService`（Redis Lua）。

### 1.8 MQ

Settlement 模块**本身不直接生产或消费任何 Kafka 主题**。触发结算的入口是 game 模块的 `GameEventConsumer`（Topic `kafka.TopicGameEvents`）：
- `GameEventRoundSettle` → `SettleAppService.SettleRound`
- `GameEventSessionEnd` → `SettleAppService.SettleGame`

---

## 2. 功能清单（完整）

基于代码逆向，Settlement 模块共提供 **21 项业务能力**，分属 7 个业务域。

### 2.1 扣款域（Deduct）

| # | 功能 | 入口 | 调用方 | 前置条件 | 后置条件 | 异常处理 |
|---|---|---|---|---|---|---|
| F1 | 首轮房费平摊扣款 | `SettleAppService.DeductForFirstRound` | `packet_round_init.go:157` | 房间开局、玩家就绪 | RoundSettlement=Deducted；成功玩家 bill=Success；失败玩家触发自动退款申请 | 部分失败：round=Failed + 成功玩家创建 RefundAudit(Pending) |
| F2 | 后续轮最低玩家房费扣款 | `SettleAppService.DeductForLaterRound` | `packet_round_init.go:212` | 后续轮开始、确定最低玩家 | RoundSettlement=Deducted；bill=Success | 失败：round=Failed |
| F3 | 系统红包扣款 | `SettleAppService.DeductForSystemPacket` | `packet_round_init.go:255` | 系统发红包触发 | RoundSettlement=Deducted；平台账户 bill=Success（纯内部记账，无 RPC） | 幂等跳过 |
| F4 | 批量扣款并发执行 | `DeductService.executeBatchDeduct`（内部） | F1 调用 | — | 最多 20 并发，panic 恢复 | panic 计数 + Warn 日志，不中断批次 |
| F5 | 单用户扣款核心流程 | `DeductService.executeSingleDeduct`（内部） | F1/F2 调用 | bill=Processing | bill=Success/Failed | fail-closed：IsRobot/ParseAmount 失败标 Failed + 异常记录 |

### 2.2 结算域（Settle）

| # | 功能 | 入口 | 调用方 | 前置条件 | 后置条件 | 异常处理 |
|---|---|---|---|---|---|---|
| F6 | 回合结算（佣金+抢红包入账） | `SettleAppService.SettleRound` | `game_event_handler.go:317`（Kafka RoundSettle 事件） | RoundSettlement=Deducted | RoundSettlement=Credited（credit+reward 全成功后） | reward 失败上抛 err 触发事务回滚，重试可补偿 |
| F7 | 系统奖励结算 | `RewardSettler.SettleReward`（事务内） | F6 调用 | RewardType>0 && RewardAmount>0 | 平台支出 bill + 玩家收入 bill（均 Success，纯内部记账） | 幂等：平台支出 bill 已 Success 则跳过 |
| F8 | 佣金结算 | `RoundSettleService.settleCommission`（事务内） | F6 调用 | Commission>0 | 平台账户佣金 bill=Success | 幂等跳过 |
| F9 | 抢红包入账记录 | `RoundSettleService.creditRound`（事务内） | F6 调用 | player.Amount>0 | 玩家 grab bill=Success（待会话级入账） | 幂等：已 Success 跳过 |
| F10 | 整局上报平台 | `SettleAppService.SettleGame` | `game_event_handler.go:377`（Kafka SessionEnd 事件） | 所有 round=Credited | session GameSettleStatus=Success/Failed；玩家 BillGameSettle=Settled | 部分失败：session=Failed，可重试 |
| F11 | 单玩家整局上报 | `GameSettleReportingService.settlePlayer`（内部） | F10/F12 调用 | bill 未 Settled | bill GameSettle=Settled；机器人跳过 RPC | RPC 失败回退 GameSettle=None |

### 2.3 派奖域（Payout）

| # | 功能 | 入口 | 调用方 | 前置条件 | 后置条件 | 异常处理 |
|---|---|---|---|---|---|---|
| F12 | 会话级派奖入账 | `SessionPayoutService.CreditSessionPayouts` | F10 调用 | payOutMap 已聚合 | 玩家 session_credit bill=Success | 首个失败即中止；指数退避重试；超限创建异常 |

### 2.4 罚款域（Penalty）

| # | 功能 | 入口 | 调用方 | 前置条件 | 后置条件 | 异常处理 |
|---|---|---|---|---|---|---|
| F13 | 罚扣上交平台 | `SettleAppService.DeductPenaltyToPlatform` | `penalty_service.go:86` | 玩家违规 | 玩家 bill=Success（扣款）+ 平台 bill=Success（收入） | fail-closed；无 Redis 锁，靠幂等状态检查+DB 唯一索引 |
| F14 | 罚款分配给替补玩家 | `SettleAppService.DistributePenaltyFromPlatform` | `game_lifecycle_timeout.go:188` | 替补超时触发 | 平台 bill=Success（支出）+ 每个替补 bill=Success（分红，整除余数分配） | 纯内部记账，无 RPC |

### 2.5 退款域（Refund）

| # | 功能 | 入口 | 调用方 | 前置条件 | 后置条件 | 异常处理 |
|---|---|---|---|---|---|---|
| F15 | 申请退款 | `RefundService.ApplyForRefund` | SettlementCheckService（自动）/ RefundAppService（手动，死代码） | bill=Success && refund_status=None | RefundAudit=Pending + bill refund_status=Pending | 幂等：已有 Pending 返回原单号 |
| F16 | 审批退款（执行退款） | `RefundService.ApproveRefund` | SchedulerAppService.ProcessPendingRefunds（自动，ApprovedBy=0）/ RefundAppService（死代码） | RefundAudit=Pending | RefundAudit=Refunded + bill=Refunded（跨表事务） | RPC 失败回退 Pending 供重试 |
| F17 | 驳回退款 | `RefundService.RejectRefund` | RefundAppService（死代码） | RefundAudit=Pending | RefundAudit=Rejected + bill refund_status=Rejected | 跨表事务 |
| F18 | 首轮失败自动退款 | `DeductService.handleFirstRoundDeductFailure`（内部） | F1 部分失败触发 | 首轮扣款部分失败 | 成功玩家创建 RefundAudit(Pending) | 单玩家失败 continue |

### 2.6 重试与对账域（Retry & Reconcile）

| # | 功能 | 入口 | 调用方 | 前置条件 | 后置条件 | 异常处理 |
|---|---|---|---|---|---|---|
| F19 | 入账重试 | `SchedulerAppService.RetryCreditBills` | CreditRetryScheduler（定时） | bill=Processing/Failed && retry_count<3 | bill=Success 或创建异常记录 | 指数退避；超限创建 ExceptionTypeCreditRetryExceed |
| F20 | 整局结算重试 | `SchedulerAppService.RetryGameSettle` | GameSettleRetryScheduler（定时） | session GameSettleStatus=Failed | 玩家 GameSettle=Settled + session=Success | 单玩家失败置 allSuccess=false |
| F21 | 整局超时兜底结算 | `SchedulerAppService.SettleGameByTimeout` | GameSettleTimeoutScheduler（定时） | 所有 round=Credited && GameSettle=None && 超时 | 调 SettleGame | 失败仅 Warn |
| F22 | 待处理退款自动审批 | `SchedulerAppService.ProcessPendingRefunds` | RefundProcessScheduler（定时） | RefundAudit=Pending && type=FirstRoundFail | 执行 ApproveRefund（ApprovedBy=0 系统自动） | 单条失败 continue |
| F23 | 对账检查-首轮扣款失败 | `SchedulerAppService.RunSettlementCheck`（第 1 项） | SettlementCheckScheduler（定时） | round=DeductedFirstRound && status=Failed && 时间窗内 | 为成功 bill 创建退款申请 | 单条失败 continue |
| F24 | 对账检查-扣款未结算 | `SchedulerAppService.RunSettlementCheck`（第 2 项） | SettlementCheckScheduler（定时） | round=Deducted && settle_amount IS NULL && 时间窗内 | 创建 ExceptionRecord（不自动退款，人工介入） | 单条失败 continue |
| F25 | 整局结算重试（单玩家） | `GameSettleReportingService.RetryPlayerSettle` | F20 调用 | 玩家未 Settled | 调 settlePlayer | 锁内重试 |

### 2.7 查询域（Query）

| # | 功能 | 入口 | 调用方 | 前置条件 | 后置条件 |
|---|---|---|---|---|---|
| F26 | 余额校验（准备/入房） | `SettleAppService.CheckBalanceForReady` | `room_app_service.go:187,313`、`seat_app_service.go:239` | — | 返回 BalanceCheckResult（含 required_fee 计算） |
| F27 | 余额查询 | `SettleAppService.CheckBalance` | `room_app_service.go:504` | — | 返回 (balance, isSufficient) |
| F28 | 用户余额查询（gRPC） | `BalanceService.CheckUserBalance` | `generic_service.go:501` | — | 返回余额 |
| F29 | 账单查询 | `BalanceQueryService.GetBillByTraceID/GetBillsByUserID/GetBillsByRoundID` | 内部 | — | 返回 bill 列表 |
| F30 | 回合结算查询 | `BalanceQueryService.GetRoundSettlement` | 内部 | — | 返回 RoundSettlement |

### 2.8 基础设施支持域（Infrastructure）

| # | 功能 | 入口 | 调用方 |
|---|---|---|---|
| F31 | 机器人识别 | `RobotChecker.IsRobot` | 所有涉及资金的 Service |
| F32 | 内部 ID→平台 ID 转换 | `UserIDConvertService.GetPlatformUserID` | 所有含 RPC 的 Service |
| F33 | 业务号生成 | `TraceIDGenerator.*` | 几乎所有 Service |
| F34 | 虚拟余额操作 | `VirtualBalanceService.Deduct/Credit/GetBalance/SyncToDB/...` | 扣款/入账/罚扣 Service |
| F35 | 虚拟余额同步 DB | `VirtualBalanceService.SyncToDB` | `VirtualBalanceSyncScheduler`（game 侧） |

---

## 3. 核心业务流程

### 3.1 主流程一：一局游戏的完整结算生命周期

```
房间开局
  │
  ▼
[首轮发红包] packet_round_init.go
  │ DeductForFirstRound（F1）
  │   ├─ 幂等预检查（ExistsRoundSettlement）
  │   ├─ Redis 锁 + 二次检查
  │   ├─ 生成 roundTraceID / batchID
  │   ├─ 事务：CreateRoundSettlementAndBills（settlement + N 个 player bill）
  │   ├─ 并发扣款（executeBatchDeduct，最多 20 并发）
  │   │   ├─ UpdateBillStatus(→Processing)
  │   │   ├─ 机器人→VirtualBalance.Deduct；真人→platform.Debit
  │   │   ├─ ParseAmount fail-closed
  │   │   └─ UpdateBillSuccess(→Success) 或 UpdateBillStatus(→Failed)
  │   ├─ 全成功：UpdateRoundSettlementDeductSuccess + status=Deducted
  │   └─ 部分失败：status=Failed + handleFirstRoundDeductFailure（成功玩家自动退款申请）
  ▼
[游戏进行：抢红包]（game 模块，触发结算）
  │
  ▼
[回合结算] Kafka RoundSettle 事件 → game_event_handler.go:317
  │ SettleRound（F6）
  │   ├─ 事务（AppService 层 WithTransaction）
  │   ├─ 幂等：round=Credited 直接返回
  │   ├─ Redis 锁 + 二次检查
  │   ├─ UpdateRoundSettlementSettleInfo
  │   ├─ creditRound（事务内）
  │   │   ├─ settleCommission（佣金 bill，幂等）
  │   │   └─ 遍历 players：创建 grab bill（幂等，已 Success 跳过）
  │   ├─ SettleReward（事务内，若有奖励）
  │   │   └─ 创建平台支出 bill + 玩家收入 bill（幂等）
  │   └─ UpdateRoundSettlementCredited（credit+reward 全成功才标记）
  ▼
[后续轮] packet_round_init.go
  │ DeductForLaterRound（F2）→ deductSingleUser → executeSingleDeduct
  ▼
[会话结束] Kafka SessionEnd 事件 → game_event_handler.go:377
  │ SettleGame（F10）
  │   ├─ 锁前 cheap pre-check（全已 Settled 跳过）
  │   ├─ Redis 锁 + 二次检查
  │   ├─ 校验所有 round=Credited
  │   ├─ UpdateGameSettleStatusBySession(None→Settling)
  │   ├─ 聚合 betMap / payOutMap（CQRS 读模型）
  │   ├─ CreditSessionPayouts（F12，会话级派奖，失败不中断）
  │   ├─ 遍历 allUsers：
  │   │   ├─ 跳过平台账户 / 零流水
  │   │   ├─ 幂等检查（IsPlayerGameSettled）
  │   │   └─ settlePlayer（F11）
  │   │       ├─ 机器人：UpdateGameSettleStatusByUser(→Settled)，跳过 RPC
  │   │       └─ 真人：platform.Settle（Processing 中间态，失败回退 None）
  │   └─ UpdateGameSettleStatusBySession(Settling→Success/Failed)
  ▼
[整局结算完成]
```

### 3.2 主流程二：罚扣与分配

```
玩家违规
  │
  ▼ penalty_service.go:86
DeductPenaltyToPlatform（F13）
  ├─ 生成 penaltyDeductTraceID
  ├─ 幂等：GetBillByTraceTypeAndUser（Success/Processing/Pending 跳过，仅 Failed 放行）
  ├─ 事务：CreateBillsPair（playerBill=扣款 + platformBill=收入）
  ├─ 机器人→VirtualBalance.Deduct；真人→platform.Debit
  └─ fail-closed

替补超时
  │
  ▼ game_lifecycle_timeout.go:188
DistributePenaltyFromPlatform（F14）
  ├─ 事务（AppService 层）
  ├─ 幂等检查
  ├─ 整除余数分配（前 remainder 个 recipient 多分 1）
  └─ CreateBills（platformBill=支出 + N 个 shareBill=分红，均 Success，纯记账）
```

### 3.3 主流程三：退款全流程

```
[触发退款申请]
  │
  ├─ 自动：SettlementCheckService.CheckFirstRoundDeductFailure（F23）
  │        → refundSvc.ApplyForRefund（F15）
  ├─ 自动：DeductService.handleFirstRoundDeductFailure（F18）
  │        → 直接创建 RefundAudit（不经 RefundService）
  └─ 手动：RefundAppService.ApplyForRefund（死代码，无调用方）
  │
  ▼ RefundAudit=Pending
[自动审批] RefundProcessScheduler（定时，仅 FirstRoundFail 类型）
  │ ProcessPendingRefunds（F22）→ ApproveRefund（ApprovedBy=0）
  │   ├─ 幂等：非 Pending 返回 nil
  │   ├─ UpdateRefundAuditStatus(→Approved)
  │   └─ executeRefund
  │       ├─ UpdateRefundAuditToProcessing（乐观锁）
  │       ├─ platform.Credit
  │       │   ├─ 失败：UpdateRefundAuditToPendingForRetry（回退，供重试）
  │       │   └─ 成功：事务 UpdateRefundSuccess（refund_audit=Refunded + bill=Refunded）
  │       └─ ParseAmount fail-closed
  ▼ RefundAudit=Refunded
```

### 3.4 主流程四：失败重试与对账

```
[入账重试] CreditRetryScheduler（30s）
  │ RetryCreditBills（F19）
  │   ├─ GetRetryableCredits（Processing/Failed && retry_count<3 && next_retry_at<=now）
  │   └─ 逐条 RetryCredit
  │       ├─ 锁（BillRetryLockKey）
  │       ├─ Success 跳过；超限→创建异常
  │       ├─ 平台账户直接标 Success
  │       ├─ platform.Credit
  │       └─ 指数退避：next_retry_at = now + 5s * 2^retryCount（封顶 5min）
  ▼
[整局重试] GameSettleRetryScheduler（30s）
  │ RetryGameSettle（F20）
  │   ├─ GetFailedGameSettlements
  │   ├─ 逐 session：GetUnsettledUsersBySession → RetryPlayerSettle
  │   └─ 全成功：UpdateGameSettleStatusBySession(Failed→Success)
  ▼
[超时兜底] GameSettleTimeoutScheduler（5min）
  │ SettleGameByTimeout（F21）
  │   └─ round=Credited && GameSettle=None && 超时 → SettleGame
  ▼
[对账检查] SettlementCheckScheduler（5min）
  │ RunSettlementCheck（F23+F24）
  │   ├─ CheckFirstRoundDeductFailure：Failed 首轮 → 自动创建退款申请
  │   └─ CheckDeductedButNotSettled：扣款未结算 → 创建异常（人工介入，不自动退款）
  ▼
[虚拟余额同步] VirtualBalanceSyncScheduler（game 侧）
  │ SyncToDB
  │   └─ SPOP dirty 集合 → UpdateBalance → 失败 SAdd 回去
```

### 3.5 时序图：首轮扣款（F1）核心时序

```
game/application          SettleAppService         DeductService            platform.Client     DB/Redis
packet_round_init.go
     │                          │                       │                       │                │
     │──DeductForFirstRound────▶│                       │                       │                │
     │                          │──DeductForFirstRound─▶│                       │                │
     │                          │                       │──ExistsRoundSettlement──────────────▶│(幂等)
     │                          │                       │──WithRedisLock──────────────────────▶│(锁)
     │                          │                       │──ExistsRoundSettlement──────────────▶│(二次)
     │                          │                       │──GenerateRoundTraceID/BatchID         │
     │                          │                       │──WithTransaction────────────────────▶│(事务开始)
     │                          │                       │  CreateRoundSettlementAndBills──────▶│(settlement+bills)
     │                          │                       │◀─────────────────────────────────────│(事务提交)
     │                          │                       │──executeBatchDeduct (goroutine x N)  │
     │                          │                       │  ├─UpdateBillStatus(→Processing)────▶│
     │                          │                       │  ├─IsRobot─────────────────────────▶│(Redis SISMEMBER)
     │                          │                       │  ├─[机器人] VirtualBalance.Deduct──▶│(Lua)
     │                          │                       │  ├─[真人] userIDConvert→Debit──────▶│(RPC)
     │                          │                       │  ├─ParseAmount (fail-closed)         │
     │                          │                       │  └─UpdateBillSuccess/UpdateBillStatus▶│
     │                          │                       │──UpdateRoundSettlementDeductSuccess─▶│
     │                          │                       │──UpdateRoundSettlementStatus────────▶│
     │                          │                       │──[部分失败] handleFirstRoundDeductFailure
     │                          │                       │    └─WithTransaction: CreateRefundAuditAndUpdateBillRefundStatus
     │◀─────────────────────────│◀──────────────────────│                       │                │
```

---

## 4. 调用链分析

### 4.1 外部入口完整清单（入向）

| # | 调用方文件:行 | 入口方法 | 触发场景 | 事务策略 |
|---|---|---|---|---|
| 1 | game/application/game_event_handler.go:317 | SettleAppService.SettleRound | Kafka RoundSettle | AppService 开事务 |
| 2 | game/application/game_event_handler.go:377 | SettleAppService.SettleGame | Kafka SessionEnd | Service 内部（含 RPC） |
| 3 | game/application/packet_round_init.go:157 | SettleAppService.DeductForFirstRound | 首轮发红包 | Service 内部（含 RPC） |
| 4 | game/application/packet_round_init.go:212 | SettleAppService.DeductForLaterRound | 后续轮发红包 | Service 内部（含 RPC） |
| 5 | game/application/packet_round_init.go:255 | SettleAppService.DeductForSystemPacket | 系统红包 | AppService 开事务 |
| 6 | game/application/penalty_service.go:86 | SettleAppService.DeductPenaltyToPlatform | 玩家违规 | Service 内部（含 RPC） |
| 7 | game/application/game_lifecycle_timeout.go:188 | SettleAppService.DistributePenaltyFromPlatform | 替补超时 | AppService 开事务 |
| 8 | game/application/seat_app_service.go:239 | SettleAppService.CheckBalanceForReady | SetReady | 只读 |
| 9 | game/application/room_app_service.go:187 | SettleAppService.CheckBalanceForReady | 加入房间 | 只读 |
| 10 | game/application/room_app_service.go:313 | SettleAppService.CheckBalanceForReady | 房间流程 | 只读 |
| 11 | game/application/room_app_service.go:504 | SettleAppService.CheckBalance | 房间流程 | 只读 |
| 12 | game/server/generic_service.go:501 | BalanceService.CheckUserBalance | gRPC | 只读 |
| 13-17 | 5 个 settlement scheduler.execute | SchedulerAppService.{RetryCreditBills,RetryGameSettle,SettleGameByTimeout,ProcessPendingRefunds,RunSettlementCheck} | 定时 | Service 内部 |
| 18 | game/scheduler/virtual_balance_sync.go | VirtualBalanceService.SyncToDB | 定时（game 侧） | — |
| 19 | (DI bridge) UserSaverAdapter | domain.UserService | 依赖注入 | — |
| 20 | (DI bridge) robotAccountStoreAdapter | domain.RobotAccountStore | 依赖注入 | — |

### 4.2 Service 间调用关系

```
SettleAppService
  ├─→ RoundSettleService.SettleRound
  │     ├─→ RewardSettler.SettleReward（事务内）
  │     ├─→ RoundSettleService.settleCommission（事务内）
  │     └─→ RoundSettleService.creditRound（事务内）
  ├─→ RoundSettleService.SettleGame → GameSettleReportingService.SettleGame
  │     ├─→ SessionPayoutService.CreditSessionPayouts
  │     │     └─→ SessionPayoutService.executeSessionCredit → platform.Credit
  │     └─→ GameSettleReportingService.settlePlayer → platform.Settle
  ├─→ DeductService.DeductForFirstRound
  │     ├─→ DeductService.executeBatchDeduct → executeSingleDeduct → platform.Debit
  │     └─→ DeductService.handleFirstRoundDeductFailure → refundAuditRepo.CreateRefundAuditAndUpdateBillRefundStatus
  ├─→ DeductService.DeductForLaterRound → deductSingleUser → executeSingleDeduct
  ├─→ DeductService.DeductForSystemPacket（事务内）
  ├─→ PenaltySettlementService.DeductPenaltyToPlatform → platform.Debit
  ├─→ PenaltySettlementService.DistributePenaltyFromPlatform（事务内）
  ├─→ DeductService（注入 CreditRetryService.CreateDebitFailedException）
  ├─→ BalanceQueryService.CheckBalance
  └─→ BalanceService.CheckBalanceForReady

SchedulerAppService
  ├─→ CreditRetryService.RetryCredit → platform.Credit
  ├─→ GameSettleReportingService.RetryPlayerSettle → settlePlayer
  ├─→ GameSettleReportingService.SettleGame
  ├─→ RefundService.ApproveRefund → executeRefund → platform.Credit
  └─→ SettlementCheckService
        ├─→ CheckFirstRoundDeductFailure → RefundService.ApplyForRefund
        └─→ CheckDeductedButNotSettled → exceptionRepo.Create
```

### 4.3 跨模块依赖

- settlement → game 的反向依赖**已通过接口倒置解除**：
  - `domain.UserService` 由 game 的 `UserSaverAdapter` 实现
  - `domain.RobotAccountStore` 由 game 的 `robotAccountStoreAdapter` 实现
  - `BalanceService.CheckBalanceForReady` 调用 `game/domain/room.CalculateRequiredFee`（**仍存在跨模块直接依赖**）
- settlement → common：lock、redis、logger、trace、idgen、async、scheduler、rediskeys、config
- settlement → api/platform：Client 接口

---

## 5. 状态流分析

### 5.1 BillRecord 状态机

```
                    ┌──────────────────────────────────────┐
                    ▼                                       │
  Processing(0) ────────► Success(1) ────────► Refunded(3) [终态]
       │                    ▲
       │                    │
       └────────────────────► Failed(2)
                                ▲
                                │ (可重试，非终态)
```

- `CanRefund()`：仅 Success 返回 true
- `IsTerminalStatus()`：仅 Refunded 返回 true
- **Failed 非终态**：扣款域允许重新创建 bill 重试；入账域靠 CreditRetryService 重试至 MaxRetryCount 后创建异常

### 5.2 RoundSettlement 状态机

```
  Deducting(0) ──► Deducted(1) ──► Settling(2) ──► Success(3) ──► Credited(6) [终态]
       │                               │                │
       │                               ├───────────────► Partial(4) ──► Credited(6) [终态]
       │                               └───────────────► Failed(5) [终态]
       └──────────────────────────────────────────────► Failed(5) [终态]
```

- `CanSettle()`：仅 Deducted 返回 true
- `IsTerminalStatus()`：Credited 或 Failed
- **关键设计**：`creditRound` 不标记 Credited，由 `SettleRound` 在 credit+reward 全成功后统一标记（避免 reward 失败但 round 被 Credited 导致无法补偿）
- 注意：状态机定义了 Settling(2)/Success(3)/Partial(4)，但**实际代码中 creditRound 不经过 Settling**，直接从 Deducted 跳到 Credited（Settling/Success/Partial 在当前实现中**未被使用**，见 §11）

### 5.3 GameSettle 状态机（会话级）

```
  None(0) ──► Settling(1) ──► Success(2) [终态]
                  │
                  └──────────► Failed(3) [可重试，非终态]
```

玩家级 `BillGameSettle`：
```
  None(0) ──► Processing(2) ──► Settled(1) [终态]
                  │
                  └──────────► None(0) (RPC 失败回退)
```

### 5.4 RefundAudit 状态机

```
  None(0) ──► Pending(1) ──► Approved(2) ──► Processing(5) ──► Refunded(3) [终态]
                  │                              │
                  │                              └──► Pending(1) (RPC 失败回退，供重试)
                  └──────────────────────────────► Rejected(4) [终态]
```

- `CanApprove()`：仅 Pending
- `CanRetry()`：仅 Processing
- `IsTerminalStatus()`：Refunded 或 Rejected
- **Approved(2) 是过渡态**：实际由 `UpdateRefundAuditStatus(→Approved)` 后立即进入 `executeRefund`（→Processing）

### 5.5 ExceptionRecord 状态机

```
  Pending(0) ──► Processing(1) ──► Resolved(2) [终态]
       │
       └──────────────────────► Ignored(3) [终态]
```

- `CanHandle()`：仅 Pending
- 注意：当前 `ExceptionRepository` **仅实现 Create**，无查询/更新/处理方法，状态机守卫方法的调用路径在代码中**不可见**（异常记录处理可能由人工 DB 操作或未实现的功能承担）

---

## 6. 数据流分析

### 6.1 资金数据流

```
[真人资金流]
  玩家钱包(平台) ◄──Debit──── DeductService/PenaltySettlementService
                ────Credit──► SessionPayoutService/CreditRetryService/RefundService
                ────Settle──► (仅上报结果，不移动资金) GameSettleReportingService

[机器人资金流]（隔离于平台）
  Redis 虚拟余额 ◄──Deduct(Lua)── DeductService/PenaltySettlementService
                ────Credit(Lua)─► SessionPayoutService
                ────SyncToDB───► MySQL robot_account（异步，SPOP dirty 集合）

[内部账目流]（bill_record，纯记账，不调 RPC）
  - 系统红包扣款：平台账户 bill（-总额，Success）
  - 佣金：平台账户 bill（+佣金，Success）
  - 系统奖励：平台账户 bill（-总支出，Success）+ 玩家 bill（+奖励，Success）
  - 抢红包：玩家 bill（+金额，Success，待会话级入账）
  - 罚款分配：平台 bill（-总额）+ 替补 bill（+分红）
```

### 6.2 账单数据流

```
所有资金操作 → 生成 BizOrderNo（确定性：{roundTraceID}_{billType}_{userID}）
            → 写入 bill_record（带唯一索引 idx_round_trace_bill_user + biz_order_no 唯一）
            → 状态机更新（乐观锁 WHERE status=?）
            → 失败可重试（CreditRetryService / GameSettleRetryScheduler）
            → 超限创建 exception_record
            → 退款创建 refund_audit + 更新 bill refund_status
            → 整局聚合（settlementQueryRepo.AggregateBet/PayOutBySession）
```

### 6.3 关键字段流向

- `round_trace_id`：贯穿 settlement + bill_record，是回合级关联键
- `biz_order_no`：平台 RPC 的 BizID，幂等去重核心
- `session_id`：会话级聚合键（GameSettle）
- `batch_id`：仅首轮批量扣款使用（雪花 ID，非确定性）
- `game_settle_status`：bill 级 + round 级 + session 级三套独立状态

---

## 7. 事件流分析

### 7.1 Kafka 事件（间接触发）

Settlement 不直接消费 Kafka，但由 game 的 `GameEventConsumer` 触发：

| Kafka 事件 | 消费者 | 触发的结算入口 |
|---|---|---|
| `GameEventRoundSettle` | GameEventConsumer.handleRoundSettle | SettleAppService.SettleRound |
| `GameEventSessionEnd` | GameEventConsumer.handleSessionEnd | SettleAppService.SettleGame |

### 7.2 内部事件流（状态转换即事件）

无显式领域事件发布。状态转换通过 DB UPDATE 体现，跨模块通知通过：
- 同步返回 error（触发 Kafka 重试）
- 定时调度器轮询 DB 状态（CreditRetry / GameSettleRetry / RefundProcess / SettlementCheck / GameSettleTimeout）

### 7.3 调度器事件流

```
[Cron 30s] CreditRetryScheduler ──► RetryCreditBills ──► platform.Credit
[Cron 30s] GameSettleRetryScheduler ──► RetryGameSettle ──► settlePlayer
[Cron 5m]  GameSettleTimeoutScheduler ──► SettleGameByTimeout ──► SettleGame
[Cron 1m]  RefundProcessScheduler ──► ProcessPendingRefunds ──► ApproveRefund
[Cron 5m]  SettlementCheckScheduler ──► RunSettlementCheck ──► ApplyForRefund / CreateException
[Cron ?]   VirtualBalanceSyncScheduler ──► SyncToDB（game 侧）
```

所有调度器通过 `BaseScheduler.executeTask` 获取 Redis 分布式锁后执行，ctx 带 `WithTimeout(interval)`。

---

## 8. 核心对象分析

### 8.1 聚合根

| 聚合根 | 职责 | 生命周期 | 依赖 | SRP 评估 |
|---|---|---|---|---|
| `BillRecord` | 单笔账务流水 | Processing→Success/Failed→Refunded | 无 | ✅ 单一职责 |
| `RoundSettlement` | 单回合结算汇总 | Deducting→Deducted→Credited/Failed | 聚合多个 BillRecord | ✅ |
| `RefundAudit` | 退款审批单 | Pending→Approved→Processing→Refunded/Rejected | 关联 BillRecord | ✅ |
| `ExceptionRecord` | 异常对账记录 | Pending→Processing→Resolved/Ignored | 关联 BillRecord | ✅ |

### 8.2 领域服务接口

| 接口 | 实现方 | 职责 |
|---|---|---|
| `UserService` | game/UserSaverAdapter | 内部 ID → 平台用户信息查询 |
| `VirtualBalanceService` | settlement/VirtualBalanceRepository | 机器人虚拟余额操作（7 方法） |
| `RobotAccountStore` | game/robotAccountStoreAdapter | 机器人账户 DB 读写（2 方法） |

### 8.3 Service 层对象

| Service | 职责 | 依赖数 | SRP 评估 |
|---|---|---|---|
| `DeductService` | 扣款（首轮/后续/系统） | 15 字段 | ⚠️ 偏大，承载 3 种扣款场景 + 批量并发 + 失败退款 |
| `RoundSettleService` | 回合结算编排 | 8 字段 | ✅ |
| `GameSettleReportingService` | 整局上报 | 12 字段 | ⚠️ 含聚合 + 派奖编排 + 单玩家上报 |
| `SessionPayoutService` | 会话级派奖 | 9 字段 | ✅ |
| `CreditRetryService` | 入账重试 | 10 字段 | ✅ |
| `RefundService` | 退款全流程 | 10 字段 | ⚠️ 含申请+审批+执行+驳回 |
| `PenaltySettlementService` | 罚扣 | 10 字段 | ⚠️ 含上交+分配两种场景 |
| `RewardSettler` | 系统奖励 | 3 字段 | ✅ 极简 |
| `BalanceService` / `BalanceQueryService` | 余额校验/查询 | 5-7 字段 | ⚠️ 职责重叠 |
| `SettlementCheckService` | 对账 | 5 字段 | ✅ |
| `RobotChecker` | 机器人识别 | 1 字段 | ✅ 极简 |
| `UserIDConvertService` | ID 转换 | 1 字段 | ✅ 极简 |
| `TraceIDGenerator` | 业务号生成 | 1 字段 | ✅ |

### 8.4 AppService 对象

| AppService | 职责 | 字段数 |
|---|---|---|
| `SettleAppService` | 结算用例 facade + 事务编排 | 6 |
| `RefundAppService` | 退款用例 facade | 1（**死代码**） |
| `SchedulerAppService` | 5 个调度器统一逻辑承载 | 7 |

### 8.5 Repository 对象

| Repository | 方法数 | 跨表操作 |
|---|---|---|
| `BillRepository` | 20 | 无 |
| `RoundSettlementRepository` | 13 | CreateRoundSettlementAndBills（settlement + bills） |
| `RefundAuditRepository` | 9 | UpdateRefundSuccess / CreateRefundAuditAndUpdateBillRefundStatus / RejectRefund（refund_audit + bill_record） |
| `ExceptionRepository` | 1 | 无（仅 Create） |
| `PlatformCallLogRepository` | 4 | 无 |
| `SettlementQueryRepository` | 4 | 无（只读） |

---

## 9. 当前优秀设计（保留理由）

### 9.1 Unit of Work 事务编排模式

**实现**：`DBRepository.WithTransaction(ctx, fn func(tx Transaction) error)` + `Transaction` 接口暴露 6 个子 repo 访问器。子 repo 不感知事务，通过 `m.db` 字段隐式继承事务连接。

**为什么优秀**：
- 事务边界集中在 AppService 层，Repository 不开事务，职责清晰
- 短事务原则：RPC 严格在事务外执行（含 RPC 的用例由 Service 内部对 DB 写入片段开事务）
- 跨表写（refund_audit + bill_record）通过同一 `gormTx` 连接自动纳入事务
- 与 game 模块的 `db_repository.go` 模式一致

**保留理由**：这是资金系统的正确事务模式，重构应延续。

### 9.2 状态机守卫方法（domain 层）

**实现**：每个聚合根提供 `CanXxx()` / `IsTerminalStatus()` 守卫方法，状态机注释明确。

**为什么优秀**：将状态转换规则内聚到聚合根，避免散落在 Service 层的 if-else。

**保留理由**：DDD 状态机守卫是最佳实践。

### 9.3 fail-closed 资金安全模式

**实现**：三道防线
1. `IsRobot` 返回 error → 中止，不调 RPC、不调 VirtualBalance、不标 Success
2. `ParseAmount` 失败 → 标 Failed + 创建 ExceptionRecord（"平台可能已扣款/入账但余额无法解析，需人工对账"）
3. `robotChecker == nil` → error

**为什么优秀**：资金系统宁可失败重试，不可错记。fail-closed 是正确选择。

**保留理由**：这是资金安全的基石，绝不可放松。

### 9.4 确定性业务号生成

**实现**：`TraceIDGenerator` 的 BizOrderNo/RefundOrderNo/ExceptionNo 等均为确定性生成（相同输入相同输出），仅 BatchID/ReconcileNo 用随机。

**为什么优秀**：支持幂等重试——重试时生成相同 BizOrderNo，平台侧 BizID 去重。

**保留理由**：幂等设计的核心。

### 9.5 双层 domain / model 设计

**实现**：`domain.BillRecord`（纯领域，无 GORM tag）+ `model.BillRecord`（GORM 持久化）。

**为什么优秀**：领域逻辑与持久化解耦，符合 DDD。

**保留理由**：设计意图正确（**但当前实现存在脱节问题，见 §10.1**）。

### 9.6 机器人虚拟余额 Lua 原子化

**实现**：`luaDeductBalance`（INCRBY + 余额检查 + 回滚 + SADD dirty）+ `luaCreditBalance`（INCRBY + SADD dirty），消除原 Go 三步竞态。

**为什么优秀**：Lua 单线程原子执行，消除并发扣减凭空创造资金的竞态。

**保留理由**：资金安全的必要保障。

### 9.7 CQRS 读模型分离

**实现**：`SettlementQueryRepository` 专门承载按会话聚合的只读查询（AggregateBet/AggregatePayOut/IsPlayerGameSettled/GetUnsettledUsers），与 BillRepository 的 CRUD 职责分离。

**为什么优秀**：读写模型分离，查询不污染聚合 Repository。

**保留理由**：良好的职责划分。

### 9.8 乐观锁 + 终态保护

**实现**：所有状态机 UPDATE 含 `WHERE status = fromStatus` 或 `WHERE status != Credited`；`RowsAffected == 0` 视为幂等成功。

**为什么优秀**：数据库层防并发覆盖 + 防终态覆盖。

**保留理由**：并发安全的基石。

### 9.9 Processing 中间态 + 失败回退

**实现**：含 RPC 的用例（Refund、GameSettle）在 RPC 前置 Processing，RPC 失败回退（Refund 回退 Pending 供重试；GameSettle 回退 None）。

**为什么优秀**：RPC-DB 一致性的标准模式（Processing 中间态 + 最终态）。

**保留理由**：跨系统一致性的正确实践。

### 9.10 依赖倒置解除反向依赖

**实现**：`domain.UserService` / `domain.RobotAccountStore` 接口由 game 层 adapter 实现，避免 settlement → game/model 反向依赖。

**为什么优秀**：符合依赖倒置原则，模块边界清晰。

**保留理由**：模块化解耦的正确方式。

### 9.11 调度器薄壳 + 逻辑下沉

**实现**：5 个 scheduler 文件均为薄壳（仅持有 base + schedulerApp + 配置），所有循环/错误处理/日志在 `SchedulerAppService` 中。

**为什么优秀**：调度器只关心"何时执行"，业务逻辑集中在可测试的 AppService。

**保留理由**：职责分离清晰。

### 9.12 批量扣款并发控制

**实现**：`executeBatchDeduct` 用信号量（`chan struct{}`，上限 20）+ WaitGroup + Mutex + atomic（panicCount）+ recover + debug.Stack。

**为什么优秀**：并发限流 + panic 隔离 + 可观测。

**保留理由**：高并发批量扣款的稳健实现。

---

## 10. 当前存在问题（原因+风险+建议）

### 10.1 domain 聚合根与 Repository 契约脱节（P0 架构问题）

**当前实现**：所有 6 个 Repository 接口的方法签名引用 `model.*` 类型（如 `BillRepository.CreateBill(ctx, bill *model.BillRecord)`），**不是** `domain.*` 类型。domain 聚合根的 `CanRefund`/`CanSettle` 等守卫方法在代码中**实际未被调用**（调用路径不可见）。

**为什么不好**：
- domain 层定义了聚合根但 Repository 不使用，domain 层形同虚设（贫血 + 死代码）
- domain ↔ model 的转换逻辑不存在，双层设计未落地
- 守卫方法（状态机校验）未被调用，状态转换实际仅靠 DB 乐观锁 WHERE 条件保证

**风险**：
- 状态机守卫失效：Service 层可能在错误状态下调用 Repository（虽然 DB 层有兜底）
- 维护成本：两套同名字段类型，开发者困惑该用哪个

**改进方案**：二选一
- 方案 A（推荐）：Repository 接口改用 `domain.*` 类型，Repository 实现层负责 domain ↔ model 转换，Service 层调用聚合根守卫方法
- 方案 B：删除 domain 聚合根，直接用 model（放弃 DDD 双层，简化）

**改进收益**：消除死代码 + 状态机守卫生效 + 架构意图落地

### 10.2 RefundAppService 是死代码（P1）

**当前实现**：`RefundAppService` 的 5 个方法（ApplyForRefund/ApproveRefund/RejectRefund/GetRefundAuditByOrderNo/GetRefundsByStatus）在整个 backend 中**无任何调用方**。`NewRefundAppService` 构造函数也未被调用。退款逻辑实际通过：
- `SchedulerAppService.ProcessPendingRefunds`（直接持有 `refundSvc`）
- `SettlementCheckService`（直接持有 `refundSvc`）

**为什么不好**：与 `doc.go` 声称的"外部调用方应通过 RefundAppService facade 调用"不一致，文档与代码矛盾。

**风险**：维护成本 + 误导开发者

**改进方案**：删除 `RefundAppService`（含 `refund_app_service.go`），更新 `doc.go`。

**改进收益**：消除死代码 + 文档与代码一致

### 10.3 枚举定义位置不统一（P1）

**当前实现**：
- Bill/Round/Refund 的状态枚举：`domain` 层（untyped int）
- Exception 的枚举类型（ExceptionType/ExceptionStatus/HandleType）：`model` 层，domain 层 type alias 重导出
- PlatformCallLog 的状态/类型常量：`model` 层，domain 层**无**重导出
- `dto/constants.go` 又从 domain 重导出全部（兼容垫片）

**为什么不好**：枚举定义分散在 3 个包，同一类概念有 3 个引用路径（domain.X / dto.X / model.X），开发者困惑。

**风险**：引用混乱 + 修改遗漏

**改进方案**：所有业务枚举统一定义在 `domain` 层，model/dto 层仅引用不定义；删除 dto 兼容垫片。

**改进收益**：单一事实来源

### 10.4 事务超时硬编码 30s（P1）

**当前实现**：`dbRepositoryImpl.WithTransaction` 硬编码 `context.WithTimeout(ctx, 30*time.Second)`。

**为什么不好**：违反 CODING_STANDARD 规约（事务超时应通过配置控制，默认 30s）。

**风险**：长事务（如批量扣款）可能超时；短事务无法优化。

**改进方案**：通过配置注入超时（`common/config` 增加 `TransactionTimeout` 字段）。

**改进收益**：可配置 + 可优化

### 10.5 BalanceService 跨模块直接依赖 game/domain/room（P1）

**当前实现**：`BalanceService.CheckBalanceForReady` 调用 `game/domain/room.CalculateRequiredFee(req.RoomFee, req.MaxPlayers, req.MaxRounds)`。

**为什么不好**：settlement → game/domain 的直接编译依赖，破坏模块边界（其他跨模块依赖已通过接口倒置解除）。

**风险**：模块耦合 + settlement 无法独立测试

**改进方案**：在 `domain` 层定义 `FeeCalculator` 接口，由 game 层 adapter 实现。

**改进收益**：模块边界完整 + 可独立测试

### 10.6 RoundSettlement 状态机存在未使用的中间态（P1）

**当前实现**：domain 定义了 `RoundStatusSettling(2)`、`RoundStatusSuccess(3)`、`RoundStatusPartial(4)`，但 `creditRound` 实际**直接从 Deducted 跳到 Credited**，未经过 Settling/Success/Partial。

**为什么不好**：状态机定义与实现不一致，定义了 3 个实际未使用的状态。

**风险**：误导开发者以为存在中间流程

**改进方案**：删除未使用的 Settling/Success/Partial 状态，或实现完整的中间态流程。

**改进收益**：状态机与实现一致

### 10.7 PenaltySettlementService.DeductPenaltyToPlatform 无 Redis 锁（P2）

**当前实现**：与 DeductService 各扣款方法（均有 Redis 锁 + 双重检查）不同，`DeductPenaltyToPlatform` **无 Redis 锁**，仅靠幂等状态检查（GetBillByTraceTypeAndUser）+ DB 唯一索引兜底。

**为什么不好**：并发违规场景下可能重复创建 bill（虽然 DB 唯一索引兜底，但会产生 error 日志噪声）。

**风险**：可观测性噪声 + 与其他扣款方法不一致

**改进方案**：补充 Redis 锁（PenaltyDeductLockKey）+ 双重检查，与其他扣款方法对齐。

**改进收益**：一致性强 + 噪声减少

### 10.8 json.Marshal 错误被忽略（P2）

**当前实现**：`PlatformCallLogRepositoryImpl.CreateLog/UpdateLog` 中 `reqBody, _ := json.Marshal(params.ReqBody)` 忽略 marshal 错误。

**为什么不好**：理论上 ReqBody 应可序列化，但忽略 error 是代码异味，违反 CODING_STANDARD。

**风险**：静默丢失审计日志内容

**改进方案**：检查 marshal error，失败时 logger.Warn + 用占位字符串。

**改进收益**：审计日志完整性

### 10.9 SyncToDB 的 SAdd 不检查 error（P2）

**当前实现**：`VirtualBalanceRepository.SyncToDB` 失败重试路径的 `s.redis.SAdd(ctx, dirtyKey, member)` 不检查返回 error（fire-and-forget）。

**为什么不好**：极小概率丢失脏标记，导致该 userID 余额不同步。

**风险**：低概率数据不一致

**改进方案**：检查 SAdd error，失败 logger.Error。

**改进收益**：数据一致性保障

### 10.10 IncrementRetryCountWithNextRetryTime 无乐观锁（P2）

**当前实现**：`billRepository.IncrementRetryCountWithNextRetryTime` 仅 `WHERE id=?`，无乐观锁条件，不检查 RowsAffected。

**为什么不好**：并发重试可能多次自增 retry_count（虽然 gorm.Expr 原子自增防覆盖，但不防重复触发）。

**风险**：retry_count 可能虚高，提前触发超限异常

**改进方案**：补充 `WHERE id=? AND status IN (Processing, Failed)` 条件。

**改进收益**：retry_count 准确性

### 10.11 BalanceService 与 BalanceQueryService 职责重叠（P2）

**当前实现**：两个 Service 都依赖 platform.Client/userIDConvert/robotChecker/virtualBalance，都提供余额查询。差异仅在 BalanceService 多了 `CheckBalanceForReady`（含 required_fee 计算）。

**为什么不好**：职责重叠 + 命名易混（`virtualBalance` vs `virtualBalanceSvc` 字段名还不一致）。

**风险**：维护成本 + 字段命名不一致

**改进方案**：合并为单一 BalanceService，或明确拆分（Query 专查 / Business 专校验）并统一字段命名。

**改进收益**：职责清晰

### 10.12 RefundService 职责过载（P2）

**当前实现**：`RefundService` 承载申请（ApplyForRefund）+ 审批执行（ApproveRefund/executeRefund）+ 驳回（RejectRefund）+ 查询，10 个字段。

**为什么不好**：单 Service 职责过多，违反 SRP。

**风险**：维护成本 + 测试复杂

**改进方案**：拆分为 RefundApplyService（申请）+ RefundExecuteService（审批执行）+ RefundQueryService（查询）。

**改进收益**：SRP + 可测试性

### 10.13 PlatformCallLog 无 domain 层抽象（P3）

**当前实现**：`PlatformCallLog` 是唯一没有对应 domain 聚合根/实体的持久化模型，仅在 Repository 接口与 DTO 参数中出现。

**为什么不好**：与其他 4 个聚合根的设计不一致。

**风险**：架构不一致

**改进方案**：补 domain 层抽象，或明确归类为"审计日志"不纳入聚合根。

**改进收益**：架构一致性

### 10.14 ExceptionRepository 仅实现 Create（P3）

**当前实现**：`ExceptionRepository` 接口只有 1 个方法（Create），无查询/更新/处理方法。异常记录的处理流程在代码中不可见。

**为什么不好**：异常记录创建后无系统化处理路径，依赖人工 DB 操作。

**风险**：异常记录堆积无人处理

**改进方案**：补全查询/处理方法 + 提供 admin 处理接口（若业务需要）。

**改进收益**：异常闭环可处理

### 10.15 SettlementCheckService limit 硬编码 100（P3）

**当前实现**：`CheckFirstRoundDeductFailure` / `CheckDeductedButNotSettled` 硬编码 `limit=100`。

**为什么不好**：违反规约（调度器参数应通过配置）。

**改进方案**：通过配置注入。

**改进收益**：可配置

---

## 11. Code Smell 清单

| # | 类型 | 位置 | 说明 | 可否删除 | 影响 |
|---|---|---|---|---|---|
| CS1 | 死代码 | `application/refund_app_service.go` 整个文件 | `RefundAppService` 5 方法 + 构造函数无调用方 | ✅ 删除 | 无（退款逻辑已由 SchedulerAppService/SettlementCheckService 承载） |
| CS2 | 死状态值 | `domain/refund.go:RefundStatusApproved=2` | 定义但状态流转图与守卫方法未引用（实际由 UpdateRefundAuditStatus 设置后立即进入 executeRefund） | ⚠️ 谨慎 | Approved 实际被用作中间态，不可删；但应在状态机注释中明确 |
| CS3 | 未使用状态 | `domain/round_settlement.go:RoundStatusSettling(2)/Success(3)/Partial(4)` | 定义但 creditRound 直接 Deducted→Credited，未经过这些态 | ⚠️ 删除或实现 | 删除需确认无外部引用 |
| CS4 | 兼容垫片 | `dto/constants.go` 全部重导出别名 | 注释明确"兼容别名"，新代码应直接引用 domain | ✅ 迁移后删除 | 需先迁移所有调用方 |
| CS5 | 无意义接口 | `domain/repository/ExceptionRepository` 仅 1 方法 | 过小接口 + 实现也仅 Create | ⚠️ 补全或合并 | 见 §10.14 |
| CS6 | 文件名不一致 | `model/platform_settle_log.go` | 结构体 `PlatformCallLog`，表名 `platform_call_log`，文件名却是 `platform_settle_log` | ✅ 重命名 | 影响小（仅文件名） |
| CS7 | 字段命名不一致 | `BalanceQueryService.virtualBalance` vs `BalanceService.virtualBalanceSvc` | 同一概念两种命名 | ✅ 统一 | 影响小 |
| CS8 | 构造函数返回类型不一致 | `NewBillRepository` 返回接口 vs `NewExceptionRepository`/`NewPlatformCallLogRepository` 返回具体类型 | 不一致 | ✅ 统一返回接口 | 影响小 |
| CS9 | json tag 策略不一致 | `dto/response.go` 部分 DTO 有 tag 部分无 | `FirstRoundDeductResult`/`FailedPlayerInfo`/`RefundResult`/`BillQueryResult` 无 tag | ✅ 补全或明确 | 影响小 |
| CS10 | 吞错 | `CreditRetryService.doRetryCredit` 的 `IncrementRetryCountWithNextRetryTime` 失败仅 logger.Error | 吞错但保留原 err 返回 | ⚠️ 保留 | 设计权衡（重试退避失败不影响本次结果） |
| CS11 | 吞错 | `GameSettleReportingService.SettleGame` 多处 UpdateGameSettleStatusBySession 失败仅 Error | 状态更新失败不中断 | ⚠️ 保留 | 设计权衡（尽力而为） |
| CS12 | 吞错 | `callMgr.CreateLog` 失败仅 Warn | 审计日志失败不中断业务 | ⚠️ 保留 | 设计权衡（审计不应阻塞业务） |
| CS13 | 魔法数字 | `db_repository.go:30 * time.Second` | 事务超时硬编码 | ✅ 配置化 | 见 §10.4 |
| CS14 | 魔法数字 | `settlement_check_service.go:100` | limit 硬编码 | ✅ 配置化 | 见 §10.15 |
| CS15 | 魔法字符串 | `game_settle_reporting_service.go:265` `Multiplier: "1"` | 硬编码倍率 | ✅ 常量化 | 影响小 |
| CS16 | 魔法数字 | `deduct_service.go:23` `defaultMaxConcurrentDeduct = 20` | 并发上限 | ✅ 配置化 | 影响小 |
| CS17 | 业务逻辑泄漏到 Repository | `billRepository.UpdateGameSettleStatusByUser` 当 toStatus=Settled 时附 game_settled_at | 状态分支在仓储层 | ⚠️ 轻度 | 可接受，但建议上移到 Service |
| CS18 | 业务逻辑泄漏到 Repository | `roundSettlementRepository.UpdateRoundSettlementCredited` 把 settle_user_count 写入 settle_success_count | 业务等价假设在仓储层 | ⚠️ 轻度 | 可接受 |
| CS19 | 业务逻辑泄漏到 Repository | `PlatformCallLogRepositoryImpl.UpdateLog` 用 CASE WHEN 实现 retry_count 自增 | 业务语义在仓储层 | ⚠️ 轻度 | 可接受 |
| CS20 | 业务逻辑泄漏到 Repository | `settlementQueryRepository` 在 SQL 用 `amount<0`/`amount>0` 分类 | 金额符号语义在仓储层 | ⚠️ 轻度 | 可接受 |
| CS21 | 无事务回退日志 | `GameSettleReportingService.settlePlayer` RPC 失败回退 `UpdateGameSettleStatusByUser` 用 `_ =` 忽略 error | 回退失败不可见 | ✅ 补日志 | 影响中 |
| CS22 | 迁移文件并存 | `migrations/add_bill_record_compound_unique_index.sql` (v1) + `_v2.sql` (v2) | v2 DROP v1 的索引 | ⚠️ 保留 | v2 用 IF EXISTS 幂等，可保留 |
| CS23 | 测试硬编码 key | `virtual_balance_test.go` 硬编码 `testBalanceKey="cashparty:robot:virtual_balance:user1"` | 与生产用 `converter.FormatID(userID)` 不同 | ⚠️ 保留 | 测试专用，可接受 |
| CS24 | round_settle_service_test.go 空玩家仍标记 Credited | `TestSettleRound_EmptyPlayerList` | Players=[] 时 settleUserCount=0 但仍 Credited | ⚠️ 需确认业务 | 可能是预期行为（空回合也算结算完成） |

---

## 12. 可删除代码清单（附理由与影响分析）

| # | 代码 | 位置 | 删除理由 | 影响范围 | 删除收益 |
|---|---|---|---|---|---|
| D1 | `RefundAppService` 整个文件 | `application/refund_app_service.go` | `NewRefundAppService` 与 5 方法均无调用方，退款逻辑已由 SchedulerAppService/SettlementCheckService 承载 | 无业务影响；需同步更新 `application/doc.go` 移除 RefundAppService 描述 | 消除死代码 + 文档与代码一致 |
| D2 | `dto/constants.go` 重导出别名 | `dto/constants.go`（保留 TraceType/PlatformAccountID/MaxRetryCount/PenaltyRoundID/CreditRetryBaseDelay/MaxDelay） | 注释明确"兼容垫片"，新代码应直接引用 domain | 需先迁移所有 `dto.BillType*`/`dto.BillStatus*` 等引用到 `domain.*` | 单一事实来源 |
| D3 | `RoundStatusSettling/Success/Partial`（待确认） | `domain/round_settlement.go` | 定义但 creditRound 未使用 | 需 grep 确认无外部引用 | 状态机与实现一致 |
| D4 | `model/platform_settle_log.go` 文件名（重命名） | `model/platform_settle_log.go` → `model/platform_call_log.go` | 文件名与结构体/表名不一致 | 仅文件名变更 | 命名一致 |

**注意**：D3 需先全局 grep 确认无引用。其余 D1/D2/D4 经分析确认安全。

---

## 13. 重构目标

### 13.1 为什么重构

当前 Settlement 模块**整体设计质量较高**（Unit of Work / 状态机守卫 / fail-closed / 确定性业务号 / Lua 原子化等优秀设计已落地），但存在以下问题需要重构解决：

1. **架构意图未落地**：domain 双层设计存在但 Repository 契约脱节，domain 聚合根守卫方法未被调用
2. **死代码与兼容垫片**：RefundAppService 死代码、dto/constants.go 兼容垫片
3. **不一致**：枚举位置、构造函数返回类型、字段命名、json tag 策略
4. **硬编码**：事务超时、limit、并发上限、Multiplier
5. **模块边界破洞**：BalanceService 直接依赖 game/domain/room
6. **状态机与实现脱节**：RoundSettlement 中间态未使用
7. **职责过载**：RefundService 承载 4 类操作

### 13.2 最终希望达到的效果

- domain 双层设计真正落地，聚合根守卫方法生效
- 零死代码、零兼容垫片
- 枚举/命名/返回类型/tag 策略全局统一
- 所有可配置参数通过配置注入
- settlement 模块对外零 game 编译依赖（完全接口倒置）
- 状态机定义与实现一致
- Service 职责单一，可独立测试

---

## 14. 推荐整体架构

延续当前分层架构，强化 domain 层落地：

```
┌─────────────────────────────────────────────────────────┐
│  game/application（调用方）                              │
│    ├─ game_event_handler（Kafka 触发）                   │
│    ├─ packet_round_init / penalty_service / ...         │
│    └─ generic_service（gRPC）                           │
└────────────────────────┬────────────────────────────────┘
                         │ 通过 AppService facade
┌────────────────────────▼────────────────────────────────┐
│  settlement/application（用例编排层 / AppService facade）│
│    ├─ SettleAppService（事务编排）                       │
│    └─ SchedulerAppService（调度器逻辑）                 │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│  settlement/service（领域服务层 / 业务逻辑）             │
│    ├─ deduct / round_settle / game_settle / payout      │
│    ├─ credit_retry / refund / penalty / reward          │
│    ├─ balance / settlement_check                        │
│    └─ robot_checker / user_id_convert / trace_id_gen    │
└────────────────────────┬────────────────────────────────┘
                         │ 依赖 domain 接口
┌────────────────────────▼────────────────────────────────┐
│  settlement/domain（领域层 / 聚合根 + 接口）             │
│    ├─ 聚合根（BillRecord/RoundSettlement/RefundAudit/   │
│    │         ExceptionRecord）含守卫方法                │
│    ├─ 领域服务接口（UserService/VirtualBalanceService/  │
│    │               RobotAccountStore/FeeCalculator）    │
│    ├─ 仓储接口（Repository + Transaction/DBRepository） │
│    └─ 枚举（统一定义，单一事实来源）                     │
└────────────────────────┬────────────────────────────────┘
                         │ 实现
┌────────────────────────▼────────────────────────────────┐
│  settlement/infrastructure（基础设施层）                 │
│    ├─ persistence/mysql（Repository 实现 + Unit of Work）│
│    └─ persistence/redis（VirtualBalanceRepository + Lua）│
└────────────────────────┬────────────────────────────────┘
                         │ 依赖
┌────────────────────────▼────────────────────────────────┐
│  common / api/platform（通用基础设施 + 平台 RPC）        │
└─────────────────────────────────────────────────────────┘
```

依赖方向：game → application → service → domain ← infrastructure → common/platform

**关键原则**：
- domain 层零外部依赖（仅标准库 + 自身）
- service 层依赖 domain 接口，不依赖 infrastructure 具体实现
- infrastructure 实现 domain 接口（依赖倒置）
- settlement 对 game 零编译依赖（通过 domain 接口倒置）

---

## 15. 推荐目录结构

```
settlement/
├── application/
│   ├── doc.go
│   ├── settle_app_service.go
│   └── scheduler_app_service.go        # （删除 refund_app_service.go）
├── config/
│   └── config.go                       # 增加 TransactionTimeout / DeductConcurrency 等
├── domain/
│   ├── bill.go                         # 聚合根 + 枚举（统一定义）
│   ├── round_settlement.go             # 删除未使用中间态（或实现）
│   ├── refund.go
│   ├── exception.go                    # 枚举从 model 迁入此处
│   ├── platform_call_log.go            # 新增 domain 抽象（或明确归类审计）
│   ├── errors.go
│   ├── platform_user.go
│   ├── fee_calculator.go               # 新增接口（解除 game/domain/room 依赖）
│   ├── user_service.go
│   ├── virtual_balance_service.go
│   └── repository/
│       ├── transaction.go
│       ├── bill_repository.go          # 接口签名改用 domain.* 类型
│       ├── round_settlement_repository.go
│       ├── refund_audit_repository.go
│       ├── exception_repository.go     # 补全查询/处理方法（若业务需要）
│       ├── platform_call_log_repository.go
│       └── settlement_query_repository.go
├── dto/
│   ├── platform_call.go
│   ├── request.go
│   └── response.go                     # （删除 constants.go，迁移到 domain）
├── model/
│   ├── bill.go
│   ├── exception_record.go             # 枚举类型定义移除（迁入 domain）
│   ├── platform_call_log.go            # 重命名（原 platform_settle_log.go）
│   └── refund.go
├── service/
│   └── （同现状，可选拆分 RefundService）
├── scheduler/
│   └── （同现状）
└── infrastructure/
    └── persistence/
        ├── mysql/
        │   ├── db_repository.go        # 事务超时配置化
        │   └── （其余同现状）
        └── redis/
            └── （同现状）
```

---

## 16. 推荐模块划分

### 16.1 业务域划分（7 域，延续现状）

| 业务域 | Service | 聚合根 |
|---|---|---|
| 扣款域 | DeductService | BillRecord, RoundSettlement |
| 结算域 | RoundSettleService, RewardSettler | RoundSettlement, BillRecord |
| 派奖域 | SessionPayoutService | BillRecord |
| 罚款域 | PenaltySettlementService | BillRecord |
| 退款域 | RefundService（建议拆分） | RefundAudit, BillRecord |
| 重试对账域 | CreditRetryService, SettlementCheckService | BillRecord, ExceptionRecord |
| 查询域 | BalanceService, BalanceQueryService（建议合并） | — |

### 16.2 基础设施域

| 子域 | 组件 |
|---|---|
| 机器人支持 | RobotChecker, VirtualBalanceService, UserIDConvertService |
| 业务号生成 | TraceIDGenerator |
| 审计 | PlatformCallLogRepository |

---

## 17. 推荐对象设计

### 17.1 聚合根（强化守卫方法调用）

```go
// domain/bill.go
type BillRecord struct { /* 同现状 */ }

func (b *BillRecord) CanRefund() bool { return b.Status == BillStatusSuccess }
func (b *BillRecord) IsTerminalStatus() bool { return b.Status == BillStatusRefunded }

// 新增：状态转换方法（返回 error，由 Service 调用）
func (b *BillRecord) TransitionTo(newStatus int) error {
    switch b.Status {
    case BillStatusProcessing:
        if newStatus == BillStatusSuccess || newStatus == BillStatusFailed { /* ok */ }
        else { return ErrInvalidBillStatus }
    case BillStatusSuccess:
        if newStatus == BillStatusRefunded { /* ok */ }
        else { return ErrInvalidBillStatus }
    default:
        return ErrInvalidBillStatus
    }
    return nil
}
```

**Service 层调用**：
```go
bill, _ := billRepo.GetBillByID(ctx, billID)
if err := bill.TransitionTo(BillStatusFailed); err != nil { return err }
billRepo.UpdateBillStatus(ctx, bill.ID, bill.Status, BillStatusFailed, errMsg)
```

### 17.2 Repository 接口（改用 domain 类型）

```go
// domain/repository/bill_repository.go
type BillRepository interface {
    CreateBill(ctx context.Context, bill *domain.BillRecord) error
    UpdateBillStatus(ctx context.Context, billID int64, fromStatus, toStatus int, errMsg string) error
    GetBillByID(ctx context.Context, billID int64) (*domain.BillRecord, error)
    // ... 其余方法签名改用 domain.BillRecord
}
```

**Repository 实现层负责 domain ↔ model 转换**：
```go
// infrastructure/persistence/mysql/bill_repository.go
func (m *billRepository) GetBillByID(ctx context.Context, billID int64) (*domain.BillRecord, error) {
    var m2 model.BillRecord
    if err := m.db.WithContext(ctx).First(&m2, billID).Error; err != nil { return nil, err }
    return billModelToDomain(&m2), nil
}
```

### 17.3 FeeCalculator 接口（解除 game 依赖）

```go
// domain/fee_calculator.go
type FeeCalculator interface {
    CalculateRequiredFee(roomFee int64, maxPlayers int, maxRounds int) int64
}
```

game 层提供 adapter 实现，BalanceService 依赖此接口。

### 17.4 退款服务拆分（可选）

```go
// service/refund_apply_service.go
type RefundApplyService struct { /* ApplyForRefund */ }

// service/refund_execute_service.go
type RefundExecuteService struct { /* ApproveRefund/executeRefund/RejectRefund */ }

// service/refund_query_service.go
type RefundQueryService struct { /* GetRefundAuditByOrderNo/GetRefundsByStatus */ }
```

---

## 18. 推荐业务流程

### 18.1 流程优化原则

- **保持业务逻辑不变**（核心要求）
- 强化聚合根守卫方法在 Service 层的调用
- 状态转换显式化（TransitionTo + error）
- 移除未使用的状态流转

### 18.2 优化后的扣款流程（示例）

```
executeSingleDeduct:
  ├─ billRepo.GetBillByID → bill（domain.BillRecord）
  ├─ bill.TransitionTo(Processing) → error 则返回（守卫生效）
  ├─ billRepo.UpdateBillStatus(bill.Status→Processing)
  ├─ robotChecker.IsRobot
  │   ├─ [机器人] virtualBalance.Deduct
  │   └─ [真人] platform.Debit
  ├─ ParseAmount fail-closed
  ├─ bill.TransitionTo(Success/Failed) → error 则返回
  └─ billRepo.UpdateBillSuccess/UpdateBillStatus
```

差异：增加 `TransitionTo` 守卫调用（业务逻辑不变，仅增加前置校验）。

### 18.3 优化后的整局结算流程

保持现状（F10），仅补全：
- 状态更新失败时记录回退日志（CS21）
- Multiplier 常量化（CS15）

---

## 19. 分阶段重构计划（Phase 1 ~ Phase N）

### Phase 1：死代码与一致性清理（低风险）

**目标**：消除死代码与明显不一致，零业务逻辑变更。

| 任务 | 文件 | 风险 |
|---|---|---|
| 删除 RefundAppService | application/refund_app_service.go + doc.go | 低（无调用方） |
| 重命名 platform_settle_log.go → platform_call_log.go | model/ | 低（仅文件名） |
| 统一 BalanceService/BalanceQueryService 字段命名（virtualBalanceSvc → virtualBalance） | service/balance*.go | 低 |
| 统一 NewExceptionRepository/NewPlatformCallLogRepository 返回接口 | infrastructure/ | 低 |
| 补全 dto/response.go json tag | dto/response.go | 低 |
| Multiplier 常量化 | service/game_settle_reporting_service.go | 低 |

**验收**：编译通过 + 全部测试通过 + 无业务行为变更。

### Phase 2：枚举统一与兼容垫片移除（中风险）

**目标**：枚举单一事实来源，移除 dto 兼容垫片。

| 任务 | 文件 | 风险 |
|---|---|---|
| Exception 枚举从 model 迁入 domain（type alias 反转） | domain/exception.go, model/exception_record.go | 中（需迁移引用） |
| PlatformCallLog 状态常量迁入 domain | domain/, model/ | 中 |
| dto/constants.go 中的重导出别名迁移到 domain 直接引用 | 全模块 dto.X → domain.X | 中（大量引用替换） |
| 删除 dto/constants.go 中的重导出部分（保留 TraceType 等特有常量） | dto/constants.go | 中 |

**验收**：编译通过 + 测试通过 + grep 确认无残留 dto.BillType* 引用。

### Phase 3：配置化硬编码（低风险）

**目标**：所有可配置参数通过配置注入。

| 任务 | 配置键 | 文件 |
|---|---|---|
| 事务超时 | settlement.transaction_timeout（默认 30s） | infrastructure/mysql/db_repository.go |
| 对账 limit | settlement.settlement_check.limit（默认 100） | service/settlement_check_service.go |
| 批量扣款并发上限 | settlement.deduct.max_concurrent（默认 20） | service/deduct_service.go |
| CreditRetry 退避参数 | settlement.credit_retry.{base_delay,max_delay,max_count}（已有部分） | service/credit_retry_service.go |

**验收**：配置可覆盖默认值 + 测试通过。

### Phase 4：模块边界修复（中风险）

**目标**：settlement 对 game 零编译依赖。

| 任务 | 文件 | 风险 |
|---|---|---|
| 新增 domain.FeeCalculator 接口 | domain/fee_calculator.go | 中 |
| game 层提供 FeeCalculator adapter 实现 | game/infrastructure/adapter/ | 中 |
| BalanceService 依赖 FeeCalculator 接口替代 game/domain/room | service/balance_service.go | 中 |

**验收**：settlement 包 import 无 game/ 路径 + 测试通过。

### Phase 5：状态机一致性（中风险）

**目标**：状态机定义与实现一致。

| 任务 | 文件 | 风险 |
|---|---|---|
| grep 确认 RoundStatusSettling/Success/Partial 无引用 → 删除；或实现中间态 | domain/round_settlement.go | 中（需确认） |
| 更新 domain/refund.go 状态机注释明确 Approved(2) 是过渡态 | domain/refund.go | 低 |

**验收**：状态机注释与代码一致 + 测试通过。

### Phase 6：domain 双层落地（高风险，可选）

**目标**：Repository 契约改用 domain 类型，聚合根守卫方法生效。

| 任务 | 文件 | 风险 |
|---|---|---|
| 所有 Repository 接口签名 model.* → domain.* | domain/repository/*.go | 高（大范围签名变更） |
| Repository 实现层增加 domain ↔ model 转换函数 | infrastructure/persistence/mysql/*.go | 高 |
| Service 层调用聚合根守卫方法 | service/*.go | 高 |
| 移除 model 层重复的业务枚举（仅保留 GORM tag） | model/*.go | 中 |

**风险控制**：
- 分子任务逐步迁移（先 BillRepository，再 RoundSettlement，...）
- 每步编译 + 测试通过再进行下一步
- 保留 model 层的 TableName() 与 GORM tag

**验收**：domain 聚合根守卫方法被调用 + Repository 契约用 domain 类型 + 测试通过。

### Phase 7：职责拆分与补全（中风险，可选）

**目标**：SRP 优化 + 异常闭环。

| 任务 | 风险 |
|---|---|
| 拆分 RefundService 为 Apply/Execute/Query 三 Service | 中 |
| 合并 BalanceService + BalanceQueryService（或明确拆分） | 中 |
| 补全 ExceptionRepository 查询/处理方法 | 中（需确认业务需求） |
| 补充 PenaltySettlementService.DeductPenaltyToPlatform 的 Redis 锁 | 低 |
| 补充 json.Marshal/SAdd error 检查 | 低 |
| 补充 IncrementRetryCountWithNextRetryTime 乐观锁条件 | 低 |
| 补充 settlePlayer 回退日志 | 低 |

**验收**：各 Service 单一职责 + 测试通过。

---

## 20. 重构风险评估

| 风险项 | 等级 | 影响 | 缓解措施 |
|---|---|---|---|
| domain 双层落地（Phase 6）大范围签名变更 | 高 | 编译错误 + 转换函数遗漏 | 分子任务逐步迁移 + 每步测试 + 保留 model |
| 枚举迁移引用遗漏 | 中 | 编译错误 | grep 全模块 + 编译验证 |
| 状态机删除未使用态误删 | 中 | 运行时错误 | grep 确认无引用 + 保留兜底 |
| 退款服务拆分影响 SchedulerAppService | 中 | 调度器逻辑变更 | 拆分后 SchedulerAppService 调整注入 |
| 配置化默认值与原硬编码不一致 | 低 | 行为变更 | 默认值与原值对齐 + 测试 |
| 模块边界修复影响 BalanceService | 中 | required_fee 计算变更 | adapter 透传原逻辑 + 测试 |

**总体风险控制原则**：
- 每个 Phase 独立可回滚
- 每个 Phase 完成后全量测试 + 灰度验证
- Phase 6（高风险）建议最后执行，且分子任务逐步推进
- 资金安全相关（fail-closed / 幂等 / 乐观锁）在任何 Phase 都不得放松

---

## 21. 最终建议

### 21.1 整体评价

Settlement 模块**整体设计质量较高**，体现了资金系统的专业素养：
- ✅ Unit of Work 事务编排正确
- ✅ fail-closed 资金安全三道防线到位
- ✅ 确定性业务号 + 乐观锁 + Processing 中间态的幂等设计完整
- ✅ 机器人虚拟余额 Lua 原子化
- ✅ CQRS 读模型分离
- ✅ 依赖倒置（大部分）解除反向依赖
- ✅ 调度器薄壳 + 逻辑下沉
- ✅ 批量扣款并发控制稳健

### 21.2 重构优先级建议

| 优先级 | Phase | 建议 |
|---|---|---|
| P0（立即） | Phase 1 | 死代码与一致性清理，低风险高收益 |
| P1（短期） | Phase 2 + Phase 3 | 枚举统一 + 配置化，中低风险 |
| P2（中期） | Phase 4 + Phase 5 | 模块边界 + 状态机一致性，中风险 |
| P3（长期，可选） | Phase 6 + Phase 7 | domain 落地 + 职责拆分，高风险，需充分测试 |

### 21.3 不可触碰的红线

- **任何 Phase 都不得改变业务逻辑**（核心要求）
- **fail-closed 三道防线不得放松**（IsRobot error 中止 / ParseAmount 失败标 Failed / robotChecker nil 检查）
- **幂等机制不得移除**（BizOrderNo 确定性 / 乐观锁 WHERE / Processing 中间态 + 回退）
- **事务边界不得扩大**（短事务原则，RPC 不在事务内）
- **Lua 原子化不得回退**（虚拟余额操作必须 Lua）
- **删除任何代码前必须 grep 确认无引用 + 分析影响范围**

### 21.4 建议执行顺序

1. 先执行 Phase 1（死代码清理），立即收益
2. 并行推进 Phase 2（枚举统一）与 Phase 3（配置化）
3. 评估 Phase 4（模块边界）的必要性，若 settlement 未来需独立部署则必做
4. Phase 5（状态机一致性）与 Phase 4 一同完成
5. Phase 6（domain 落地）与 Phase 7（职责拆分）作为长期优化，按资源情况推进

### 21.5 长期演进方向

- 若 settlement 未来需独立部署，可基于当前 AppService facade 演进为独立服务（facade 边界已就绪）
- 异常记录处理闭环（ExceptionRepository 补全 + admin 接口）可视业务需求补充
- 对账可考虑接入自动化对账引擎（当前仅检测 + 人工介入）

---

> **本文档所有结论均基于对 Settlement 模块源码的完整逆向阅读，可追溯至具体代码位置。重构方案遵循"不改变业务逻辑"原则，所有建议均说明原因与收益，保留优秀设计，不为重构而重构。**
