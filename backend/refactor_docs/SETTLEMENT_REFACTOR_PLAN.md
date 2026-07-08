# Settlement 模块重构方案

> **文档版本**：v1.0
> **生成日期**：2026-07-08
> **审查范围**：`backend/settlement/` 全模块（domain / application / service / infrastructure / scheduler / dto / model / config）
> **关联文档**：[GAME_SERVICE_ARCHITECTURE_REVIEW.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/refactor_docs/GAME_SERVICE_ARCHITECTURE_REVIEW.md) §6.3、[CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md)
> **核心原则**：**不改变任何业务功能逻辑**，仅按架构最佳实践调整代码结构、补全分层、收敛依赖、消除坏味道。当前功能逻辑在重构前后必须 100% 等价。

---

## 1. 文档目的与定位

本文档是 settlement 模块的**工程级重构方案**，不是重新设计。它的产出基于对 settlement 全模块功能的完整调查（domain 13 文件、service 16 文件、infrastructure、scheduler、application、dto、model、config、bootstrap 接线、调用方），确保重构不会破坏任何既有业务行为。

### 1.1 重构目标

| 目标 | 说明 |
| --- | --- |
| 目录对齐 | 与 [GAME_SERVICE_ARCHITECTURE_REVIEW.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/refactor_docs/GAME_SERVICE_ARCHITECTURE_REVIEW.md) §6.3 推荐目录一致 |
| 分层补全 | 补全 Application 层（缺 RefundAppService）、补全 Repository 抽象（ExceptionManager / PlatformCallManager 持有 `*gorm.DB`） |
| 入口收敛 | 所有外部调用方（game/application 4 处 + 5 个 scheduler）统一通过 AppService facade 访问 settlement 用例 |
| 职责清晰 | Service 层按"单一职责"组织，消除"很多 service 很乱"的体感 |
| 依赖规整 | settlement 不依赖 game/model；game/application 不直接依赖 settlement/service 具体类型 |
| 行为零变更 | 所有业务流程、事务边界、幂等机制、错误处理、状态机在重构前后语义等价 |

### 1.2 非目标（Out of Scope）

- **不**修改任何业务规则（reward 倍率、费用公式、状态机流转条件、幂等键生成逻辑）
- **不**调整数据库表结构、Redis Key、Kafka Topic
- **不**合并/拆分 Service 的业务方法（仅调整归属与命名）
- **不**处理跨模块的 P0 项（P0-4 游戏规则下沉到 game、虚拟余额双写归一）——这些是独立的架构改造，不在本次结构重构范围

---

## 2. 当前架构现状

### 2.1 目录结构现状

```
settlement/
├── application/
│   └── settle_app_service.go          # 唯一的 Application 层文件（109 行）
├── config/
│   └── config.go
├── domain/                            # 扁平结构，未细分 repository/ 子目录
│   ├── bill.go
│   ├── bill_repository.go
│   ├── db_repository.go
│   ├── errors.go
│   ├── exception.go
│   ├── platform_user.go
│   ├── refund.go
│   ├── refund_audit_repository.go
│   ├── round_settlement.go
│   ├── round_settlement_repository.go
│   ├── settlement_query_repository.go
│   ├── user_service.go
│   └── virtual_balance_service.go
├── dto/
│   ├── constants.go
│   ├── request.go
│   └── response.go
├── infrastructure/
│   └── persistence/
│       ├── mysql/                     # 缺 exception_repository、platform_call_log_repository
│       │   ├── bill_repository.go
│       │   ├── db_repository.go
│       │   ├── refund_audit_repository.go
│       │   ├── round_settlement_repository.go
│       │   └── settlement_query_repository.go
│       └── redis/
│           ├── scripts/
│           │   ├── registry.go
│           │   ├── virtual_balance.lua.go
│           │   └── virtual_balance_test.go
│           └── virtual_balance_repository.go
├── model/
│   ├── bill.go
│   ├── exception_record.go
│   ├── platform_settle_log.go
│   └── refund.go
├── scheduler/                         # 5 个 scheduler 全部绕过 AppService
│   ├── credit_retry_scheduler.go
│   ├── game_settle_retry_scheduler.go
│   ├── game_settle_timeout_scheduler.go
│   ├── refund_process_scheduler.go
│   └── settlement_check_scheduler.go
└── service/                           # 16 文件，混合了 Domain Service + 基础设施伪装
    ├── balance_query_service.go
    ├── balance_service.go
    ├── credit_retry_service.go
    ├── deduct_service.go
    ├── exception_manager.go           # 持有 *gorm.DB，应为 Repository
    ├── game_settle_reporting_service.go
    ├── mocks_test.go
    ├── penalty_settlement_service.go
    ├── platform_call_manager.go       # 持有 *gorm.DB，应为 Repository
    ├── refund_service.go
    ├── reward_settler.go
    ├── robot_checker.go
    ├── round_settle_service.go
    ├── session_payout_service.go
    ├── settlement_check_service.go
    ├── trace_id_generator.go
    └── user_id_convert_service.go
```

### 2.2 分层现状评估

| 层 | 现状 | 评估 |
| --- | --- | --- |
| **Domain** | 13 文件，含聚合根、值对象、Repository 接口、errors | 内容完整，但**目录扁平**，未按 §6.3 细分 `repository/` 子目录 |
| **Application** | 仅 1 文件 `settle_app_service.go`（109 行） | **严重缺失**：缺 `RefundAppService`；SettleAppService 是"混合 facade"（3 个真正编排事务的方法 + 6 个纯转发，其中 3 个为死代码） |
| **Service** | 16 文件，3,025 行 | **职责混乱**：2 个文件（exception_manager、platform_call_manager）持有 `*gorm.DB` 是伪装的 Repository；service 之间互相依赖复杂 |
| **Infrastructure** | mysql 5 文件、redis 2 文件 | **不完整**：缺 `exception_repository.go`、`platform_call_log_repository.go`（对应 service 层 2 个伪装者） |
| **Scheduler** | 5 文件 | **全部绕过 AppService**，直接调用具体 Service |
| **DTO** | 3 文件 | 完整 |
| **Model** | 4 文件 | 完整 |

### 2.3 依赖关系现状（问题视图）

```
game/application                    settlement/service                 settlement/infrastructure
┌─────────────────────┐             ┌──────────────────────┐           ┌──────────────────────┐
│ RoomAppService      │──直接依赖──▶│ BalanceQueryService  │           │ mysql/bill_repo      │
│ SeatAppService      │──直接依赖──▶│ BalanceService       │           │ mysql/round_settle   │
│ PenaltyService      │──直接依赖──▶│ PenaltySettlementSvc │           │ mysql/refund_audit   │
│ HistoryService      │──直接依赖──▶│ domain.BillRepository│◀─直接依赖─│ mysql/settlement_qry │
│ GameEventHandler    │──AppService▶│ (SettleAppService)   │           │ redis/virtual_balance│
│ PacketRoundInit     │──AppService▶│ (SettleAppService)   │           └──────────────────────┘
│ GameLifecycleTimeout│──AppService▶│ (SettleAppService)   │                    ▲
└─────────────────────┘             │                      │                    │
                                    │  ExceptionManager ◀──┼── 持有 *gorm.DB ──┘ (违规)
                                    │  PlatformCallMgr  ◀──┼── 持有 *gorm.DB ──┘ (违规)
                                    └──────────────────────┘
                                    ┌──────────────────────┐
                                    │ 5 个 Scheduler       │──全部直接调用 Service（绕过 AppService）
                                    └──────────────────────┘
```

---

## 3. 功能全景（重构保护基线）

> 以下功能清单是重构的**保护基线**。重构后每一项都必须能对应到等价实现。

### 3.1 领域层（domain/）功能清单

| 文件 | 类型 | 核心内容 |
| --- | --- | --- |
| `bill.go` | 聚合根 | `BillRecord` + `BillType`(7 种) + `BillStatus`(4 态) + `DeductScene`(3 种) + 状态机方法 |
| `round_settlement.go` | 聚合根 | `RoundSettlement` + `RoundSettlementStatus`(Deducting→Deducted→Credited) + `GameSettleStatus` |
| `refund.go` | 聚合根 | `RefundAudit` + `RefundStatus`(None→Pending→Approved→Processing→Refunded/Rejected) |
| `exception.go` | 聚合根 | `ExceptionRecord` + `ExceptionStatus`(Pending→Processing→Resolved/Ignored) + `ExceptionType` |
| `platform_user.go` | 值对象 | `PlatformUser{UserID}`（防腐层，解除 settlement→game 依赖） |
| `bill_repository.go` | 接口 | 21 方法：CRUD + 状态机更新（MarkBillSuccess/Failed/Refunded 等）+ 聚合查询 |
| `round_settlement_repository.go` | 接口 | 13 方法：回合结算 CRUD + 状态机 + GameSettleStatus |
| `refund_audit_repository.go` | 接口 | 9 方法：含 4 个跨表原子操作（CreateRefundAuditAndUpdateBill、UpdateRefundSuccess、RejectRefund 等） |
| `settlement_query_repository.go` | 接口 | 4 只读聚合查询方法 |
| `db_repository.go` | 接口 | `DBRepository` + `Transaction`（事务编排抽象，含 9 个子 Repo 访问器） |
| `user_service.go` | 接口 | `UserService.GetUserById`（防腐层接口，由 game 实现） |
| `virtual_balance_service.go` | 接口 | `VirtualBalanceService` + `RobotAccountStore`（机器人虚拟钱包抽象） |
| `errors.go` | 哨兵错误 | 4 个 "not found" + 4 个 "invalid status transition" |

### 3.2 服务层（service/）功能清单

| 文件 | 行数 | 职责 | 事务边界 | 关键幂等机制 |
| --- | --- | --- | --- | --- |
| `deduct_service.go` | 632 | 3 种扣款场景：首回合/后续回合/系统红包 | Service 内 `dbRepo.WithTransaction` 编排 DB 片段（含 RPC，短事务原则） | round_settlement 状态机 + bill BizOrderNo |
| `round_settle_service.go` | 219 | 单局结算：写 grab/commission bill + 触发奖励 + 标记 Credited + 委托 GameSettle | AppService 层 `dbRepo.WithTransaction`（纯 DB） | RoundStatusCredited 跳过 |
| `game_settle_reporting_service.go` | 343 | 会话级结算上报：platform.Settle + session payout | Service 内 `dbRepo.WithTransaction`（含 RPC） | allSettled 幂等短路 + BillStatus 幂等 |
| `session_payout_service.go` | 266 | 会话级派奖：platform.Credit 给真实玩家 | Service 内 `dbRepo.WithTransaction`（含 RPC） | bill BizOrderNo + 状态机 |
| `penalty_settlement_service.go` | 294 | 罚款扣款（Debit 上交平台）+ 罚款分配（批量 CreateBills） | Deduct: Service 内 tx；Distribute: AppService tx | lock + bill BizOrderNo |
| `refund_service.go` | 266 | 退款全流程：Apply/Approve/Reject + executeRefund | Service 内 `dbRepo.WithTransaction`（跨表 refund_audit+bill） | refund_order_no + 状态机 + lock 双重检查 |
| `credit_retry_service.go` | 243 | 重试失败的 Credit bill（指数退避） | Service 内 `dbRepo.WithTransaction` | bill 状态机 + NextRetryAt |
| `settlement_check_service.go` | 125 | 对账扫描：卡住的 RoundSettlement + 失败的 platform call log | Service 内 tx | 状态机 |
| `balance_query_service.go` | 132 | 只读余额查询 + platform.GetBalance RPC | 无事务 | - |
| `balance_service.go` | 97 | 开局余额检查（CalculateRequiredFee） | 无事务 | - |
| `reward_settler.go` | 93 | 系统奖励 bill 创建（纯记账，无资金移动） | 由调用方 tx 包裹 | bill BizOrderNo |
| `robot_checker.go` | 42 | Redis 识别机器人（fail-closed：Redis 错误返回 false 走真实玩家路径） | 无事务 | - |
| `trace_id_generator.go` | 115 | 确定性生成 round_trace_id / biz_order_no / refund_order_no | 无事务 | 确定性格式本身保证幂等 |
| `user_id_convert_service.go` | 40 | 内部 ID → 平台 ID 转换（通过 domain.UserService 防腐层） | 无事务 | - |
| `exception_manager.go` | 20 | **异常记录创建（持有 `*gorm.DB`，违规）** | 无事务 | - |
| `platform_call_manager.go` | 93 | **平台调用审计日志（持有 `*gorm.DB`，违规）** | 无事务 | retry_count CASE 表达式 |

### 3.3 应用层（application/）功能清单

`SettleAppService`（109 行）共 9 个方法：

| 方法 | 类型 | 事务编排 | 调用方 |
| --- | --- | --- | --- |
| `SettleRound` | 真正编排 | AppService 层 `dbRepo.WithTransaction` | GameEventHandler |
| `SettleGame` | 纯转发 | Service 内部 tx（含 RPC） | GameEventHandler |
| `DeductPenaltyToPlatform` | 纯转发 | Service 内部 tx（含 RPC） | **死代码**（game/application/penalty_service.go 直接调用 service） |
| `DistributePenaltyFromPlatform` | 真正编排 | AppService 层 `dbRepo.WithTransaction` | GameLifecycleTimeout |
| `DeductForFirstRound` | 纯转发 | Service 内部 tx（含 RPC） | PacketRoundInit |
| `DeductForLaterRound` | 纯转发 | Service 内部 tx（含 RPC） | PacketRoundInit |
| `DeductForSystemPacket` | 真正编排 | AppService 层 `dbRepo.WithTransaction` | PacketRoundInit |
| `CheckBalance` | 纯转发 | 只读 | **死代码**（无调用方） |
| `CheckBalanceForReady` | 纯转发 | 只读 | **死代码**（room_app_service 直接调用 BalanceService） |

### 3.4 调度器（scheduler/）功能清单

| 调度器 | 周期 | 直接调用的 Service | 应走的 AppService 方法 |
| --- | --- | --- | --- |
| `credit_retry_scheduler` | cfg.Interval | `CreditRetryService.RetryCredit` | （新增）`RetryCreditBills` |
| `game_settle_retry_scheduler` | cfg.Interval | `GameSettleReportingService.RetryPlayerSettle` | （新增）`RetryGameSettle` |
| `game_settle_timeout_scheduler` | cfg.Interval | `GameSettleReportingService.SettleGame` | `SettleGame`（已存在） |
| `refund_process_scheduler` | cfg.Interval | `RefundService.ApproveRefund` | （新增）`ProcessPendingRefunds` |
| `settlement_check_scheduler` | cfg.Interval | `SettlementCheckService`（多方法） | （新增）`RunSettlementCheck` |

### 3.5 基础设施层功能清单

| 文件 | 实现的接口 | 说明 |
| --- | --- | --- |
| `mysql/bill_repository.go` | `domain.BillRepository` | 完整 21 方法 |
| `mysql/round_settlement_repository.go` | `domain.RoundSettlementRepository` | 完整 13 方法 |
| `mysql/refund_audit_repository.go` | `domain.RefundAuditRepository` | 完整 9 方法（含跨表原子操作） |
| `mysql/settlement_query_repository.go` | `domain.SettlementQueryRepository` | 4 只读方法 |
| `mysql/db_repository.go` | `domain.DBRepository` + `domain.Transaction` | 事务编排 + 9 子 Repo 访问器 |
| `redis/virtual_balance_repository.go` | `domain.VirtualBalanceService` | 原子 Deduct(Lua) + 原子 Credit(Lua) + GetBalance + SetBalance + Dirty 标记 |
| `redis/scripts/registry.go` | - | 2 个 Lua 脚本注册：DeductBalance、CreditBalance |

---

## 4. 问题清单

### 4.1 P0 — 结构性违规（必须修复）

| ID | 问题 | 现状证据 | 影响 |
| --- | --- | --- | --- |
| **P0-S1** | Application 层不完整，缺 RefundAppService | 退款全流程（Apply/Approve/Reject）由 `service.RefundService` 直接暴露给外部，未经 AppService 编排 | 违反 §13.1 #5"事务边界在 AppService"；外部调用方直接依赖 service 具体类型 |
| **P0-S2** | 2 个 service 持有 `*gorm.DB`，是伪装的 Repository | `service/exception_manager.go:11` `db *gorm.DB`；`service/platform_call_manager.go:13` `db *gorm.DB` | 违反 Repository 模式；service 层混入基础设施职责；无法通过 domain 接口替换实现 |
| **P0-S3** | 4 处 game/application 直接依赖 settlement/service 具体类型，绕过 AppService | `room_app_service.go:30-31`、`seat_app_service.go:33-37`、`penalty_service.go:23`、`history_service.go:19` | AppService facade 形同虚设；settlement/service 改动直接波及 game 层 |
| **P0-S4** | 5 个 scheduler 全部绕过 AppService | `container.go:433-437` 直接注入 `creditRetrySvc`/`gameSettleSvc`/`RefundSvc` 等给 scheduler | scheduler 与 service 耦合；无法在 AppService 层统一加监控/超时/事务 |
| **P0-S5** | SettleAppService 存在 3 个死代码方法 | `DeductPenaltyToPlatform`、`CheckBalance`、`CheckBalanceForReady` 无调用方 | 误导维护者；违反"死代码必须删除"规约 |
| **P0-S6** | domain/ 目录扁平，未按 §6.3 细分 `repository/` 子目录 | `domain/bill_repository.go` 等 5 个 repository 接口散落在 domain 根目录 | 与推荐目录不一致；聚合根与 repository 接口混杂 |

### 4.2 P1 — 规范性问题（建议修复）

| ID | 问题 | 现状证据 | 影响 |
| --- | --- | --- | --- |
| **P1-S1** | SettleAppService 是"混合 facade"，3 个纯转发方法（SettleGame/DeductForFirstRound/DeductForLaterRound）含 RPC，按短事务原则由 Service 内部编排 tx | `settle_app_service.go:61-89` | 设计文档 §13.1 #5 与 §5.4 存在张力，当前处理方式合理但需文档化说明 |
| **P1-S2** | service 层"很多 service 很乱"的体感来源于缺乏按业务域分组 | 16 个 service 文件平铺在 `service/` 目录 | 可读性差；新成员难以定位 |
| **P1-S3** | RefundService 同时承担"业务逻辑"+"事务编排"+"RPC 调用"三重职责 | `refund_service.go` 266 行 | 职责过重；但拆分需谨慎，避免破坏跨表事务原子性 |
| **P1-S4** | exception_manager.go 与 platform_call_manager.go 命名为 "manager" 而非 "repository" | `service/exception_manager.go:10` `ExceptionManager` | 命名误导；迁移到 infrastructure 后应命名为 Repository |

### 4.3 P2 — 优化建议（可选）

| ID | 问题 | 说明 |
| --- | --- | --- |
| **P2-S1** | service 层缺少包级文档（package comment）说明各 service 的职责边界 | 16 个文件无统一导览 |
| **P2-S2** | 部分服务方法签名返回 `*model.Xxx`（ORM 模型）而非 domain 聚合根 | `refund_service.go` 返回 `*model.RefundAudit`；应返回 `*domain.RefundAudit` |
| **P2-S3** | dto/constants.go 与 domain/*.go 存在状态常量重复定义 | `dto.BillStatusSuccess` vs `domain.BillStatusSuccess` |

---

## 5. 重构方案

### 5.1 重构总原则

1. **行为等价**：每个步骤完成后，对应包的现有单元测试必须全部通过；无测试的部分通过 `go build ./...` + 人工 diff 验证。
2. **小步推进**：每个阶段独立可交付、独立可回滚。
3. **兼容过渡**：对调用方影响大的变更（P0-S3、P0-S4）采用"新增 AppService 方法 → 切换调用方 → 删除旧路径"三步走，避免大爆炸。
4. **不引入新依赖**：不新增第三方库，不引入新的抽象层（如不需要 Application Service 之外的事件总线等）。

### 5.2 目标目录结构（对齐 §6.3）

```
settlement/
├── application/                       # Application 层（补全）
│   ├── settle_app_service.go          # 保留，删除 3 个死代码方法，补全 doc
│   ├── refund_app_service.go          # 新增：退款用例编排
│   ├── scheduler_app_service.go       # 新增：scheduler 用例编排（5 个 scheduler 共用）
│   └── doc.go                         # 包级文档
├── config/
│   └── config.go
├── domain/                            # 细分 repository/ 子目录
│   ├── bill.go                        # 聚合根
│   ├── round_settlement.go
│   ├── refund.go
│   ├── exception.go
│   ├── platform_user.go
│   ├── errors.go
│   ├── user_service.go                # 防腐层接口
│   ├── virtual_balance_service.go     # 虚拟钱包接口
│   └── repository/                    # 新子目录：所有 repository 接口
│       ├── bill_repository.go
│       ├── round_settlement_repository.go
│       ├── refund_audit_repository.go
│       ├── settlement_query_repository.go
│       ├── exception_repository.go    # 新增接口
│       ├── platform_call_log_repository.go  # 新增接口
│       └── transaction.go             # DBRepository + Transaction
├── dto/
│   ├── constants.go
│   ├── request.go
│   └── response.go
├── infrastructure/
│   └── persistence/
│       ├── mysql/
│       │   ├── bill_repository.go
│       │   ├── round_settlement_repository.go
│       │   ├── refund_audit_repository.go
│       │   ├── settlement_query_repository.go
│       │   ├── exception_repository.go       # 新增（从 service/exception_manager.go 迁移）
│       │   ├── platform_call_log_repository.go  # 新增（从 service/platform_call_manager.go 迁移）
│       │   └── db_repository.go
│       └── redis/
│           ├── scripts/
│           │   ├── registry.go
│           │   ├── virtual_balance.lua.go
│           │   └── virtual_balance_test.go
│           └── virtual_balance_repository.go
├── model/
│   ├── bill.go
│   ├── exception_record.go
│   ├── platform_settle_log.go
│   └── refund.go
├── service/                           # Domain Service（无事务编排）
│   ├── doc.go                         # 新增：包级文档，说明各 service 职责
│   ├── balance_query_service.go
│   ├── balance_service.go
│   ├── credit_retry_service.go
│   ├── deduct_service.go
│   ├── game_settle_reporting_service.go
│   ├── penalty_settlement_service.go
│   ├── refund_service.go
│   ├── reward_settler.go
│   ├── robot_checker.go
│   ├── round_settle_service.go
│   ├── session_payout_service.go
│   ├── settlement_check_service.go
│   ├── trace_id_generator.go
│   └── user_id_convert_service.go
└── scheduler/                         # 改为依赖 AppService
    ├── credit_retry_scheduler.go
    ├── game_settle_retry_scheduler.go
    ├── game_settle_timeout_scheduler.go
    ├── refund_process_scheduler.go
    └── settlement_check_scheduler.go
```

> **说明**：§6.3 推荐目录中的 `domain/events/`、`domain/policy/reward_policy.go`、`infrastructure/platform/` 属于 P0-4（游戏规则下沉）的范畴，不在本次结构重构范围内，保持原状。

### 5.3 阶段化执行计划

#### 阶段 1：domain 目录细分（P0-S6）

**目标**：将 repository 接口集中到 `domain/repository/` 子目录，对齐 §6.3。

**变更清单**：

| 操作 | 源 | 目标 |
| --- | --- | --- |
| 移动 | `domain/bill_repository.go` | `domain/repository/bill_repository.go` |
| 移动 | `domain/round_settlement_repository.go` | `domain/repository/round_settlement_repository.go` |
| 移动 | `domain/refund_audit_repository.go` | `domain/repository/refund_audit_repository.go` |
| 移动 | `domain/settlement_query_repository.go` | `domain/repository/settlement_query_repository.go` |
| 移动 | `domain/db_repository.go` | `domain/repository/transaction.go`（重命名，对齐 §6.3） |
| 新增 | - | `domain/repository/exception_repository.go`（接口定义） |
| 新增 | - | `domain/repository/platform_call_log_repository.go`（接口定义） |

**新增接口定义**（基于现有 service 层 2 个 manager 的方法签名提取）：

```go
// domain/repository/exception_repository.go
package repository

import (
    "context"
    "github.com/cashparty/backend/settlement/model"
)

// ExceptionRepository 提供异常记录的持久化能力。
type ExceptionRepository interface {
    Create(ctx context.Context, exception *model.ExceptionRecord) error
}
```

```go
// domain/repository/platform_call_log_repository.go
package repository

import (
    "context"
    "github.com/cashparty/backend/settlement/model"
)

// PlatformCallLogRepository 提供平台调用审计日志的持久化与查询能力。
type PlatformCallLogRepository interface {
    CreateLog(ctx context.Context, params *CallLogCreateParams) (*model.PlatformCallLog, error)
    UpdateLog(ctx context.Context, params *CallLogUpdateParams) error
    GetLogByID(ctx context.Context, id int64) (*model.PlatformCallLog, error)
    GetFailedLogs(ctx context.Context, limit int) ([]*model.PlatformCallLog, error)
}
```

> **注意**：`CallLogCreateParams` / `CallLogUpdateParams` 当前定义在 `service` 包，需迁移到 `dto` 或 `repository` 包（推荐 `dto`，因为是跨层传输结构）。

**兼容性处理**：在 `domain/` 根目录保留 `bill_repository.go` 等文件的**空文件 + re-export**，内容为 `package domain // import "..." // deprecated, use repository` —— 但 Go 不支持包级 re-export，因此采用**全局替换 import 路径**方式：通过 `gofmt -r` 或 IDE 全局重构，将所有 `domain.BillRepository` 替换为 `repository.BillRepository`。

**事务接口扩展**：`domain/repository/transaction.go` 的 `Transaction` 接口需新增 2 个访问器：

```go
type Transaction interface {
    // ... 现有 9 个访问器
    ExceptionRepo() ExceptionRepository
    PlatformCallLogRepo() PlatformCallLogRepository
}
```

**验收**：
- `go build ./settlement/... ./game/...` 通过
- 现有单测全部通过
- `domain/` 根目录仅保留聚合根、值对象、防腐层接口、errors

---

#### 阶段 2：基础设施层补全（P0-S2）

**目标**：将 `service/exception_manager.go` 和 `service/platform_call_manager.go` 迁移到 `infrastructure/persistence/mysql/`，实现阶段 1 新增的 Repository 接口。

**变更清单**：

| 操作 | 源 | 目标 |
| --- | --- | --- |
| 迁移+重命名 | `service/exception_manager.go` | `infrastructure/persistence/mysql/exception_repository.go` |
| 迁移+重命名 | `service/platform_call_manager.go` | `infrastructure/persistence/mysql/platform_call_log_repository.go` |

**实现要点**：

1. `ExceptionRepository` 实现类命名为 `ExceptionRepositoryImpl`，实现 `domain/repository.ExceptionRepository` 接口。
2. `PlatformCallLogRepository` 实现类命名为 `PlatformCallLogRepositoryImpl`，实现 `domain/repository.PlatformCallLogRepository` 接口。
3. **行为零变更**：内部 `*gorm.DB` 调用、`retry_count` 的 CASE 表达式、JSON marshal 逻辑全部原样保留。
4. `DBRepositoryImpl` 与 `GormTransactionImpl` 的构造函数新增这 2 个子 Repository 的 eager 初始化（遵循 project memory：聚合对象必须 eager 初始化）。
5. `Transaction` 接口的 2 个新访问器在 `GormTransactionImpl` 中实现，返回事务内的子 Repository 实例。

**依赖方切换**：

| 调用方 | 原依赖 | 新依赖 |
| --- | --- | --- |
| `service/settlement_check_service.go` | `*ExceptionManager` | `domain/repository.ExceptionRepository` |
| `service/refund_service.go` | `*PlatformCallManager` | `domain/repository.PlatformCallLogRepository` |
| `service/deduct_service.go` | `*PlatformCallManager` | `domain/repository.PlatformCallLogRepository` |
| `service/game_settle_reporting_service.go` | `*PlatformCallManager` | `domain/repository.PlatformCallLogRepository` |
| `service/penalty_settlement_service.go` | `*PlatformCallManager` | `domain/repository.PlatformCallLogRepository` |
| `service/session_payout_service.go` | `*PlatformCallManager` | `domain/repository.PlatformCallLogRepository` |

**bootstrap 切换**：`container.go` 中 `exceptionMgr`、`callMgr` 字段类型改为接口，由 `mysqlRepo.NewExceptionRepository` / `mysqlRepo.NewPlatformCallLogRepository` 构造。

**验收**：
- `go build ./settlement/... ./game/...` 通过
- `service/` 目录下不再有任何文件 import `gorm.io/gorm`
- 现有单测全部通过（`mocks_test.go` 需同步更新 mock 类型）

---

#### 阶段 3：Application 层补全（P0-S1、P0-S5）

**目标**：新增 `RefundAppService`、`SchedulerAppService`；清理 `SettleAppService` 死代码。

##### 3.1 新增 RefundAppService

**文件**：`settlement/application/refund_app_service.go`

**职责**：编排退款用例（Apply/Approve/Reject/Query），将 RefundService 的事务编排上提到 AppService 层。

**方法清单**（完全对应现有 RefundService 公开方法，行为零变更）：

```go
type RefundAppService struct {
    refundService *service.RefundService
}

func NewRefundAppService(refundService *service.RefundService) *RefundAppService

// ApplyForRefund 编排退款申请用例。
// 事务由 RefundService 内部通过 dbRepo.WithTransaction 编排（跨表 refund_audit+bill）。
func (s *RefundAppService) ApplyForRefund(ctx context.Context, req *dto.RefundApplyRequest) (string, error)

// ApproveRefund 编排退款审批+执行用例。
func (s *RefundAppService) ApproveRefund(ctx context.Context, req *dto.RefundApproveRequest) error

// RejectRefund 编排退款拒绝用例。
func (s *RefundAppService) RejectRefund(ctx context.Context, req *dto.RefundRejectRequest) error

// GetRefundAuditByOrderNo 只读查询，直接转发。
func (s *RefundAppService) GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error)

// GetRefundsByStatus 只读查询，直接转发。
func (s *RefundAppService) GetRefundsByStatus(ctx context.Context, status int, limit, offset int) ([]*model.RefundAudit, error)
```

> **说明**：RefundService 的事务边界保持不变（Service 内部 `dbRepo.WithTransaction` 编排跨表原子操作），AppService 仅作 facade 转发并统一入口。这与 SettleAppService 处理含 RPC 用例（SettleGame 等）的方式一致。

##### 3.2 新增 SchedulerAppService

**文件**：`settlement/application/scheduler_app_service.go`

**职责**：为 5 个 scheduler 提供统一入口，便于未来加监控/超时/熔断。

**方法清单**：

```go
type SchedulerAppService struct {
    creditRetryService      *service.CreditRetryService
    gameSettleSvc           *service.GameSettleReportingService
    refundService           *service.RefundService
    settlementCheckService  *service.SettlementCheckService
    roundSettlementRepo     repository.RoundSettlementRepository
    settlementQueryRepo     repository.SettlementQueryRepository
    refundAuditRepo         repository.RefundAuditRepository
}

func NewSchedulerAppService(...) *SchedulerAppService

// RetryCreditBills 重试失败的 Credit bill（供 credit_retry_scheduler 调用）。
func (s *SchedulerAppService) RetryCreditBills(ctx context.Context, limit int) error

// RetryGameSettle 重试会话级结算上报（供 game_settle_retry_scheduler 调用）。
func (s *SchedulerAppService) RetryGameSettle(ctx context.Context, limit int) error

// SettleGameByTimeout 超时触发会话级结算（供 game_settle_timeout_scheduler 调用）。
func (s *SchedulerAppService) SettleGameByTimeout(ctx context.Context, sessionID int64) error

// ProcessPendingRefunds 处理待审批退款（供 refund_process_scheduler 调用）。
func (s *SchedulerAppService) ProcessPendingRefunds(ctx context.Context, limit int) error

// RunSettlementCheck 执行对账扫描（供 settlement_check_scheduler 调用）。
func (s *SchedulerAppService) RunSettlementCheck(ctx context.Context) error
```

> **行为零变更**：每个方法内部仅转发到对应 Service，逻辑完全保留。例如 `RetryCreditBills` 内部就是 `creditRetryService.GetRetryableCredits` + 循环 `RetryCredit`，与现有 scheduler.execute 完全一致。

##### 3.3 清理 SettleAppService 死代码（P0-S5）

**删除 3 个方法**：
- `DeductPenaltyToPlatform` — 死代码（game/application/penalty_service.go:86 直接调用 `settlementService.DeductPenaltyToPlatform`，绕过 AppService）
- `CheckBalance` — 死代码（无调用方）
- `CheckBalanceForReady` — 死代码（room_app_service 直接调用 BalanceService）

**同步处理**：`DeductPenaltyToPlatform` 删除后，`game/application/penalty_service.go:86` 的调用改为走 AppService（见阶段 4）。

**验收**：
- `go build ./settlement/... ./game/...` 通过
- `go vet ./settlement/...` 无 unused method 警告
- 现有单测全部通过

---

#### 阶段 4：调用方收敛（P0-S3、P0-S4）

**目标**：所有外部调用方统一通过 AppService facade 访问 settlement。

##### 4.1 game/application 4 处 bypass 收敛

| 调用方文件 | 现状依赖 | 收敛后依赖 | 调用方法变更 |
| --- | --- | --- | --- |
| `room_app_service.go:30-31,507` | `*BalanceQueryService` + `*BalanceService` | `*SettleAppService` | `CheckBalance` / `CheckBalanceForReady` 走 AppService |
| `seat_app_service.go:33-37,242` | `*BalanceQueryService` + `*BalanceService` | `*SettleAppService` | 同上 |
| `penalty_service.go:23,86` | `*PenaltySettlementService` | `*SettleAppService` | `DeductPenaltyToPlatform` 改走 AppService（AppService 需保留或重新暴露此方法） |
| `history_service.go:19` | `domain.BillRepository` | `*SettleAppService` 或新增查询方法 | 通过 AppService 暴露只读查询方法 |

**SettleAppService 方法调整**：

阶段 3 删除了 `DeductPenaltyToPlatform` 死代码，但 `penalty_service.go` 实际在调用 service 层的同名方法。收敛时需在 AppService **重新暴露** `DeductPenaltyToPlatform`（纯转发，Service 内部 tx），让 penalty_service 走 AppService。这样 AppService 成为唯一入口。

同时，`CheckBalance` / `CheckBalanceForReady` 虽然是死代码（无调用方走 AppService），但收敛后 room_app_service / seat_app_service 会改走 AppService，因此这两个方法**不删除，改为真正使用**。

> **修订阶段 3 决策**：P0-S5 的 3 个死代码方法中，`DeductPenaltyToPlatform`、`CheckBalance`、`CheckBalanceForReady` 在阶段 4 收敛后都会被重新使用。因此阶段 3 **不删除这 3 个方法**，仅补全文档注释说明"纯转发，Service 内部 tx"。P0-S5 降级为 P1（文档完善）。

##### 4.2 scheduler 5 处 bypass 收敛

| Scheduler | 现状依赖 | 收敛后依赖 |
| --- | --- | --- |
| `credit_retry_scheduler.go` | `*CreditRetryService` | `*SchedulerAppService` |
| `game_settle_retry_scheduler.go` | `*GameSettleReportingService` + 2 个 repo | `*SchedulerAppService` |
| `game_settle_timeout_scheduler.go` | `*GameSettleReportingService` + 1 个 repo | `*SchedulerAppService` |
| `refund_process_scheduler.go` | `*RefundService` + 1 个 repo | `*SchedulerAppService` |
| `settlement_check_scheduler.go` | `*SettlementCheckService` | `*SchedulerAppService` |

**scheduler execute 方法简化**（以 credit_retry_scheduler 为例）：

```go
// 重构前
func (s *CreditRetryScheduler) execute(ctx context.Context) error {
    bills, err := s.creditRetry.GetRetryableCredits(ctx, s.limit)
    // ... 循环 RetryCredit
}

// 重构后
func (s *CreditRetryScheduler) execute(ctx context.Context) error {
    return s.schedulerApp.RetryCreditBills(ctx, s.limit)
}
```

**bootstrap 切换**：`container.go:433-437` 的 `initSettlementSchedulers` 改为注入 `*SchedulerAppService`。

**验收**：
- `go build ./settlement/... ./game/...` 通过
- `game/application/` 下无任何文件 import `settlement/service`（仅 import `settlement/application` + `settlement/domain` + `settlement/dto`）
- `settlement/scheduler/` 下无任何文件 import `settlement/service`（仅 import `settlement/application`）
- 现有单测全部通过

---

#### 阶段 5：Service 层文档化与命名规整（P1-S1、P1-S2、P1-S4）

**目标**：消除"很多 service 很乱"的体感，通过文档化与命名规范提升可读性。

##### 5.1 新增 service/doc.go 包级文档

```go
// Package service 提供 settlement 领域的 Domain Service 实现。
//
// 各 Service 按业务域组织，职责单一，无事务编排（事务边界由 application 层编排）。
//
// # 按业务域分组
//
// 扣款域：
//   - DeductService: 3 种扣款场景（首回合/后续回合/系统红包）
//
// 结算域：
//   - RoundSettleService: 单局结算（grab/commission bill + 奖励触发）
//   - GameSettleReportingService: 会话级结算上报（platform.Settle）
//   - SessionPayoutService: 会话级派奖（platform.Credit）
//   - RewardSettler: 系统奖励 bill 创建（纯记账）
//
// 罚款域：
//   - PenaltySettlementService: 罚款扣款 + 罚款分配
//
// 退款域：
//   - RefundService: 退款申请/审批/拒绝/执行
//
// 重试与对账域：
//   - CreditRetryService: 重试失败的 Credit bill
//   - SettlementCheckService: 对账扫描
//
// 查询域（只读）：
//   - BalanceQueryService: 余额查询
//   - BalanceService: 开局余额检查
//
// 基础设施支持：
//   - RobotChecker: Redis 机器人识别（fail-closed）
//   - TraceIDGenerator: 确定性 ID 生成
//   - UserIDConvertService: 内部 ID ↔ 平台 ID 转换
package service
```

##### 5.2 命名规整（P1-S4）

阶段 2 已将 `ExceptionManager` → `ExceptionRepositoryImpl`、`PlatformCallManager` → `PlatformCallLogRepositoryImpl`，命名对齐 Repository 模式。

##### 5.3 文档注释完善（P1-S1）

在 `SettleAppService` 的 godoc 中补充说明 §13.1 #5 与 §5.4 的张力及当前处理方式：

```go
// SettleAppService 是 settlement 模块的 Application 层入口。
//
// 事务编排策略（§13.1 #5 与 §5.4 的协调）：
//   - 纯 DB 写入用例（SettleRound、DistributePenaltyFromPlatform、DeductForSystemPacket）：
//     在 AppService 层通过 dbRepo.WithTransaction 开启事务，向下传递 tx。
//   - 含 RPC 的用例（SettleGame、DeductForFirstRound、DeductForLaterRound、DeductPenaltyToPlatform）：
//     遵循短事务原则（§5.4 禁止事务内 RPC），由 Service 内部对 DB 写入片段开事务，
//     AppService 仅作 facade 转发。
//   - 只读用例（CheckBalance、CheckBalanceForReady）：直接转发，无事务。
```

---

## 6. 风险控制

### 6.1 风险矩阵

| 风险 | 概率 | 影响 | 缓解措施 |
| --- | --- | --- | --- |
| 阶段 1 目录迁移导致 import 路径批量变更遗漏 | 中 | 编译失败 | 使用 IDE 全局重构；`go build ./...` 每步验证 |
| 阶段 2 ExceptionRepository/PlatformCallLogRepository 接口提取与实现不匹配 | 低 | 编译失败 | 接口方法签名严格基于现有 manager 公开方法；单测覆盖 |
| 阶段 3 RefundAppService 编排方式与原 RefundService 事务边界不一致 | 中 | 退款原子性破坏 | **严格保留** RefundService 内部 `dbRepo.WithTransaction` 编排，AppService 仅转发 |
| 阶段 4 调用方收敛时遗漏某个调用点 | 中 | 编译失败 | 阶段 4 完成后用 `grep -r "settlement/service" game/application settlement/scheduler` 验证零命中 |
| 阶段 4 SettleAppService 方法签名调整导致调用方不兼容 | 低 | 编译失败 | 保持方法签名完全不变，仅调整内部实现 |
| 阶段 2 后 `mocks_test.go` 未同步更新 | 高 | 测试编译失败 | 同步更新 mock 类型；优先跑 `go test ./settlement/service/...` |

### 6.2 回滚策略

每个阶段独立 commit，回滚粒度为单个阶段。关键检查点：

| 阶段 | 检查命令 | 预期结果 |
| --- | --- | --- |
| 1 | `go build ./settlement/... ./game/...` | 通过 |
| 1 | `go test ./settlement/...` | 全部通过 |
| 2 | `grep -r "gorm.io/gorm" settlement/service/` | 零命中 |
| 2 | `go test ./settlement/service/...` | 全部通过 |
| 3 | `go vet ./settlement/...` | 无 unused 警告 |
| 4 | `grep -r "settlement/service" game/application/ settlement/scheduler/` | 零命中 |
| 4 | `go test ./...` | 全部通过 |
| 5 | `go vet ./settlement/...` | 通过 |

### 6.3 行为等价性验证

由于不改变业务逻辑，主要依赖现有单元测试 + 编译验证。关键业务流程的人工 diff 检查点：

1. **单局结算流程**：`GameEventHandler → SettleAppService.SettleRound → RoundSettleService.SettleRound` — 事务边界不变（AppService tx 包裹 creditRound + rewardSettler + 标记 Credited）
2. **会话级结算流程**：`GameEventHandler → SettleAppService.SettleGame → RoundSettleService.SettleGame → GameSettleReportingService` — 事务边界不变（Service 内部 tx）
3. **退款流程**：`RefundAppService.ApplyForRefund → RefundService.applyForRefundLocked` — 跨表事务不变（Service 内部 `dbRepo.WithTransaction` 编排 `CreateRefundAuditAndUpdateBillRefundStatus`）
4. **扣款流程**：`PacketRoundInit → SettleAppService.DeductForFirstRound → DeductService.DeductForFirstRound` — 事务边界不变（Service 内部 tx 包裹 CreateRoundSettlementAndBills，RPC 在 tx 外）
5. **scheduler 重试流程**：`credit_retry_scheduler → SchedulerAppService.RetryCreditBills → CreditRetryService` — 循环逻辑不变

---

## 7. 执行顺序与依赖

```
阶段 1 (domain 目录细分)
   │
   ▼
阶段 2 (基础设施补全)  ─── 依赖阶段 1 的新接口定义
   │
   ▼
阶段 3 (Application 补全)  ─── 依赖阶段 2 的 Repository 接口
   │
   ▼
阶段 4 (调用方收敛)  ─── 依赖阶段 3 的新 AppService
   │
   ▼
阶段 5 (文档化与命名)  ─── 独立，可并行或最后做
```

**并行机会**：
- 阶段 1 和阶段 5 的 doc.go 编写可并行
- 阶段 3 的 RefundAppService 和 SchedulerAppService 可并行编写

---

## 8. 验收清单

### 8.1 结构验收

- [ ] `settlement/domain/repository/` 子目录存在，包含 7 个 repository 接口文件
- [ ] `settlement/infrastructure/persistence/mysql/` 包含 `exception_repository.go`、`platform_call_log_repository.go`
- [ ] `settlement/application/` 包含 `settle_app_service.go`、`refund_app_service.go`、`scheduler_app_service.go`、`doc.go`
- [ ] `settlement/service/` 不再包含 `exception_manager.go`、`platform_call_manager.go`
- [ ] `settlement/service/doc.go` 包存在

### 8.2 依赖验收

- [ ] `grep -r "gorm.io/gorm" settlement/service/` 零命中
- [ ] `grep -r "settlement/service" game/application/` 零命中
- [ ] `grep -r "settlement/service" settlement/scheduler/` 零命中
- [ ] `settlement/` 不 import `game/model`（保持现有状态）
- [ ] `game/application/` 仅 import `settlement/application`、`settlement/domain`、`settlement/dto`

### 8.3 行为验收

- [ ] `go build ./settlement/... ./game/...` 通过
- [ ] `go vet ./settlement/...` 通过
- [ ] `go test ./settlement/...` 全部通过
- [ ] `go test ./game/application/...` 全部通过
- [ ] 单局结算流程（SettleRound）事务边界与重构前等价
- [ ] 会话级结算流程（SettleGame）事务边界与重构前等价
- [ ] 退款流程（Apply/Approve/Reject）跨表事务原子性保留
- [ ] 扣款流程（DeductForFirstRound/LaterRound/SystemPacket）事务边界与重构前等价
- [ ] 5 个 scheduler 的扫描/重试逻辑与重构前等价

### 8.4 文档验收

- [ ] `settlement/application/doc.go` 说明 Application 层入口与事务编排策略
- [ ] `settlement/service/doc.go` 按 7 个业务域分组说明各 Service 职责
- [ ] `SettleAppService` godoc 说明 §13.1 #5 与 §5.4 的协调策略
- [ ] 新增的 Repository 接口有完整 godoc

---

## 9. 附录

### 9.1 文件变更总览

| 操作 | 文件数 | 说明 |
| --- | --- | --- |
| 移动 | 5 | domain repository 接口迁入 `domain/repository/` |
| 迁移+重命名 | 2 | service 层 2 个 manager 迁入 infrastructure |
| 新增 | 6 | 2 个 AppService + 2 个 Repository 接口 + 2 个 Repository 实现 + doc.go |
| 修改 | ~15 | 调用方 import 路径调整、bootstrap 接线调整、mock 更新 |
| 删除 | 0 | 无文件删除（死代码方法保留并重新使用） |

### 9.2 不变更清单（明确保护边界）

以下内容在重构中**严格不变**：

- 所有聚合根的字段、状态机方法、常量值
- 所有 Repository 接口的方法签名（仅目录位置变化）
- 所有 Service 的业务方法实现逻辑
- 所有 Lua 脚本内容
- 所有数据库表结构、Redis Key、Kafka Topic
- 所有 DTO 字段定义
- 所有配置项与默认值
- 所有事务边界（AppService tx vs Service tx 的划分）
- 所有幂等机制（BizOrderNo 生成规则、状态机条件、lock + 双重检查）
- 所有错误处理路径（fail-closed、指数退避、DLQ）
- 所有状态机流转条件

### 9.3 与架构审计文档的对应关系

| 本方案阶段 | 对应 GAME_SERVICE_ARCHITECTURE_REVIEW.md 条目 |
| --- | --- |
| 阶段 1 | §6.3 settlement/ 推荐目录 |
| 阶段 2 | §P1-6（事务边界错位，Repository 模式）+ project memory（Repository 聚合对象 eager 初始化） |
| 阶段 3 | §6.3（新增 Application 层）+ §3.5（settlement 引入 Application 层） |
| 阶段 4 | §13.1 #5（事务边界在 AppService）+ project memory（AppService facade 统一入口） |
| 阶段 5 | §17（代码质量） |

### 9.4 术语表

| 术语 | 含义 |
| --- | --- |
| AppService | Application 层 Service，编排用例、开启事务，不包含业务规则 |
| Domain Service | Service 层实现，包含业务规则，不开启事务（事务由 AppService 或 Service 内部片段编排） |
| Repository | 持久化抽象接口，定义在 domain，实现在 infrastructure |
| 防腐层（ACL） | Anti-Corruption Layer，通过接口隔离外部领域模型，如 `domain.UserService` 隔离 game/model |
| 短事务原则 | §5.4 规约：事务内禁止 RPC、Kafka Producer、耗时计算 |
| fail-closed | 失败时拒绝操作（保守策略），如 RobotChecker Redis 错误时返回 false 走真实玩家路径 |

---

**文档结束**
