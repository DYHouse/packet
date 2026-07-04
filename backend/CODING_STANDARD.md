# CashParty 后端编码规范（Backend Coding Standard）

> 本规范基于对 `backend/` 全量代码（共 171 个 Go 文件）的实际阅读整理而成，目的是消除"相同功能写出不同风格"的问题。
> 规范中每一条都给出 **必须（MUST）/ 应当（SHOULD）/ 不可（MUST NOT）** 三档约束，并附"参考实现"和"反面案例"。
> AI 在新增或修改代码时必须先查阅本规范；冲突时以本规范为准。

---

## 目录

1. [总则](#1-总则)
2. [项目结构与分层](#2-项目结构与分层)
3. [命名规范](#3-命名规范)
4. [错误处理](#4-错误处理)
5. [日志规范](#5-日志规范)
6. [并发与异步任务](#6-并发与异步任务)
7. [Redis 与分布式锁](#7-redis-与分布式锁)
8. [数据库（MySQL/GORM）](#8-数据库mysqlgorm)
9. [幂等性](#9-幂等性)
10. [HTTP / WebSocket / gRPC 接口](#10-http--websocket--grpc-接口)
11. [配置管理](#11-配置管理)
12. [依赖注入与生命周期](#12-依赖注入与生命周期)
13. [注释与文档](#13-注释与文档)
14. [格式化与 gofmt](#14-格式化与-gofmt)
15. [禁止的写法（Anti-Patterns）](#15-禁止的写法anti-patterns)
16. [字符串拼接](#16-字符串拼接)
17. [调度器（Scheduler）](#17-调度器scheduler)

---

## 1. 总则

### 1.1 适用范围
本规范适用于 `backend/` 下所有 Go 代码：`game/`、`settlement/`、`gateway/`、`stats/`、`common/`、`api/platform/`、`cmd/`。

### 1.2 优先级
当本规范与既有代码冲突时：
- **既有代码已被项目记忆（project_memory.md）标记为"约定"的**，以项目记忆为准（例如结算模块的 SettleRound 移出主事务）。
- **其它既有代码**：本规范优先。允许在重构时同步收敛历史代码，但禁止"在新代码里复刻旧的不一致"。

### 1.3 Go 版本与工具链
- 代码必须通过 `gofmt -l`、`go vet ./...`、`go build ./...` 三项检查。
- 不得引入未被 `go.mod` 已记录的依赖。

---

## 2. 项目结构与分层

### 2.1 模块内部结构（MUST）

**业务复杂模块（game / settlement）必须采用 DDD 分层**：

```
<module>/
├── domain/              # 领域层：实体、值对象、Repository 接口、领域事件、错误码
├── application/         # 应用层：应用服务（用例编排）、DTO
├── infrastructure/      # 基础设施层
│   ├── persistence/
│   │   ├── mysql/       # GORM Repository 实现
│   │   └── redis/       # Redis Repository + Lua 脚本
│   ├── messaging/       # Kafka 消费者/发布者
│   └── broadcast/       # 广播适配器
├── model/               # GORM 持久化模型（仅 game 模块）
├── scheduler/           # 后台调度器
├── server/              # gRPC/HTTP 入口
├── bootstrap/           # 组合根：app.go + container.go
└── config/              # 模块专属配置
```

**轻量模块（gateway / stats）SHOULD 采用 handler/service/repository 分层**，但必须满足：
- 有 `bootstrap/` 包，禁止把所有装配塞进 `cmd/<svc>/main.go`。
- 入口 `cmd/<svc>/main.go` 只允许一行业务调用：`bootstrap.Run()`。

**参考实现**：[game/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go)、[game/bootstrap/container.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/container.go)、[cmd/game/main.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/cmd/game/main.go)。

**反面案例**：[cmd/stats/main.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/cmd/stats/main.go) 把装配、路由、健康检查、CORS 全部塞进 main，必须重构为 `bootstrap/` 包。

### 2.2 依赖方向（MUST）

- `domain` 不得 import `infrastructure`、`application`、`bootstrap`。
- `application` 只能依赖 `domain` 中定义的接口，不得直接 import `gorm`/`go-redis`。
- `infrastructure` 实现 `domain` 接口，依赖外部库。
- `bootstrap` 是唯一允许"把具体实现注入到接口"的位置。

### 2.3 Repository 接口位置（MUST）

- 接口定义在 `domain/` 包中（game 模块范式）。
- 消费方在 `application/` 中以接口字段持有依赖。
- 实现在 `infrastructure/persistence/{mysql,redis}/` 中。
- 不得在 `repository/` 包里直接定义具体 struct 又被 service 直接依赖（stats 模块的反面案例）。

### 2.4 DTO 归属（MUST）

- 每个模块统一使用 `dto/` 子包承载对外 Request/Response 与对外枚举常量。
- 内部领域类型放在 `domain/`。
- **禁止**把 Request/Response struct 散落在 service / handler 文件里（gateway 当前的写法，需收敛）。

### 2.5 常量集中（MUST）

- 同一类枚举必须在**唯一**位置声明，不得分散。
- **对外协议枚举**（BillType、BillStatus、RoundStatus、RefundStatus、ReconcileStatus、GameSettleStatus、CallLogStatus、CallType 等）：放在 `settlement/dto/constants.go`。
- **领域内部枚举**（ExceptionType、ExceptionStatus、HandleType）：放在 `settlement/model/<entity>.go` 同文件，或单独的 `model/constants.go`；二者只能选其一，当前 settlement 模块混合两种写法，新代码统一放 `dto/constants.go`。
- **禁止**在 service 文件里写 `case 1` / `case 2` 这种裸数字（[settlement/service/reward_settler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/reward_settler.go) 当前的写法，必须补 `RewardTypeStraight=1` / `RewardTypeLeopard=2` 常量）。

---

## 3. 命名规范

### 3.1 文件命名（MUST）

- 全部 lowercase snake_case：`game_app_service.go`、`credit_retry_service.go`、`distributed_lock.go`。
- **禁止**冗余前缀：文件名不要重复包名。`stats/handler/stats_handler.go` 应改为 `stats/handler/handler.go`（包名 `handler` 已经表达了语义）。
- 测试文件 `_test.go`；Lua 脚本文件 `.lua.go`。

### 3.2 类型命名（MUST）

| 类别 | 命名规则 | 示例 |
|---|---|---|
| 应用服务 | `<Domain>Service` | `SettlementService`、`GameAppService`、`RefundService` |
| DB 访问层 | `<Domain>Manager` 或 `<Domain>Repository`（接口） | `BillManager`、`ExceptionManager`、`PlatformCallManager` |
| 后台调度器 | `<Domain>Scheduler` | `CreditRetryScheduler`、`TimeoutScheduler` |
| Repository 实现（unexported） | `gorm<Domain>Repository`（小写 gorm 前缀） | `gormRoomRepository`、`gormRoundRepository` |
| Repository 实现（exported） | `<Domain>RepositoryImpl` 或 `Gorm<Domain>Repository`，二选一 | `DBRepositoryImpl`、`GormTransactionImpl` |
| 配置 struct | `<Domain>Config` | `ServerConfig`、`CreditRetryConfig` |
| 参数 struct | `<Action>Params` | `CallLogCreateParams` |
| 事件 | `<Verb><Object>Event` | `SpectatorJoinEvent` |
| 错误码常量 | `ErrCode<Reason>` | `ErrCodeInvalidTotalAmount` |
| 业务状态枚举 | `<Entity>Status<State>` | `BillStatusSuccess`、`RoundStatusCredited` |

**必须收敛的历史不一致**（[game/infrastructure/persistence/mysql/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/)）：
- `RobotAccountRepository`（PascalCase 无前缀）
- `GormUserRepository`（PascalCase 带 Gorm）
- `gormRoomRepository`（小写带 gorm）

新代码统一采用 **小写 `gorm<Domain>Repository`**（unexported struct + `New<Domain>Repository` 构造函数返回 domain 接口）。

### 3.3 接口命名（MUST）

- 单方法行为接口：`-er` 后缀（`Grabber`、`Broadcaster`、`UserSaver`、`RobotChecker`）。
- 多方法资源接口：裸名词（`Client`、`GameStore`、`ServiceClient`、`RoomRepository`）。
- **禁止**给接口加 `I` 前缀（C# 风格）。
- **禁止**给具体 struct 起名 `UserService` 又同时是接口（[settlement/service/user_id_convert_service.go:12](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/user_id_convert_service.go) 当前的写法，应改名 `UserIDConverter` 或类似）。

### 3.4 方法命名（MUST）

- 构造函数：`New<Type>(deps...) *Type` 或 `New<Type>(deps...) <Interface>`。
- 公开方法：PascalCase 动词开头，`<Verb><Object>`：`SettleGame`、`DeductForFirstRound`、`ApplyForRefund`。
- 私有 helper：camelCase：`executeBatchDeduct`、`creditRound`、`doRetryCredit`。
- 幂等检查方法：`Is<Player>GameSettled`、`Exists<By><Field>`；**同一模块内** `Exists` 系列必须统一带 `By` 介词（`ExistsByRoundAndType`），或全部不带，不得混用（[settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) 中 `ExistsByRoundAndType` 与 `ExistsRoundSettlement` 并存，需统一为 `ExistsByRoundSettlement`）。
- 资源生命周期方法：`Start(ctx) error` + `Stop()` + `Close() error`，三选一时优先 `Start/Stop`，关闭需要错误返回值时用 `Close`。

### 3.5 Receiver 命名（MUST）

- 一律使用类型首字母的 1-3 字母缩写，**整个文件内一致**。
- 服务/调度器：`s`（`s *SettlementService`、`s *TimeoutScheduler`）。
- Manager：`m`（`m *BillManager`）。
- Repository：`r`（`r *gormRoomRepository`）。
- Generator：`g`（`g *TraceIDGenerator`）。
- Checker：`c`（`c *redisRobotChecker`）。
- 模型值方法：类型首字母（`func (u *User) GetUserID()`、`func (r Room) TableName()`）。
- **禁止**同一类型在不同方法里 receiver 名不同。

### 3.6 变量与常量（MUST）

- 缩写词全大写：`userID`、`roomID`、`URL`、`IP`，**不得**写 `userId`、`roomId`。
- 常量：导出 PascalCase（`RewardTypeStraight`），未导出 camelCase（`defaultCheckIntervals`）。
- 魔法数字：禁止出现在业务逻辑里。所有阈值、超时、状态码必须有命名常量。

### 3.7 构造函数（MUST）

- 统一 `New<Type>(deps...)`。
- **禁止** `Load`、`InitLocker`、`Obtain`、`WithLock` 作为"构造"函数名（[common/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/config.go) 用 `Load`、[common/lock/distributed_lock.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/distributed_lock.go) 用 `Obtain`/`InitLocker` 是历史遗留，新代码不得沿用）。
- `Load` 仅用于"从外部源加载配置"语义。
- 单例初始化：统一 `sync.Once`，不得用 `sync.Mutex` + nil 检查（[common/lock/distributed_lock.go:25-27](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/distributed_lock.go) 的写法不沿用）。

---

## 4. 错误处理

### 4.1 错误模型（MUST）

业务错误统一使用 `*message.GameError`（带 `Code int` 和 `Msg string`），通过 `message.NewError(code, msg)` / `message.NewErrorWithMsg(code, msg)` 构造。

- 对外返回（gRPC handler / HTTP handler）：必须返回 `*message.GameError`，由 handler 统一转换为响应。
- 算法库（`game/algorithm/`）保留独立的 `*algorithm.Error`，因为它是无外部依赖的纯库。
- **禁止**再创建新的自定义 error 类型。

### 4.2 哨兵错误（SHOULD）

- 可恢复的、需 `errors.Is` 判别的错误，使用 `var ErrXxx = errors.New("...")`，放在 `domain/errors.go` 或 `application/errors.go`。
- 示例：[game/application/robot_player.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/robot_player.go) 的 `ErrNoEmptySeat`。
- 业务状态机"不符合预期状态"应使用哨兵，**不得**用 `fmt.Errorf("refund status is not pending")` 这种字符串错误（[settlement/service/refund_service.go:229](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) 当前的写法需改为哨兵）。

### 4.3 错误包装（MUST）

- 跨层返回错误时必须用 `fmt.Errorf("<action> failed: %w", err)` 包装，保留调用栈。
- 包装消息格式统一：`"<动词+对象> failed: %w"`，例如 `"get virtual balance failed: %w"`、`"create round settlement and bills failed: %w"`。
- **`%w` vs `%v`**：包装 `error` 类型时 MUST 使用 `%w`（保留 `errors.Is`/`errors.As` 解包能力），禁止 `%v`。Go 1.20+ 支持多个 `%w`（如 `fmt.Errorf("%w: %w", err1, err2)`）。`%v` 仅用于包装非 error 类型（如 `recover()` 返回的 `interface{}`）。
- **必须收敛**：[settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) 当前直接返回 `m.db.WithContext(ctx).Create(bill).Error` 而不包装，新代码必须包装为 `fmt.Errorf("create bill failed: %w", err)`。
- **必须收敛**：[game/application/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/) 各 service 不包装错误，新代码必须在 service 边界做包装。
- [common/broadcast/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/broadcast/) 各 broadcaster 直接 `return err`，必须包装。

### 4.4 错误吞没（MUST NOT）

- **禁止** `if exists, _ := s.billMgr.ExistsRoundSettlement(...)` 这种丢弃 error 的写法（[settlement/service/deduct_service.go:67, 76, 414, 420, 467, 472](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) 当前的写法）。必须显式检查 error 并返回或记录。
- 例外：callLog 创建失败可以降级为 `logger.Warn`，但必须捕获变量名 `callLogErr` 并记录（项目记忆约定）。

### 4.5 fail-open vs fail-closed（MUST）

按业务场景决定，但必须有显式注释说明选择：

| 场景 | 策略 | 理由 |
|---|---|---|
| 资金扣减/入账、Kafka 事件去重 | **fail-closed**（返回 error 触发重试） | 不可丢钱、不可重复消费 |
| 限流（rate limiter） | fail-open（放行 + Warn 日志） | 用户体验优先 |
| 房间事件消费（room_event_consumer） | fail-open | 非关键路径 |
| 广播失败 | fail-closed（返回 error 给上层决策） | 项目记忆约定 |
| Redis 不可用导致幂等检查失败 | **fail-closed** | 防重复处理 |

### 4.6 panic 恢复（MUST）

所有长生命周期 goroutine 必须有 `defer recover()`，并在恢复时记录 `logger.Error("panic", "stack", debug.Stack(), ...)`。

参考：[game/scheduler/timeout_scheduler.go:256-262](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go)、[game/scheduler/virtual_balance_sync.go:67-72](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/virtual_balance_sync.go)。

---

## 5. 日志规范

### 5.1 Logger 使用（MUST）

- 统一通过 `github.com/cashparty/backend/common/logger` 包级函数：`logger.Info/Warn/Error/Fatal`。
- **禁止**直接使用 `fmt.Println`、`log.Printf` 输出业务日志。
- **禁止**使用 `zap` 原生 API，必须通过 `logger.*` 封装。

### 5.2 结构化字段（MUST）

- 字段名一律 `snake_case`：`user_id`、`room_id`、`round_trace_id`、`biz_order_no`、`error`。
- 顺序：消息字符串 → 业务 ID 字段 → `error` 字段。
- 示例：
  ```go
  logger.Error("settle player failed",
      "session_id", sessionID,
      "user_id", userID,
      "error", err)
  ```

### 5.3 日志级别（MUST）

| 级别 | 使用场景 |
|---|---|
| `Debug` | 仅本地调试用，生产关闭 |
| `Info` | 启动/停止、调度器 tick、状态机正向流转、关键决策结果 |
| `Warn` | 可恢复失败（callLog 写入失败、缓存 miss、限流放行、幂等命中跳过、调度器单次重试失败） |
| `Error` | 不可恢复失败（panic、扣款失败、广播失败、DB 写入失败） |
| `Fatal` | 仅启动阶段致命错误（配置加载失败、端口监听失败） |

**必须收敛**：调度器重试失败应统一用 `Warn`（因为下次还会重试）。当前 [settlement/scheduler/credit_retry_scheduler.go:50](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/credit_retry_scheduler.go) 和 [settlement/scheduler/game_settle_retry_scheduler.go:59](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/scheduler/game_settle_retry_scheduler.go) 用 `Error`，与 [settlement/service/settlement_check_service.go:85](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/settlement_check_service.go) 用 `Warn` 不一致，新代码统一 `Warn`。

### 5.4 广播失败日志（MUST）

按项目记忆约定：游戏广播失败必须以 `Warn` 级别记录，并带 `roomID`、`event`、`userID` 上下文。

参考：[game/infrastructure/broadcast/broadcaster.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/broadcast/broadcaster.go)。

---

## 6. 并发与异步任务

### 6.1 AsyncTaskRunner（MUST）

应用层所有异步 goroutine 必须通过 `AsyncTaskRunner` 调度，**禁止**直接 `go func()`。

- 必须传入从 `AsyncTaskRunner` 派生的 context，**禁止** `context.Background()`。
- 必须设置 per-task 超时（5-30s 视业务而定）。
- `AsyncTaskRunner` 必须有 closed 状态保护，`Wait` 后再 `Add` 必须安全返回错误而非 panic。
- 必须有 panic 恢复 + `WaitGroup` 跟踪。

### 6.2 调度器根 context（SHOULD）

后台调度器（`scheduler/`）的根 context MUST 由 `bootstrap` 传入的 appCtx 派生（`context.WithCancel(appCtx)`），禁止 `context.Background()`。调度器内部派生的每次扫描必须有 per-scan 超时。

完整调度器规约见 [§17](#17-调度器scheduler)。

参考：[common/scheduler/base.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/scheduler/base.go) 的 `Start(ctx)` 接收 appCtx 并 `context.WithCancel(ctx)` 派生。

### 6.3 热配置并发（MUST）

被并发读取、由 nacos 热更新的配置字段必须使用 `atomic.Pointer[T]`，**禁止** `sync.RWMutex` 保护普通字段。

参考：[game/algorithm/packet_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go) 的 `config atomic.Pointer[Config]` + `generator atomic.Pointer[Generator]`。

`Generate` 方法必须在方法入口一次性 load snapshot，方法内不得再次 load，保证一次调用看到一致快照。

### 6.4 随机数（MUST）

涉及金额、红包拆分、奖励生成的随机数必须使用 `crypto/rand`，**禁止** `math/rand`。

**必须收敛**：[game/algorithm/straight.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/straight.go) 当前用 `math/rand`，必须改为 `crypto/rand`，与 [game/algorithm/packet_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go) 保持一致。

### 6.5 Stop 超时（SHOULD）

- 后台调度器 `Stop()`：10s 超时。
- 用户面服务（gRPC/HTTP）`GracefulStop`：30s 超时，超时后 fallback 到 `Stop()`。

参考：[game/scheduler/timeout_scheduler.go:137](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go)、[game/server/generic_service.go:766](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go)。

### 6.6 单例初始化（MUST）

统一 `sync.Once`，参考 [common/logger/logger.go:24](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/logger/logger.go)、[common/idgen/snowflake.go:124](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go)。

不得使用 `sync.Mutex` + nil 检查（[common/lock/distributed_lock.go:25-27](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/distributed_lock.go) 的写法不沿用）。

---

## 7. Redis 与分布式锁

### 7.1 Key 命名（MUST）

- 统一前缀 `cashparty:`，分隔符 `:`。
- 所有 Redis key 常量与工厂函数 MUST 集中在 [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go)（单一真相源，跨服务共享）。
- `game/infrastructure/persistence/redis/keys.go`、`settlement/infrastructure/persistence/redis/keys.go`、`gateway/keys.go` 仅为 re-export 兼容层，MUST 委托到 `common/rediskeys`，禁止新增定义。
- 工厂函数命名 `XxxKey(args...)`。
- 禁止裸字符串拼 key 散落在 service 里。
- 所有 `*Prefix` 常量 MUST 带尾随冒号（如 `KeyRoomHashPrefix = "cashparty:room:hash:"`），调用方一律 `+ "*"` 或 `+ specificKey`。
- 多参数 key 的分隔符 MUST 统一为 `:`，禁止下划线 `_`（如 `KeyDeductLock = "cashparty:deduct:%d:%d:%d"`）。
- Lua 脚本中使用的 key 前缀 MUST 在 `common/rediskeys` 有对应常量，禁止出现 Lua 孤儿 key。

参考：[common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go)、[game/infrastructure/persistence/redis/scripts/packet.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/packet.lua.go)（Lua key 与 Go 常量映射注释）。

### 7.2 Lua 脚本（MUST）

- 所有 Lua 脚本必须经 `cRedis.NewScript(name, src)` 注册到 `scripts/registry.go`，享受 EVALSHA 优化。
- 脚本源码放在 `scripts/<name>.lua.go` 中作为 Go 字符串常量，命名 `lua<Action>`。
- **禁止**在 service / repository 里内联 Lua 字符串再调 `redis.Eval`（[common/limiter/limiter.go:34-52](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go) 当前的写法，必须收敛到 `redis.Script`）。
- Lua 返回值第一项必须是整数 code，由 `parseLuaCode` 解析，经 `domain.MapLuaError` 映射为 `*message.GameError`。

### 7.3 分布式锁（MUST）

按项目记忆约定：

- **抢占锁**使用 `SetNX` + 随机 UUID token。
- **释放锁**必须用 Lua 脚本 `if GET key == token then DEL key end`，禁止裸 `DEL`。
- 锁值必须是 token，**不得**是 roomID 或其它业务字段。

参考：[game/infrastructure/persistence/redis/scripts/robot_lock.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/robot_lock.lua.go)。

`common/lock` 包当前依赖 `redsync` 内部实现，未自持 Lua；新模块如需自持锁应直接采用上述 token + Lua 模式，不得引入新的依赖。

### 7.4 Kafka 幂等锁（MUST）

- `tryAcquire` 必须返回 `(bool, error)`：
  - `false, nil`：已处理（幂等命中）
  - `false, error`：Redis 不可用（fail-closed，触发 Kafka 重试）
  - `true, nil`：抢占成功
- 业务逻辑失败时必须调用 `releaseAcquire`（`Del`）释放锁，允许重试。
- **禁止**用 `Exists` + `Set` 两步式实现抢占。

参考：[game/infrastructure/messaging/game_event_consumer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/messaging/game_event_consumer.go)。

### 7.5 Redis Wrapper（SHOULD）

- 调用方应通过 `*cRedis.Client` 调用，避免直接 import `go-redis/v9`。
- 新增 Redis 操作时优先扩展 `common/redis/redis.go` 包装方法。
- 已知的泄漏点（返回 `*redis.StringCmd` 等）暂不强制收敛，但不得在新调用方直接使用底层 `*redis.Client`。

---

## 8. 数据库（MySQL/GORM）

### 8.1 Model 定义（MUST）

- 字段 PascalCase，`gorm:"..."` 标签完整：`primaryKey`、`autoIncrement`、`uniqueIndex`、`index`、`index:idx_xxx`（复合索引）、`size:N`、`not null`、`default:...`、`type:text`、`autoCreateTime`、`autoUpdateTime`。
- 每个 model 必须显式定义 `func (T) TableName() string { return "snake_case_table" }`。
- 表名 snake_case 单数形式：`bill_record`、`round_settlement`、`exception_record`、`refund_audit`。
- 时间字段：
  - `CreatedAt time.Time` / `UpdatedAt time.Time`：值类型，必填。
  - 可空事件时间：指针 `*time.Time`（`StartedAt *time.Time`、`EndedAt *time.Time`、`RefundAppliedAt *time.Time`、`NextRetryAt *time.Time`）。

### 8.2 事务边界（MUST）

- 事务通过 `domain.Transaction.Execute(ctx, func(txCtx context.Context) error { ... })` 调用，**禁止** service 直接持有 `*gorm.DB` 开事务。
- **必须收敛**：[settlement/service/refund_service.go:106-121](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) 当前直接 `s.db.WithContext(ctx).Transaction(...)`，必须下沉到 `BillManager`。
- 跨服务调用（如 `SettleReward`、`SettleRound`）必须移出主事务，靠内部幂等机制保证；主事务回滚不得连带回滚 bill 记录（项目记忆约定）。

### 8.3 乐观锁（MUST）

所有"状态机推进"类的 UPDATE 必须带 `WHERE status = ?` 或 `WHERE status != ?` 条件，并检查 `RowsAffected`：

- `RowsAffected == 0` 表示状态已被并发推进，按"幂等成功"处理：返回 `nil`（不得返回 error）。
- 必须收敛：[settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) 当前只有 `UpdateRoundSettlementCredited` / `UpdateRoundSettlementStatus` / `UpdateRefundAuditStatus` 三处带乐观锁，其余 `Update*` 方法（`UpdateBillStatus`、`UpdateBillSuccess`、`UpdateGameSettleStatusByUser`、`UpdateGameSettleStatusBySession` 等）必须补乐观锁条件。

### 8.4 唯一索引与幂等（MUST）

幂等键必须有 DB 唯一索引：
- `BizOrderNo`：`uniqueIndex;size:64`
- `RoundTraceID`：`uniqueIndex;size:64`
- `RoundID`（在 `RoundSettlement` 上）：`uniqueIndex;not null`
- `RefundOrderNo`：`uniqueIndex;size:64;not null`
- `ExceptionNo`：`uniqueIndex;size:32;not null`

### 8.5 Repository 聚合（MUST）

- `DBRepositoryImpl` 必须在构造函数中 eagerly 初始化所有子 repository，字段构造后只读。
- **禁止**懒加载子 repository（项目记忆约定）。

参考：[game/infrastructure/persistence/mysql/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/db_repository.go)。

### 8.6 原生 SQL（SHOULD）

复杂聚合查询允许使用 `db.Raw(sql, args...).Scan(&dest)`，但必须：
- 使用占位符 `?`，禁止字符串拼接 SQL。
- 返回 slice 时统一做 `if x == nil { x = []dto.X{} }` 归一化（参考 [stats/repository/stats_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/repository/stats_repository.go)）。

---

## 9. 幂等性

### 9.1 三层防线（MUST）

资金/状态相关写入必须有三层幂等防护：

1. **Redis SetNX 抢占**：`lock.WithRedisLock(ctx, redis, key, ttl, fn)`。
2. **DB 唯一索引**：幂等键（BizOrderNo / RoundTraceID / RefundOrderNo）。
3. **状态机检查**：进入逻辑后先查现有状态，已是终态则直接返回 `nil`。

### 9.2 双重检查模式（MUST）

锁外做 cheap 检查短路返回，锁内做 expensive 检查确保正确性：

```go
// 锁外预检
if exists, err := s.billMgr.ExistsByRoundAndType(ctx, roundID, billType); err != nil {
    return fmt.Errorf("check exists failed: %w", err)
} else if exists {
    return nil
}
// 锁内双检
return lock.WithRedisLock(ctx, s.redis, key, ttl, func() error {
    if exists, err := s.billMgr.ExistsByRoundAndType(ctx, roundID, billType); err != nil {
        return fmt.Errorf("check exists in lock failed: %w", err)
    } else if exists {
        return nil
    }
    // ... 业务逻辑
})
```

参考：[settlement/service/deduct_service.go:67-80](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go)。

**必须收敛**：[settlement/service/game_settle_service.go:59-81](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) 当前只有锁内检查无锁外预检，需补齐。

### 9.3 幂等键生成（MUST）

- `BizOrderNo` 必须确定性生成：`fmt.Sprintf("%s_%d_%d", roundTraceID, billType, userID)`。
- `RoundTraceID` 格式统一通过 `TraceIDGenerator` 生成：
  - `RT_<sessionID>_<roundNo>`
  - `PENALTY_DED_<roomID>_<sessionID>`
  - `PENALTY_DIST_<roomID>_<sessionID>`
  - `SESSION_CREDIT_<sessionID>_<userID>`
  - `GAME_SETTLE_<sessionID>`
- **必须收敛**：[settlement/service/game_settle_service.go:203, 326](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) 当前用 inline `fmt.Sprintf`，必须下沉为 `TraceIDGenerator.GenerateGameSettleTraceID` / `GenerateSessionCreditTraceID` 方法。

### 9.4 幂等检查方法（MUST）

- 同一模块内幂等检查方法**只允许一种**返回签名。
- 推荐 `(bool, error)` 风格（`ExistsByXxx`），error 必须被调用方检查。
- 已有 `GetBillByRoundTypeAndUser` 返回 model 的方式可保留，但调用方必须显式判断 `err == nil && bill != nil && bill.Status == Success`。

### 9.5 重试（MUST）

- 调度器驱动的重试统一使用**指数退避**：`delay = baseDelay * multiplier^retryCount`，封顶 `maxDelay`。
- 默认值：`baseDelay=5s`、`multiplier=2.0`、`maxDelay=5min`、`maxRetryCount=3`。
- 超过 `maxRetryCount` 必须创建 `ExceptionRecord` 升级人工处理。
- **必须收敛**：[settlement/service/game_settle_service.go:391](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) 当前用 flat `5 * time.Second` 重试且不增加 retryCount，必须改为指数退避并复用 `IncrementRetryCountWithNextRetryTime`。
- **必须收敛**：[settlement/dto/constants.go:27](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/dto/constants.go) 的 `CreditRetryBaseDelay` 常量未被使用，`DefaultCreditRetryConfig` 必须引用该常量。

---

## 10. HTTP / WebSocket / gRPC 接口

### 10.1 响应信封（MUST）

HTTP API 响应**必须**统一为：

```json
{ "code": 0, "msg": "", "data": <object|null> }
```

- 成功：`code=0`，`msg` 留空字符串或省略，`data` 为业务数据。
- 失败：`code` 为 `message.Code*` 业务码（不是 HTTP 状态码乘 100），`msg` 为可读消息，`data` 为 `null`。
- HTTP 状态码：成功 200；客户端错误 4xx；服务端错误 5xx。HTTP 状态码与业务 `code` **分离**，不要混用。

**必须收敛**（当前至少 5 种响应形状）：
- `gin.H{"success": false, "code": 429, "msg": ...}` ([gateway/middleware/ratelimit.go:144-148](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/ratelimit.go))
- `gin.H{"code": code*100, "message": msg}` ([stats/handler/stats_handler.go:43-48](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/handler/stats_handler.go))
- `gin.H{"code": 0, "data": ...}`（无 msg） ([stats/handler/stats_handler.go:63-66](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/handler/stats_handler.go))
- `gin.H{"status": "ok"}` ([cmd/stats/main.go:84-86](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/cmd/stats/main.go))
- 裸 `HealthStatus` struct ([gateway/health/health.go:59](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/health/health.go))

新代码必须用统一信封；历史代码在重构时收敛。

### 10.2 响应助手（MUST）

每个 HTTP 模块必须提供统一的响应助手：

```go
func respondOK(c *gin.Context, data interface{}) {
    c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "", "data": data})
}
func respondError(c *gin.Context, httpStatus int, code int, msg string) {
    c.JSON(httpStatus, gin.H{"code": code, "msg": msg, "data": nil})
}
```

参考 [stats/handler/stats_handler.go:43-48](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/handler/stats_handler.go) 的 `respondError` 思路，但需调整字段名为 `msg`，并增加 `respondOK`。

### 10.3 HTTP 状态码（MUST）

- 必须使用 `http.Status*` 常量，**禁止**裸数字 `429`、`401`、`500`。
- **必须收敛**：[gateway/middleware/ratelimit.go:144, 176, 208](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/ratelimit.go) 当前用裸 `429`，必须改为 `http.StatusTooManyRequests`。

### 10.4 路由注册（MUST）

- Handler 必须暴露 `RegisterRoutes(r *gin.RouterGroup)` 方法，由 bootstrap 调用。
- **禁止**在 server 内部命令式 `setupRoutes()`（[gateway/server/server.go:99-120](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/server/server.go) 当前的写法，需重构）。

### 10.5 请求绑定（MUST）

- 优先使用 `c.ShouldBindQuery` / `c.ShouldBindJSON`，禁止用 `strconv.Atoi(c.DefaultQuery(...))` 手动解析（[stats/handler/stats_handler.go:133-140](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/handler/stats_handler.go) 当前的写法，必须用 `dto.PaginationReq` + `ShouldBindQuery`）。

### 10.6 中间件风格（MUST）

- gin 中间件统一为 `func XxxMiddleware(deps...) gin.HandlerFunc` 自由函数。
- WebSocket 非中间件逻辑（如 `OnConnect`）可以是 struct 方法。
- 中间件 struct 若有 `Stop()` 方法，必须真正释放资源；无资源时不得定义空 `Stop()`。

### 10.7 健康检查（MUST）

每个对外服务必须暴露三个端点：
- `/health`：完整状态（依赖、连接数、系统资源），返回统一信封 `{"code":0,"data":<HealthStatus>}`。
- `/ready`：就绪检查（依赖连通性），返回 `{"code":0,"data":{"status":"ready"}}` 或 `{"code":1,"data":{"status":"not_ready","error":...}}`。
- `/live`：存活检查，恒返回 `{"code":0,"data":{"status":"alive"}}`。

**必须收敛**：
- stats 必须从 [cmd/stats/main.go:84-86](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/cmd/stats/main.go) 的内联 `/health` 重构为独立 `health/` 包。
- [gateway/health/health.go:36](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/health/health.go) 的 `HealthStatus.Uptime` 字段必须赋值或删除。

### 10.8 gRPC 响应（MUST）

- gRPC handler 必须用 `recoveryUnaryInterceptor` 包裹，panic 时返回 `code = CodeInternal` + `*message.GameError`。
- 业务错误转换为 `ForwardResponse{Code, Msg}`，不得把 Go `error` 字符串透传给前端。
- 参考实现：[game/server/generic_service.go:637-642, 778-790](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go)。

---

## 11. 配置管理

### 11.1 Config 结构（MUST）

- 每个服务模块的 `config/` 包定义自己的 `Config` struct，**聚合** `common/config.*Config` 子结构，不得重新声明同名字段。
- **必须收敛**：[gateway/config/config.go:42-47](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/config.go)、[stats/config/config.go:25-30](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/config/config.go)、`common/config.RedisConfig` 三处 `RedisConfig` 重复，必须复用 `common/config.RedisConfig`。
- 同理 `MySQLConfig`、`LogConfig` 必须复用 `common/config` 的定义。

### 11.2 默认值（MUST）

- 默认值统一放在 `<module>/config/defaults.go` 的 `setDefaults(cfg *Config)` 函数。
- **禁止**在多个地方定义同一类默认值。
- **必须收敛**：[common/config/defaults.go:106-120](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/defaults.go) 与 [common/nacos/config.go:16-24](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/config.go) 的 `DefaultClientConfig()` 重复定义 nacos 默认值，必须保留 `setDefaults` 一处。

### 11.3 加载入口（MUST）

- 配置加载统一函数名 `Load(path string) (*Config, error)` 和 `LoadFromContent(content string) (*Config, error)`。
- 两个入口必须对称：`Load` 能做的事 `LoadFromContent` 也要能做（包括算法配置加载）。
- **必须收敛**：[common/config/config.go:230-253, 273-289](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/config.go) 当前 `Load` 会顺带加载 `algorithm.yaml`，`LoadFromContent` 不会，必须对称化。

### 11.4 默认端口（MUST）

不同服务默认端口必须不同：
- gateway: 8081
- stats: 8082（**必须修改** [stats/config/config.go:77](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/config/config.go) 当前的 8081）
- game: gRPC 端口独立配置

### 11.5 热更新（MUST）

- 通过 nacos 热更新的配置字段必须用 `atomic.Pointer[T]` 持有（见 §6.3）。
- 热更新回调函数必须 `logger.Info` 记录变更前后的关键值，不得静默生效。

---

## 12. 依赖注入与生命周期

### 12.1 组合根（MUST）

- `bootstrap/container.go` 是唯一允许"构造具体实现并注入到接口"的位置。
- `Container` struct 字段一致性：要么全公开（供 `app.go` 访问），要么全私有（通过 getter）。**必须收敛** [gateway/bootstrap/container.go:24-45](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/container.go)（全公开）与 [game/bootstrap/container.go:25-84](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/container.go)（混合）的不一致，统一采用"全公开字段 + `Stop()` 方法"风格。

### 12.2 构造函数参数（SHOULD）

- 当 `NewXxx` 参数超过 5 个时，**应当**使用 functional options 模式或配置 struct：
  ```go
  type GameAppServiceOptions struct {
      RoomRepo      domain.RoomRepository
      DBRepo        domain.DBRepository
      // ...
  }
  func NewGameAppService(opts GameAppServiceOptions) *GameAppService
  ```
- **必须收敛**：[game/bootstrap/container.go:86-112](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/container.go) 当前 `NewContainer` 接受 24 个参数，必须改为 options struct。

### 12.3 Application 生命周期（MUST）

每个服务的 `bootstrap.Application` 必须实现：
- `NewApplicationWithConfig(cfg) (*Application, error)`
- `Start(ctx context.Context) error`
- `Wait() <-chan error`（**必须收敛** [game/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) 当前没有 `Wait()`，需补齐）
- `Stop() error`

### 12.4 致命错误处理（MUST）

- 启动阶段致命错误统一用 `logger.Fatal`，**禁止** `panic(err)` 或 `fmt.Printf + os.Exit(1)`。
- **必须收敛**：[game/bootstrap/app.go:285, 290](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) 用 `panic`、[cmd/stats/main.go:31-33](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/cmd/stats/main.go) 用 `fmt.Printf + os.Exit(1)`，必须统一为 `logger.Fatal`。

### 12.5 循环依赖（SHOULD）

通过 setter 注入打破循环依赖（参考 [game/application/seat_app_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/seat_app_service.go) 的 `SetRoomAppService`）。setter 必须在 `Container.InitServices` 中、启动服务前调用，运行期不得再调用 setter。

---

## 13. 注释与文档

### 13.1 语言（MUST）

- 代码注释统一用**英文**。
- 用户可见字符串（错误消息、日志消息）可以保留中文，但同一类消息全项目语言一致。
- **必须收敛**：
  - [common/message/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/errors.go) 当前西语注释 + 部分中文注释，必须统一为英文。
  - [common/message/types.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/types.go) 中文分节注释改英文。
  - [settlement/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/) 各文件混合中英文注释，新代码用英文。

### 13.2 注释内容（SHOULD）

- 导出类型、导出函数、接口方法必须有 godoc 注释，以类型/函数名开头。
- 复杂业务逻辑（状态机推进、乐观锁、幂等检查、Lua 脚本）必须注释说明"为什么这么做"，不要描述"做了什么"。
- 不要写无意义的 `// TODO` 而不附 issue 编号或具体描述。

### 13.3 文件头注释（MUST NOT）

- 不得在文件头添加作者、日期、版权等元信息（git 已记录）。

---

## 14. 格式化与 gofmt

### 14.1 缩进（MUST）

- 全部使用 **tab** 缩进。
- **必须收敛**：[common/message/push.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/push.go)、[common/message/request.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/request.go)、[common/message/response.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/response.go) 当前用 4 空格，必须 `gofmt -w` 修复。

### 14.2 结构体字段对齐（MUST）

- struct 字段必须由 `gofmt` 自动对齐，禁止手工空格对齐到不一致状态。
- **必须收敛**：[settlement/service/game_settle_service.go:18-28](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) 的 `virtualBalance *VirtualBalanceService` 多一个空格，必须 gofmt 修复。

### 14.3 import 分组（MUST）

- 三组：标准库 → 第三方 → 项目内部，组间空行。
- 项目内部统一使用全路径 `github.com/cashparty/backend/...`。

### 14.4 行长（SHOULD）

- 单行不超过 120 字符；超出时按参数或运算符换行。

---

## 15. 禁止的写法（Anti-Patterns）

以下写法在新代码中**绝对禁止**，历史代码在重构时收敛：

### 15.1 命名
- ❌ `UserId`、`RoomId`（应 `UserID`、`RoomID`）
- ❌ `IUserService` 接口前缀 I
- ❌ 文件名 `<pkg>_xxx.go` 冗余前缀（如 `stats_handler.go`）
- ❌ 同一模块内 `gormRoomRepository` 与 `RoomRepository` 与 `GormUserRepository` 三种命名并存
- ❌ Receiver 名不一致（同一类型方法里 `s` 和 `svc` 混用）

### 15.2 错误处理
- ❌ `if _, err := ...; err != nil { /* ignore */ }`（[settlement/service/deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) 中 `if exists, _ :=` 的写法）
- ❌ `return err` 不包装（broadcast / bill_manager 现状）
- ❌ 业务状态用 `fmt.Errorf("status is not pending")` 字符串错误（应 `ErrInvalidStatus` 哨兵）
- ❌ `panic(err)` 在启动阶段（应用 `logger.Fatal`）

### 15.3 并发
- ❌ `go func() { ... }()` 不经 `AsyncTaskRunner`（应用层）
- ❌ `context.Background()` 在应用层异步任务里
- ❌ `math/rand` 用于金额拆分
- ❌ `sync.Mutex` + nil 检查做单例（应 `sync.Once`）
- ❌ `sync.RWMutex` 保护热配置字段（应 `atomic.Pointer`）

### 15.4 数据库
- ❌ service 直接持有 `*gorm.DB` 开事务（应通过 `Transaction.Execute`）
- ❌ 状态机 UPDATE 不带 `WHERE status = ?`（必须带乐观锁）
- ❌ `RowsAffected == 0` 当 error 返回（应幂等成功返回 nil）
- ❌ 字符串拼接 SQL

### 15.5 Redis / 锁
- ❌ 内联 Lua 字符串 + `redis.Eval`（应经 `redis.Script`）
- ❌ 释放锁裸 `DEL`（必须 token + Lua）
- ❌ 锁值存 roomID（必须存 UUID token）
- ❌ `Exists` + `Set` 两步式抢占（必须 `SetNX`）

### 15.6 HTTP
- ❌ 多种响应信封并存（必须 `{"code","msg","data"}`）
- ❌ 裸数字 HTTP 状态码（必须 `http.Status*`）
- ❌ `c.JSON` + `c.Abort()` 散落在多个中间件（必须用 `respondError` 助手）
- ❌ `strconv.Atoi(c.Query(...))` 手动解析（必须 `ShouldBindQuery`）

### 15.7 配置
- ❌ 重复声明 `RedisConfig` / `MySQLConfig`（必须复用 `common/config`）
- ❌ 默认值在多处定义
- ❌ 服务默认端口冲突

### 15.8 重复代码
- ❌ 重复方法：[settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) 的 `CreateBillsInTransaction` 与 `CreateBillsOnly` 字节级重复，必须删除其一。
- ❌ 重复 helper：[gateway/service/game.go:122-135](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/game.go) 与 [gateway/service/test.go:67-80](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/test.go) 的 `saveUserAndGetInternalID` 重复，必须抽取共享方法。
- ❌ 重复常量：`kafka.TopicGatewayBroadcast` 与 `broadcast.BroadcastTopicKafka` 是同一字符串，必须复用。
- ❌ 重复 helper：`stats/service/formatDateRange` 与 `stats/handler/FormatDateRange`，必须复用。
- ❌ 重复方法：[game/scheduler/timeout_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go) 的 `ClearAllTimeouts` 与 `ClearAllRoomTimeouts`，必须删除其一。

### 15.9 注释
- ❌ 西语注释（[common/message/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/errors.go)）
- ❌ 同一文件中英西混排

### 15.10 序列化命名
- ❌ 同一项目内 `Marshal()` 与 `ToJSON()` 并存（[common/message/broadcast.go:45](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/broadcast.go) vs [common/message/request.go:42](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/request.go)）。新代码统一 `ToJSON() ([]byte, error)`，历史代码在重构时收敛。

---

## 16. 字符串拼接

字符串拼接是后端代码中最易引入安全漏洞（JSON 注入、URL 双斜杠、签名绕过）与正确性 Bug（Redis key 命名、错误包装丢失解包）的领域。本节规约覆盖 JSON、URL、host:port、文件路径、Redis key、错误包装、SQL、TraceID、UUID 七大场景。

### 16.1 SC-1：JSON 构建（MUST）

- 构建 JSON 字符串（用于协议、签名、存储）MUST 使用 `json.Marshal` 或 `json.NewEncoder`。
- **禁止** `fmt.Sprintf` 反引号模板拼接含用户输入的 JSON（token/roomID/userID 含 `"` 或 `\` 会破坏协议）。
- 签名场景如需控制 HTML 转义，使用 `json.NewEncoder` + `SetEscapeHTML(false)`。

参考：[gateway/server/server.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/server/server.go)（`json.Marshal` 替代内联 JSON）、[common/signature/signer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/signature/signer.go)（`json.NewEncoder` + `SetEscapeHTML(false)`）。

### 16.2 SC-2：URL 拼接（MUST）

- URL 路径拼接 MUST 使用 `url.JoinPath`（Go 1.19+）或 [strutil.JoinURLPath](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/strutil/url.go)，自动处理末尾斜杠避免双斜杠。
- query 参数 MUST 使用 `url.Values.Encode()`，禁止手动 `fmt.Sprintf("%s?%s", ...)`。
- 完整 URL + query 拼接使用 `strutil.BuildURLWithQuery`。

参考：[api/platform/gamingpanda_client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/api/platform/gamingpanda_client.go)、[gateway/service/game.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/game.go)。

### 16.3 SC-3：host:port 构建（MUST）

- 构建 `host:port` 地址（gRPC、HTTP 监听、服务发现）MUST 使用 `net.JoinHostPort(host, strconv.Itoa(port))` 或 [strutil.JoinHostPort](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/strutil/hostport.go)。
- **禁止** `fmt.Sprintf("%s:%d", ip, port)`（IPv6 地址不含方括号会导致解析错误）。

参考：[common/discovery/discovery.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/discovery/discovery.go)、[common/nacos/client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)。

### 16.4 SC-4：文件路径构建（MUST）

- 跨平台文件路径 MUST 使用 `filepath.Join`，禁止硬编码 `/` 分隔符。
- 临时目录 MUST 使用 `os.TempDir()` 而非硬编码 `/tmp`。

参考：[common/config/nacos.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/nacos.go)。

### 16.5 SC-5：Redis key 构建（MUST）

- Redis key MUST 使用 [common/rediskeys](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) 包的常量或工厂函数，禁止散落在 service 里的裸字符串拼接。
- 详见 §7.1。

### 16.6 SC-6：错误包装（MUST）

- 包装底层 error MUST 使用 `fmt.Errorf("...: %w", err)`，禁止 `%v` 包装 `error` 类型。
- Go 1.20+ 支持多个 `%w`（如 `fmt.Errorf("%w: %w", err1, err2)`）。
- `%v` 仅用于包装非 error 类型（如 `recover()` 返回的 `interface{}`）。
- 详见 §4.3。

### 16.7 SC-7：SQL 查询构建（MUST）

- 动态 SQL MUST 使用 GORM 占位符 `?` 传递值，禁止字符串拼接值。
- 动态 WHERE 子句仅允许拼接字面量结构（如 `AND col = ?`），值通过 args 传递。
- 无法参数化的动态结构（如 CASE WHEN 分支标签）MUST 在配置加载时做白名单字符校验。

参考：[stats/repository/stats_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/repository/stats_repository.go)（`validateAmountRanges` 白名单校验）、[stats/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/config/config.go)。

### 16.8 SC-8：TraceID / BizOrderNo 生成（MUST）

- TraceID / BizOrderNo MUST 通过 [TraceIDGenerator](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) 方法生成，禁止业务代码内联 `fmt.Sprintf`。
- 基于业务语义确定性生成（重试时可复现），便于幂等去重。

参考：[settlement/service/trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go)（`GenerateGameSettleTraceID`、`GenerateSessionCreditTraceID`）。

### 16.9 SC-9：UUID 使用（MUST）

- 生成唯一 ID MUST 使用 `github.com/google/uuid`（`uuid.New().String()`），禁止 [common/utils/utils.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/utils/utils.go) 的手写 UUID。
- 截断 UUID 不得少于 12 字符（`uuid.New().String()[:12]`），禁止 `[:8]` 增加碰撞概率。

### 16.10 SC-10：Redis key 分隔符与 Prefix 约定（MUST）

- Redis key 多参数分隔符 MUST 使用 `:`，禁止下划线 `_`。
- 所有 `*Prefix` 常量 MUST 带尾随冒号（如 `KeyRoomHashPrefix = "cashparty:room:hash:"`），调用方一律 `+ "*"` 而非 `+ ":*"`。
- 详见 §7.1。

---

## 17. 调度器（Scheduler）

后台调度器（`scheduler/`）统一遵循本节规约。`AsyncTaskRunner`（§6.1）用于应用层 fire-and-forget 异步任务，调度器用于周期性后台任务，二者分工不同但生命周期管理要求一致。

### 17.1 SCH-1：统一 Scheduler 接口（MUST）

所有调度器 MUST 实现 `common/scheduler.Scheduler` 接口：

```go
type Scheduler interface {
    Name() string
    Start(ctx context.Context) error
    Stop()
}
```

- `Name()` 返回调度器名称，用于日志、metrics、健康检查。
- `Start(ctx)` 接收 appCtx，返回 error（启动失败可聚合）。
- `Stop()` 阻塞等待 goroutine 退出，带超时兜底。

### 17.2 SCH-2：注册到 SchedulerRegistry（MUST）

调度器 MUST 注册到 `common/scheduler.Registry`，禁止散落在 `Container` 中的 ad-hoc 字段。

- `Registry.Register(s Scheduler)` 在构造时调用。
- `Registry.StartAll(appCtx)` 并行启动，`Registry.StopAll(timeout)` 并行停止。
- 仅对需要被其他服务注入的调度器（如 `TimeoutScheduler` 被 `RoomAppService` 注入）保留单独字段。

参考：[game/bootstrap/container.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/container.go) 的 `SchedulerRegistry` 字段 + `initSettlementSchedulers`。

### 17.3 SCH-3：appCtx 作为父 context（MUST）

`Start(ctx)` MUST 接收 appCtx 作为父 ctx，内部 `context.WithCancel(ctx)` 派生调度器 ctx。**禁止** `context.Background()` 作为调度器根 ctx。

- 调度器 ctx 随 appCtx 取消而取消，确保优雅关停。
- 构造函数不得接收 ctx 参数，ctx 只在 `Start` 时传入。

### 17.4 SCH-4：InitialDelay 使用 select（MUST）

`InitialDelay` MUST 用 `select` 实现，禁止 `time.Sleep`：

```go
select {
case <-s.ctx.Done():
    return
case <-time.After(s.config.InitialDelay):
}
```

- `time.Sleep` 阻塞期间无法响应 `Stop()`，导致关停延迟最长等于 `InitialDelay`。

### 17.5 SCH-5：检查 WithRedisLock 返回值（MUST）

`lock.WithRedisLock` 返回值 MUST 被检查并记录。锁获取失败与 task 错误均 MUST 记录到日志和 metrics。

- **禁止**丢弃返回值（`_ = lock.WithRedisLock(...)`）。
- 错误 MUST 记录 `name`、`lock_key`、`error` 字段。

参考：[common/scheduler/base.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/scheduler/base.go) 的 `executeTask`。

### 17.6 SCH-6：检查 service 返回值（MUST）

调度器 `execute` 中调用的 service 方法返回的 error MUST 被检查并记录；**禁止**丢弃返回值。

- service 方法返回 error 时，`execute` MUST `return err`，让 `BaseScheduler.executeTask` 记录到 metrics。
- 多个 service 调用串联时，每个错误都 MUST 记录，但可选择性 `return` 第一个错误。

### 17.7 SCH-7：配置外部化（MUST）

调度器的 `Interval`、`InitialDelay`、`LockTTL`、`CheckInterval` 等参数 MUST 通过配置文件设置，**禁止**硬编码。

- 新增调度器配置结构体定义在 `common/config/types.go`，服务特有配置放在各自 `config/` 包。
- 默认值在 `Set*Defaults` 函数中设置，与历史硬编码值保持一致。
- YAML 配置段必须与配置结构体字段对应。

### 17.8 SCH-8：并行 Stop 带全局预算（MUST）

`SchedulerRegistry.StopAll(timeout)` MUST 并行停止所有调度器，带全局预算（默认 30s）。

- 串行 Stop 在最坏情况下总耗时 = 所有调度器 Stop 超时之和（可达 80s+）。
- 并行 Stop 总耗时 = max(单调度器 Stop 超时, 全局预算)。

### 17.9 SCH-9：Metrics 收集（SHOULD）

调度器 SHOULD 收集 metrics：执行次数、耗时、错误数、panic 数。

- `common/scheduler.Metrics` 提供 `RecordExecution`、`RecordError`、`RecordPanic`、`Snapshot` 方法。
- metrics 数据用于健康检查和问题诊断。

### 17.10 SCH-10：BaseScheduler 跨服务共享（MUST）

`BaseScheduler` 定义在 `common/scheduler/base.go`，跨服务共享。**禁止**在 `settlement/scheduler/` 或其他服务包内重复定义 `BaseScheduler`。

- `settlement/scheduler/base.go`（旧）MUST 删除。
- 各调度器通过组合 `*csched.BaseScheduler` 复用通用逻辑。

### 17.11 SCH-11：TaskFunc 在构造函数中传入（MUST）

`TaskFunc` MUST 在 `NewBaseScheduler` 构造函数中传入，**禁止**在 `Start()` 中赋值。

- 在 `Start()` 中赋值会导致 `nil task` 风险（如果 `Start` 前被调用）。
- 使用闭包模式传递方法值：`s := &Scheduler{...}; s.base = csched.NewBaseScheduler(config, s.execute, redis)`。

### 17.12 SCH-12：ZSET-based 调度器豁免与 handler ctx（MUST）

ZSET-based 调度器（如 `TimeoutScheduler`）依赖 `ZRem` 原子性去重，可豁免分布式锁要求。但 handler goroutine MUST 派生 per-handler ctx：

```go
handlerCtx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
defer cancel()
handler(handlerCtx, roomID, data)
```

- **禁止**直接使用调度器根 ctx（`s.ctx`）作为 handler ctx，避免单个 handler 阻塞影响整体调度。

参考：[game/scheduler/timeout_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go) 的 `checkTimeouts`。

---

## 附录 A：参考实现索引

| 主题 | 参考文件 |
|---|---|
| DDD 分层 | [game/domain/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/)、[game/application/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/)、[game/infrastructure/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/) |
| Repository 接口 + 实现 | [game/domain/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/db_repository.go) + [game/infrastructure/persistence/mysql/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/db_repository.go) |
| 错误码 + GameError | [common/message/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/errors.go) |
| Lua 脚本注册 | [game/infrastructure/persistence/redis/scripts/registry.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/registry.go) |
| Token 锁释放 | [game/infrastructure/persistence/redis/scripts/robot_lock.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/robot_lock.lua.go) |
| 三层幂等 | [settlement/service/deduct_service.go:67-80](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) |
| 指数退避重试 | [settlement/service/credit_retry_service.go:184-191](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go) |
| 乐观锁 UPDATE | [settlement/service/bill_manager.go:99-119](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) |
| atomic.Pointer 热配置 | [game/algorithm/packet_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go) |
| AsyncTaskRunner | [game/application/game_app_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go) |
| 调度器 + Redis 锁 | [common/scheduler/base.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/scheduler/base.go) |
| TraceID 生成 | [settlement/service/trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) |
| gRPC 拦截器 | [game/server/generic_service.go:778-790](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go) |
| 健康检查三端点 | [gateway/health/health.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/health/health.go) |

---

## 附录 B：变更记录

| 日期 | 变更 |
|---|---|
| 2026-07-04 | 初版，基于 backend/ 全量代码（171 文件）分析制定 |
| 2026-07-04 | 新增 §16 字符串拼接规约（SC-1~SC-10）；§4.3 补充 `%w` vs `%v` 说明；§7.1 补充 `common/rediskeys` 统一包说明、Prefix 尾随冒号约定、Lua 孤儿 key 禁止规则 |
| 2026-07-04 | 新增 §17 调度器规约（SCH-1~SCH-12）；§6.2 更新为引用 §17；附录 A 调度器参考实现指向 `common/scheduler/base.go` |
