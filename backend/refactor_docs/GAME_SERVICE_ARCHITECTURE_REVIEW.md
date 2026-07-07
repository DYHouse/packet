# CashParty Game 服务架构审查与重构设计文档

> **审查范围**：`backend/` 下 Game 服务及其关联模块（game、settlement、common、gateway、api/platform、stats）
> **审查方法**：逐文件 Read + 跨模块依赖追踪，100% 基于实际代码（不做臆测）
> **审查日期**：2026-07-07
> **定位**：本文档是 Game 服务重构的**唯一设计依据**
> **约束**：本文档仅做分析、设计、规划，不修改代码、不生成实现代码

---

## 目录

- [1. 当前项目架构分析](#1-当前项目架构分析)
- [2. 当前存在的问题（按优先级分类）](#2-当前存在的问题按优先级分类)
- [3. 重构目标](#3-重构目标)
- [4. 重构原则](#4-重构原则)
- [5. 推荐整体架构](#5-推荐整体架构)
- [6. 推荐项目目录](#6-推荐项目目录)
- [7. 推荐模块划分](#7-推荐模块划分)
- [8. 核心对象关系](#8-核心对象关系)
- [9. 核心业务流程](#9-核心业务流程)
- [10. 调用链](#10-调用链)
- [11. 状态流](#11-状态流)
- [12. 事件流](#12-事件流)
- [13. Repository 设计](#13-repository-设计)
- [14. Service 设计](#14-service-设计)
- [15. Redis 设计](#15-redis-设计)
- [16. MQ 设计](#16-mq-设计)
- [17. 配置设计](#17-配置设计)
- [18. 错误处理设计](#18-错误处理设计)
- [19. 日志设计](#19-日志设计)
- [20. 包依赖关系](#20-包依赖关系)
- [21. 分层依赖规则](#21-分层依赖规则)
- [22. 开发规范](#22-开发规范)
- [23. 后续重构路线图（Phase 1 ~ Phase N）](#23-后续重构路线图phase-1--phase-n)
- [24. 风险评估](#24-风险评估)
- [25. 最终推荐方案](#25-最终推荐方案)

---

## 1. 当前项目架构分析

### 1.1 架构形态

当前项目属于 **混合架构（Pragmatic DDD-Lite + 三层架构）**：

| 维度 | 现状 | 评价 |
|---|---|---|
| 分层 | domain / application / infrastructure / server / bootstrap 五层 | 形式上分层，但执行不彻底 |
| DDD | 有 `domain/` 目录，定义 Repository 接口、聚合根、值对象 | 仅 DDD-Lite，缺 Domain Service、Domain Event、聚合一致性边界 |
| Clean Architecture | 基础设施依赖接口反转（domain 定义接口，infrastructure 实现） | 部分实现，部分突破（algorithm 依赖 infra，consumer 持有 *gorm.DB） |
| CQRS | 无显式 CQRS，但 HistoryService 是读侧、GameAppService 是写侧的隐性分离 | 可显式化 |

### 1.2 服务拓扑

```
┌─────────────┐     gRPC Forward      ┌─────────────────────────────────────────┐
│   Gateway    │ ────────────────────► │              Game Service               │
│  (WebSocket) │                       │  ┌────────────┐  ┌──────────────────┐  │
│              │   Kafka Broadcast     │  │  game/      │  │   settlement/    │  │
│              │ ◄────────────────────►│  │  (core)    │──│   (sibling)      │  │
└─────────────┘                       │  └────────────┘  └──────────────────┘  │
                                      │         │                │              │
       ┌──────────────────┐           │         ▼                ▼              │
       │   Platform API   │◄──RPC─────┤  Redis (共享 Key)  MySQL (共享 DB)     │
       │ (GamingPanda 钱包)│           │                                         │
       └──────────────────┘           └─────────────────────────────────────────┘
                                                │
                                                │ MySQL 只读
                                                ▼
                                       ┌──────────────────┐
                                       │   Stats Service  │
                                       │   (独立 HTTP)    │
                                       └──────────────────┘
```

**三个独立进程**：
1. `cmd/gateway/main.go` — WebSocket 网关（8081），JWT 鉴权 + gRPC 转发 + Kafka 广播 fan-out
2. `cmd/game/main.go` — 游戏核心服务（gRPC 9xxx），含 game + settlement 两套领域
3. `cmd/stats/main.go` — 统计只读服务（8082），独立部署无 Kafka/Nacos

### 1.3 当前优点（必须保留）

| # | 优点 | 所在位置 | 保留理由 |
|---|---|---|---|
| S1 | **Redis Key 单一真理源** | [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) (718 行, ~80 key 模板, ~50 工厂函数) | 统一前缀 `cashparty:`、统一分隔符 `:`、零散落；是全项目最规范的部分 |
| S2 | **Lua 脚本集中注册** | [game/infrastructure/persistence/redis/scripts/registry.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/registry.go) + `cRedis.NewScript` | 20 个脚本全部注册，无内联 `redis.Eval`，支持 EVALSHA 缓存 |
| S3 | **Scheduler 框架完善** | [common/scheduler/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/scheduler/) | `Scheduler` 接口 + `BaseScheduler` + `Registry` + `Metrics`，并行 Stop + 全局超时 |
| S4 | **幂等设计成熟** | settlement 全域 | 确定性 BizOrderNo、乐观锁 `WHERE status=?`、Processing 中间态、双重检查锁 |
| S5 | **crypto/rand 用于资金** | algorithm 全域 | PacketGenerator / RewardController 全用 `crypto/rand`，无 `math/rand` 资金路径 |
| S6 | **atomic.Pointer 并发配置** | [game/algorithm/packet_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go) | Nacos 热更新 + Generate 并发安全，每次调用一次快照加载 |
| S7 | **统一事件信封 EventHeader** | [common/message/header.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/header.go) | `event_id` / `trace_id` / `timestamp` / `version` 四字段，JSON embedding 提升到顶层 |
| S8 | **AsyncTaskRunner 生命周期** | [common/async/task_runner.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/async/task_runner.go) | app-level context 派生、per-task 超时、panic 恢复、closed 状态保护 |
| S9 | **优雅关停顺序** | [game/bootstrap/app.go:Stop()](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) | taskRunner.Stop → cancel ctx → container.Stop → wait consumers → wait runner → close Kafka → close Redis |
| S10 | **Kafka Hash Balancer + Key 策略** | [common/kafka/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/kafka/config.go) | roomID 作 Key 保证同房间事件同分区、顺序消费 |
| S11 | **Platform RPC 审计日志** | [settlement/service/platform_call_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/platform_call_manager.go) | 所有写操作 Debit/Credit/Settle 全程落库，含请求/响应体、重试次数 |
| S12 | **雪花 ID + 节点自动分配** | [common/idgen/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/) | 时钟回拨检测、Redis INCR + SET NX 抢占、5min 心跳续约、Lua 安全释放 |

### 1.4 当前主要问题概览

| 类别 | 问题数 | 详见第 2 节 |
|---|---|---|
| 架构边界 | 8 项 | 2.1 |
| God Service / God Repository | 4 项 | 2.2 |
| 分层违规 | 6 项 | 2.3 |
| 重复与散落 | 7 项 | 2.4 |
| 测试缺口 | 5 项 | 2.5 |
| 错误处理 | 5 项 | 2.6 |
| 安全与资金 | 4 项 | 2.7 |

---

## 2. 当前存在的问题（按优先级分类）

> 每项格式：**现状 → 风险 → 原因 → 建议 → 重构收益**

### 2.1 P0 — 架构边界污染（必须立即隔离）

#### P0-1：settlement 直接 import game/model（跨领域类型污染）

- **现状**：[settlement/service/user_id_convert_service.go:8](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/user_id_convert_service.go) `gameModel "github.com/cashparty/backend/game/model"`；`UserService` 接口返回 `*gameModel.User`
- **风险**：settlement 领域绑死 game 的 ORM 模型，game 重构 User 表结构将波及 settlement；领域边界形同虚设
- **原因**：早期 settlement 由 game 团队快速实现，复用了 game 的 model；后续未做边界切割
- **建议**：在 settlement/domain 定义 `PlatformUser{ID, PlatformUserID, Nickname, Avatar}` 值对象；在 game/infrastructure 提供 `UserSaverAdapter` 实现 `settlement.UserService` 接口并完成 `gameModel.User → PlatformUser` 转换
- **收益**：解除 settlement → game 的类型依赖，game 可独立演进 User 模型

#### P0-2：VirtualBalanceService 跨服务共享 Redis Key（隐式耦合）

- **现状**：[game/infrastructure/persistence/redis/virtual_balance.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/virtual_balance.go) 与 [settlement/service/virtual_balance_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/virtual_balance_service.go) 各自独立实现，但**共享同一组 Redis Key**（`cashparty:robot:virtual_balance:*`、`cashparty:robot:virtual_balance:dirty`）；settlement/service/virtual_balance_service.go:17 注释自承"独立实现以避免循环依赖"
- **风险**：两份代码各自维护读/写语义，任何一方修改 Key 编码格式或 Hash 结构都会导致另一方静默失败；IncBy + SAdd 非原子（dirty 标志可能丢失）
- **原因**：避免 `game → settlement` 反向依赖的权宜之计
- **建议**：将虚拟余额的所有 Redis 操作收敛为**单一服务**（推荐放在 settlement，因为是资金领域），game 层通过接口调用；或抽出独立的 `robotwallet` 子域
- **收益**：消除隐式耦合，虚拟余额读写原子化

#### P0-3：GameEventConsumer 是披着 infra 外衣的 Application 层

- **现状**：[game/infrastructure/messaging/game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go)（551 行）持有 `*gorm.DB` + `*cRedis.Client` + `*settlementService.SettlementService`，直接 `c.db.Transaction(func(tx *gorm.DB) error { tx.Create(...); tx.Model(...).Updates(...) })`，**完全绕过 domain.DBRepository 与 domain.Transaction 抽象**；包含业务编排（创建 session/round、调用 settlement.SettleRound/SettleGame、触发机器人行为）
- **风险**：业务逻辑散落 infra 层，无法单测；事务边界不受 Application 层控制；DB schema 变更需改 consumer 代码；与 GameAppService 形成双写路径
- **原因**：消费者模式惯用 infra 包，但本次将业务处理直接写在 handler 中未做分层
- **建议**：抽 `application/game_event_handler.go`（实现 `messaging.GameEventHandlerInterface`），consumer 仅做：解析消息 → 调 handler → 错误回报。所有 DB 操作走 `dbRepo.WithTransaction`
- **收益**：业务逻辑可单测、事务受控、消除 *gorm.DB 直接持有

#### P0-4：settlement 包含 game 业务规则

- **现状**：
  - [settlement/service/reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go) 硬编码 `StraightRewardMultiplier=1.0`、`LeopardRewardMultiplier=10.0`，且 switch on `rewardType`（1=Straight, 2=Leopard）
  - [settlement/service/game_settle_service.go:233](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) 计算 `gameResult = "win" if payOut > betAmount else "lose"` —— 游戏胜负语义
  - [settlement/service/balance_service.go:75-83](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/balance_service.go) `CalculateRequiredFee = firstRoundFee + roomFee * (maxRounds-1)` —— 游戏经济公式
- **风险**：游戏经济调参需改 settlement 代码；两个领域职责混乱；任意一方改动可能引发资金计算错误
- **原因**：settlement 在快速迭代中吸收了本属于 game 的决策逻辑
- **建议**：reward 计算移到 game/algorithm；胜负判定移到 game/domain；费用公式移到 game/domain 或 config；settlement 仅接收"已计算好的金额"执行账务
- **收益**：领域边界清晰，游戏调参不触碰资金代码

#### P0-5：gateway 含游戏入口业务逻辑

- **现状**：[gateway/service/game.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/game.go) 的 `GameService.StartGame` 调用 gRPC `SaveUser` → 生成 JWT → 拼装 `gameURL` 查询参数；[gateway/store/memory.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/store/memory.go) 硬编码游戏列表
- **风险**：gateway 自承"thin proxy"但实际承载游戏入口编排；游戏列表是 placeholder，无法扩展
- **原因**：游戏入口 REST API 与 WS 网关同进程，未做职责拆分
- **建议**：游戏入口（`/game/list`、`/game/start`）拆为独立 HTTP 服务或合并到 game 服务的 HTTP 端口；gateway 仅保留 WS 职责
- **收益**：gateway 真正变薄，游戏入口可独立演进

#### P0-6：domain.EventPublisher 接口违反 ISP

- **现状**：[game/domain/messaging.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/messaging.go) 定义 `EventPublisher` 接口含 `PublishRoomEvent` + `PublishGameEvent`；但 [game/infrastructure/messaging/game_event_publisher.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_publisher.go) 的 `PublishRoomEvent` 返回 `fmt.Errorf("does not support")`；room_event_publisher 同理
- **风险**：调用方拿到不支持的 publisher 会在运行时才报错；接口胖，违反接口隔离
- **原因**：早期统一接口的过度抽象
- **建议**：拆为 `RoomEventPublisher` + `GameEventPublisher` 两个接口；需要同时发布的聚合服务实现两者
- **收益**：接口精炼，编译期保证正确性

#### P0-7：跨领域常量三重定义

- **现状**：`RewardTypeStraight=1` / `RewardTypeLeopard=2` / `TriggerTypeGuarantee=1` / `TriggerTypeProbability=2` 同时定义在：
  1. [game/algorithm/model.go:18-21](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/model.go)
  2. [game/algorithm/reward_controller.go:17-19](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/reward_controller.go)
  3. [game/model/reward.go:5-13](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/reward.go)
- **风险**：常量漂移风险；新增类型需改三处
- **原因**：模型层与算法层各自定义，未收敛
- **建议**：单一真理源放 `game/domain`（业务核心）；algorithm 和 model 引用 domain 常量
- **收益**：单一真理源

#### P0-8：MaxPlayers / randomDelay / round-init 样板代码重复

- **现状**：
  - `MaxPlayers = 5` 散落 6+ 处：[robot_config.go:11](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_config.go)、room_state.go:141、robot_player.go:211,240、robot_scheduler_service.go:213,382
  - `randomDelay(min, max)` 散落 3 处：robot_player.go:265、robot_behavior.go:317、robot_scheduler_service.go:527
  - round-init 三段样板（createRoundRecord + updateRoundFailed + updateRoundDeductSuccess + UpdateRoundSender + UpdateRoundAmount）在 game_app_service.go:1608/1687/1752 三处复制
- **风险**：修改一处遗漏其他，行为不一致
- **建议**：常量提 domain；randomDelay 提 common/utils；round-init 抽 `RoundInitService`
- **收益**：消除"修改遗漏"风险

### 2.2 P0 — God Service / God Repository（必须拆分）

#### P0-9：GameAppService 是 God Service

- **现状**：[game/application/game_app_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go)（**1812 行**，~30 方法，19 个协作依赖）
  - 跨 7 个职责：发包、抢包编排、结算、调度、广播、持久化、事件发布，外加扣款/退款/惩罚/系统发包/恢复
  - 12/16 个异步任务集中于此
  - 3 套近似 round-init 方法
- **风险**：单文件改动引发回归风险大；新人理解成本极高；测试覆盖几乎不可能
- **建议**：拆分为 4 个 Application Service（详见 §14）
  - `PacketOrchestrator`（发包管线）
  - `RoundSettlementService`（settleRound + Lua 解析）
  - `GameLifecycleService`（StartGame/EndGame/Resume）
  - `GameEventPublisher`（4 个 publish 任务集中）
- **收益**：每个 Service 200-400 行，单一职责，可独立测试

#### P0-10：SettlementService 是 God Facade

- **现状**：[settlement/service/settlement_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go)（468 行，13 个依赖）作为 facade 暴露 SettleRound/SettleGame/DeductPenalty/DistributePenalty/CheckBalance/GetUserBalance/GetBill 等 10+ 方法
- **风险**：调用方依赖一个胖接口，违反 ISP；任何子服务改动都触动 facade
- **建议**：拆为 `RoundSettleService`、`PenaltySettlementService`、`BalanceQueryService`；调用方按需依赖
- **收益**：接口隔离，依赖最小化

#### P0-11：BillManager 是 God Repository

- **现状**：[settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go)（**722 行，~30 方法**）合并 BillRecord / RoundSettlement / RefundAudit / GameSettleStatus 4 套仓储 + 聚合查询 + 事务管理
- **风险**：单点修改影响全部账务；违反 SRP；无法独立 mock bill 仓储做单测
- **建议**：拆为 `BillRepository`、`RoundSettlementRepository`、`RefundAuditRepository`、`SettlementQueryRepository`；提取 `BillRepository` 接口（per project memory）
- **收益**：账务领域细分，可独立测试

#### P0-12：GameSettleService 混合两职责

- **现状**：[settlement/service/game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go)（491 行）既做"游戏结果上报 RPC（platform.Settle）"又做"会话级派奖资金移动 RPC（platform.Credit）"
- **风险**：两类 RPC 重试策略不同（上报幂等 vs 派奖需严格幂等），混在一起出错处理复杂
- **建议**：拆为 `GameSettleReportingService`（调 platform.Settle 上报结果）+ `SessionPayoutService`（调 platform.Credit 派奖）
- **收益**：职责清晰，重试策略可独立设计

### 2.3 P1 — 分层违规

#### P1-1：GameEventConsumer 直接持有 *gorm.DB

见 P0-3，此处补充事务维度：consumer 用 `c.db.Transaction(func(tx *gorm.DB) error { tx.Create(...); tx.Updates(...) })` 直接操作 tx，**绕过 `DBRepositoryImpl.WithTransaction` 与 `GormTransactionImpl`**。事务边界不受 Application 层控制。

#### P1-2：VirtualBalanceService 跨存储混用

[game/infrastructure/persistence/redis/virtual_balance.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/virtual_balance.go) 同时持有 `*cRedis.Client` + `*mysql.RobotAccountRepository`，是 redis 包内**唯一**混用 MySQL 的文件，造成 `redis` 包 → `mysql` 包的反向依赖。

#### P1-3：algorithm 包依赖 infrastructure

[game/algorithm/packet_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go) import `game/infrastructure/persistence/redis`（用 RoomHashKey 等）；clean architecture 应只依赖 domain 接口。

#### P1-4：RobotAccountService 依赖具体仓储

[game/application/robot_account_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_account_service.go) 直接 import `mysql.RobotAccountRepository`（具体 struct，非接口）；其他 service 都通过 `domain.DBRepository` 抽象。

#### P1-5：仓储接口返回类型不一致

- 返回接口：`gormRoomRepository` / `gormSessionRepository` / `gormRoundRepository` / `gormHistoryRepository` / `gormRoomConfigRepository` → 返回 `domain.*DBRepository`
- 返回具体：`RobotAccountRepository` → `*RobotAccountRepository`；`GormUserRepository` → `*GormUserRepository`；`RoomRepository` → `*RoomRepository`

具体返回阻断 mock，违反依赖反转。

#### P1-6：settlement 事务边界错位

[settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) 的 `m.db.WithContext(ctx).Transaction(...)` 在 Repository 层开事务；但 project memory 明确要求"Transaction boundaries must be placed at the Application layer via `dbRepo.WithTransaction`"。当前 settlement **没有 Application 层**，service 直接调 service。

### 2.4 P1 — 重复与散落

#### P1-7：默认值散落 5 处

- [common/config/types.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/types.go)（479 行，所有 config 类型堆在一文件）
- [game/config/defaults.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/config/defaults.go)（委托 commonconfig）
- [game/config/rate_limiter.go:48-55](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/config/rate_limiter.go)（硬编码命令限流默认值）
- [game/algorithm/config.go:28-33](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/config.go)（`DefaultConfig()`）
- [game/scheduler/timeout_scheduler.go:57-96](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go)（硬编码 fallback 时长）

#### P1-8：common/utils 是"common 地狱"

[common/utils/utils.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/utils/utils.go) 混合：网关特定（`GenerateConnID`/`GetClientIP`）、游戏特定（`GenerateRoomID`/`GetRandomAvatar`）、结算特定（`GenerateOrderNo`）、通用工具（`NowMillis`/`ContainsX`/`MinInt64`/`RandomInt64`/`ShuffleInt64`/`CryptoRandPerm`）。

#### P1-9：common/message 包含领域特定结构

- `payload.go` 含红包游戏 push 结构（`RoundStartPush`/`PenaltyPush` 等）—— 应在 game/domain
- `errors.go` 316 行含游戏错误码 + 西语消息 —— 西语应在 i18n 包
- `request.go`/`response.go` 是 gateway 协议 —— 应在 gateway/protocol

#### P1-10：common/config/types.go 479 行混堆

应按域拆分：`gateway.go`/`timeout.go`/`robot.go`/`settlement.go`/`idgen.go`/`lock.go`/`redis_ttl.go`/`platform.go`/`broadcast.go`，对齐已存在的 `log.go`/`mysql.go`/`redis.go`/`server.go`/`nacos.go` 风格。

#### P1-11：RoomEventConsumer 6 个 handler 全调 syncRoomCounts

[game/infrastructure/messaging/room_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/room_event_consumer.go) 6 个事件 handler 全部调用 `syncRoomCounts`，dispatch 似乎过度设计。

#### P1-12：refundSvc / billMgr / redis 字段"死依赖"

- `GameAppService.refundSvc` 注入但未在 game_app_service.go 中调用
- `HistoryService.billMgr` 注入但未在 history_service.go 中调用
- `RobotBehaviorEngine.redis` 字段未使用

#### P1-13：Lua 脚本错误码协议不一致

多数脚本 `0=success`，但 `luaTryStartGame`/`luaPlayerReady`/`luaHandleSeatTimeout` 用 `1=success`、`0=failure`，虽有注释但与全局约定冲突。

### 2.5 P1 — 测试缺口

#### P1-14：资金相关核心文件零测试

| 文件 | 行数 | 职责 | 测试 |
|---|---|---|---|
| `game/algorithm/packet_generator.go` | 273 | 红包生成编排 | **无** |
| `game/algorithm/reward_controller.go` | 213 | 奖励判定 | **无** |
| `game/algorithm/leopard.go` | 59 | 豹子生成 | **无** |
| `game/scheduler/timeout_scheduler.go` | 305 | 超时调度 | **无** |
| `game/application/game_app_service.go` | 1812 | God Service | **无** |
| `settlement/service/settlement_service.go` | 468 | 结算 facade | **无** |
| `settlement/service/deduct_service.go` | 587 | 扣款 | **无** |
| `settlement/service/game_settle_service.go` | 491 | 派奖 | **无** |
| `settlement/service/bill_manager.go` | 722 | God Repository | **无** |

仅 `straight.go`/`straight_test.go`、`limiter_test.go`、`scheduler base`、`async/task_runner_test.go`、`auth_test.go`、`generic_service_test.go`（仅 checkRateLimit）有测试。

#### P1-15：Mockability 低

- `*TaskRunner`、`*cRedis.Client`、`*kafka.Producer`、`*nacos.Client`、`signature.Signer` 均为具体类型，无接口
- 调用方无法 mock，单测必须用 `miniredis` 或 `go-sqlmock`
- 部分仓储返回具体类型（P1-4），无法替换实现

#### P1-16：集成测试缺失

无端到端"发包→抢包→结算→入账"集成测试用例。

### 2.6 P1 — 错误处理

#### P1-17：creditSessionPayouts 错误吞没

[settlement/service/game_settle_service.go:344-346](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) `creditSessionPayouts` 内部循环 `creditSessionPayout` 失败仅 log，不返回 error，最后 `return nil`；违反 project memory 明确禁止。

#### P1-18：commission 失败吞没

[settlement/service/settlement_service.go:140](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_service.go) `creditRound` 内 commission 失败仅 log，结算继续；commission bill 永久丢失。

#### P1-19：RobotChecker fail-open

[settlement/service/robot_checker.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/robot_checker.go) Redis 错误时 `IsRobot` 返回 `false`，导致真实玩家资金可能走虚拟钱包路径。应 fail-closed。

#### P1-20：ParseAmount 失败回退为 balance=0

`deduct_service.go:300-314`、`credit_retry_service.go`、`game_settle_service.go` 多处 `platform.ParseAmount` 失败时把 bill 标记 Success 且 balance=0，平台实际可能已扣款但本地余额显示 0。

#### P1-21：PlatformCallManager.UpdateLog 误用 retry_count

[settlement/service/platform_call_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/platform_call_manager.go) `UpdateLog` 总是自增 retry_count，即使首次成功调用也 +1，语义错位。

### 2.7 P2 — 安全与资金

#### P2-1：signature.Signer 用字符串相等比较

[common/signature/signer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/signature/signer.go) `VerifyGET`/`VerifyPOST` 用 `sign == expectedSign`，存在时序攻击风险，应用 `hmac.Equal`。

#### P2-2：gateway CORS 允许 `*`

生产环境安全风险。

#### P2-3：gateway 签名无 timestamp 窗口 + nonce 防重放

[middleware/signature.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/signature.go) 仅校验签名本身，无 timestamp 有效期、无 nonce 去重，存在重放攻击风险。

#### P2-4：GenerateRoomID 非雪花

[common/utils/utils.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/utils/utils.go) `GenerateRoomID = timestamp*1000000 + roomType*10000 + random`，高并发下碰撞风险，且可预测。应用 `idgen`。

### 2.8 P2 — 其他

#### P2-5：scheduler 硬编码魔法数

- `credit_retry_scheduler.go` limit=100
- `game_settle_timeout_scheduler.go` 1h + 100
- `settlement_check_scheduler.go` 5min / 10min
- `refund_process_scheduler.go` offset/limit

违反 project memory "Scheduler intervals/timeouts/InitialDelay MUST be set via configuration files"。

#### P2-6：duplicate 方法

`timeout_scheduler.go` `ClearAllTimeouts` (L190) 与 `ClearAllRoomTimeouts` (L196) 实现完全相同。

#### P2-7：dead 字段

- `packet_generator.go:22` `db *gorm.DB` 未使用
- `robot_behavior.go:31` `redis *cRedis.Client` 未使用

#### P2-8：i18n 硬编码

[common/message/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/errors.go) 316 行含西语消息 map，应移到 i18n 包。

#### P2-9：lock.WithRedisLock 忽略 client 参数

[common/lock/distributed_lock.go:154](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/distributed_lock.go) 签名含 `client *cRedis.Client` 但实现用全局 `redsyncClient`，参数误导。

#### P2-10：kafka.Consumer.Start 用 time.Sleep

`fetch error → time.Sleep(time.Second)` 应改 `select { case <-ctx.Done(): ...; case <-time.After(...) }` 保证关停响应。

---

## 3. 重构目标

### 3.1 终态画像

> 任何开发人员：**10 分钟了解整体架构，30 分钟了解业务，1 小时即可开始开发**。

| 维度 | 目标 |
|---|---|
| 结构可读性 | 目录即文档；看到目录即知职责 |
| 业务入口可见 | 一眼定位"发红包"业务入口 |
| 调用链清晰 | 一眼追完"前端 WS → gateway → game → settlement → platform" |
| 模块边界稳定 | game / settlement / wallet 三领域零类型互相 import |
| 依赖单向 | domain ← application ← infrastructure ← bootstrap；无循环 |
| 事件流统一 | Domain Event + Integration Event 分离；同步异步清晰 |
| 状态流可追 | RoomStatus / GamePhase / BillStatus / RoundStatus 状态机一张图 |
| 测试可达 | 核心 Application Service + Domain Service 100% 可 mock 单测 |

### 3.2 量化指标

| 指标 | 当前 | 目标 |
|---|---|---|
| 单文件最大行数 | 1812 (GameAppService) | ≤ 500 |
| God Service 内方法数 | 30 (GameAppService) | ≤ 10 |
| 跨领域 import 数 | 5+ (settlement→game, redis→mysql, algorithm→infra) | 0 |
| 重复常量定义数 | 3 (RewardType) | 1 |
| 核心资金文件零测试数 | 9 | 0 |
| 循环依赖 breaker 数 | 6 | ≤ 2（仅保留必要的回调） |

---

## 4. 重构原则

### 4.1 必须遵循

- **SOLID**（特别是 SRP 与 DIP）
- **DRY**（消除 §2.4 重复）
- **KISS**（不过度设计，按业务规模选层）
- **YAGNI**（不实现未明确需求）
- **High Cohesion + Low Coupling**
- **Composition over Inheritance**
- **Dependency Injection**（构造器注入，禁字段赋值）
- **Interface Segregation**（拆胖接口，如 EventPublisher）

### 4.2 合理借鉴

- **DDD**：聚合根、值对象、Domain Service、Domain Event；不生搬硬套 Repository/Factory 全套
- **Clean Architecture**：依赖反转、用例隔离；不强制 Use Case 类（Application Service 已够）
- **CQRS**：读写分离已有雏形（HistoryService vs GameAppService），显式化即可

### 4.3 反原则（禁止）

- 为模式而模式（不强制每层一个 Repository，简单 CRUD 可省）
- 为重构而重构（保留 S1-S12 优秀实践）
- 过度抽象（不引入 5 层以上的接口嵌套）
- 业务迁就架构（架构服务于业务）

---

## 5. 推荐整体架构

### 5.1 架构选型

**推荐：分层 + 模块化 DDD（Package-Level DDD）**

- 不是全量 DDD（Aggregate Root + Bounded Context 重型化对当前规模过度）
- 不是纯三层（业务逻辑无处安放，会回退到 God Service）
- 不是 Clean Architecture 全套（Use Case 类与 Application Service 重复）

### 5.2 分层模型

```
┌──────────────────────────────────────────────────────────────┐
│ Interface Layer     │ server (gRPC) / handler (HTTP)         │
│                     │ router / middleware / dto              │
├──────────────────────────────────────────────────────────────┤
│ Application Layer   │ AppService（用例编排）                 │
│                     │ AsyncTaskRunner / EventPublisher     │
├──────────────────────────────────────────────────────────────┤
│ Domain Layer        │ 聚合根 / 值对象 / Domain Service      │
│                     │ Repository Interface / Domain Event   │
├──────────────────────────────────────────────────────────────┤
│ Infrastructure     │ persistence/mysql, persistence/redis  │
│ Layer              │ messaging (kafka) / broadcast          │
│                    │ platform client / external adapters   │
├──────────────────────────────────────────────────────────────┤
│ Bootstrap           │ container / app lifecycle / config    │
└──────────────────────────────────────────────────────────────┘
                            ▲
                            │
                   Shared Kernel:
              common/{redis,rediskeys,kafka,lock,
                  idgen,trace,logger,scheduler,
                  async,broadcast,message,currency,
                  strutil,converter,config,limiter,
                  signature,mysql,nacos,discovery}
```

### 5.3 领域划分（Bounded Context）

| 领域 | 当前归属 | 推荐归属 | 边界 |
|---|---|---|---|
| Room（房间生命周期） | game | game | 房间创建、座位、队列、状态机 |
| Round（单局） | game | game | 发包、抢包、结算触发 |
| Robot（机器人） | game | game | 账户、行为、调度 |
| Packet Algorithm | game/algorithm | game/algorithm | 红包生成、奖励判定 |
| Settlement | settlement | settlement（独立） | 账单、扣款、派奖、退款、对账 |
| Wallet（虚拟余额） | game + settlement 共享 | **settlement**（统一） | 机器人虚拟钱包 |
| Account（用户） | game | game | 用户档案、机器人识别 |
| Wallet（外部钱包） | api/platform | api/platform | GamingPanda RPC |
| Stats | stats | stats（独立） | 只读聚合 |
| Gateway | gateway | gateway（独立） | WS 网关 |

### 5.4 关键架构决策

| 决策 | 选择 | 理由 |
|---|---|---|
| 事务边界 | Application 层 via `dbRepo.WithTransaction` | 短事务原则；RPC/Kafka 在事务外；幂等+重试保证最终一致性 |
| 领域事件 | Domain Event（内存） + Integration Event（Kafka）分离 | Domain Event 同步发布用于解耦；Integration Event 异步用于跨服务 |
| 读写分离 | 隐式 CQRS：Query Service 直接读 Repository，Command 走 AppService | 当前规模不需要 CQRS 全套 |
| 缓存策略 | Cache-Aside + 显式 CacheService 接口 | 业务层不直接操作 Redis 客户端 |
| 异步任务 | AsyncTaskRunner 统一管理 | 已实现，保留 |
| 调度 | SchedulerRegistry + BaseScheduler | 已实现，保留 |

---

## 6. 推荐项目目录

### 6.1 backend/ 根目录

```
backend/
├── cmd/                      # 各服务入口
│   ├── game/main.go
│   ├── gateway/main.go
│   └── stats/main.go
├── game/                     # 游戏核心服务
├── settlement/               # 结算服务（与 game 同进程，独立领域）
├── gateway/                  # WebSocket 网关
├── stats/                    # 统计只读服务
├── api/                      # 外部 API 客户端
│   └── platform/             # 钱包 RPC 客户端
├── proto/                    # gRPC proto 定义
├── common/                   # Shared Kernel（跨域共享）
├── migrations/               # SQL 迁移
├── config/                   # 各服务 YAML 配置
├── deploy/                   # 部署脚本
├── scripts/                  # 一次性脚本
└── go.mod
```

### 6.2 game/ 推荐目录

```
game/
├── cmd/                      # 占位（实际入口在 backend/cmd/game）
├── bootstrap/                # 应用启动与生命周期
│   ├── app.go
│   ├── container.go
│   ├── config_listener.go
│   ├── mq_helper.go
│   └── nacos_helper.go
├── config/                   # 配置类型与加载
│   ├── config.go
│   ├── defaults.go
│   ├── algorithm.go
│   ├── nacos.go
│   └── rate_limiter.go
├── server/                   # Interface 层：gRPC
│   ├── generic_service.go
│   └── generic_service_test.go
├── application/              # Application 层：用例编排
│   ├── room_app_service.go
│   ├── seat_app_service.go
│   ├── packet_orchestrator.go     # 新：从 GameAppService 拆出
│   ├── round_settlement_service.go # 新：settleRound + Lua 解析
│   ├── game_lifecycle_service.go  # 新：StartGame/EndGame/Resume
│   ├── grab_service.go
│   ├── penalty_service.go
│   ├── history_service.go
│   ├── history_dto.go
│   ├── user_service.go
│   ├── room_state.go
│   ├── game_event_handler.go      # 新：从 consumer 抽出
│   ├── robot/                     # 机器人子模块
│   │   ├── account_service.go
│   │   ├── player.go
│   │   ├── behavior_engine.go
│   │   ├── scheduler_service.go
│   │   └── config.go
└── domain/                   # Domain 层
    ├── room/                 # 聚合根 + 值对象
    │   ├── room.go
    │   ├── player.go
    │   ├── spectator.go
    │   ├── queue.go
    │   └── status.go
    ├── round/                # 单局聚合
    │   ├── round.go
    │   ├── packet.go
    │   └── phase.go
    ├── game/                 # 游戏会话聚合
    │   ├── session.go
    │   └── game_state.go
    ├── reward/               # 奖励值对象（单一真理源）
    │   └── reward.go
    ├── events/               # Domain Event + Integration Event
    │   ├── room_event.go
    │   ├── game_event.go
    │   └── publisher.go      # 拆为 RoomEventPublisher + GameEventPublisher
    ├── repository/           # Repository 接口
    │   ├── room_repo.go
    │   ├── round_repo.go
    │   ├── session_repo.go
    │   ├── user_repo.go
    │   ├── history_repo.go
    │   └── transaction.go
    ├── errors.go
    └── lua_codes.go
infrastructure/                # Infrastructure 层
├── persistence/
│   ├── mysql/                 # 全部返回接口
│   │   ├── db_repository.go
│   │   ├── transaction.go
│   │   ├── room_repository.go
│   │   ├── round_repository.go
│   │   ├── session_repository.go
│   │   ├── user_repository.go
│   │   ├── history_repository.go
│   │   ├── room_config_repository.go
│   │   └── robot_account_repository.go
│   └── redis/                 # 不再混用 MySQL
│       ├── room_repository.go
│       ├── robot_pool.go
│       ├── robot_scheduler.go
│       └── scripts/
│           ├── registry.go
│           ├── room_seat.lua.go
│           ├── packet.lua.go
│           ├── queue.lua.go
│           ├── round.lua.go
│           └── penalty.lua.go
├── messaging/
│   ├── room_event_consumer.go   # 仅做：解析 → 调 handler
│   ├── room_event_publisher.go
│   ├── game_event_consumer.go   # 仅做：解析 → 调 handler
│   └── game_event_publisher.go
├── broadcast/
│   └── broadcaster.go
└── adapter/                     # 跨领域适配器
    └── user_saver_adapter.go    # settlement → game model 转换
algorithm/                       # 纯算法（只依赖 domain 接口）
├── packet_generator.go
├── reward_controller.go
├── straight.go
├── leopard.go
├── config.go
├── model.go
├── errors.go
└── straight_test.go
scheduler/                       # 调度器
├── timeout_scheduler.go
└── virtual_balance_sync.go      # 调 settlement 接口，不直接持有 Redis+MySQL
model/                           # GORM 实体（仅 ORM 映射，无业务）
├── room.go
├── round.go
├── session.go
├── user.go
├── packet.go
├── reward.go                    # 引用 domain/reward 常量
├── robot_account.go
└── config.go
```

### 6.3 settlement/ 推荐目录

```
settlement/
├── config/
├── dto/
├── model/
├── application/              # 新增 Application 层
│   ├── settle_app_service.go     # 用例编排：SettleRound/SettleGame
│   └── refund_app_service.go
├── domain/                  # 新增 Domain 层
│   ├── bill.go              # 聚合根
│   ├── round_settlement.go
│   ├── refund.go
│   ├── exception.go
│   ├── platform_user.go     # 新：替代 gameModel.User
│   ├── events/
│   ├── repository/
│   │   ├── bill_repo.go
│   │   ├── round_settlement_repo.go
│   │   ├── refund_repo.go
│   │   ├── exception_repo.go
│   │   └── transaction.go
│   ├── errors.go
│   └── policy/              # 业务规则
│       └── reward_policy.go
├── infrastructure/
│   ├── persistence/
│   │   ├── bill_repository.go
│   │   ├── round_settlement_repository.go
│   │   ├── refund_repository.go
│   │   ├── exception_repository.go
│   │   └── transaction.go
│   └── platform/            # platform client adapter
├── service/                 # Domain Service（无事务编排）
│   ├── deduct_service.go
│   ├── credit_retry_service.go
│   ├── game_settle_service.go
│   ├── refund_service.go
│   ├── reward_settler.go
│   ├── balance_service.go
│   ├── settlement_check_service.go
│   ├── exception_manager.go
│   ├── platform_call_manager.go
│   ├── robot_checker.go
│   ├── trace_id_generator.go
│   ├── user_id_convert_service.go
│   └── virtual_balance_service.go  # 唯一虚拟余额真理源
└── scheduler/
```

### 6.4 common/ 推荐调整

```
common/
├── async/
├── broadcast/
├── config/
│   ├── loader.go
│   ├── log.go
│   ├── mysql.go
│   ├── nacos.go
│   ├── redis.go
│   ├── server.go
│   ├── types.go         # 拆分（见 §17）
│   ├── gateway.go       # 新
│   ├── timeout.go       # 新
│   ├── robot.go         # 新
│   ├── settlement.go   # 新
│   ├── idgen.go         # 新
│   ├── lock.go          # 新
│   ├── redis_ttl.go     # 新
│   ├── platform.go      # 新
│   └── broadcast.go     # 新
├── converter/
├── currency/
├── discovery/
│   └── # UserSaverAdapter 移到 game/infrastructure/adapter
├── idgen/
├── i18n/                # 新：从 message/errors.go 抽出
│   └── messages_es.go
├── kafka/
├── limiter/
├── lock/
├── logger/
├── message/
│   ├── header.go
│   ├── broadcast.go
│   ├── push.go
│   ├── request.go       # 考虑移到 gateway/protocol
│   ├── response.go      # 考虑移到 gateway/protocol
│   ├── types.go         # 仅保留通用常量
│   └── errors.go        # 仅保留 Error 类型与构造，消息移到 i18n
├── mysql/
├── nacos/
├── redis/
├── rediskeys/
├── scheduler/
├── signature/
├── strutil/
├── trace/
└── utils/
    └── # 仅保留通用工具；GenerateConnID/GetClientIP/GenerateRoomID/GenerateOrderNo/GetRandomAvatar/IsValidPlatform 移到对应领域
```

---

## 7. 推荐模块划分

### 7.1 模块分类矩阵

| 模块 | 类别 | 职责 | 依赖方向 |
|---|---|---|---|
| `game/domain/room` | Core Domain | 房间聚合 | ← application |
| `game/domain/round` | Core Domain | 单局聚合 | ← application |
| `game/domain/game` | Core Domain | 会话聚合 | ← application |
| `game/domain/reward` | Core Domain | 奖励值对象 | ← application, algorithm |
| `game/domain/events` | Core Domain | 领域事件 | ← application |
| `game/domain/repository` | Core Domain | 仓储接口 | ← application, infrastructure(impl) |
| `game/algorithm` | Core Domain | 算法纯逻辑 | ← application（仅依赖 domain 接口） |
| `game/application` | Application | 用例编排 | ← domain, infrastructure(interfaces) |
| `game/infrastructure/*` | Infrastructure | 适配器实现 | ← domain(interfaces) |
| `game/bootstrap` | Bootstrap | 装配 | ← application, infrastructure |
| `game/server` | Interface | gRPC handler | ← application |
| `game/scheduler` | Infrastructure | 定时任务 | ← application |
| `settlement/*` | Core Domain（独立） | 账务 | ← game/application（仅通过 DTO） |
| `api/platform` | Infrastructure | 外部钱包 RPC | ← settlement |
| `gateway/*` | Interface | WS 网关 | ← proto, discovery |
| `stats/*` | Interface + Read | 只读统计 | ← mysql |
| `common/*` | Shared Kernel | 跨域共享 | （被所有领域依赖） |

### 7.2 依赖规则矩阵

```
           ┌────────────────────────────────────────┐
           │ 依赖方 \ 被依赖方                      │
           ├────────────────────────────────────────┤
Interface  │ → Application → Domain → Infrastructure│
Application│ → Domain, 其他 Application(同域), Common │
Domain     │ → Common(仅 rediskeys/trace/errors)   │
Infra      │ → Domain(实现接口), Common             │
Bootstrap  │ → Application, Infra, Common, Config   │
Common     │ → Common(内部单向), 不依赖任何领域     │
```

### 7.3 跨模块调用规则

- game → settlement：**允许**，通过 DTO（不可 import settlement/model）
- settlement → game：**禁止**，通过 `PlatformUser` 抽象（game 提供 adapter）
- game/settlement/gateway → common：**允许**
- common → 任何领域：**禁止**
- infrastructure → infrastructure（同域）：**允许**（如 redis/scripts → redis/client）
- infrastructure（甲域）→ infrastructure（乙域）：**禁止**

---

## 8. 核心对象关系

### 8.1 核心领域对象

```
Room (聚合根)
 ├─ RoomID (值对象)
 ├─ RoomStatus {Idle, Waiting, Playing, Interrupted}
 ├─ Player[] (聚合内实体)
 │   ├─ UserID, Nickname, Avatar
 │   ├─ SeatNo
 │   └─ IsRobot
 ├─ Spectator[] (聚合内实体)
 ├─ QueueInfo[] (值对象)
 └─ RoomMeta (值对象)

GameSession (聚合根)
 ├─ SessionID
 ├─ RoomID (引用)
 ├─ Status {0,1,2}
 ├─ MaxRounds, ActualRounds
 └─ SessionPlayer[]

Round (聚合根)
 ├─ RoundID, RoundNo
 ├─ SessionID (引用)
 ├─ SenderID, SenderType
 ├─ TotalAmount, Commission
 ├─ Status {Creating, Deducting, Deducted, Settling, Success, Partial, Failed, Credited}
 └─ Packet[] (实体)
      ├─ PacketID
      ├─ Amount, Position
      └─ GrabbedBy

GameState (值对象)
 ├─ Phase {Waiting, Countdown, RoundStart, Grabbing, Settling, WaitSend, GameEnd}
 ├─ CurrentRound, MaxRounds
 └─ NextSenderID

BillRecord (聚合根, settlement 域)
 ├─ BillID, BizOrderNo
 ├─ BillType (枚举：FirstRoundDeduct, GrabPacket, ...)
 ├─ Status {Processing, Success, Failed, Refunded}
 ├─ Amount
 ├─ UserID, PlatformUserID
 ├─ RoundTraceID
 └─ RefundStatus

RoundSettlement (聚合根, settlement 域)
 ├─ RoundID, SessionID
 ├─ RoundTraceID (唯一)
 ├─ Status {None, Deducting, Deducted, Settling, Success, Partial, Failed, Credited}
 └─ DeductInfo
```

### 8.2 对象生命周期

| 对象 | 创建 | 状态变迁 | 销毁 |
|---|---|---|---|
| Room | 系统初始化 `InitRoom` | Idle → Waiting → Playing → Interrupted → Waiting | 不销毁 |
| Player | `SelectSeat` Lua | 加入 → 离线 → 重新上线 → 离开 | `LeaveRoom` |
| GameSession | `StartGame` | 0 → 1(进行中) → 2(结束) | 不销毁（历史） |
| Round | `sendPacketPipeline` | Creating → Deducting → Deducted → Settling → Credited | 不销毁（历史） |
| Packet | `SendPacket` Lua | 可用 → 已抢 | 不销毁（历史） |
| BillRecord | `CreateBill` | Processing → Success / Failed → Refunded | 不销毁 |
| RoundSettlement | `CreateRoundSettlementAndBills` | Deducting → Deducted → Settling → Credited | 不销毁 |

### 8.3 单一职责审视

| 对象 | 当前问题 | 重构后 |
|---|---|---|
| GameAppService | 7 职责混合 | 拆 4 个 Service |
| SettlementService | 10+ 方法 facade | 拆 3 个 Service |
| BillManager | 4 仓储合一 | 拆 4 个 Repository |
| GameEventConsumer | 业务逻辑寄生 | 仅做解析 + 调 handler |

---

## 9. 核心业务流程

### 9.1 红包业务总览

```
玩家进入房间 → 选座 → 全员 Ready → 倒计时 → 开始游戏(创建 Session)
   → 第 1 轮：首位发红包者发送 → 扣首轮流水 → 抢红包(全员) → 结算单局
   → 第 2 轮：最小抢者发包 → 扣后轮流水 → 抢 → 结算
   → ... 循环到 MaxRounds
   → 游戏结束：派奖、上报平台、清理座位
```

### 9.2 发红包流程（详）

```
玩家 WS 发送 send_packet 命令
   ↓
Gateway 路由 → gRPC Forward → game/server/GenericServiceServer.Forward
   ↓ rate limit (financial_fail_open)
   ↓
GameAppService.SendPacket
   ↓ WithRedisLock(SendPacketLock)
   ↓ 房间状态校验 (Waiting/Playing, phase=WaitSend)
   ↓ RewardController.DetermineRewardType (决定是否触发 Straight/Leopard)
   ↓ PacketGenerator.Generate (crypto/rand 拆分金额)
   ↓ GrabService.InitRoundPackets (Lua 初始化 round 状态)
   ↓ DeductService.DeductForFirstRound / DeductForLaterRound
   ↓     ├─ platform.Debit RPC
   ↓     └─ BillManager.CreateBillsInTransaction
   ↓ TimeoutScheduler.SetTimeout(Grab, ...)
   ↓ 广播 PacketCreated 事件
   ↓ 异步: publish_packet_created (Kafka)
```

### 9.3 抢红包流程（详）

```
玩家 WS 发送 grab_packet 命令
   ↓ rate limit (grab_fail_open)
   ↓
GameAppService.GrabPacket
   ↓ GrabService.GrabPacket (Lua 原子抢)
   ↓     ├─ 校验 phase=GRABBING
   ↓     ├─ 校验未抢过
   ↓     ├─ DEL availableKey, SET userGrabKey
   ↓     └─ 若最后一个：phase → SETTLING
   ↓ 广播抢包结果
   ↓ 若 phase == SETTLING:
   ↓     异步: settle_round (TaskRunner)
   ↓         ├─ SettleRound Lua (计算输家、累计 totals、判定 game end)
   ↓         ├─ SettlementService.SettleRound
   ↓         │   ├─ creditRound (写 grab bills)
   ↓         │   ├─ settleCommission (写 commission bill)
   ↓         │   ├─ RewardSettler.SettleReward (若触发奖励)
   ↓         │   └─ UpdateRoundSettlementCredited
   ↓         └─ publish_round_settle (Kafka)
```

### 9.4 退款流程

```
首轮流水失败 → SettlementCheckService 检测
   ↓ RefundService.ApplyForRefund (创建 RefundAudit, status=Pending)
   ↓ RefundProcessScheduler 定时扫描 Pending
   ↓ 若 RefundTypeFirstRoundFail → 自动 Approve
   ↓ RefundService.ApproveRefund
   ↓     ├─ UpdateRefundAuditToProcessing
   ↓     ├─ platform.Credit RPC (退款到玩家钱包)
   ↓     └─ UpdateRefundSuccessInTransaction (RefundAudit + BillRecord 原子更新)
```

### 9.5 异步事件流

```
Application 层发布 Domain Event
   ↓ 同步: 直接调用 handler (解耦内部模块)
   ↓
Application 层发布 Integration Event (Kafka)
   ├─ TopicGameEvents (Key=roomID:sessionID)
   │   └─ GameEventConsumer 消费
   │       ├─ handleSessionStart → 创建 GameSession/SessionPlayer
   │       ├─ handlePacketCreated → 创建 Round/Packet 记录
   │       ├─ handleRoundSettle → 触发结算 (调 SettlementService)
   │       └─ handleSessionEnd → 触发派奖 (调 GameSettleService)
   └─ TopicRoomEvents (Key=roomID)
       └─ RoomEventConsumer 消费
           └─ syncRoomCounts → 同步 player_count/spectator_count 到 MySQL
```

### 9.6 文字时序图：发红包到结算

```
[Client] --WS send_packet--> [Gateway] --gRPC--> [GameServer.Forward]
[GameServer] --rate limit--> [GameAppService.SendPacket]
[GameAppService] --lock--> [RewardController.DetermineRewardType]
[GameAppService] --call--> [PacketGenerator.Generate] (crypto/rand)
[GameAppService] --call--> [GrabService.InitRoundPackets] (Lua)
[GameAppService] --call--> [DeductService.DeductForFirstRound]
   [DeductService] --RPC--> [Platform.Debit]
   [DeductService] --tx--> [BillManager.CreateBillsInTransaction]
[GameAppService] --call--> [TimeoutScheduler.SetTimeout(Grab)]
[GameAppService] --async broadcast--> [Broadcaster.Broadcast]
[GameAppService] --async publish--> [GameEventPublisher.PublishPacketCreated]
   ↓ Kafka
   [GameEventConsumer] --handlePacketCreated--> DB (Round/Packet 落库)

... 抢包 ...

[GameAppService.GrabPacket] --last grab--> [SettleRound Lua]
[GameAppService] --async--> [SettlementService.SettleRound]
   [SettlementService] --creditRound--> [BillManager.CreateBills]
   [SettlementService] --settleCommission--> [BillManager]
   [SettlementService] --call--> [RewardSettler.SettleReward] (若有奖励)
   [SettlementService] --update--> [UpdateRoundSettlementCredited]
[GameAppService] --async publish--> [PublishRoundSettle]
   ↓ Kafka
   [GameEventConsumer.handleRoundSettle] --call--> [SettlementService.SettleGame] (若游戏结束)
       [SettlementService] --call--> [GameSettleService.SettleGame]
           [GameSettleService] --RPC--> [Platform.Settle] (上报结果)
           [GameSettleService] --RPC--> [Platform.Credit] (派奖)
```

---

## 10. 调用链

### 10.1 模块依赖图

```
                    ┌────────────┐
                    │  Gateway   │
                    └─────┬──────┘
                          │ gRPC
                          ▼
                    ┌────────────┐
                    │ GameServer │ (Interface)
                    └─────┬──────┘
                          │
              ┌───────────┼───────────┐
              ▼           ▼           ▼
       ┌──────────┐ ┌──────────┐ ┌──────────┐
       │RoomAppSvc│ │GameAppSvc│ │SeatAppSvc│ (Application)
       └────┬─────┘ └────┬─────┘ └────┬─────┘
            │            │            │
            └────────────┼────────────┘
                         │
                         ▼
                  ┌─────────────┐
                  │  Domain     │
                  │  (Room/Round/Session/Events)
                  │  Repository Interfaces
                  └──────┬──────┘
                         │
                         ▼
                  ┌─────────────┐
                  │Infrastructure│
                  │  mysql/redis │
                  │  messaging   │
                  └──────┬──────┘
                         │
                         ▼
                  ┌─────────────┐
                  │Settlement   │ (sibling domain)
                  └──────┬──────┘
                         │
                         ▼
                  ┌─────────────┐
                  │Platform API │
                  │(GamingPanda)│
                  └─────────────┘
```

### 10.2 关键调用链（一句话版）

| 业务 | 调用链 |
|---|---|
| 进入房间 | WS → gateway → game.Forward → RoomAppService.JoinRoom → UserService.GetUserById → RoomRepository.JoinAsSpectator(Lua) → broadcast |
| 选座 | WS → ... → SeatAppService.SelectSeat → BalanceService.CheckBalanceForReady → RoomRepository.SelectSeat(Lua) → broadcast |
| 开始游戏 | SeatAppService.HandleReadyTimeout → GameAppService.StartGame → TryStartGame(Lua) → startGameCore → publish session_start |
| 发红包 | ... → GameAppService.SendPacket → PacketGenerator.Generate → GrabService.InitRoundPackets(Lua) → DeductService.DeductForFirstRound → broadcast |
| 抢红包 | ... → GameAppService.GrabPacket → GrabService.GrabPacket(Lua) → broadcast → 若 last: SettleRound(Lua) → SettlementService.SettleRound |
| 结束游戏 | SettleRound(Lua) 检测 roundNo >= maxRounds → endGameWithOptions → EndGame(Lua) → publish session_end → GameSettleService.SettleGame |
| 退款 | SettlementCheckService → RefundService.ApplyForRefund → RefundProcessScheduler → RefundService.ApproveRefund → platform.Credit |

---

## 11. 状态流

### 11.1 Room 状态机

```
            InitRoom
               ↓
           ┌────────┐  全员Ready           ┌─────────┐
           │  Idle  │ ──────────────────► │ Waiting │
           │   0    │                       │   1     │
           └────────┘                       └────┬────┘
                ▲                                │ StartGame
                │                                ▼
                │ EndGame           ┌─────────┐
                │ Interrupt ──────► │ Playing │
                │   4              │   2     │
                │                   └────┬────┘
                │                        │ 中断(玩家离开/超时)
                │                        ▼
                │                   ┌────────────┐
                │                   │ Interrupted│
                │                   │     4       │
                │                   └─────┬──────┘
                │                         │ ResumeGame
                │                         ▼
                └──────────────────► ┌─────────┐
                                    │ Waiting │
                                    └─────────┘
```

### 11.2 GamePhase 状态机

```
Waiting(1) ──全员Ready──► Countdown(2) ──倒计时结束──► RoundStart(3)
   ▲                                                  │
   │                                                  ▼
   │                                              Grabbing(4)
   │                                                  │
   │                                                  ▼  最后一个包
   │                                              Settling(5)
   │                                                  │
   │                                                  ▼
   │                                              WaitSend(6)
   │                                                  │
   │                                                  ▼  下一轮发包
   │                                              RoundStart(3)
   │                                                  │
   │                                                  ▼  最后一轮结算后
   └──────────────────────────────────────────────  GameEnd(7)
```

### 11.3 Round 状态机

```
Creating(0) ──deduct成功──► Deducted(2) ──settle──► Settling(3) ──credit完成──► Credited(6)
   │                            │                       │
   │                            │                       │
   └─deduct失败─► Failed(5)     └─settle失败─► Partial(4) ─► 重试 ─► Settling ─► Credited
```

### 11.4 Bill 状态机

```
Processing(0) ──RPC成功──► Success(1)
   │
   └─RPC失败──► Failed(2) ──refund──► Refunded(3)
```

### 11.5 Refund 状态机

```
None(0) ──ApplyForRefund──► Pending(1) ──Approve──► Processing(3) ──RPC成功──► Refunded(2)
                                  │                       │
                                  │                       └─RPC失败──► Pending(1) 重试
                                  └─Reject──► Rejected(5)
```

---

## 12. 事件流

### 12.1 事件分类

| 类型 | 范围 | 传输 | 用途 |
|---|---|---|---|
| Domain Event | 同进程 | 直接调用 | 解耦内部模块（如 RoundSettled → 通知 RewardService） |
| Integration Event | 跨服务 | Kafka | 跨模块通信（如 Game → Settlement） |
| Broadcast Event | 推客户端 | Kafka + WS | 推前端 UI 更新 |
| Push Message | 单用户 | WS | 个人消息 |

### 12.2 当前事件清单

**RoomEvent（Kafka TopicRoomEvents, Key=roomID）**：
- spectator_join / spectator_leave / seat_select / seat_cancel / player_ready / spectator_kick / player_reconnect / queue_join / queue_leave / substitute

**GameEvent（Kafka TopicGameEvents, Key=roomID:sessionID）**：
- session_start / packet_created / round_settle / session_end

**BroadcastMessage（Kafka TopicGatewayBroadcast 或 Redis Pub/Sub）**：
- TargetTypeRoom / TargetTypeUser → 推到 gateway → WS 推客户端

### 12.3 事件流图

```
Application Service 发布
   ├─ Domain Event (同步)
   │   └─ 直接调本进程 handler
   ├─ Integration Event (Kafka, 异步)
   │   ├─ RoomEventPublisher → TopicRoomEvents
   │   │   └─ RoomEventConsumer 消费 → syncRoomCounts
   │   └─ GameEventPublisher → TopicGameEvents
   │       └─ GameEventConsumer 消费 → application.GameEventHandler
   │           ├─ 创建 Session/Round/Packet 落库
   │           ├─ 调 SettlementService.SettleRound
   │           └─ 调 SettlementService.SettleGame
   └─ Broadcast (Kafka 或 Redis Pub/Sub)
       └─ gateway.BroadcastService 消费 → WS 推客户端
```

### 12.4 事件信封（保留当前 EventHeader）

```json
{
  "event_id": "uuid-v4",
  "trace_id": "tr_<snowflake>",
  "timestamp": 1735689600000,
  "version": 1,
  "event_type": "packet_created",
  "room_id": "room_xxx",
  "session_id": "sess_xxx",
  "payload": { ... }
}
```

---

## 13. Repository 设计

### 13.1 设计原则

1. **纯数据访问**：Repository 不含业务决策
2. **接口在 domain**：domain 定义 `XxxRepository` 接口，infrastructure 实现
3. **返回接口**：所有 `NewXxxRepository` 返回 domain 接口，不返回具体 struct
4. **存储单一**：一个 Repository 只访问一种存储（MySQL 或 Redis，不混用）
5. **事务在 Application 层**：Repository 不开事务，由 AppService 通过 `dbRepo.WithTransaction` 编排

### 13.2 推荐接口定义

```go
// domain/repository/room_repo.go
type RoomRepository interface {
    // Redis-backed（房间实时状态）
    GetRoomMeta(ctx, roomID) (*RoomMeta, error)
    GetPlayers(ctx, roomID) (map[string]*Player, error)
    SelectSeat(ctx, roomID, userID, seatNo, isRobot) error
    // ... 其他 Lua 操作
}

// domain/repository/room_db_repo.go
type RoomDBRepository interface {
    // MySQL-backed（房间配置与列表）
    GetRoom(ctx, roomID) (*model.Room, error)
    UpdateRoom(ctx, roomID, updates) error
    MatchRoomByBalance(ctx, balance) (string, error)
}

// domain/repository/transaction.go
type Transaction interface {
    RoomDBRepo() RoomDBRepository
    SessionDBRepo() SessionDBRepository
    UserDBRepo() UserDBRepository
    RoundDBRepo() RoundDBRepository
    // 注意：HistoryDBRepo / RoomConfigDBRepo 只读，不在事务内
}

type DBRepository interface {
    RoomDBRepo() RoomDBRepository
    SessionDBRepo() SessionDBRepository
    UserDBRepo() UserDBRepository
    RoundDBRepo() RoundDBRepository
    RoomConfigDBRepo() RoomConfigDBRepository
    HistoryDBRepo() HistoryDBRepository
    WithTransaction(ctx, fn func(tx Transaction) error) error
}
```

### 13.3 当前 Repository 纯度审视

| Repository | 当前问题 | 重构动作 |
|---|---|---|
| gormRoomRepository | MatchRoomByBalance 含排序业务规则 | 排序条件移到 Application 层构造 |
| gormHistoryRepository | SQL 含 bill_type 魔法数与 profit 公式 | bill_type 用常量；profit 公式考虑移到 service 层 |
| RobotAccountRepository | 返回具体 struct | 改返回 `domain.RobotAccountRepository` 接口 |
| GormUserRepository | 返回具体 struct | 改返回 `domain.UserDBRepository` 接口 |
| RoomRepository (Redis) | parseRoomMeta 含 `MaxSpectators=0→10` 默认值 | 默认值移到 domain 或 config |
| RoomRepository (Redis) | AutoSubstitute 吞错误码 | 错误码上抛，由 AppService 决定 |
| VirtualBalanceService | Redis + MySQL 混用 | 统一到 settlement 域，单一服务 |
| BillManager | God Repository | 拆 4 个 Repository（见 §14） |

---

## 14. Service 设计

### 14.1 拆分 GameAppService

```
原 GameAppService (1812 行, 19 依赖)
拆分为:

├── PacketOrchestrator (新, ~400 行)
│   职责: 发红包管线（sendPacketPipeline + 3 个 initRound 变体合一）
│   依赖: PacketGenerator, GrabService, DeductService, RoomRepository,
│         TimeoutScheduler, Broadcaster, EventPublisher, TaskRunner
│   方法: SendPacket, sendPacketPipeline, initRoundAndDeduct
│
├── RoundSettlementService (新, ~350 行)
│   职责: 单局结算（settleRound + Lua 结果解析）
│   依赖: GrabService, SettlementService, RewardSettler,
│         Broadcaster, EventPublisher, RoomRepository, TaskRunner
│   方法: SettleRound, parseSettleResult, distributeRewards
│
├── GameLifecycleService (新, ~300 行)
│   职责: 游戏开始/结束/恢复/超时
│   依赖: RoomRepository, SettlementService, GameEventPublisher,
│         TimeoutScheduler, Broadcaster, TaskRunner
│   方法: StartGame, endGameWithOptions, ResumeGame,
│         OnGrabTimeout, OnSendTimeout, OnReplaceTimeout
│
└── GameAppService (瘦身后, ~250 行)
    职责: GrabPacket 入口（编排 PacketOrchestrator 与 RoundSettlementService）
    依赖: PacketOrchestrator, RoundSettlementService, GameLifecycleService,
          GrabService, RoomRepository, Broadcaster, TaskRunner
    方法: GrabPacket, OnRobotGrabbed
```

### 14.2 拆分 SettlementService

```
原 SettlementService (468 行, 13 依赖)
拆分为:

├── RoundSettleService
│   方法: SettleRound (creditRound + settleCommission + RewardSettler)
│   依赖: BillRepository, RewardSettler, TraceIDGenerator
│
├── PenaltySettlementService
│   方法: DeductPenaltyToPlatform, DistributePenaltyFromPlatform
│   依赖: BillRepository, PlatformClient, TraceIDGenerator
│
└── BalanceQueryService
    方法: CheckBalance, GetUserBalance, GetPendingCredit
    依赖: PlatformClient, BalanceService
```

### 14.3 拆分 BillManager

```
原 BillManager (722 行, 30 方法)
拆为:

├── BillRepository (interface + impl)
│   方法: CreateBill, UpdateBillStatus, UpdateBillSuccess,
│         GetBillByTraceID, GetBillByID, ExistsByRoundAndType,
│         GetBillsByBatchID, GetBillsByTraceID, GetBillsByUserID,
│         GetBillsByRoundID, GetBillByRoundTypeAndUser
│
├── RoundSettlementRepository
│   方法: CreateRoundSettlementAndBills, UpdateRoundSettlementCredited,
│         UpdateRoundSettlementStatus, UpdateRoundSettlementDeductSuccess,
│         GetRoundSettlement, ExistsRoundSettlement
│
├── RefundAuditRepository
│   方法: CreateRefundAudit, UpdateRefundAuditStatus,
│         GetRefundAuditByOrderNo, GetRefundAuditByBillID,
│         UpdateRefundSuccessInTransaction, RejectRefundInTransaction
│
└── SettlementQueryRepository (只读)
    方法: AggregateBetBySession, AggregatePayOutBySession,
          GetFailedFirstRoundSettlements, GetDeductedButNotSettled,
          GetRetryableCredits, GetFailedGameSettlements, GetTimedOutGameSettlements
```

### 14.4 Application 层级

| 层 | 职责 | 示例 |
|---|---|---|
| Application Service | 用例编排 + 事务边界 | `PacketOrchestrator.SendPacket` |
| Domain Service | 跨聚合业务规则 | `RewardPolicy.Calculate` |
| Infrastructure Service | 技术关注点 | `PlatformCallManager.LogCall` |

### 14.5 6 个循环依赖 breaker 收敛

| 当前 breaker | 是否必要 | 重构后 |
|---|---|---|
| `GameAppService.SetRoomAppService` | 必要（kick 后触发 substitute） | 保留，但改为事件驱动（Domain Event: PlayerKicked → RoomAppService 监听） |
| `GameAppService.SetGameEndCallback` | 必要（通知 robot scheduler） | 保留，或改 Integration Event |
| `RoomAppService.SetResumeGameCallback` | 必要 | 保留，或改 Domain Event |
| `SeatAppService.SetRoomAppService` | 必要 | 保留，或改 Domain Event |
| `RobotPlayer.SetBehaviorEngine` | 必要（接口反转） | 保留（已是接口模式） |
| `RobotSchedulerService.SetBehaviorEngine` | 必要 | 保留 |

**目标**：保留必要的回调，引入 Domain Event 后可减少 2-3 个 breaker。

---

## 15. Redis 设计

### 15.1 Key 规范（保留 S1）

- **单一真理源**：[common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) 保留为唯一来源
- **命名**：`cashparty:` 前缀 + `:` 分隔符
- **常量**：`Key<Domain><Subtype>` + 尾随 `:`
- **工厂**：`<Domain><Subtype>Key(args...)`
- **禁直接拼接**：必须用 rediskeys 工厂函数

### 15.2 Lua 脚本规范（保留 S2）

- **注册**：必须 `cRedis.NewScript(name, src)`，禁止内联 `redis.Eval`
- **KEYS**：脚本通过 `KEYS[]` 接收完整 key，禁止在 Lua 内拼接前缀（动态 key 场景必须在 Go 侧构造）
- **ARGV**：TTL 必须经 ARGV 传入，禁止硬编码
- **随机数**：禁止 `math.random`，由 Go 侧传入
- **错误码**：业务脚本返回 `{code, ...}` + `MapLuaError`；通用脚本返回 `0/1` + 直接 `.Int()`
- **header 注释**：每个脚本必须有 return 语义说明 + key 模式与 rediskeys 常量映射

### 15.3 缓存职责分层

| 缓存类型 | 当前归属 | 推荐归属 | 说明 |
|---|---|---|---|
| 用户档案 cache-aside | UserService | game/application/UserService | 30min TTL |
| 房间状态 | RoomRepository (Redis) | 保留 | 房间实时态存 Redis |
| 房间配置 | MySQL | 保留 | 不缓存（低频读） |
| 虚拟余额 | game/redis + settlement/redis 双写 | settlement/VirtualBalanceService 唯一 | 统一 |
| 包/抢包记录 | Redis Lua 内 | 保留 | 抢包原子操作 |
| 玩家累计 | Redis Hash | 保留 | 实时累计 |
| 限流计数 | Redis ZSET | 保留 | 滑窗 |

### 15.4 当前问题修正

| 问题 | 修正 |
|---|---|
| BroadcastChannelGateway 硬编码 | 移到 rediskeys |
| VirtualBalanceService.Credit 非原子 | 用 Lua 脚本 `IncBy + SAdd` 一次原子 |
| RobotChecker fail-open | 改 fail-closed（Redis 故障返回 error） |
| fmt.Sscanf 解析 Redis 值 | 改 strconv.ParseInt |

---

## 16. MQ 设计

### 16.1 Kafka Topic 规划（保留 S10）

| Topic | Key | Producer | Consumer | 用途 |
|---|---|---|---|---|
| TopicRoomEvents | roomID | game/RoomEventPublisher | game/RoomEventConsumer | 房间事件（同步到 MySQL 计数） |
| TopicGameEvents | roomID:sessionID | game/GameEventPublisher | game/GameEventConsumer | 游戏事件（落库 + 触发结算） |
| TopicGatewayBroadcast | roomID 或 userID | game/Broadcaster | gateway/BroadcastService | 推前端 |

### 16.2 消费者 GroupID 规范

- 业务消费者：`{base}-{nodeID}`（如 `game-events-3`）—— 保证同实例消息顺序
- 广播消费者：`gateway-broadcast-{nodeID}` —— 每实例独立 group 保证全量接收

### 16.3 错误处理（保留当前规约）

- **fail-closed**：handler 失败不提交 offset，触发 Kafka 重试
- **指数退避**：`RetryBackoff * 2^attempt` + jitter
- **DLQ**：超过 `MaxRetries` 转 DLQ，提交 offset
- **DLQ 失败**：log + 提交 offset（避免毒消息阻塞）
- **幂等**：Redis SetNX + 业务层幂等（确定性 BizOrderNo + 乐观锁）

### 16.4 关停顺序（保留 S9）

```
1. taskRunner.Stop()       // 不接受新任务
2. cancel(appCtx)          // 通知所有 goroutine
3. container.Stop()        // 停止 schedulers
4. wait consumers (10s)    // 等 consumer goroutine 退出
5. wait taskRunner (30s)   // 等在飞任务完成
6. close Kafka consumers (5s/个)
7. close Kafka producer
8. close Redis
```

---

## 17. 配置设计

### 17.1 配置层级

```
环境变量（覆盖）
   ↓
Nacos 动态配置（运行时热更新）
   ↓
YAML 文件（启动加载）
   ↓
代码默认值（兜底）
```

### 17.2 配置分类

| 类型 | 内容 | 来源 |
|---|---|---|
| 静态 | DB 连接、Redis、Kafka broker、端口 | YAML + env |
| 半静态 | 限流阈值、超时时长、TTL | YAML + Nacos 可热更 |
| 动态 | 算法参数、奖励控制 | Nacos 热更 |
| 业务 | 房间费率、最大轮数 | DB（room_configs 表） |

### 17.3 当前问题与重构

| 问题 | 重构 |
|---|---|
| `common/config/types.go` 479 行混堆 | 按域拆分（gateway.go/timeout.go/robot.go/...） |
| 默认值散落 5 处 | 集中到 `defaults.go` + 各 config 类型的 `SetXxxDefaults` |
| 限流默认值硬编码 | 移到 YAML |
| scheduler 魔法数（100 limit, 1h timeout） | 加到 `SettlementSchedulerConfig` |
| timeout_scheduler 硬编码 30s handler timeout | 加到 `TimeoutConfig.HandlerTimeout` |
| leopard 10x 奖励倍数 | 移到 `RewardControlConfig` |

### 17.4 配置校验

- 启动时 `ValidateRobotConfig` 模式扩展到所有 config
- fail-fast：关键字段缺失直接 `logger.Fatal`
- 范围校验：概率 ∈ [0,1]、TTL > 0、并发数 > 0

---

## 18. 错误处理设计

### 18.1 错误分类

| 类型 | 示例 | 处理 |
|---|---|---|
| 业务错误 | 房间已满、余额不足 | 返回 `message.Error{Code, Msg}`，前端处理 |
| 校验错误 | 参数缺失 | 返回 `CodeBadRequest` |
| 系统错误 | DB 故障、Redis 不可用 | 返回 `CodeSystemError` + log.Error |
| 外部依赖错误 | Platform RPC 失败 | 重试 + DLQ + 告警 |

### 18.2 错误码规范

```
0          Success
1000-1999   房间/座位相关
2000-2999   游戏流程相关
3000-3999   钱包/余额相关
5000-5999   系统错误
6000-6999   算法错误
```

### 18.3 错误处理原则

1. **禁止吞没**：所有 error 必须处理（log 或 return）
2. **禁止 `if err == nil && x != nil`**：必须显式检查 err
3. **wrap with %w**：保留错误链
4. **日志结构化**：key-value pairs，含 traceID/roomID/userID
5. **fail-closed on funds**：资金相关 Redis 故障必须返回 error，不 fail-open
6. **乐观锁幂等**：`RowsAffected == 0` 视为已处理，不报错

### 18.4 当前错误处理问题修正

| 问题 | 修正 |
|---|---|
| creditSessionPayouts 吞 error | 返回 error，触发重试 |
| commission 失败吞 error | 返回 error，结算失败 |
| RobotChecker fail-open | 改 fail-closed |
| ParseAmount 失败标 Success balance=0 | 标 Failed + 异常记录 |
| UpdateLog 误用 retry_count | 区分 success_count 与 retry_count |

---

## 19. 日志设计

### 19.1 日志规范（保留当前）

- **库**：zap sugared logger
- **级别**：Debug / Info / Warn / Error / Fatal
- **结构化**：key-value，禁止 `fmt.Sprintf` 拼消息
- **context**：必带 `trace_id`，资金相关带 `room_id`/`user_id`/`round_id`/`bill_id`
- **英文消息**：third person past tense（如 `"failed to deduct balance"`）
- **不泄敏**：禁止日志打印 JWT、密码、完整银行卡号

### 19.2 日志级别使用

| 级别 | 用途 | 示例 |
|---|---|---|
| Debug | 开发调试 | 算法中间值 |
| Info | 业务关键节点 | "game started", "round settled" |
| Warn | 预期内失败 | 广播失败、Kafka 重试、限流触发 |
| Error | 意外失败 | DB 故障、RPC 失败、panic |
| Fatal | 不可恢复 | 配置加载失败、ID 生成器未初始化 |

### 19.3 日志归档

- 当前用 lumberjack 滚动，保留
- 建议：资金相关日志单独文件，便于审计

---

## 20. 包依赖关系

### 20.1 完整依赖图

```
                       cmd/game/main
                            │
                            ▼
                       bootstrap
                     ╱   │   ╲
                    ╱    │    ╲
        ┌─────────┐  ┌──────┐  ┌──────────────┐
        │application│  │config│  │infrastructure│
        └────┬─────┘  └──────┘  └──────┬───────┘
             │                          │
             ▼                          │
          domain ◄──────────────────────┘
             │
             ▼
          common (shared kernel)
   (rediskeys, trace, logger, async,
    kafka, lock, idgen, scheduler,
    broadcast, message, currency, ...)
```

### 20.2 关键依赖约束

| 包 | 允许依赖 | 禁止依赖 |
|---|---|---|
| `domain` | common（仅 rediskeys/trace/errors） | infrastructure, application, model |
| `application` | domain, common, infrastructure(仅接口) | model, *gorm.DB, *redis.Client |
| `infrastructure` | domain(实现接口), common, model | application |
| `algorithm` | domain(仅接口), common | infrastructure |
| `bootstrap` | 所有 | - |
| `common` | common(内部) | 任何领域包 |

### 20.3 当前违规修正

| 违规 | 修正 |
|---|---|
| algorithm → infrastructure/redis | algorithm 改依赖 domain 接口 |
| VirtualBalanceService(redis) → mysql | 移到 settlement 统一 |
| settlement → game/model | 用 PlatformUser 抽象 |
| GameEventConsumer 持有 *gorm.DB | 走 dbRepo.WithTransaction |
| RobotAccountService → mysql.RobotAccountRepository | 改依赖 domain 接口 |

---

## 21. 分层依赖规则

### 21.1 层级定义

```
Layer 4: Interface      server/, handler/, router/, middleware/
Layer 3: Application    application/, AppService, TaskRunner
Layer 2: Domain         domain/, 聚合根, 值对象, Domain Service, Repository 接口
Layer 1: Infrastructure infrastructure/, Repository 实现, Messaging, Broadcast
Layer 0: Bootstrap      bootstrap/, Container, App lifecycle
Shared:  Common         common/*
```

### 21.2 依赖方向规则

- **上层 → 下层**：允许
- **同层 → 同层**：允许（同域内）
- **下层 → 上层**：禁止
- **跨域**：仅通过 DTO / Interface

### 21.3 验证机制

- **lint 规则**：用 `depguard` 配置层间禁止 import 规则
- **架构测试**：用 `go-arch-lint` 或自写脚本验证依赖图
- **CI 集成**：每次 PR 自动检查

### 21.4 例外清单

| 例外 | 理由 |
|---|---|
| algorithm → domain.RoomRepository | 算法需读房间状态做奖励判定，通过接口注入 |
| application → *cRedis.Client | 仍需直接调 Lua 脚本（封装为 GrabService/PenaltyService 后已收敛） |

---

## 22. 开发规范

### 22.1 必须遵循 CODING_STANDARD.md

所有新代码必须遵循 [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md)（2057 行，0-19 章 + 4 附录）。

### 22.2 关键规范摘要

| 类别 | 规范 |
|---|---|
| 注释 | 中文（§17.1），godoc 以类型/函数名开头 + 中文描述 |
| 日志 | 英文 third person past tense（§5.4） |
| 错误 | %w wrap（§4），禁止吞没 |
| 字符串拼接 | §16 SC-1~SC-10：JSON via Marshal、URL via strutil、Redis key via rediskeys |
| 并发 | app-level context，禁止 context.Background()（§6） |
| Lua | cRedis.NewScript 注册，KEYS via 数组，TTL via ARGV |
| 配置 | 禁硬编码 magic number，配置文件驱动 |
| 测试 | 核心资金逻辑 100% 单测 |

### 22.3 新增规范

| 规范 | 内容 |
|---|---|
| Application Service 行数上限 | ≤ 500 行 |
| Service 方法数上限 | ≤ 10 |
| 单一 Repository 行数上限 | ≤ 300 行 |
| 跨领域 import | 禁止（仅通过 DTO） |
| 循环依赖 breaker | 必须注释说明 + 优先用 Domain Event |
| 常量定义 | 单一真理源，禁止重复 |

---

## 23. 后续重构路线图（Phase 1 ~ Phase N）

### Phase 1：边界隔离（2-3 周，P0）

**目标**：消除跨领域类型污染，建立清晰领域边界

| 任务 | 文件 | 收益 |
|---|---|---|
| 1.1 定义 `settlement/domain/platform_user.go` | 新增 | 解除 settlement→game 类型依赖 |
| 1.2 在 game/infrastructure/adapter/ 提供 UserSaverAdapter | 新增 | 转换 gameModel.User → PlatformUser |
| 1.3 settlement/service/user_id_convert_service.go 改用 PlatformUser | 改 | 边界清晰 |
| 1.4 统一 VirtualBalanceService 到 settlement | 合并 | 消除双写 + Redis Key 隐式耦合 |
| 1.5 拆 EventPublisher 接口为 RoomEventPublisher + GameEventPublisher | 改 | ISP |
| 1.6 收敛 RewardType/TriggerType 常量到 game/domain/reward | 改 | 单一真理源 |
| 1.7 收敛 MaxPlayers/randomDelay 到 domain/common | 改 | 消除重复 |

**风险**：中。VirtualBalanceService 合并需保证灰度切换平滑。

### Phase 2：God Service 拆分（3-4 周，P0）

**目标**：GameAppService / SettlementService / BillManager 拆分

| 任务 | 输入 | 输出 |
|---|---|---|
| 2.1 从 GameAppService 抽 PacketOrchestrator | game_app_service.go (1812) | packet_orchestrator.go (~400) |
| 2.2 抽 RoundSettlementService | 同上 | round_settlement_service.go (~350) |
| 2.3 抽 GameLifecycleService | 同上 | game_lifecycle_service.go (~300) |
| 2.4 瘦身 GameAppService 仅留 GrabPacket 入口 | 同上 | game_app_service.go (~250) |
| 2.5 拆 SettlementService 为 3 个 Service | settlement_service.go (468) | round_settle / penalty_settle / balance_query |
| 2.6 拆 BillManager 为 4 个 Repository | bill_manager.go (722) | bill_repo / round_settlement_repo / refund_repo / query_repo |
| 2.7 拆 GameSettleService 为 Reporting + Payout | game_settle_service.go (491) | 2 个 Service |

**风险**：高。God Service 拆分需保证调用方正确迁移 + 充分回归测试。建议每个拆分配独立 PR + 集成测试。

### Phase 3：分层修正（2 周，P1）

**目标**：消除分层违规

| 任务 | 详情 |
|---|---|
| 3.1 抽 application/game_event_handler.go | GameEventConsumer 仅做解析+调 handler；handler 走 dbRepo.WithTransaction |
| 3.2 algorithm 改依赖 domain 接口 | 移除 import infrastructure/redis |
| 3.3 RobotAccountService 改依赖 domain 接口 | mysql.RobotAccountRepository 改返回接口 |
| 3.4 所有 mysql Repository 返回接口 | GormUserRepository 等 |
| 3.5 settlement 引入 Application 层 | settle_app_service.go 编排事务 |
| 3.6 BillRepository 接口提取 | service 依赖接口而非 *BillManager |

**风险**：低-中。主要是接口提取，行为不变。

### Phase 4：错误处理收敛（1-2 周，P1）

| 任务 | 详情 |
|---|---|
| 4.1 creditSessionPayouts 返回 error | 不再吞没 |
| 4.2 commission 失败返回 error | settlement_service.go:140 |
| 4.3 RobotChecker fail-closed | Redis 故障返回 error |
| 4.4 ParseAmount 失败标 Failed | 不标 Success balance=0 |
| 4.5 UpdateLog 区分 success/retry count | 语义修正 |
| 4.6 修正 P1-17 ~ P1-21 全部错误处理问题 | 见 §2.6 |

### Phase 5：测试补齐（持续，P1）

| 优先级 | 文件 | 测试类型 |
|---|---|---|
| 高 | algorithm/packet_generator.go | 单测（mock Redis） |
| 高 | algorithm/reward_controller.go | 单测 |
| 高 | algorithm/leopard.go | 单测 |
| 高 | settlement/service/settlement_service.go | 单测（mock repo） |
| 高 | settlement/service/deduct_service.go | 单测 |
| 高 | settlement/service/game_settle_service.go | 单测 |
| 高 | settlement/service/bill_manager.go（拆分后） | 单测 |
| 中 | game/scheduler/timeout_scheduler.go | 单测 |
| 中 | application/game_app_service.go（拆分后） | 单测 |
| 中 | 端到端集成测试 | "发包→抢→结算→入账"完整流程 |

### Phase 6：Common 清理（1 周，P2）

| 任务 | 详情 |
|---|---|
| 6.1 拆 common/config/types.go | 按域分文件 |
| 6.2 common/utils 拆分 | GenerateConnID→gateway, GenerateRoomID→game, GenerateOrderNo→settlement |
| 6.3 common/message/payload.go 移到 game/domain | 游戏特定 push 结构 |
| 6.4 common/message/errors.go 抽 i18n | 西语消息移到 common/i18n |
| 6.5 提取接口 | TaskRunner / Locker / RedisClient / NacosClient / Signer |
| 6.6 修正 signature 时序攻击 | hmac.Equal |
| 6.7 GenerateRoomID 改用 idgen | 雪花 ID |
| 6.8 移除死代码 | packet_generator.db 字段、robot_behavior.redis 字段、duplicate 方法 |

### Phase 7：Gateway 收敛（1 周，P2）

| 任务 | 详情 |
|---|---|
| 7.1 游戏入口 API 拆出 | /game/list /game/start 移到独立服务或 game HTTP |
| 7.2 CORS 配置化 | 生产禁用 * |
| 7.3 签名加 timestamp 窗口 + nonce | 防重放 |
| 7.4 UserSaverAdapter 移到 game/infrastructure | 不在 common |

### Phase 8：scheduler 与配置收敛（1 周，P2）

| 任务 | 详情 |
|---|---|
| 8.1 所有 scheduler 魔法数移到 config | 100 limit / 1h timeout / 5min window |
| 8.2 timeout_scheduler HandlerTimeout 配置化 | 30s → config |
| 8.3 leopard 10x 倍数配置化 | 移到 RewardControlConfig |
| 8.4 packet cache TTL 配置化 | 1h → config |

---

## 24. 风险评估

### 24.1 重构风险矩阵

| Phase | 风险等级 | 主要风险 | 缓解措施 |
|---|---|---|---|
| Phase 1 | 中 | VirtualBalance 合并灰度 | 双写期 + 切流前对比 |
| Phase 2 | **高** | God Service 拆分引发回归 | 每拆分配独立 PR + 集成测试 + 灰度发布 |
| Phase 3 | 低-中 | 接口提取改变调用签名 | 编译期保证 + 全量单测 |
| Phase 4 | 中 | 错误处理改变行为（原吞没现报错） | 可能暴露原隐藏 bug，需监控告警 |
| Phase 5 | 低 | 测试不影响生产 | 仅 CI 阻塞 |
| Phase 6 | 低 | 包移动影响 import | 编译期保证 |
| Phase 7 | 中 | gateway API 拆分影响前端 | API 兼容期 |
| Phase 8 | 低 | 配置化需迁移 YAML | 默认值兜底 |

### 24.2 资金安全风险

| 风险 | 措施 |
|---|---|
| 拆分过程中 bill 创建逻辑被改 | 拆分前后账本对账脚本每日跑 |
| 错误处理修正触发原隐藏 bug | Phase 4 单独灰度，监控 SettleFailed 率 |
| VirtualBalance 合并期余额不一致 | 双写期 + 余额快照对比 |

### 24.3 回滚预案

- 每个 Phase 必须可独立回滚
- 关键 Phase（2、4）准备 feature flag，可快速切回旧逻辑
- 数据库变更前必须备份

### 24.4 不重构的风险

| 不重构 | 后果 |
|---|---|
| GameAppService 继续膨胀 | 1812 行 → 3000 行，无法维护 |
| 跨领域污染加深 | 任何 game 改动波及 settlement |
| 测试缺口持续 | 每次改动靠人工回归，缺陷率上升 |
| 错误吞没累积 | 资金对账差异逐年放大 |

---

## 25. 最终推荐方案

### 25.1 一句话总结

> **保留当前 12 项优秀实践（S1-S12），按 8 个 Phase 渐进重构，核心是"边界隔离 + God Service 拆分 + 分层修正"三步走，最终达到"目录即文档、代码即设计、设计即业务"的境界。**

### 25.2 核心决策清单

| # | 决策 | 选择 | 理由 |
|---|---|---|---|
| 1 | 架构风格 | 分层 + 模块化 DDD | 适配当前规模，不引入全套 DDD 复杂度 |
| 2 | 事务边界 | Application 层 via dbRepo.WithTransaction | 短事务 + 幂等 + 重试 |
| 3 | 跨域通信 | DTO + Interface（禁止类型 import） | 领域独立演进 |
| 4 | 虚拟余额归属 | settlement 统一 | 资金领域单一真理源 |
| 5 | GameAppService 拆分 | 4 个 Service | 每个单一职责 ≤ 500 行 |
| 6 | SettlementService 拆分 | 3 个 Service + 4 个 Repository | 解 facade |
| 7 | 事件分类 | Domain Event + Integration Event | 同步解耦 + 异步跨服务 |
| 8 | 读写分离 | 隐式 CQRS（Query Service 直读） | 不引入 CQRS 全套 |
| 9 | 测试策略 | 优先资金核心文件单测 + 端到端集成 | 投入产出比 |
| 10 | 重构节奏 | 8 Phase 渐进 | 风险可控 |

### 25.3 保留清单（绝对不动）

- S1 rediskeys 单一真理源
- S2 Lua 脚本集中注册
- S3 Scheduler 框架
- S4 幂等设计（确定性 BizOrderNo + 乐观锁 + Processing 中间态）
- S5 crypto/rand 用于资金
- S6 atomic.Pointer 并发配置
- S7 EventHeader 统一信封
- S8 AsyncTaskRunner
- S9 优雅关停顺序
- S10 Kafka Hash Balancer + Key 策略
- S11 PlatformCallManager 审计日志
- S12 雪花 ID + 节点自动分配

### 25.4 重构优先级排序

```
P0（立即）:
  Phase 1: 边界隔离         ← 阻塞后续所有 Phase
  Phase 2: God Service 拆分 ← 阻塞测试补齐
  Phase 3: 分层修正         ← 阻塞 mockability

P1（近期）:
  Phase 4: 错误处理收敛     ← 资金安全
  Phase 5: 测试补齐         ← 持续投入

P2（中期）:
  Phase 6: Common 清理
  Phase 7: Gateway 收敛
  Phase 8: 配置收敛
```

### 25.5 成功标准

重构完成时应满足：

| 维度 | 标准 |
|---|---|
| 结构 | 任何 .go 文件 ≤ 500 行；任何 Service ≤ 10 方法 |
| 边界 | settlement 不 import game/model；algorithm 不 import infrastructure |
| 依赖 | 无循环依赖；依赖方向单向 |
| 测试 | 核心资金文件单测覆盖率 ≥ 80% |
| 可读性 | 新人 10 分钟看懂架构图，30 分钟定位业务入口 |
| 可维护性 | 修改一个 Service 不影响其他 Service |
| 可观测性 | 任何线上行为可由 traceID 串联日志 |

### 25.6 哲学回归

最终追求的不是复杂架构，而是 **返璞归真、大道至简**：

> 让代码自然表达业务，让架构服务于业务，而不是业务迁就架构。

- **目录就是文档**：看到目录即知职责
- **代码就是设计**：代码结构即架构设计
- **设计就是业务**：每个模块对应一个业务概念

---

## 附录 A：审查文件清单

本次审查共完整阅读以下文件（按模块分组）：

### game/application/ (14 文件, ~5760 LOC)
game_app_service.go (1812), grab_service.go (285), history_dto.go (116), history_service.go (271), penalty_service.go (165), robot_account_service.go (244), robot_behavior.go (328), robot_config.go (97), robot_player.go (270), robot_scheduler_service.go (565), room_app_service.go (774), room_state.go (198), seat_app_service.go (470), user_service.go (165)

### game/infrastructure/ (含 scripts)
broadcast/broadcaster.go (50), messaging/{game_event_consumer.go (551), game_event_publisher.go (98), room_event_consumer.go (216), room_event_publisher.go (82)}, persistence/mysql/{db_repository.go (72), history_repository.go (206), robot_account_repo.go (80), room_repository.go (103), round_repository.go (66), session_repository.go (35), user_repository.go (53), room_config_repository.go (37)}, persistence/redis/{keys.go (295), repository.go (750), robot_pool.go (57), robot_scheduler.go (125), virtual_balance.go (117)}, persistence/redis/scripts/{registry.go (37), packet.lua.go (407), penalty.lua.go (90), queue.lua.go (195), round.lua.go (291), room_seat.lua.go (632)}

### game/domain/
db_repository.go, events.go, game_state.go, grab.go, lua_codes.go, messaging.go, penalty.go, repository.go, room.go, sender_type.go, errors.go, events_test.go

### game/algorithm/ (8 文件)
config.go (37), errors.go (26), leopard.go (59), model.go (29), packet_generator.go (273), reward_controller.go (213), straight.go (97), straight_test.go (192)

### game/scheduler/ (2 文件)
timeout_scheduler.go (305), virtual_balance_sync.go (43)

### game/server/ (2 文件)
generic_service.go (908), generic_service_test.go (195)

### game/config/ (5 文件)
algorithm.go (43), config.go (46), defaults.go (26), nacos.go (14), rate_limiter.go (64)

### game/model/ (8 文件)
config.go (18), packet.go (13), reward.go (30), robot_account.go (33), room.go (33), round.go (51), session.go (53), user.go (44)

### game/bootstrap/ (5 文件)
app.go (458), config_listener.go, container.go (393), mq_helper.go, nacos_helper.go

### settlement/ (28 文件, ~5800 LOC)
config/config.go (33), dto/{constants.go (99), request.go (130), response.go (133)}, model/{bill.go (84), exception_record.go (53), platform_settle_log.go (29), refund.go (34)}, scheduler/{5 files ~330 LOC}, service/{14 files: balance_service.go (99), bill_manager.go (722), credit_retry_service.go (233), deduct_service.go (587), exception_manager.go (53), game_settle_service.go (491), platform_call_manager.go (90), refund_service.go (251), reward_settler.go (112), robot_checker.go (39), settlement_check_service.go (121), settlement_service.go (468), trace_id_generator.go (115), user_id_convert_service.go (42), virtual_balance_service.go (69)}

### gateway/ (23 文件, ~3500 LOC)
bootstrap/{app.go (356), config_listener.go (17), container.go (152), mq_helper.go (30)}, broadcast/broadcast.go (254), config/{config.go (97), defaults.go (82), nacos.go (12), router.go (26)}, connection/{connection.go (146), manager.go (449), scripts/register_connection.lua.go (60)}, handler/game.go (133), health/health.go (117), middleware/{auth.go (183), auth_test.go (172), signature.go (137)}, model/game.go (31), router/router.go (248), server/server.go (445), service/{game.go (139), test.go (80), token.go (96)}, store/memory.go (87), keys.go (73), README.md (270)

### common/ (21 子目录, 60+ 文件)
async/{doc.go, task_runner.go (95), task_runner_test.go (284)}, broadcast/{broadcaster.go (10), consumer.go (14), consumer_factory.go (82), factory.go (72), kafka_broadcaster.go (76), kafka_consumer.go (34), redis_pubsub_broadcaster.go (76), redis_pubsub_consumer.go (143)}, config/{loader.go (38), log.go (26), mysql.go (20), nacos.go (66), redis.go (37), server.go (16), types.go (479)}, converter/converter.go (100), currency/{convert.go (46), money.go (77)}, discovery/discovery.go (226), idgen/{generator.go (53), node_allocator.go (140), registry.go (109), release_node.lua.go (27), snowflake.go (115), snowflake_test.go (318)}, kafka/{config.go (68), consumer.go (148), dlq.go (40), producer.go (118), retry.go (79), topics.go (13)}, limiter/{limiter.go (122), limiter_test.go (263), metrics.go (26), scripts/{sliding_window.lua.go (43), sliding_window_test.go (170)}}, lock/{distributed_lock.go (164), errors.go (12), scripts/release_lock.lua.go (22)}, logger/logger.go (98), message/{header.go (54), header_test.go (149), broadcast.go (70), broadcast_test.go (109), push.go (65), push_test.go (151), request.go (52), response.go (54), types.go (89), errors.go (316)}, mysql/{gorm_logger.go (50), mysql.go (48)}, nacos/{client.go (200), errors.go (7)}, redis/{redis.go (307), script.go (59)}, rediskeys/keys.go (718), scheduler/{base.go (140), metrics.go (78), registry.go (71), scheduler.go (12)}, signature/signer.go (165), strutil/{hostport.go (17), strutil_test.go (185), url.go (61)}, trace/{trace.go (61), trace_test.go (60)}, utils/{avatar.go (18), utils.go (152)}

### api/platform/ (6 文件)
client.go (10), client_factory.go (40), gamingpanda_client.go (282), mock_client.go (250), types.go (71), utils.go (9)

### stats/ (10 文件)
bootstrap/{app.go (186), container.go (74)}, config/{config.go (66), defaults.go (43)}, dto/stats_dto.go (70), handler/{parse_date_range.go (45), stats_handler.go (172)}, repository/stats_repository.go (262), service/{format.go (12), stats_service.go (177)}

### 其他
proto/common/generic.proto (42), cmd/{game/main.go (7), gateway/main.go, stats/main.go}, CODING_STANDARD.md (2057), BACKEND_AUDIT_REFACTOR.md, refactor_docs/ (11 个重构计划文档)

---

## 附录 B：术语表

| 术语 | 含义 |
|---|---|
| God Service | 承担过多职责的巨型 Service |
| God Repository | 合并多个聚合仓储的巨型 Repository |
| God Object | 承担过多职责的对象 |
| SRP | Single Responsibility Principle，单一职责原则 |
| ISP | Interface Segregation Principle，接口隔离原则 |
| DIP | Dependency Inversion Principle，依赖倒置原则 |
| Bounded Context | DDD 中的限界上下文 |
| Aggregate Root | DDD 中的聚合根 |
| Domain Event | 领域内同步事件 |
| Integration Event | 跨服务异步事件（通常经 Kafka） |
| Cache-Aside | 读时查缓存→未命中查 DB→回填缓存 |
| Processing 中间态 | RPC 前置本地状态为 Processing，RPC 成功后置终态，失败回退重试 |
| 乐观锁 | UPDATE 带 WHERE status=?，RowsAffected==0 视为已处理 |
| fail-closed | 故障时拒绝请求（返回 error） |
| fail-open | 故障时放行请求（返回 true/默认值） |

---

## 附录 C：与 CODING_STANDARD.md 对齐说明

本文档所有规约与 [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) 完全对齐：

- §2 项目结构与分层架构 → 本文档 §5、§6、§21
- §4 错误处理 → 本文档 §18
- §5 日志与可观测性 → 本文档 §19
- §6 并发与异步编程 → 本文档 §16（AsyncTaskRunner）
- §8 数据访问 → 本文档 §13
- §9 消息队列（Kafka）→ 本文档 §16
- §11 配置管理 → 本文档 §17
- §12 依赖注入与生命周期 → 本文档 §14、§20
- §13 测试规范 → 本文档 §23 Phase 5
- §16 字符串拼接 → 保留 CODING_STANDARD 规约
- §17 注释与文档 → 保留 CODING_STANDARD 规约（中文注释）
- 附录 C 技术债务 TD-1~TD-30 → 本文档 §2 已覆盖新发现违规

冲突时以 CODING_STANDARD.md 为准；本文件未覆盖的回退到 CODING_STANDARD。

---

**文档结束**

> 本文档是 Game 服务重构的唯一设计依据。所有重构 PR 必须对照本文档相应 Phase 与任务。任何偏离须经架构评审。
