# CashParty 后端编码规范（Backend Coding Standard）

> 本规范是 CashParty 后端 Go 代码的**唯一权威编码标准**，适用于所有新增与修改代码。
> 规范以**业界最佳实践**为蓝本（Google Go Style Guide、Effective Go、Go Code Review Comments、Twelve-Factor App、Clean Architecture），结合本项目技术栈（Go 1.24、Gin、gRPC、GORM、go-redis、Kafka、Nacos、Redis Lua）落地为可执行的约束。
>
> **目标读者**：AI Coding 工具与人类工程师。每条规约给出 **必须（MUST）/ 应当（SHOULD）/ 不可（MUST NOT）** 三档约束，并尽量附"参考实现"与"反面案例"。
>
> AI 在新增或修改代码时**必须**先查阅本规范；冲突时以本规范为准，仅当本规范未覆盖时回退到业界通用实践。

---

## 目录

- [0. 前言](#0-前言)
- [1. 总则与核心原则](#1-总则与核心原则)
- [2. 项目结构与分层架构](#2-项目结构与分层架构)
- [3. 命名规范](#3-命名规范)
- [4. 错误处理](#4-错误处理)
- [5. 日志与可观测性](#5-日志与可观测性)
- [6. 并发与异步编程](#6-并发与异步编程)
- [7. 接口设计（HTTP / gRPC / WebSocket）](#7-接口设计http--grpc--websocket)
- [8. 数据访问（MySQL / GORM / Redis）](#8-数据访问mysql--gorm--redis)
- [9. 消息队列（Kafka）](#9-消息队列kafka)
- [10. 分布式系统](#10-分布式系统)
- [11. 配置管理](#11-配置管理)
- [12. 依赖注入与生命周期](#12-依赖注入与生命周期)
- [13. 测试规范](#13-测试规范)
- [14. 安全规范](#14-安全规范)
- [15. 性能与资源管理](#15-性能与资源管理)
- [16. 字符串拼接](#16-字符串拼接)
- [17. 注释与文档](#17-注释与文档)
- [18. 格式化与工具链](#18-格式化与工具链)
- [19. 反模式（Anti-Patterns）](#19-反模式anti-patterns)
- [附录 A：参考实现索引](#附录-a参考实现索引)
- [附录 B：业界规范参考](#附录-b业界规范参考)
- [附录 C：项目技术债务清单（必须收敛项）](#附录-c项目技术债务清单必须收敛项)
- [附录 D：变更记录](#附录-d变更记录)

---

## 0. 前言

### 0.1 设计目标

1. **一致性**：消除"相同功能写出不同风格"的问题，让代码像一个人写的。
2. **可读性优先**：代码被阅读的次数远多于被编写的次数，优化阅读体验。
3. **可维护性**：降低 bug 引入概率，提升重构与扩展安全性。
4. **可观测性**：任一线上行为可被日志、metrics、trace 追踪定位。
5. **健壮性**：失败可恢复、重试可幂等、关停可优雅。

### 0.2 适用范围

本规范适用于 `backend/` 下所有 Go 代码：`game/`、`settlement/`、`gateway/`、`stats/`、`common/`、`api/platform/`、`cmd/`。

### 0.3 优先级与冲突解决

当本规范与既有代码冲突时，按以下优先级处理：

1. **本规范**优先于既有代码（除非既有代码已被项目记忆标记为"约定"）。
2. **项目记忆**（`project_memory.md`）中标记为"约定"的，以项目记忆为准。
3. **业界通用实践**（Google Go Style Guide 等）作为本规范的兜底补充。
4. **新代码不得复刻旧的不一致**；历史代码在重构时同步收敛到本规范。

### 0.4 Go 版本与工具链

- Go 版本：`go 1.24.x`（见 [go.mod](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/go.mod)）。
- 所有代码必须通过 `gofmt -l`、`go vet ./...`、`go build ./...` 三项检查。
- 推荐使用 `golangci-lint` 进行静态检查（见 [§18 工具链](#18-格式化与工具链)）。
- 不得引入未被 `go.mod` 已记录的依赖；新增依赖须经过评审。

### 0.5 规约级别

| 级别 | 含义 |
|---|---|
| **MUST** | 必须遵守，违反视为缺陷 |
| **SHOULD** | 强烈推荐，偏离需注释说明理由 |
| **MUST NOT** | 严禁，违反视为缺陷 |
| **MAY** | 可选，由开发者根据场景判断 |

---

## 1. 总则与核心原则

### 1.1 核心原则（MUST）

| 原则 | 含义 | 应用 |
|---|---|---|
| **可读性优先** | 代码为人编写，机器次之 | 命名清晰、避免炫技、复杂逻辑必注释 |
| **显式优于隐式** | 行为可见，无魔法 | 显式错误处理、显式 context 传递、显式依赖注入 |
| **组合优于继承** | Go 无继承，用组合 + 接口 | 嵌入 struct 复用，接口定义行为 |
| **接口隔离** | 小接口、按消费者定义 | 接口定义在消费方，不在实现方 |
| **错误是值** | 错误是普通值，正常处理 | 检查、包装、传递，不忽略 |
| **并发安全优先** | 默认线程安全 | 共享状态加锁或用 atomic/channel |
| **简单性** | 用最简单的正确方案 | 不提前抽象、不过度设计 |
| **局部性** | 相关代码靠近放置 | 同一职责的代码放同一文件/包 |

### 1.2 设计原则（SHOULD）

- **单一职责**：每个函数/类型/包只做一件事。
- **开闭原则**：对扩展开放，对修改关闭；通过接口与组合实现扩展。
- **依赖倒置**：高层模块不依赖低层模块，二者都依赖抽象。
- **最少知识**：只与直接朋友通信，不与陌生人说话。
- **失败快速（Fail-Fast）**：配置错误、不变量违反应立即失败，不延迟到运行期。

### 1.3 代码质量红线（MUST）

以下情况视为质量不达标，必须修改：

1. 编译警告、`go vet` 警告、`gofmt -l` 报告未格式化文件。
2. 单元测试覆盖率低于阈值（见 [§13 测试规范](#13-测试规范)）。
3. 吞掉 error（`if _, _ := ...`）、裸 `return err` 不包装。
4. goroutine 泄漏（无生命周期管理、无超时、无 cancel）。
5. 硬编码的魔法数字、字符串（连接串、密钥、超时、端口）。
6. SQL 注入、Redis key 拼接、JSON 拼接等安全风险。
7. 死代码、重复代码（字节级重复或语义级重复）。

---

## 2. 项目结构与分层架构

### 2.1 项目布局（MUST）

参考 [golang-standards/project-layout](https://github.com/golang-standards/project-layout) 思想，结合 DDD 分层：

```
backend/
├── cmd/                        # 入口：每个服务一个子目录，main.go 仅调用 bootstrap.Run()
│   ├── game/main.go
│   ├── gateway/main.go
│   └── stats/main.go
├── common/                     # 跨服务共享的基础设施代码
│   ├── async/                  # AsyncTaskRunner
│   ├── broadcast/              # 广播抽象
│   ├── config/                 # 通用配置结构与加载
│   ├── idgen/                  # 雪花 ID 生成器
│   ├── kafka/                  # Kafka producer/consumer 抽象
│   ├── lock/                   # 分布式锁
│   ├── logger/                 # 日志封装
│   ├── message/                # 协议消息与错误码
│   ├── mysql/                  # MySQL 连接与 GORM 封装
│   ├── redis/                  # Redis 客户端封装
│   ├── rediskeys/              # Redis key 单一真相源
│   ├── scheduler/              # 调度器框架
│   ├── strutil/                # 字符串/URL/hostport 工具
│   └── trace/                  # TraceID 传播
├── game/                       # 游戏服务（DDD 分层）
├── settlement/                 # 结算服务（DDD 分层）
├── gateway/                    # 网关服务（handler/service/repository）
├── stats/                      # 统计服务（handler/service/repository）
├── api/platform/               # 外部平台 API 客户端
├── config/                     # 配置文件（YAML）
├── migrations/                 # 数据库迁移脚本
├── proto/                      # gRPC protobuf 定义
└── go.mod
```

### 2.2 模块内分层（MUST）

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

### 2.3 依赖方向（MUST）

依赖方向必须**单向向内**，不得反向：

- `domain` 不得 import `infrastructure`、`application`、`bootstrap`。
- `application` 只能依赖 `domain` 中定义的接口，不得直接 import `gorm`/`go-redis`。
- `infrastructure` 实现 `domain` 接口，依赖外部库。
- `bootstrap` 是唯一允许"把具体实现注入到接口"的位置（组合根）。

```
bootstrap → application → domain ← infrastructure
                                    ↓
                              外部库（gorm/redis/kafka）
```

### 2.4 Repository 接口位置（MUST）

- 接口定义在 `domain/` 包中（消费方定义接口原则）。
- 消费方在 `application/` 中以接口字段持有依赖。
- 实现在 `infrastructure/persistence/{mysql,redis}/` 中。
- 不得在 `repository/` 包里直接定义具体 struct 又被 service 直接依赖。

### 2.5 DTO 归属（MUST）

- 对外 Request/Response 与对外枚举常量统一放在 `dto/` 子包。
- 内部领域类型放在 `domain/`。
- **禁止**把 Request/Response struct 散落在 service / handler 文件里。

### 2.6 常量集中（MUST）

- 同一类枚举必须在**唯一**位置声明，不得分散。
- **对外协议枚举**（BillType、BillStatus、RoundStatus 等）：放在 `settlement/dto/constants.go`。
- **领域内部枚举**：放在 `model/constants.go` 或 `domain/constants.go`，全模块统一位置。
- **禁止**在 service 文件里写 `case 1` / `case 2` 这种裸数字，必须用命名常量。

### 2.7 包内组织（SHOULD）

- 一个包聚焦一个职责，包名应能概括包内所有内容。
- 包内文件按职责拆分，单文件不超过 500 行（超出考虑拆分）。
- 包的公开 API 通过 `doc.go` 或包注释说明用途。

---

## 3. 命名规范

参考 [Google Go Style Guide - Naming](https://google.github.io/styleguide/go/decisions#naming) 与 [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)。

### 3.1 文件命名（MUST）

- 全部 lowercase snake_case：`game_app_service.go`、`credit_retry_service.go`、`distributed_lock.go`。
- **禁止**冗余前缀：文件名不要重复包名。`stats/handler/stats_handler.go` 应改为 `stats/handler/handler.go`。
- 测试文件 `_test.go`；Lua 脚本文件 `.lua.go`；mock 文件 `mock_*.go` 或 `*_mock.go`。

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

**新代码统一采用** **小写 `gorm<Domain>Repository`**（unexported struct + `New<Domain>Repository` 构造函数返回 domain 接口）。

### 3.3 接口命名（MUST）

- 单方法行为接口：`-er` 后缀（`Grabber`、`Broadcaster`、`UserSaver`、`RobotChecker`）。
- 多方法资源接口：裸名词（`Client`、`GameStore`、`ServiceClient`、`RoomRepository`）。
- **禁止**给接口加 `I` 前缀（C# 风格，违反 Go 惯例）。
- **禁止**给具体 struct 起名 `UserService` 又同时是接口。

### 3.4 方法命名（MUST）

- 构造函数：`New<Type>(deps...) *Type` 或 `New<Type>(deps...) <Interface>`。
- 公开方法：PascalCase 动词开头，`<Verb><Object>`：`SettleGame`、`DeductForFirstRound`、`ApplyForRefund`。
- 私有 helper：camelCase：`executeBatchDeduct`、`creditRound`、`doRetryCredit`。
- 幂等检查方法：`Is<Player>GameSettled`、`Exists<By><Field>`；**同一模块内** `Exists` 系列必须统一风格（全带 `By` 或全不带）。
- 资源生命周期方法：`Start(ctx) error` + `Stop()` + `Close() error`，三选一时优先 `Start/Stop`，关闭需要错误返回值时用 `Close`。
- 布尔返回方法：`Is<X>` / `Has<X>` / `Can<X>` / `Should<X>`，禁止 `Check<X>` 返回 bool。

### 3.5 Receiver 命名（MUST）

- 一律使用类型首字母的 1-3 字母缩写，**整个文件内一致**。
- 服务/调度器：`s`（`s *SettlementService`、`s *TimeoutScheduler`）。
- Manager：`m`（`m *BillManager`）。
- Repository：`r`（`r *gormRoomRepository`）。
- Generator：`g`（`g *TraceIDGenerator`）。
- Checker：`c`（`c *redisRobotChecker`）。
- 模型值方法：类型首字母（`func (u *User) GetUserID()`、`func (r Room) TableName()`）。
- **禁止**同一类型在不同方法里 receiver 名不同。
- **禁止**使用 `this`、`self`（Python/Java 风格）。

### 3.6 变量与常量（MUST）

- 缩写词全大写：`userID`、`roomID`、`URL`、`IP`、`HTTP`、`API`，**不得**写 `userId`、`roomId`、`Url`、`Ip`。
- 常量：导出 PascalCase（`RewardTypeStraight`），未导出 camelCase（`defaultCheckIntervals`）。
- 魔法数字：禁止出现在业务逻辑里。所有阈值、超时、状态码、重试次数必须有命名常量。
- 分组常量用 `const ()` 块，相关常量聚集。

### 3.7 构造函数（MUST）

- 统一 `New<Type>(deps...)`。
- **禁止** `Load`、`InitLocker`、`Obtain`、`WithLock` 作为"构造"函数名（`Load` 仅用于"从外部源加载配置"语义）。
- 单例初始化：统一 `sync.Once`，不得用 `sync.Mutex` + nil 检查。
- 参数超过 5 个时，**应当**使用 functional options 模式或配置 struct（见 [§12.2](#122-构造函数参数should)）。

### 3.8 包命名（MUST）

- 全小写、单单词、无下划线、无连字符：`logger`、`rediskeys`、`strutil`。
- 包名应与目录名一致。
- **禁止**包名包含大写、下划线、复数（`utils` 可接受，`Util`/`my_utils`/`Utils` 禁止）。
- 避免与常用包冲突：`util`、`common`、`config` 等泛化名称应加前缀上下文（`strutil`、`redisutil`）。

---

## 4. 错误处理

参考 [Go Blog - Error handling and Go](https://go.dev/blog/error-handling-and-go)、[Go 1.13 errors](https://go.dev/blog/go1.13-errors)、[Dave Cheney - Practical Go](https://dave.cheney.net/practical-go)。

### 4.1 错误模型（MUST）

业务错误统一使用 `*message.GameError`（带 `Code int` 和 `Msg string`），通过 `message.NewError(code, msg)` / `message.NewErrorWithMsg(code, msg)` 构造。

- 对外返回（gRPC handler / HTTP handler）：必须返回 `*message.GameError`，由 handler 统一转换为响应。
- 算法库（`game/algorithm/`）保留独立的 `*algorithm.Error`，因为它是无外部依赖的纯库。
- **禁止**再创建新的自定义 error 类型（已有 `*message.GameError` 与 `*algorithm.Error` 两个）。

### 4.2 哨兵错误（SHOULD）

- 可恢复的、需 `errors.Is` 判别的错误，使用 `var ErrXxx = errors.New("...")`，放在 `domain/errors.go` 或 `application/errors.go`。
- 哨兵错误命名：`Err<Action>` 或 `Err<Condition>`，如 `ErrNoEmptySeat`、`ErrInvalidStatus`、`ErrBillAlreadySettled`。
- 业务状态机"不符合预期状态"应使用哨兵，**不得**用 `fmt.Errorf("refund status is not pending")` 这种字符串错误。

### 4.3 错误包装（MUST）

- 跨层返回错误时必须用 `fmt.Errorf("<action> failed: %w", err)` 包装，保留调用栈与 `errors.Is`/`errors.As` 解包能力。
- 包装消息格式统一：`"<动词+对象> failed: %w"`，例如 `"get virtual balance failed: %w"`、`"create round settlement and bills failed: %w"`。
- **`%w` vs `%v`**：
  - 包装 `error` 类型时 **MUST** 使用 `%w`，**禁止** `%v`（`%v` 会丢失 `errors.Is`/`errors.As` 解包能力）。
  - Go 1.20+ 支持多个 `%w`（如 `fmt.Errorf("%w: %w", err1, err2)`），可在合并多错误时使用。
  - `%v` 仅用于包装非 error 类型（如 `recover()` 返回的 `interface{}`）。
- **禁止**裸 `return err` 不包装（除以下例外）：
  - 已是哨兵错误且无需附加上下文。
  - 已是 `*message.GameError` 业务错误（自带 Code，无需再包装）。

### 4.4 错误检查（MUST）

- 检查特定错误用 `errors.Is`（哨兵）或 `errors.As`（类型断言），**禁止**字符串匹配（`strings.Contains(err.Error(), "...")`）。
- 检查后若需分支处理，必须显式处理所有分支，不得遗漏。

### 4.5 错误吞没（MUST NOT）

- **禁止** `if exists, _ := ...` 这种丢弃 error 的写法。必须显式检查 error 并返回或记录。
- **禁止** `defer fn()` 中忽略 error（必须 `if err := fn(); err != nil { logger.Warn(...) }`）。
- 例外：callLog 创建失败可以降级为 `logger.Warn`，但必须捕获变量名（如 `callLogErr`）并记录上下文。
- **禁止**在 goroutine 中吞掉 panic（必须 `defer recover()` 并记录 stack）。

### 4.6 fail-open vs fail-closed（MUST）

按业务场景决定，但必须有显式注释说明选择：

| 场景 | 策略 | 理由 |
|---|---|---|
| 资金扣减/入账、Kafka 事件去重 | **fail-closed**（返回 error 触发重试） | 不可丢钱、不可重复消费 |
| 限流（rate limiter） | fail-open（放行 + Warn 日志） | 用户体验优先 |
| 房间事件消费（room_event_consumer） | fail-open | 非关键路径 |
| 广播失败 | fail-closed（返回 error 给上层决策） | 由上层决定是否阻塞 |
| 事件发布失败（PublishRoomEvent/PublishGameEvent） | fail-open（Warn 日志，不阻塞主流程） | Redis 状态已更新，MySQL 计数为异步快照，可由下一次事件或定时对账补齐 |
| Redis 不可用导致幂等检查失败 | **fail-closed** | 防重复处理 |
| 健康检查依赖失败 | fail-closed（返回 not_ready） | 避免流量打到不健康实例 |

### 4.7 panic 恢复（MUST）

所有长生命周期 goroutine 必须有 `defer recover()`，并在恢复时记录 `logger.Error("panic", "stack", debug.Stack(), ...)`。

- panic 应仅在"程序无法继续运行"的致命错误时使用（如不变量违反、配置错误）。
- **禁止**用 panic 作为正常错误处理流程（业务错误必须用 error 返回）。
- 启动阶段致命错误用 `logger.Fatal`，**禁止** `panic(err)`。

参考：[game/scheduler/timeout_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go)、[game/scheduler/virtual_balance_sync.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/virtual_balance_sync.go)。

---

## 5. 日志与可观测性

参考 [Twelve-Factor App - Logs](https://12factor.net/logs)、[OpenTelemetry Go](https://opentelemetry.io/docs/instrumentation/go/)。

### 5.1 Logger 使用（MUST）

- 统一通过 `github.com/cashparty/backend/common/logger` 包级函数：`logger.Info/Warn/Error/Fatal`。
- **禁止**直接使用 `fmt.Println`、`log.Printf` 输出业务日志。
- **禁止**使用 `zap` 原生 API，必须通过 `logger.*` 封装（便于统一格式与级别控制）。

### 5.2 结构化字段（MUST）

- 字段名一律 `snake_case`：`user_id`、`room_id`、`trace_id`、`round_trace_id`、`biz_order_no`、`error`。
- 字段值不得包含敏感信息（密码、token、身份证号等），如需记录必须脱敏。
- 顺序：消息字符串 → 业务 ID 字段 → `error` 字段。
- `trace_id` 是日志标准字段，所有跨越进程/服务边界的请求和异步消息处理 MUST 在日志中携带 `trace_id`，便于全链路追踪（详见 [§5.6](#56-traceid-传播must)）。

```go
logger.Error("settle player failed",
    "session_id", sessionID,
    "user_id", userID,
    "trace_id", traceID,
    "error", err)
```

### 5.3 日志级别（MUST）

| 级别 | 使用场景 |
|---|---|
| `Debug` | 仅本地调试用，生产关闭；包含详细数据流、状态变化 |
| `Info` | 启动/停止、调度器 tick、状态机正向流转、关键决策结果、外部调用发起 |
| `Warn` | 可恢复失败（callLog 写入失败、缓存 miss、限流放行、幂等命中跳过、调度器单次重试失败、降级触发） |
| `Error` | 不可恢复失败（panic、扣款失败、广播失败、DB 写入失败、外部调用失败） |
| `Fatal` | 仅启动阶段致命错误（配置加载失败、端口监听失败、依赖初始化失败） |

- **禁止**在 `Info` 级别记录高频事件（每秒 > 100 次），降级为 `Debug`。
- **禁止**在 `Error` 级别记录可恢复错误（应 `Warn`）。
- 调度器重试失败应统一用 `Warn`（因为下次还会重试）。

### 5.4 日志内容规范（MUST）

- 消息字符串：英文、第三人称、过去时、点号结尾。如 `"user joined room"`、`"bill created"`。
- **禁止**用 `fmt.Sprintf` 拼接日志消息，必须用结构化字段。
- **禁止**在循环内打日志（除非循环次数 < 10 且每次有意义的决策点）。
- 错误日志必须包含足够上下文（who/what/when/why），便于排查。

### 5.5 Metrics（SHOULD）

服务 SHOULD 暴露 Prometheus 格式的 metrics，至少覆盖：

- **请求级**：QPS、延迟分布（P50/P95/P99）、错误率（按 endpoint/code 维度）。
- **资源级**：goroutine 数、内存占用、GC 耗时、连接池使用率。
- **业务级**：房间在线数、活跃用户数、结算队列深度、Kafka lag。
- **调度器级**：执行次数、耗时、错误数、panic 数（见 [§10.6](#106-调度器规约)）。

### 5.6 TraceID 传播（MUST）

TraceID 是一次业务请求/操作的端到端追踪标识，MUST 满足：入口生成、Context 传播、跨边界注入/恢复、与幂等键分离。

完整规约见 [§10.10 TraceID 传播规约](#1010-traceid-传播规约)。

### 5.7 广播失败日志（MUST）

游戏广播失败必须以 `Warn` 级别记录，并带 `roomID`、`event`、`userID` 上下文。

参考：[game/infrastructure/broadcast/broadcaster.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/broadcast/broadcaster.go)。

---

## 6. 并发与异步编程

参考 [Go Blog - Go Concurrency Patterns](https://go.dev/blog/pipelines)、[Context](https://pkg.go.dev/context)、[Practical Go - Concurrency](https://dave.cheney.net/2014/08/24/go-has-both-sync-and-async-execution-models)。

### 6.1 goroutine 生命周期（MUST）

**所有 goroutine 必须有明确的生命周期管理**：

1. 必须有退出机制（`ctx.Done()` 或显式 `stop` channel）。
2. 必须有超时兜底（避免永久阻塞）。
3. 必须有 panic 恢复（避免进程崩溃）。
4. 必须被 `WaitGroup` 或类似机制跟踪（确保优雅关闭）。

- **禁止**应用层直接 `go func() { ... }()`，必须通过 `AsyncTaskRunner` 调度。
- **禁止** goroutine 泄漏（无退出机制的"fire-and-forget" goroutine）。

### 6.2 context.Context 使用（MUST）

`context.Context` 是 Go 并发的核心，必须正确使用：

- **必须**作为函数第一个参数传递：`func DoSomething(ctx context.Context, ...) error`。
- **禁止**将 context 存入 struct 字段（context 应流式传递，不存储）。
- **禁止**使用 `context.Background()` 在应用层（仅用于 main 顶层、测试、daemon 的根 ctx）。
- **禁止**使用 `context.TODO()`（应明确是 `Background` 还是派生的 ctx）。
- **必须**在可能阻塞的调用前派生超时 ctx：`ctx, cancel := context.WithTimeout(ctx, timeout); defer cancel()`。
- **必须**调用 `cancel()` 释放资源（即使函数提前 return，用 `defer cancel()`）。
- **禁止**将 nil context 传入函数（不确定时用 `context.Background()`）。

### 6.3 AsyncTaskRunner（MUST）

应用层所有异步 goroutine 必须通过 `AsyncTaskRunner` 调度：

- 必须传入从 `AsyncTaskRunner` 派生的 context，**禁止** `context.Background()`。
- 必须设置 per-task 超时（5-30s 视业务而定）。
- `AsyncTaskRunner` 必须有 closed 状态保护，`Wait` 后再 `Add` 必须安全返回错误而非 panic。
- 必须有 panic 恢复 + `WaitGroup` 跟踪。

参考：[common/async/task_runner.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/async/task_runner.go)。

### 6.4 调度器根 context（SHOULD）

后台调度器（`scheduler/`）的根 context MUST 由 `bootstrap` 传入的 appCtx 派生（`context.WithCancel(appCtx)`），禁止 `context.Background()`。调度器内部派生的每次扫描必须有 per-scan 超时。

完整调度器规约见 [§10.6](#106-调度器规约)。

### 6.5 并发原语选择（MUST）

| 场景 | 推荐原语 | 理由 |
|---|---|---|
| 一次性初始化 | `sync.Once` | 简单、安全 |
| 读多写少的配置 | `atomic.Pointer[T]` | 无锁、高性能 |
| 计数器 | `atomic.Int64` / `atomic.Uint64` | 比 mutex 高效 |
| 复杂共享状态 | `sync.RWMutex` | 读多写少时优于 mutex |
| 简单互斥 | `sync.Mutex` | 默认选择 |
| goroutine 间通信 | channel | 不要通过共享内存通信 |
| 等待一组 goroutine | `sync.WaitGroup` | 标准模式 |

- **禁止** `sync.Mutex` + nil 检查做单例（应 `sync.Once`）。
- **禁止** `sync.RWMutex` 保护热配置字段（应 `atomic.Pointer`）。
- **禁止**通过共享变量通信而不加锁（应"通过通信共享内存"或加锁）。
- **禁止**复制 mutex/atomic 值（值类型含 mutex 必须指针传递）。

### 6.6 热配置并发（MUST）

被并发读取、由 nacos 热更新的配置字段必须使用 `atomic.Pointer[T]`，**禁止** `sync.RWMutex` 保护普通字段。

- `Generate` 等方法必须在方法入口一次性 load snapshot，方法内不得再次 load，保证一次调用看到一致快照。

参考：[game/algorithm/packet_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go) 的 `config atomic.Pointer[Config]` + `generator atomic.Pointer[Generator]`。

### 6.7 随机数（MUST）

| 场景 | 必须使用 | 禁止 | 理由 |
|---|---|---|---|
| 金额、红包拆分、奖励生成、token 生成 | `crypto/rand` | `math/rand` | 安全性：可预测会导致资金损失 |
| 业务唯一 ID | 雪花 ID（`idgen`） | `math/rand` | 唯一性保证 |
| Robot AI 行为（座位选择、延迟、跳过概率） | `math/rand` 允许 | - | 不涉及资金，性能优先 |

### 6.8 Stop 超时（SHOULD）

- 后台调度器 `Stop()`：10s 超时。
- 用户面服务（gRPC/HTTP）`GracefulStop`：30s 超时，超时后 fallback 到 `Stop()`。
- 应用层 `AsyncTaskRunner.Wait()`：30s 超时。

### 6.9 单例初始化（MUST）

统一 `sync.Once`，参考 [common/logger/logger.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/logger/logger.go)、[common/idgen/snowflake.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go)。

不得使用 `sync.Mutex` + nil 检查。

---

## 7. 接口设计（HTTP / gRPC / WebSocket）

参考 [REST API Design Rulebook](https://www.oreilly.com/library/view/rest-api-design/9781449317904/)、[Google API Design Guide](https://cloud.google.com/apis/design)、[gRPC Best Practices](https://grpc.io/docs/guides/best-practices/)。

### 7.1 HTTP 响应信封（MUST）

HTTP API 响应**必须**统一为：

```json
{ "code": 0, "msg": "", "data": <object|null> }
```

- 成功：`code=0`，`msg` 留空字符串或省略，`data` 为业务数据。
- 失败：`code` 为 `message.Code*` 业务码（不是 HTTP 状态码乘 100），`msg` 为可读消息，`data` 为 `null`。
- HTTP 状态码：成功 200；客户端错误 4xx；服务端错误 5xx。HTTP 状态码与业务 `code` **分离**，不要混用。
- **禁止**多种响应形状并存（如 `{"success": false}`、`{"status": "ok"}` 等）。

### 7.2 响应助手（MUST）

每个 HTTP 模块必须提供统一的响应助手：

```go
func respondOK(c *gin.Context, data interface{}) {
    c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "", "data": data})
}

func respondError(c *gin.Context, httpStatus int, code int, msg string) {
    c.JSON(httpStatus, gin.H{"code": code, "msg": msg, "data": nil})
}
```

### 7.3 HTTP 状态码（MUST）

- 必须使用 `http.Status*` 常量，**禁止**裸数字 `429`、`401`、`500`。
- 常用映射：
  - `200 OK`：成功获取/更新资源。
  - `201 Created`：成功创建资源。
  - `204 No Content`：成功但无响应体。
  - `400 Bad Request`：请求格式错误、参数校验失败。
  - `401 Unauthorized`：未认证。
  - `403 Forbidden`：无权限。
  - `404 Not Found`：资源不存在。
  - `409 Conflict`：资源冲突（如重复创建）。
  - `429 Too Many Requests`：限流。
  - `500 Internal Server Error`：服务端错误。
  - `503 Service Unavailable`：服务不可用（维护中、依赖故障）。

### 7.4 路由注册（MUST）

- Handler 必须暴露 `RegisterRoutes(r *gin.RouterGroup)` 方法，由 bootstrap 调用。
- **禁止**在 server 内部命令式 `setupRoutes()`。
- 路由分组按版本与资源：`/api/v1/users`、`/api/v1/rooms`。
- 路由命名：复数名词、kebab-case：`/api/v1/round-settlements`。

### 7.5 请求绑定与校验（MUST）

- 优先使用 `c.ShouldBindQuery` / `c.ShouldBindJSON`，绑定到 DTO struct。
- **禁止**用 `strconv.Atoi(c.DefaultQuery(...))` 手动解析。
- DTO struct 必须有 `binding` tag 校验：`min`、`max`、`required`、`oneof`、`email` 等。
- 校验失败统一返回 `400 Bad Request` + 错误消息。

```go
type PaginationReq struct {
    Page     int    `form:"page" binding:"min=1"`
    PageSize int    `form:"page_size" binding:"min=1,max=100"`
    Sort     string `form:"sort" binding:"omitempty,oneof=asc desc"`
}
```

### 7.6 中间件风格（MUST）

- gin 中间件统一为 `func XxxMiddleware(deps...) gin.HandlerFunc` 自由函数。
- 中间件 struct 若有 `Stop()` 方法，必须真正释放资源；无资源时不得定义空 `Stop()`。
- 中间件必须显式调用 `c.Next()` 或 `c.Abort()`，不要遗漏。
- 错误统一通过 `respondError` 返回，**禁止** `c.JSON` + `c.Abort()` 散落。

### 7.7 健康检查（MUST）

每个对外服务必须暴露三个端点：

- `/health`：完整状态（依赖、连接数、系统资源），返回统一信封 `{"code":0,"data":<HealthStatus>}`。
- `/ready`：就绪检查（依赖连通性），返回 `{"code":0,"data":{"status":"ready"}}` 或 `{"code":1,"data":{"status":"not_ready","error":...}}`。
- `/live`：存活检查，恒返回 `{"code":0,"data":{"status":"alive"}}`。

- `/health` 必须返回真实状态（依赖、连接池、goroutine 数），**禁止**恒返回 ok。
- `/ready` 失败时 HTTP 状态码必须 503，让负载均衡摘除流量。

### 7.8 gRPC 设计（MUST）

- gRPC handler 必须用 `recoveryUnaryInterceptor` 包裹，panic 时返回 `code = CodeInternal` + `*message.GameError`。
- 业务错误转换为 `ForwardResponse{Code, Msg}`，不得把 Go `error` 字符串透传给前端。
- gRPC 服务必须实现优雅关闭：`GracefulStop()` + 30s 超时。
- gRPC 跨服务调用 MUST 通过 metadata 透传 TraceID（见 [§10.10](#1010-traceid-传播规约)）。

参考实现：[game/server/generic_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go)。

### 7.9 WebSocket 设计（MUST）

- WebSocket 消息必须有统一信封（`type`、`data`、`trace_id`）。
- 连接必须有心跳机制（ping/pong），超时未响应必须断开。
- 连接必须有重连机制（客户端），服务端必须支持断线重连后状态恢复。
- 广播消息必须幂等（同一消息多次投递不产生副作用）。

### 7.10 API 版本控制（SHOULD）

- HTTP API 在 URL 中携带版本：`/api/v1/...`。
- gRPC 通过 package 名携带版本：`cashparty.v1.GameService`。
- 破坏性变更必须升版本，旧版本至少保留 1 个迭代周期。

参考：[proto/common/generic.proto](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/proto/common/generic.proto)。

---

## 8. 数据访问（MySQL / GORM / Redis）

参考 [GORM Best Practices](https://gorm.io/docs/)、[go-redis Best Practices](https://redis.io/docs/clients/go/)。

### 8.1 Model 定义（MUST）

- 字段 PascalCase，`gorm:"..."` 标签完整：`primaryKey`、`autoIncrement`、`uniqueIndex`、`index`、`index:idx_xxx`（复合索引）、`size:N`、`not null`、`default:...`、`type:text`、`autoCreateTime`、`autoUpdateTime`。
- 每个 model 必须显式定义 `func (T) TableName() string { return "snake_case_table" }`。
- 表名 snake_case 单数形式：`bill_record`、`round_settlement`、`exception_record`、`refund_audit`。
- 时间字段：
  - `CreatedAt time.Time` / `UpdatedAt time.Time`：值类型，必填。
  - 可空事件时间：指针 `*time.Time`（`StartedAt *time.Time`、`EndedAt *time.Time`、`RefundAppliedAt *time.Time`、`NextRetryAt *time.Time`）。
- 金额字段：使用 `int64`（分）或 `decimal.Decimal`，**禁止** `float64`（浮点精度问题）。
- JSON 字段：使用 `datatypes.JSON` 或 `json.RawMessage`，避免 `string` 手动序列化。

### 8.2 事务边界（MUST）

- 事务通过 `domain.Transaction.Execute(ctx, func(txCtx context.Context) error { ... })` 调用，**禁止** service 直接持有 `*gorm.DB` 开事务。
- **事务必须短小**：事务内禁止 RPC 调用、Kafka Producer、耗时计算。
- 跨服务调用必须移出主事务，靠内部幂等机制保证；主事务回滚不得连带回滚 bill 记录。
- 事务超时必须通过配置控制（默认 30s），通过 `context.WithTimeout` 派生 ctx。

### 8.3 乐观锁（MUST）

所有"状态机推进"类的 UPDATE 必须带 `WHERE status = ?` 或 `WHERE status != ?` 条件，并检查 `RowsAffected`：

- `RowsAffected == 0` 表示状态已被并发推进，按"幂等成功"处理：返回 `nil`（**不得**返回 error）。
- `RowsAffected == 1` 表示推进成功。
- **禁止**不带条件的 `UPDATE`（会全表更新）。

```go
result := db.Model(&Bill{}).Where("id = ? AND status = ?", id, StatusPending).Update("status", StatusSuccess)
if result.Error != nil {
    return fmt.Errorf("update bill status failed: %w", result.Error)
}
if result.RowsAffected == 0 {
    // 状态已被并发推进，幂等成功
    return nil
}
```

### 8.4 唯一索引与幂等（MUST）

幂等键必须有 DB 唯一索引：

- `BizOrderNo`：`uniqueIndex;size:64`
- `RoundTraceID`：`uniqueIndex;size:64`
- `RoundID`（在 `RoundSettlement` 上）：`uniqueIndex;not null`
- `RefundOrderNo`：`uniqueIndex;size:64;not null`
- `ExceptionNo`：`uniqueIndex;size:32;not null`
- 复合唯一索引：`(round_trace_id, bill_type, user_id)` 防止重复创建账单。

### 8.5 Repository 聚合（MUST）

- `DBRepositoryImpl` 必须在构造函数中 eagerly 初始化所有子 repository，字段构造后只读。
- **禁止**懒加载子 repository（并发安全风险）。
- `domain.Transaction` 接口必须扩展为子 repo 访问器集合，便于事务内调用。

参考：[game/infrastructure/persistence/mysql/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/db_repository.go)。

### 8.6 原生 SQL（SHOULD）

复杂聚合查询允许使用 `db.Raw(sql, args...).Scan(&dest)`，但必须：

- 使用占位符 `?`，**禁止**字符串拼接 SQL（SQL 注入风险）。
- 动态 WHERE 子句仅允许拼接字面量结构（如 `AND col = ?`），值通过 args 传递。
- 无法参数化的动态结构（如 CASE WHEN 分支标签）MUST 在配置加载时做白名单字符校验。
- 返回 slice 时统一做 `if x == nil { x = []dto.X{} }` 归一化。

### 8.7 连接池（MUST）

- MySQL 连接池参数必须从配置读取：`MaxOpenConns`、`MaxIdleConns`、`ConnMaxLifetime`、`ConnMaxIdleTime`。
- Redis 连接池参数必须从配置读取：`PoolSize`、`MinIdleConns`、`MaxIdleConns`、`ConnMaxIdleTime`、`ConnMaxLifetime`。
- **禁止**硬编码连接池参数。
- 连接池参数必须有合理默认值（见 [§11 配置管理](#11-配置管理)）。

### 8.8 Redis Key 命名（MUST）

- 统一前缀 `cashparty:`，分隔符 `:`。
- 所有 Redis key 常量与工厂函数 MUST 集中在 [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go)（单一真相源，跨服务共享）。
- `game/infrastructure/persistence/redis/keys.go`、`settlement/infrastructure/persistence/redis/keys.go`、`gateway/keys.go` 仅为 re-export 兼容层，MUST 委托到 `common/rediskeys`，禁止新增定义。
- 工厂函数命名 `XxxKey(args...)`。
- 禁止裸字符串拼 key 散落在 service 里。
- 所有 `*Prefix` 常量 MUST 带尾随冒号（如 `KeyRoomHashPrefix = "cashparty:room:hash:"`），调用方一律 `+ "*"` 或 `+ specificKey`。
- 多参数 key 的分隔符 MUST 统一为 `:`，禁止下划线 `_`。
- Lua 脚本中使用的 key 前缀 MUST 在 `common/rediskeys` 有对应常量，禁止出现 Lua 孤儿 key。

参考：[common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go)。

### 8.9 Redis Wrapper（SHOULD）

- 调用方应通过 `*cRedis.Client` 调用，避免直接 import `go-redis/v9`。
- 新增 Redis 操作时优先扩展 `common/redis/redis.go` 包装方法。
- Lua 脚本必须经 `cRedis.NewScript(name, src)` 注册（见 [§10.7](#107-lua-脚本规约)）。

### 8.10 数据库迁移（MUST）

- 所有 schema 变更必须有迁移脚本，放在 `migrations/` 目录。
- 迁移脚本命名：`YYYYMMDD_description.sql`。
- 迁移脚本必须可回滚（提供 down 脚本或明确回滚步骤）。
- **禁止**通过 GORM AutoMigrate 在生产环境修改 schema。

---

## 9. 消息队列（Kafka）

参考 [Kafka Best Practices](https://www.confluent.io/blog/kafka-best-practices/)、[segmentio/kafka-go](https://github.com/segmentio/kafka-go)。

### 9.1 消息格式（MUST）

- 所有消息必须使用统一 envelope，包含：`event_id`、`trace_id`、`timestamp`、`version`、`payload`。
- payload 使用 `json.RawMessage` 灵活承载不同事件类型。
- 所有消息 MUST 包含 Key：
  - `RoomEvent`：`roomID`
  - `GameEvent`：`roomID_sessionID`
  - 广播：`roomID` 或 `userID`
- Key 用于 Kafka 分区，保证同一 Key 的消息有序。

### 9.2 Producer 规范（MUST）

- Kafka producer 必须使用 `sync.RWMutex` 或 `atomic.Pointer` 保护并发访问。
- 必须使用 **Hash balancer**（不是 LeastBytes），保证消息按 Key 分区有序。
- brokers 配置必须有 guard 检查，防止空配置启动。
- 发送失败必须重试，重试次数与间隔从配置读取。
- 发送成功后必须记录 Info 日志（含 topic、partition、offset）。

### 9.3 Consumer 规范（MUST）

- GroupID 必须使用 `{base}-{nodeID}` 模式，区分多实例。
- 必须实现 **fail-closed** 错误处理：
  - handler 失败时不提交 offset，触发重试。
  - 重试采用指数退避：`delay = base * 2^retryCount`，封顶 `maxDelay`。
  - 超过最大重试次数进入 DLQ（Dead Letter Queue）。
- 必须有 panic 恢复，防止单条消息 panic 导致 consumer 崩溃。
- `Close()` 必须在 `taskRunner.Wait()` 之后、`Producer.Close()` 之前调用，防止消息处理中断。
- 必须通过 `bootstrap/mq_helper.go` 统一初始化。

### 9.4 幂等消费（MUST）

- 必须实现三层幂等防护（见 [§8.4](#84-唯一索引与幂等must) 与 [§10.2](#102-分布式锁must)）：
  1. Redis SetNX 抢占锁（`tryAcquire` 返回 `(bool, string, error)`，token 用于安全释放）。
  2. DB 唯一索引兜底。
  3. 状态机检查。
- 业务逻辑失败时必须释放 Redis 锁（`Del`），允许重试。
- 业务逻辑成功时保留锁作为幂等标记（不释放），由 TTL 过期自动清理。
- **禁止**用 `Exists` + `Set` 两步式实现抢占（race condition）。

### 9.5 顺序性保证（MUST）

- 同一 Key 的消息必须按顺序处理（Hash balancer 保证分区有序）。
- consumer 必须串行处理同一分区的消息（不得并行）。
- 跨分区的消息无顺序保证，业务设计不得依赖跨 Key 顺序。

### 9.6 重试与 DLQ（MUST）

- 重试退避：`delay = base * 2^retryCount`，封顶 `maxDelay`，默认 `base=5s`、`maxDelay=5min`、`maxRetryCount=3`。
- 超过 `maxRetryCount` 必须进入 DLQ，并创建 `ExceptionRecord` 升级人工处理。
- DLQ 消息必须保留原始消息内容、失败原因、重试次数。
- 必须收敛：重试必须更新 `retry_count` 和 `next_retry_time`，**禁止**只更新状态不递增 retry_count。

---

## 10. 分布式系统

### 10.1 一致性模式（MUST）

分布式系统优先采用**最终一致性**，通过以下模式实现：

| 模式 | 应用场景 | 实现 |
|---|---|---|
| **幂等 + 重试** | 跨服务调用、消息消费 | 业务幂等键 + 指数退避重试 |
| **Processing 中间态** | RPC + DB 一致性 | 本地置 Processing → 调 RPC → 置终态 |
| **Outbox 模式** | DB + 消息一致性 | DB 写入 + 同事务写 outbox 表，异步投递 |
| **Saga** | 跨服务事务 | 每步有补偿操作，失败时反向补偿 |
| **对账** | 数据最终一致 | 定时对账任务，发现差异触发修复 |

- **禁止**跨服务分布式事务（XA、2PC），性能与可用性代价过高。
- RPC 调用与 DB 写操作必须有 Processing 中间态：先置 Processing（带乐观锁）→ 调 RPC → 置终态。
- 重试发现 Processing 状态时，必须先查平台侧状态（用 BizID），平台已成功则置终态不重复调 RPC。

### 10.2 分布式锁（MUST）

- **抢占锁**使用 `SetNX` + 随机 UUID token。
- **释放锁**必须用 Lua 脚本 `if GET key == token then DEL key end`，**禁止**裸 `DEL`。
- 锁值必须是 token，**不得**是 roomID 或其它业务字段。
- 锁 TTL 必须从配置读取，**禁止**硬编码。
- watchdog 续期 goroutine 必须监听 `ctx.Done()`，**禁止** `time.Sleep`。
- `WithLock` 必须用 `defer recover()` 捕获业务 panic，转换为 error 返回。
- `WithLock` 获取锁后必须 `defer Release(ctx)`，确保 panic 或 return 时锁被释放。
- `InitLocker` 必须用 `sync.Once` 初始化，**禁止**运行期 nil 检查。
- `tryAcquire` 必须返回 `(bool, string, error)`：token 传给调用方用于安全释放。

参考：[common/lock/distributed_lock.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/distributed_lock.go)、[common/lock/scripts/release_lock.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/scripts/release_lock.lua.go)。

### 10.3 事务规约（MUST）

- **TX-1**：所有"状态机推进"类的 UPDATE 必须带 `WHERE status = ?`，`RowsAffected == 0` 当幂等成功。
- **TX-2**：跨表写操作必须在同一事务。
- **TX-3**：RPC 调用与 DB 写必须有 Processing 中间态。
- **TX-4**：重试发现 Processing 必须先查平台侧状态。
- **TX-5**：跨服务调用必须在主事务外执行。
- **TX-6**：重试退避必须用指数退避。
- **TX-7**：重试必须更新 `retry_count` 和 `next_retry_time`。
- **TX-8**：幂等性检查必须覆盖所有非终态，**禁止**只检查 Success。
- **TX-9**：BillRecord 必须有复合唯一索引 `(round_trace_id, bill_type, user_id)`。
- **TX-10**：ExceptionNo 必须确定性生成：`EXC_{billID}_{exceptionType}`。
- **TX-11**：`return err` 必须用 `%w` 包装。
- **TX-12**：**禁止** `if exists, _ :=` 模式。

### 10.4 三层幂等防护（MUST）

资金/状态相关写入必须有三层幂等防护：

1. **Redis SetNX 抢占**：`lock.WithRedisLock(ctx, redis, key, ttl, fn)`。
2. **DB 唯一索引**：幂等键（BizOrderNo / RoundTraceID / RefundOrderNo）。
3. **状态机检查**：进入逻辑后先查现有状态，已是终态则直接返回 `nil`。

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

参考：[settlement/service/deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go)。

### 10.5 幂等键生成（MUST）

- `BizOrderNo` 必须确定性生成：`fmt.Sprintf("%s_%d_%d", roundTraceID, billType, userID)`。
- `RoundTraceID` 格式统一通过 `TraceIDGenerator` 生成：
  - `RT_<sessionID>_<roundNo>`
  - `PENALTY_DED_<roomID>_<sessionID>`
  - `PENALTY_DIST_<roomID>_<sessionID>`
  - `SESSION_CREDIT_<sessionID>_<userID>`
  - `GAME_SETTLE_<sessionID>`
- `ExceptionNo` 必须用确定性生成：`EXC_{billID}_{exceptionType}`，**禁止**时间戳 + 随机数。
- **禁止**业务代码内联 `fmt.Sprintf` 生成幂等键，必须通过 `TraceIDGenerator` 方法。

### 10.6 调度器规约

后台调度器统一遵循本节规约。`AsyncTaskRunner`（[§6.3](#63-asynctaskrunnermust)）用于应用层 fire-and-forget 异步任务，调度器用于周期性后台任务，二者分工不同但生命周期管理要求一致。

#### 10.6.1 SCH-1：统一 Scheduler 接口（MUST）

所有调度器 MUST 实现 `common/scheduler.Scheduler` 接口：

```go
type Scheduler interface {
    Name() string
    Start(ctx context.Context) error
    Stop()
}
```

- `Name()` 返回调度器名称，用于日志、metrics、健康检查。
- `Start(ctx)` 接收 appCtx，返回 error。
- `Stop()` 阻塞等待 goroutine 退出，带超时兜底。

#### 10.6.2 SCH-2：注册到 SchedulerRegistry（MUST）

调度器 MUST 注册到 `common/scheduler.Registry`，禁止散落在 `Container` 中的 ad-hoc 字段。

- `Registry.Register(s Scheduler)` 在构造时调用。
- `Registry.StartAll(appCtx)` 并行启动，`Registry.StopAll(timeout)` 并行停止。
- 仅对需要被其他服务注入的调度器保留单独字段。

#### 10.6.3 SCH-3：appCtx 作为父 context（MUST）

`Start(ctx)` MUST 接收 appCtx 作为父 ctx，内部 `context.WithCancel(ctx)` 派生调度器 ctx。**禁止** `context.Background()`。

- 调度器 ctx 随 appCtx 取消而取消，确保优雅关停。
- 构造函数不得接收 ctx 参数，ctx 只在 `Start` 时传入。

#### 10.6.4 SCH-4：InitialDelay 使用 select（MUST）

`InitialDelay` MUST 用 `select` 实现，**禁止** `time.Sleep`：

```go
select {
case <-s.ctx.Done():
    return
case <-time.After(s.config.InitialDelay):
}
```

- `time.Sleep` 阻塞期间无法响应 `Stop()`，导致关停延迟。

#### 10.6.5 SCH-5：检查 WithRedisLock 返回值（MUST）

`lock.WithRedisLock` 返回值 MUST 被检查并记录。锁获取失败与 task 错误均 MUST 记录到日志和 metrics。

- **禁止**丢弃返回值（`_ = lock.WithRedisLock(...)`）。
- 错误 MUST 记录 `name`、`lock_key`、`error` 字段。

#### 10.6.6 SCH-6：检查 service 返回值（MUST）

调度器 `execute` 中调用的 service 方法返回的 error MUST 被检查并记录；**禁止**丢弃返回值。

#### 10.6.7 SCH-7：配置外部化（MUST）

调度器的 `Interval`、`InitialDelay`、`LockTTL`、`CheckInterval` 等参数 MUST 通过配置文件设置，**禁止**硬编码。

- 新增调度器配置结构体定义在 `common/config/types.go`，服务特有配置放在各自 `config/` 包。
- 默认值在 `Set*Defaults` 函数中设置。
- YAML 配置段必须与配置结构体字段对应。

#### 10.6.8 SCH-8：并行 Stop 带全局预算（MUST）

`SchedulerRegistry.StopAll(timeout)` MUST 并行停止所有调度器，带全局预算（默认 30s）。

- 串行 Stop 在最坏情况下总耗时 = 所有调度器 Stop 超时之和（可达 80s+）。
- 并行 Stop 总耗时 = max(单调度器 Stop 超时, 全局预算)。

#### 10.6.9 SCH-9：Metrics 收集（SHOULD）

调度器 SHOULD 收集 metrics：执行次数、耗时、错误数、panic 数。

- `common/scheduler.Metrics` 提供 `RecordExecution`、`RecordError`、`RecordPanic`、`Snapshot` 方法。

#### 10.6.10 SCH-10：BaseScheduler 跨服务共享（MUST）

`BaseScheduler` 定义在 `common/scheduler/base.go`，跨服务共享。**禁止**在 `settlement/scheduler/` 或其他服务包内重复定义。

#### 10.6.11 SCH-11：TaskFunc 在构造函数中传入（MUST）

`TaskFunc` MUST 在 `NewBaseScheduler` 构造函数中传入，**禁止**在 `Start()` 中赋值（nil task 风险）。

#### 10.6.12 SCH-12：ZSET-based 调度器豁免与 handler ctx（MUST）

ZSET-based 调度器（如 `TimeoutScheduler`）依赖 `ZRem` 原子性去重，可豁免分布式锁要求。但 handler goroutine MUST 派生 per-handler ctx：

```go
handlerCtx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
defer cancel()
handler(handlerCtx, roomID, data)
```

- **禁止**直接使用调度器根 ctx（`s.ctx`）作为 handler ctx。

参考：[game/scheduler/timeout_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go)。

### 10.7 Lua 脚本规约

#### 10.7.1 L-1：脚本必须经 cRedis.NewScript 注册（MUST）

- 所有 Lua 脚本必须经 `cRedis.NewScript(name, src)` 注册，**禁止**内联 `redis.Eval`。
- 通过 EVALSHA 优化（首次 EVALSHA 失败回退 EVAL 并缓存 SHA）。
- 注册位置统一在 `scripts/registry.go`，脚本源码放在 `scripts/<name>.lua.go` 中作为 Go 字符串常量，命名 `lua<Action>`。

#### 10.7.2 L-2：脚本禁止字节级重复（MUST）

- 跨服务复用的脚本（如限流、token 锁释放）必须放在 `common/` 下。
- 业务专属脚本放在各服务 `scripts/` 下。

#### 10.7.3 L-3：Lua 内禁止拼接 key（MUST）

- 所有 key 必须由 Go 侧通过 KEYS 传入，**禁止**在 Lua 内 `KEYS[1] .. ":" .. ARGV[1]` 拼接。
- 循环内动态 packetID 场景（无法预先枚举 KEYS）保留 `keyPrefix` 传入方式，但必须在脚本头部注释标注 `keyPrefix` 与 `common/rediskeys` 常量的映射关系。

#### 10.7.4 L-4：禁止 Lua 孤儿 key（MUST）

- Lua 内所有 key（含 `keyPrefix`）必须在 [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) 有对应常量。
- 脚本头部 MUST 注释 KEYS 与 `common/rediskeys` 常量的映射关系。

#### 10.7.5 L-5：禁止硬编码 TTL（MUST）

- TTL 必须由 Go 侧从 `cfg.RedisTTL.XxxTTL` 读取后通过 `ARGV` 传入 Lua，**禁止**在 Lua 内 `redis.call('EXPIRE', key, 60)`。

#### 10.7.6 L-6：禁止 math.random / math.randomseed（MUST）

- 涉及随机数场景必须在 Go 侧用 `crypto/rand` 生成后通过 ARGV 传入 Lua。

#### 10.7.7 L-7：禁止 KEYS 命令（MUST）

- `redis.call('KEYS', pattern)` 会阻塞 Redis（O(N) 扫描全库），**禁止**使用。
- 需要枚举 key 时必须用 `SCAN`（Go 侧循环）或维护显式索引（ZSET / SET）。

#### 10.7.8 L-8：每个脚本必须有单元测试（MUST）

- 测试使用 [alicebob/miniredis](https://github.com/alicebob/miniredis) 在内存中模拟 Redis，**禁止**依赖外部 Redis 实例。
- 必须**至少**覆盖：成功路径 + 错误路径（key 不存在、状态不符、并发竞争）。

#### 10.7.9 L-B1：业务脚本返回值首项必须为整数 code（MUST）

业务脚本（仅适用 `game/.../scripts/`）返回值第一项必须是整数 code，引用 [game/domain/lua_codes.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/lua_codes.go) 常量名（注释标注）。

#### 10.7.10 L-B2：业务脚本返回值经 MapLuaError 映射（MUST）

- code=0 表示成功，返回 `nil`。
- 非 0 表示错误，`MapLuaError` 将 Lua code 映射为对应的 `*message.GameError`。

#### 10.7.11 L-G1：通用脚本返回值直接由调用方解析（MUST）

通用脚本（`common/`、`settlement/`、`gateway/`）返回值 0/1 直接由调用方 `.Int()` / `.Result()` 解析，**禁止**走 `MapLuaError`。

#### 10.7.12 L-G2：通用脚本头部必须注释返回值语义（MUST）

- 注释格式：`-- 返回值: 1=允许, 0=拒绝` 或 `-- 返回值: {0,'',''}=首次注册, {1,oldConnID,oldNodeID}=踢旧`。

参考：[common/lock/scripts/release_lock.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/scripts/release_lock.lua.go)。

### 10.8 雪花 ID 规约

#### 10.8.1 适用范围

雪花 ID 实现位于 [common/idgen/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/)，基于 [bwmarrin/snowflake](https://github.com/bwmarrin/snowflake) 封装，自定义纪元 `2026-01-01 00:00:00 UTC`（`1735689600000`），位分配 `timestamp(41) + nodeID(10) + sequence(12)`。

#### 10.8.2 通用规约（MUST）

| 编号 | 规约 |
|---|---|
| SID-1 | 所有业务实体唯一标识（用户 ID、房间 ID、回合 ID、sessionID）MUST 使用雪花 ID，禁止使用 UUID 或数据库自增 |
| SID-2 | 雪花 ID 生成 MUST 通过 `idgen.IDGenerator` 接口调用，禁止业务代码直接实例化 `SnowflakeGenerator` |
| SID-3 | `GenerateInt64()` / `GenerateString()` / `GenerateID()` 返回 `(T, error)`，调用方 MUST 检查 error |
| SID-4 | 时钟回拨时返回 `ErrClockMovedBackwards`（大幅回拨 >5ms）或等待追上（小幅回拨 ≤5ms），调用方遇 error MUST 记录 Warn 日志并 retry |
| SID-5 | nodeID MUST 从 yaml 配置读取（`IDGeneratorConfig.NodeID`） |
| SID-6 | nodeID MUST 在 [0, 1023] 范围内。多实例部署时每个实例 MUST 唯一 |
| SID-7 | `idgen.Init` MUST 在 `bootstrap` 层启动时显式调用，`GetGenerator()` 未初始化时返回 `ErrGeneratorNotInitialized` |
| SID-8 | 业务订单号（BizOrderNo、RefundOrderNo、ExceptionNo）MUST 确定性生成，禁止使用雪花 ID |
| SID-9 | 事件 TraceID 允许使用雪花 ID，但 MUST 与幂等键区分，禁止将雪花 ID 作为幂等键。日志追踪 TraceID MUST 通过 `common/trace.Generate()` 生成（格式 `tr_<snowflake>`） |
| SID-10 | `TraceIDGenerator` MUST 依赖 `IDGenerator` 接口，禁止依赖 `*SnowflakeGenerator` 具体类型 |
| SID-11 | 雪花 ID 生成器 MUST 有单元测试，覆盖并发唯一性、时钟回拨、序列号溢出、nodeID 边界值 |
| SID-12 | 需要随机数的场景（如 `GenerateReconcileNo`）MUST 使用 `crypto/rand`，禁止用雪花 ID 取模 |
| SID-13 | 雪花 ID 框架 MUST 使用 `bwmarrin/snowflake`，禁止自研或替换 |
| SID-14 | 自定义纪元 MUST 设置为 `1735689600000`（2026-01-01 00:00:00 UTC） |

#### 10.8.3 调用方规约（MUST）

| 编号 | 规约 |
|---|---|
| SID-C1 | 业务服务 SHOULD 在构造函数注入 `IDGenerator` 接口，禁止在方法内部调用 `idgen.GetGenerator()` |
| SID-C2 | 禁止在 `game/application/`、`settlement/service/`、`gateway/` 业务代码中调用 `idgen.GetGenerator()` |
| SID-C3 | `scripts/` 下的脚本工具可直接调用 `idgen.Init()` + `idgen.GetGenerator()`，但 MUST 检查 error |

#### 10.8.4 nodeID 自动分配规约（MUST）

当 `node_id: 0` 时通过 [common/idgen/node_allocator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/node_allocator.go) 自动分配：

| 编号 | 规约 |
|---|---|
| SID-NA1 | 自动分配 MUST 使用 Redis `INCR` 获取递增序列 + `SET NX` 抢占 |
| SID-NA2 | 抢占记录 MUST 带 TTL（默认 3600 秒） |
| SID-NA3 | 心跳续约 MUST 每 5 分钟 `EXPIRE`，禁止使用 `SET` 覆盖 |
| SID-NA4 | 释放 nodeID MUST 通过 Lua 脚本校验 `value == instanceID` 后 `DEL` |
| SID-NA5 | 释放脚本 MUST 通过 `cRedis.NewScript` 注册 |
| SID-NA6 | instanceID MUST 使用 `google/uuid` 生成 |
| SID-NA7 | 心跳续约 goroutine MUST 使用 appCtx 派生的 context |

### 10.9 Redis Pub/Sub 规约（MUST）

- Redis Pub/Sub 实现必须包含指数退避重连（1s→30s 上限）。
- 重连后必须做状态同步（重新订阅、恢复状态）。
- 消息必须使用统一 envelope（含 `event_id`、`trace_id`、`timestamp`、`version`）。
- 必须有 panic 恢复 + 消息缓存上限（防止内存溢出）。

### 10.10 TraceID 传播规约

TraceID 是一次业务请求/操作的端到端追踪标识，MUST 满足：入口生成、Context 传播、跨边界注入/恢复、与幂等键分离。

#### 10.10.1 TP-1：TraceID Context 传播工具（MUST）

系统 MUST 通过 [common/trace](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/trace/trace.go) 包提供 TraceID 的 context 传播能力：

- `WithTraceID(ctx, traceID) context.Context`：注入 TraceID，空值时自动生成。
- `FromContext(ctx) string`：提取 TraceID，缺失返回空字符串，不 panic。
- `Generate() string`：生成新 TraceID，格式 `tr_<snowflake>`，idgen 不可用时降级为 `tr_fallback_<uuid>`。

**禁止**业务代码自行生成 TraceID 或用 `fmt.Sprintf` 拼接。

#### 10.10.2 TP-2：请求入口生成 TraceID（MUST）

请求最外层入口 MUST 生成 TraceID 注入 context：

- **Gateway WS 入口**：`MessageRouter.Route` MUST 调用 `trace.WithTraceID(ctx, trace.Generate())`。
- **gRPC 入口**：`GenericServiceServer.Forward` MUST 从 metadata 提取 `x-trace-id`，无则生成。
- **禁止**在 application service 或 infrastructure 层生成 TraceID。

#### 10.10.3 TP-3：Context 透传（MUST）

TraceID MUST 通过 `context.Context` 在同步调用链中透传，**禁止**通过方法参数传递 TraceID（污染签名）。

- **禁止**在方法签名中增加 `traceID string` 参数。

#### 10.10.4 TP-4：跨服务透传 via gRPC metadata（MUST）

- 调用方：`metadata.AppendToOutgoingContext(ctx, "x-trace-id", trace.FromContext(ctx))`。
- 被调用方：`metadata.FromIncomingContext(ctx)` 提取 `x-trace-id`。
- header 名称统一 `x-trace-id`。

#### 10.10.5 TP-5：Publisher 从 Context 注入 TraceID（MUST）

消息发布者 MUST 从 context 提取 TraceID 注入消息 envelope：

1. 优先使用 event 已有的 TraceID（向后兼容）；
2. 其次从 context 提取（`trace.FromContext(ctx)`）；
3. 两者都为空时自动生成（`trace.Generate()`），并打 Warn 日志。

**禁止**因空 TraceID 返回 error 阻塞消息发送（fail-open）。

#### 10.10.6 TP-6：Consumer 从消息恢复 TraceID（MUST）

```go
if event.TraceID != "" {
    ctx = trace.WithTraceID(ctx, event.TraceID)
}
```

#### 10.10.7 TP-7：同请求多事件共享 TraceID（MUST）

一次业务操作触发的多个事件 MUST 共享同一 TraceID。

#### 10.10.8 TP-8：TraceID 与幂等键分离（MUST）

| 消息类型 | TraceID 用途 | 幂等键 | 幂等键 Redis Key |
|---|---|---|---|
| RoomEvent | 日志追踪（非确定性） | `EventID`（UUID） | `cashparty:room:event:processed:<eventID>` |
| GameEvent | 日志追踪 + 幂等键（确定性） | `TraceID`（业务语义拼接） | `cashparty:game:event:processed:<traceID>` |

- RoomEvent 的 TraceID **禁止**作为幂等键。
- GameEvent 的 TraceID 是确定性的，同时承担日志追踪和幂等键两个职责。
- Publisher 的 context 兜底逻辑 **不得覆盖** GameEvent 已有的确定性 TraceID。

#### 10.10.9 TP-9：事件发布失败 fail-open（MUST）

`PublishRoomEvent` / `PublishGameEvent` 调用失败时 MUST 采用 fail-open：

- 检查 error 并记录 Warn 日志（含 `room_id`、`user_id`、`trace_id`、`error`）。
- **禁止**阻塞主流程。
- **禁止**吞掉 error。

#### 10.10.10 TP-10：日志必须携带 trace_id（MUST）

所有跨越进程/服务边界的请求和异步消息处理的日志 MUST 携带 `trace_id` 字段。

#### 10.10.11 TP-11：Consumer 事件分发必须覆盖所有事件类型（MUST）

- Consumer 的 switch 语句 MUST 覆盖所有已定义的事件类型。
- 新增事件类型时 MUST 同步在 Consumer 增加对应 handler。
- unknown 事件 MUST 记录 Warn 日志并返回 error（触发重试 + DLQ）。

#### 10.10.12 TP-12：TraceID 降级策略（MUST）

`trace.Generate()` 在 idgen 不可用时 MUST 降级到 UUID：

- 降级格式：`tr_fallback_<uuid>`。
- 降级时 MUST 记录 Warn 日志。
- **禁止**因 TraceID 生成失败而 panic 或返回 error。

---

## 11. 配置管理

参考 [Twelve-Factor App - Config](https://12factor.net/config)。

### 11.1 配置外置原则（MUST）

- 配置必须外置（YAML 文件 + nacos 远程配置），**禁止**硬编码在代码中。
- 不同环境（dev/staging/prod）的配置必须不同，通过不同 YAML 文件 + nacos namespace 区分。
- 敏感信息（密码、token、密钥）不得出现在代码或日志中，通过环境变量或密钥管理服务注入。

### 11.2 Config 结构（MUST）

- 每个服务模块的 `config/` 包定义自己的 `Config` struct，**聚合** `common/config.*Config` 子结构，不得重新声明同名字段。
- `RedisConfig`、`MySQLConfig`、`LogConfig` 等通用配置必须复用 `common/config` 的定义。
- 配置 struct 必须有 `mapstructure` tag，便于 viper 反序列化。

### 11.3 默认值（MUST）

- 默认值统一放在 `<module>/config/defaults.go` 的 `setDefaults(cfg *Config)` 函数。
- **禁止**在多个地方定义同一类默认值。
- 默认值必须合理（如连接池大小、超时时间），不得为 0 或空。

### 11.4 加载入口（MUST）

- 配置加载统一函数名 `Load(path string) (*Config, error)` 和 `LoadFromContent(content string) (*Config, error)`。
- 两个入口必须对称：`Load` 能做的事 `LoadFromContent` 也要能做。
- 加载失败必须 `logger.Fatal`，不得继续启动。

### 11.5 热更新（MUST）

- 通过 nacos 热更新的配置字段必须用 `atomic.Pointer[T]` 持有（见 [§6.6](#66-热配置并发must)）。
- 热更新回调函数必须 `logger.Info` 记录变更前后的关键值，不得静默生效。
- 热更新不得影响进行中的请求（snapshot 模式）。

### 11.6 配置校验（MUST）

- 配置加载后必须校验：端口范围、超时合理、连接池大小、必填项。
- 校验失败必须 `logger.Fatal`，不得用默认值继续。
- 端口冲突检测：不同服务默认端口必须不同。

---

## 12. 依赖注入与生命周期

参考 [Google Wire - Dependency Injection](https://github.com/google/wire)、[Clean Architecture - Dependency Rule](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)。

### 12.1 组合根（MUST）

- `bootstrap/container.go` 是唯一允许"构造具体实现并注入到接口"的位置。
- `Container` struct 字段一致性：要么全公开（供 `app.go` 访问），要么全私有（通过 getter）。统一采用"全公开字段 + `Stop()` 方法"风格。
- 依赖图必须无环；循环依赖通过 setter 注入打破（仅在 `Container.InitServices` 中、启动服务前调用 setter）。

### 12.2 构造函数参数（SHOULD）

- 当 `NewXxx` 参数超过 5 个时，**应当**使用 functional options 模式或配置 struct：

```go
type GameAppServiceOptions struct {
    RoomRepo domain.RoomRepository
    DBRepo   domain.DBRepository
    // ...
}

func NewGameAppService(opts GameAppServiceOptions) *GameAppService
```

### 12.3 Application 生命周期（MUST）

每个服务的 `bootstrap.Application` 必须实现：

- `NewApplicationWithConfig(cfg) (*Application, error)`
- `Start(ctx context.Context) error`
- `Wait() <-chan error`（等待致命错误）
- `Stop() error`

### 12.4 致命错误处理（MUST）

- 启动阶段致命错误统一用 `logger.Fatal`，**禁止** `panic(err)` 或 `fmt.Printf + os.Exit(1)`。
- 运行阶段致命错误通过 `Wait()` 返回，由上层决定是否重启。

### 12.5 优雅关闭（MUST）

关闭顺序必须遵循依赖关系：

1. 停止接收新请求（HTTP/gRPC GracefulStop）。
2. 等待进行中的请求完成（带超时）。
3. 停止调度器（`SchedulerRegistry.StopAll`，并行 + 全局预算）。
4. 停止 AsyncTaskRunner（`Wait`，带超时）。
5. 关闭 Kafka consumer（在 taskRunner.Wait 后、Producer.Close 前）。
6. 关闭 Kafka producer。
7. 关闭数据库连接。
8. 关闭 Redis 连接。
9. flush 日志。

- 每一步必须有超时兜底，不得永久阻塞。
- 收到 SIGTERM/SIGINT 必须触发优雅关闭。

---

## 13. 测试规范

参考 [Go Blog - Testing](https://go.dev/blog/testing)、[Go Advanced Testing](https://go.dev/doc/articles/wiki/)、[TableDrivenTests](https://github.com/golang/go/wiki/TableDrivenTests)。

### 13.1 测试分层（MUST）

| 层级 | 范围 | 工具 | 覆盖率要求 |
|---|---|---|---|
| **单元测试** | 单个函数/方法，依赖全部 mock | 标准 `testing` + `testify` | ≥ 80% |
| **集成测试** | 多个组件协作，依赖真实或嵌入式中间件 | `testing` + `miniredis` + `testcontainers` | ≥ 60% |
| **端到端测试** | 完整业务流程 | `testing` + 真实中间件 | 关键路径覆盖 |

- 单元测试必须**快速**（单测总时长 < 30s）。
- 单元测试必须**隔离**，不得依赖网络、数据库、Redis 等外部服务。
- 集成测试可以使用嵌入式中间件（miniredis、sqlite）。

### 13.2 表驱动测试（MUST）

测试用例必须使用表驱动风格，便于扩展与维护：

```go
func TestSettleGame(t *testing.T) {
    tests := []struct {
        name    string
        input   SettleInput
        want    SettleResult
        wantErr error
    }{
        {
            name:    "normal settle",
            input:   SettleInput{RoundID: 1, UserID: 100},
            want:    SettleResult{Status: "success"},
            wantErr: nil,
        },
        {
            name:    "already settled",
            input:   SettleInput{RoundID: 1, UserID: 100},
            want:    SettleResult{},
            wantErr: ErrAlreadySettled,
        },
        // ... more cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := SettleGame(tt.input)
            if !errors.Is(err, tt.wantErr) {
                t.Errorf("SettleGame() error = %v, want %v", err, tt.wantErr)
            }
            if !reflect.DeepEqual(got, tt.want) {
                t.Errorf("SettleGame() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

### 13.3 测试命名（MUST）

- 测试函数：`Test<Type>_<Method>_<Scenario>`，如 `TestBillManager_CreateBill_AlreadyExists`。
- 子测试：`t.Run("scenario description", ...)`，使用人类可读的场景描述。
- **禁止** `Test1`、`Test2` 这种无意义命名。

### 13.4 测试组织（MUST）

- 测试文件与被测文件同包，命名 `<file>_test.go`。
- 测试数据（fixture）放在 `testdata/` 目录。
- mock 文件命名 `mock_*.go` 或 `*_mock.go`，放在 `mocks/` 子包。
- 集成测试使用 build tag `//go:build integration`，默认不运行。

### 13.5 断言库（SHOULD）

- 推荐使用 `github.com/stretchr/testify/assert` 与 `require`。
- `assert` 用于非关键断言（失败后继续执行），`require` 用于关键断言（失败后停止）。
- **禁止**用 `if got != want { t.Error(...) }` 手写断言（可用，但推荐 testify）。

### 13.6 Mock 与 Stub（MUST）

- 接口的 mock 必须通过代码生成（如 `mockgen`），**禁止**手写 mock。
- mock 必须实现完整接口，不得遗漏方法。
- 测试中不得调用真实的外部服务（数据库、Redis、Kafka、HTTP）。
- 嵌入式中间件推荐：
  - Redis：[alicebob/miniredis](https://github.com/alicebob/miniredis)
  - MySQL：sqlite（仅简单场景）或 testcontainers/mysql
  - Kafka：[segmentio/kafka-go](https://github.com/segmentio/kafka-go) mock 或 testcontainers

### 13.7 测试覆盖率（MUST）

- 单元测试覆盖率 ≥ 80%（核心业务逻辑）。
- 关键路径（资金、状态机、幂等）必须 100% 覆盖。
- 覆盖率检查通过 `go test -cover -coverprofile=coverage.out` 生成。
- 覆盖率不得下降（CI 中检查 delta）。

### 13.8 测试内容要求（MUST）

每个测试必须覆盖：

1. **正常路径（happy path）**：输入合法，输出符合预期。
2. **边界条件**：空输入、最大值、最小值、零值。
3. **错误路径**：输入非法、依赖失败、并发竞争。
4. **幂等性**：重复调用不产生副作用。
5. **并发安全**：多 goroutine 并发调用（用 `sync.WaitGroup` + `-race` 检测）。

### 13.9 Lua 脚本测试（MUST）

- 每个 Lua 脚本必须有单元测试（miniredis）。
- 覆盖成功路径 + 错误路径（key 不存在、状态不符、并发竞争）。
- 测试文件命名 `<name>_test.go`，与脚本同包。

参考：[game/infrastructure/persistence/redis/scripts/packet_test.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/packet_test.go)。

### 13.10 并发测试（SHOULD）

- 涉及共享状态的代码必须有并发测试。
- 使用 `go test -race` 检测数据竞争。
- 并发测试用例：≥100 goroutine × ≥1000 操作，验证无数据竞争。

### 13.11 基准测试（SHOULD）

- 性能关键路径必须有基准测试（`Benchmark*`）。
- 基准测试必须使用 `b.ResetTimer()` 排除初始化时间。
- 基准测试结果应记录在 PR 描述中，便于性能回归检测。

---

## 14. 安全规范

参考 [OWASP Top 10](https://owasp.org/Top10/)、[Go Security](https://go.dev/doc/security)。

### 14.1 输入验证（MUST）

- 所有外部输入（HTTP 请求、gRPC 请求、消息）必须验证。
- 使用 struct tag（`binding:"required,min=1,max=100"`）+ 显式校验。
- **禁止**信任客户端输入，所有输入都是不可信的。
- 字符串输入必须校验长度、字符集、格式。
- 数值输入必须校验范围。

### 14.2 SQL 注入防护（MUST）

- 必须使用 GORM 占位符 `?` 传递值，**禁止**字符串拼接 SQL。
- 动态 WHERE 子句仅允许拼接字面量结构（如 `AND col = ?`），值通过 args 传递。
- 无法参数化的动态结构（如 CASE WHEN 分支标签）MUST 在配置加载时做白名单字符校验。
- 用户输入不得直接进入 `db.Raw`、`db.Exec` 的 SQL 字符串。

### 14.3 敏感数据处理（MUST）

- 密码必须用 `bcrypt` 或 `argon2` 哈希存储，**禁止**明文或 MD5/SHA1。
- 密码、token、密钥不得出现在日志中，如需记录必须脱敏（如 `***1234`）。
- 配置中的敏感信息通过环境变量或密钥管理服务注入，**禁止**硬编码在 YAML。
- API 响应不得泄露内部错误细节（如 stack trace、SQL 语句），仅返回用户可读消息。

### 14.4 加密随机数（MUST）

- 涉及安全性的随机数必须使用 `crypto/rand`：
  - token 生成
  - 金额、红包拆分、奖励生成
  - session ID、nonce
  - UUID 生成（使用 `google/uuid`，内部用 `crypto/rand`）
- `math/rand` 仅用于不涉及安全与资金的场景（如 robot AI 行为）。
- **禁止**用时间戳作为随机数种子（可预测）。

### 14.5 凭证管理（MUST）

- API key、数据库密码、JWT secret 等凭证必须通过环境变量或密钥管理服务注入。
- 凭证不得出现在代码、配置文件、日志、错误消息中。
- 凭证必须有轮换机制，定期更新。
- 凭证不得在版本控制中提交（`.gitignore` 必须覆盖）。

### 14.6 认证与授权（MUST）

- 所有对外 API 必须经过认证（JWT、session）。
- 认证失败返回 `401 Unauthorized`。
- 权限检查必须在 handler 或中间件层完成，不得依赖前端。
- 权限不足返回 `403 Forbidden`（区分"未登录"与"无权限"）。
- 内部服务调用必须通过 mTLS 或 service token 认证。

### 14.7 限流与防护（MUST）

- 对外 API 必须有限流（rate limiter），防止 DDoS 与暴力破解。
- 限流维度：IP、用户、接口。
- 限流算法：滑动窗口（Redis Lua 实现）。
- 限流触发后返回 `429 Too Many Requests` + `Retry-After` header。

参考：[gateway/middleware/ratelimit.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/ratelimit.go)。

### 14.8 CORS 配置（MUST）

- 生产环境 CORS 必须限制 `Access-Control-Allow-Origin` 为白名单域名。
- **禁止** `Access-Control-Allow-Origin: *` 在生产环境（带凭证时尤其危险）。
- `Access-Control-Allow-Credentials: true` 时必须配合白名单 origin。

### 14.9 签名校验（MUST）

- 对外 API 必须有请求签名校验（防篡改、防重放）。
- 签名算法：HMAC-SHA256。
- 签名内容：method + path + timestamp + nonce + body hash。
- timestamp 必须校验时间窗口（±5 分钟），防重放。
- nonce 必须校验唯一性（Redis 缓存 5 分钟）。

参考：[common/signature/signer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/signature/signer.go)、[gateway/middleware/signature.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/signature.go)。

---

## 15. 性能与资源管理

参考 [Go Performance](https://go.dev/doc/performance)、[High Performance Go Workshop](https://dave.cheney.net/high-performance-go-workshop-dotparis-2017.html)。

### 15.1 内存管理（SHOULD）

- 避免不必要的内存分配：复用 buffer、使用 `sync.Pool`。
- 大 slice 预分配容量：`make([]T, 0, n)`。
- 避免在热路径中分配：减少闭包捕获、避免 `fmt.Sprintf` 在循环中。
- 字符串拼接用 `strings.Builder`。
- 结构体避免过大（> 64 字节考虑指针传递）。

### 15.2 连接池（MUST）

- MySQL、Redis、Kafka、HTTP client 必须使用连接池。
- 连接池参数必须从配置读取（见 [§8.7](#87-连接池must)）。
- 连接池必须有合理上限，防止资源耗尽。
- 长连接必须有 keepalive 与心跳机制。

### 15.3 超时控制（MUST）

- 所有外部调用必须有超时（HTTP client、gRPC、DB query、Redis）。
- 超时必须通过 `context.WithTimeout` 派生 ctx。
- 超时值必须从配置读取，**禁止**硬编码。
- 默认超时：
  - HTTP client：30s
  - gRPC：10s
  - DB query：5s
  - Redis：1s
  - 外部平台调用：10s

### 15.4 资源释放（MUST）

- 所有 `Close()`、`Release()`、`cancel()` 必须用 `defer` 调用。
- 文件、连接、锁、timer 必须显式释放。
- goroutine 必须有退出机制（见 [§6.1](#61-goroutine-生命周期must)）。
- channel 必须由发送方关闭，不得由接收方关闭。

### 15.5 批量操作（SHOULD）

- 数据库批量插入：`db.CreateInBatches(records, batchSize)`。
- Redis 批量操作：`MGet`、`Pipeline`。
- HTTP 批量接口：减少往返次数。
- 批量大小必须合理（默认 100-1000），过大导致内存压力。

### 15.6 缓存策略（SHOULD）

- 热点数据必须缓存（Redis）。
- 缓存必须有 TTL，**禁止**永久缓存（数据不一致风险）。
- 缓存必须有降级策略（缓存挂了不阻塞业务，回源 DB）。
- 缓存击穿防护：singleflight 或分布式锁。
- 缓存雪崩防护：TTL 加随机抖动（±10%）。

### 15.7 N+1 查询（MUST NOT）

- **禁止** N+1 查询：循环内查数据库。
- 关联数据必须用 `Preload`、`Joins` 或批量查询后内存拼接。
- 列表接口必须分页，**禁止**一次返回全量数据。

### 15.8 异步化（SHOULD）

- 非关键路径操作异步化（通过 AsyncTaskRunner）。
- 耗时操作（如通知、日志写入、统计更新）异步执行。
- 异步操作必须保证最终一致性（幂等 + 重试）。

---

## 16. 字符串拼接

字符串拼接是后端代码中最易引入安全漏洞（JSON 注入、URL 双斜杠、签名绕过）与正确性 Bug（Redis key 命名、错误包装丢失解包）的领域。本节规约覆盖 JSON、URL、host:port、文件路径、Redis key、错误包装、SQL、TraceID、UUID 七大场景。

### 16.1 SC-1：JSON 构建（MUST）

- 构建 JSON 字符串（用于协议、签名、存储）MUST 使用 `json.Marshal` 或 `json.NewEncoder`。
- **禁止** `fmt.Sprintf` 反引号模板拼接含用户输入的 JSON（token/roomID/userID 含 `"` 或 `\` 会破坏协议）。
- 签名场景如需控制 HTML 转义，使用 `json.NewEncoder` + `SetEscapeHTML(false)`。

参考：[gateway/server/server.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/server/server.go)、[common/signature/signer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/signature/signer.go)。

### 16.2 SC-2：URL 拼接（MUST）

- URL 路径拼接 MUST 使用 `url.JoinPath`（Go 1.19+）或 [strutil.JoinURLPath](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/strutil/url.go)，自动处理末尾斜杠避免双斜杠。
- query 参数 MUST 使用 `url.Values.Encode()`，**禁止**手动 `fmt.Sprintf("%s?%s", ...)`。
- 完整 URL + query 拼接使用 `strutil.BuildURLWithQuery`。

参考：[api/platform/gamingpanda_client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/api/platform/gamingpanda_client.go)、[gateway/service/game.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/game.go)。

### 16.3 SC-3：host:port 构建（MUST）

- 构建 `host:port` 地址（gRPC、HTTP 监听、服务发现）MUST 使用 `net.JoinHostPort(host, strconv.Itoa(port))` 或 [strutil.JoinHostPort](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/strutil/hostport.go)。
- **禁止** `fmt.Sprintf("%s:%d", ip, port)`（IPv6 地址不含方括号会导致解析错误）。

参考：[common/discovery/discovery.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/discovery/discovery.go)、[common/nacos/client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)。

### 16.4 SC-4：文件路径构建（MUST）

- 跨平台文件路径 MUST 使用 `filepath.Join`，**禁止**硬编码 `/` 分隔符。
- 临时目录 MUST 使用 `os.TempDir()` 而非硬编码 `/tmp`。

### 16.5 SC-5：Redis key 构建（MUST）

- Redis key MUST 使用 [common/rediskeys](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) 包的常量或工厂函数，**禁止**散落在 service 里的裸字符串拼接。
- 详见 [§8.8](#88-redis-key-命名must)。

### 16.6 SC-6：错误包装（MUST）

- 包装底层 error MUST 使用 `fmt.Errorf("...: %w", err)`，**禁止** `%v` 包装 `error` 类型。
- Go 1.20+ 支持多个 `%w`（如 `fmt.Errorf("%w: %w", err1, err2)`）。
- `%v` 仅用于包装非 error 类型（如 `recover()` 返回的 `interface{}`）。
- 详见 [§4.3](#43-错误包装must)。

### 16.7 SC-7：SQL 查询构建（MUST）

- 动态 SQL MUST 使用 GORM 占位符 `?` 传递值，**禁止**字符串拼接值。
- 动态 WHERE 子句仅允许拼接字面量结构（如 `AND col = ?`），值通过 args 传递。
- 无法参数化的动态结构（如 CASE WHEN 分支标签）MUST 在配置加载时做白名单字符校验。

参考：[stats/repository/stats_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/repository/stats_repository.go) 的 `validateAmountRanges` 白名单校验。

### 16.8 SC-8：TraceID / BizOrderNo 生成（MUST）

TraceID 按职责分两类，生成方式不同：

| 类型 | 用途 | 生成方式 | 确定性 | 示例 |
|---|---|---|---|---|
| **业务幂等 TraceID** | GameEvent 幂等键、BizOrderNo、RoundTraceID | `TraceIDGenerator` 方法 | 确定性（相同输入相同输出） | `GAME_SETTLE_<sessionID>` |
| **日志追踪 TraceID** | 全链路日志关联、RoomEvent.TraceID | `common/trace.Generate()` 或 context 透传 | 非确定性（雪花 ID） | `tr_<snowflake>` |

- **业务幂等 TraceID** MUST 通过 [TraceIDGenerator](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) 方法生成，**禁止**业务代码内联 `fmt.Sprintf`。
- **日志追踪 TraceID** MUST 通过 [common/trace](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/trace/trace.go) 包生成或从 context 提取，**禁止**业务代码内联生成。
- 两者 MUST 严格区分，**禁止**将日志追踪 TraceID 用作幂等键。

### 16.9 SC-9：UUID 使用（MUST）

- 生成唯一 ID MUST 使用 `github.com/google/uuid`（`uuid.New().String()`），**禁止**手写 UUID。
- 截断 UUID 不得少于 12 字符（`uuid.New().String()[:12]`），**禁止** `[:8]` 增加碰撞概率。

### 16.10 SC-10：Redis key 分隔符与 Prefix 约定（MUST）

- Redis key 多参数分隔符 MUST 使用 `:`，**禁止**下划线 `_`。
- 所有 `*Prefix` 常量 MUST 带尾随冒号（如 `KeyRoomHashPrefix = "cashparty:room:hash:"`），调用方一律 `+ "*"` 而非 `+ ":*"`。
- 详见 [§8.8](#88-redis-key-命名must)。

---

## 17. 注释与文档

参考 [Google Go Style Guide - Comments](https://google.github.io/styleguide/go/decisions#comments)。

### 17.1 语言（MUST）

- 代码注释统一用**英文**。
- 用户可见字符串（错误消息、日志消息）可以保留中文，但同一类消息全项目语言一致。
- **禁止**中英文混排注释（同一文件内）。

### 17.2 godoc 规范（MUST）

- 导出类型、导出函数、接口方法必须有 godoc 注释，以类型/函数名开头。

```go
// BillManager manages bill records, including creation, status update, and query.
type BillManager struct {
    // ...
}

// CreateBill creates a new bill record with idempotency check.
// Returns ErrBillAlreadyExists if the bill with same BizOrderNo exists.
func (m *BillManager) CreateBill(ctx context.Context, bill *Bill) error {
    // ...
}
```

- 包注释：每个包必须有 `doc.go` 或在其中一个文件头部有包注释，说明包用途。
- 注释为完整句子，以句号结尾。

### 17.3 注释内容（SHOULD）

- 复杂业务逻辑（状态机推进、乐观锁、幂等检查、Lua 脚本）必须注释说明**为什么这么做**，不要描述**做了什么**（代码本身已说明）。
- magic number 必须注释来源。
- workaround 必须注释原因 + TODO + issue 编号。
- **禁止**无意义的 `// TODO` 而不附 issue 编号或具体描述。
- **禁止**注释掉的代码（git 已记录历史）。

### 17.4 文件头注释（MUST NOT）

- 不得在文件头添加作者、日期、版权等元信息（git 已记录）。

---

## 18. 格式化与工具链

参考 [Google Go Style Guide - Formatting](https://google.github.io/styleguide/go/decisions#formatting)。

### 18.1 gofmt（MUST）

- 全部使用 **tab** 缩进。
- 所有代码必须通过 `gofmt -l`（无输出表示已格式化）。
- 提交前必须执行 `gofmt -w .`。

### 18.2 goimports（MUST）

- 使用 `goimports` 替代 `gofmt`，自动管理 import。
- import 分组**三组**：标准库 → 第三方 → 项目内部，组间空行。

```go
import (
    "context"
    "fmt"

    "github.com/redis/go-redis/v9"
    "go.uber.org/zap"

    "github.com/cashparty/backend/common/logger"
    "github.com/cashparty/backend/game/domain"
)
```

- 项目内部统一使用全路径 `github.com/cashparty/backend/...`。

### 18.3 go vet（MUST）

- 所有代码必须通过 `go vet ./...`。
- `go vet` 报告的问题必须修复，**禁止**忽略。

### 18.4 golangci-lint（SHOULD）

推荐使用 `golangci-lint` 进行更全面的静态检查，启用以下 linter：

- `errcheck`：检查 error 是否被处理。
- `gofmt`：格式化检查。
- `goimports`：import 顺序检查。
- `govet`：go vet 检查。
- `staticcheck`：静态分析。
- `ineffassign`：无效赋值检查。
- `unused`：未使用代码检查。
- `misspell`：拼写检查。
- `revive`：替代 golint，更灵活。
- `gocyclo`：圈复杂度检查（阈值 < 15）。
- `gosec`：安全检查。

配置文件 `.golangci.yml` 放在项目根目录。

### 18.5 pre-commit hooks（SHOULD）

推荐配置 pre-commit hooks，在提交前自动检查：

- `gofmt -l`
- `go vet ./...`
- `golangci-lint run`
- `go test -short ./...`

### 18.6 结构体字段对齐（MUST）

- struct 字段必须由 `gofmt` 自动对齐，**禁止**手工空格对齐到不一致状态。

### 18.7 行长（SHOULD）

- 单行不超过 120 字符；超出时按参数或运算符换行。
- 换行时运算符放在行首（除 `.` 方法调用）。

### 18.8 依赖管理（MUST）

- 新增依赖必须经过评审，确认：
  - 必要性（无法用标准库或现有依赖实现）。
  - 维护活跃度（最近一年有更新）。
  - 许可证兼容（MIT、Apache 2.0、BSD）。
  - 无已知安全漏洞（`govulncheck`）。
- **禁止**引入 `go.mod` 未记录的依赖。
- 依赖升级必须通过 PR，不得直接修改 `go.sum`。

### 18.9 死代码（MUST）

- 未使用的代码必须删除（不保留"以后可能用到"）。
- 未使用的 import 必须删除。
- 未使用的变量必须删除（或改为 `_`）。
- 注释掉的代码必须删除（git 已记录）。

---

## 19. 反模式（Anti-Patterns）

以下写法在新代码中**绝对禁止**，历史代码在重构时收敛。

### 19.1 命名反模式

- `UserId`、`RoomId`（应 `UserID`、`RoomID`）
- `IUserService` 接口前缀 I
- 文件名 `<pkg>_xxx.go` 冗余前缀（如 `stats_handler.go`）
- 同一模块内 `gormRoomRepository` 与 `RoomRepository` 与 `GormUserRepository` 三种命名并存
- Receiver 名不一致（同一类型方法里 `s` 和 `svc` 混用）
- `this`、`self` 作为 receiver 名
- 包名含大写、下划线、连字符

### 19.2 错误处理反模式

- `if _, err := ...; err != nil { /* ignore */ }`（吞掉 error）
- `if exists, _ := ...`（丢弃 error）
- `return err` 不包装（除哨兵与 GameError 例外）
- 业务状态用 `fmt.Errorf("status is not pending")` 字符串错误（应哨兵）
- `strings.Contains(err.Error(), "...")` 字符串匹配错误（应 `errors.Is`）
- `panic(err)` 在启动阶段（应 `logger.Fatal`）
- `panic` 作为正常错误处理流程

### 19.3 并发反模式

- `go func() { ... }()` 不经 `AsyncTaskRunner`（应用层）
- `context.Background()` 在应用层异步任务里
- `math/rand` 用于金额拆分
- `sync.Mutex` + nil 检查做单例（应 `sync.Once`）
- `sync.RWMutex` 保护热配置字段（应 `atomic.Pointer`）
- goroutine 无退出机制、无超时、无 panic 恢复
- 通过共享变量通信而不加锁
- 复制 mutex/atomic 值

### 19.4 数据库反模式

- service 直接持有 `*gorm.DB` 开事务（应通过 `Transaction.Execute`）
- 状态机 UPDATE 不带 `WHERE status = ?`（必须带乐观锁）
- `RowsAffected == 0` 当 error 返回（应幂等成功返回 nil）
- 字符串拼接 SQL
- N+1 查询（循环内查数据库）
- 一次返回全量数据（不分页）
- 通过 GORM AutoMigrate 在生产环境修改 schema
- 金额字段用 `float64`

### 19.5 Redis / 锁反模式

- 内联 Lua 字符串 + `redis.Eval`（应经 `redis.Script`）
- 释放锁裸 `DEL`（必须 token + Lua）
- 锁值存 roomID（必须存 UUID token）
- `Exists` + `Set` 两步式抢占（必须 `SetNX`）
- 裸字符串拼 key 散落在 service 里
- `redis.call('KEYS', pattern)` 在 Lua 内
- Lua 内 `math.random`
- Lua 内硬编码 TTL
- Lua 内拼接 key

### 19.6 HTTP 反模式

- 多种响应信封并存（必须 `{"code","msg","data"}`）
- 裸数字 HTTP 状态码（必须 `http.Status*`）
- `c.JSON` + `c.Abort()` 散落在多个中间件（必须用 `respondError` 助手）
- `strconv.Atoi(c.Query(...))` 手动解析（必须 `ShouldBindQuery`）
- 不校验输入直接使用
- 错误响应泄露内部细节（stack trace、SQL）

### 19.7 配置反模式

- 重复声明 `RedisConfig` / `MySQLConfig`（必须复用 `common/config`）
- 默认值在多处定义
- 服务默认端口冲突
- 硬编码连接池参数
- 硬编码超时时间
- 敏感信息硬编码在配置文件

### 19.8 重复代码反模式

- 字节级重复方法（必须删除其一）
- 语义级重复 helper（必须抽取共享方法）
- 重复常量（必须复用）
- 重复 Lua 脚本（必须抽取到 `common/`）

### 19.9 注释反模式

- 西语注释
- 同一文件中英西混排
- 注释描述"做了什么"而不是"为什么"
- 无意义的 `// TODO` 不附 issue 编号
- 注释掉的代码

### 19.10 序列化反模式

- 同一项目内 `Marshal()` 与 `ToJSON()` 并存。新代码统一 `ToJSON() ([]byte, error)`，历史代码在重构时收敛。

### 19.11 测试反模式

- 测试依赖外部服务（数据库、Redis、Kafka）
- 测试之间有依赖（必须可独立运行）
- 测试无断言
- 测试函数命名 `Test1`、`Test2`
- 不测试错误路径
- 不测试并发安全

### 19.12 安全反模式

- 明文存储密码
- 日志记录敏感信息
- `math/rand` 生成 token
- 时间戳作为随机种子
- 凭证硬编码在代码或配置
- CORS 配置 `*` 在生产环境
- 不校验请求签名

---

## 附录 A：参考实现索引

| 主题 | 参考文件 |
|---|---|
| DDD 分层 | [game/domain/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/)、[game/application/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/)、[game/infrastructure/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/) |
| Repository 接口 + 实现 | [game/domain/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/db_repository.go) + [game/infrastructure/persistence/mysql/db_repository.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/db_repository.go) |
| 错误码 + GameError | [common/message/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/errors.go) |
| AsyncTaskRunner | [common/async/task_runner.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/async/task_runner.go) |
| 调度器框架 | [common/scheduler/base.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/scheduler/base.go)、[common/scheduler/registry.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/scheduler/registry.go) |
| 调度器实现 | [game/scheduler/timeout_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go) |
| Lua 脚本注册 | [game/infrastructure/persistence/redis/scripts/registry.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/registry.go) |
| Token 锁释放 | [game/infrastructure/persistence/redis/scripts/robot_lock.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/robot_lock.lua.go) |
| 分布式锁框架 | [common/lock/distributed_lock.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/distributed_lock.go) |
| 锁释放 Lua 脚本 | [common/lock/scripts/release_lock.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/lock/scripts/release_lock.lua.go) |
| 三层幂等 | [settlement/service/deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) |
| 指数退避重试 | [settlement/service/credit_retry_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/credit_retry_service.go) |
| 乐观锁 UPDATE | [settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) |
| atomic.Pointer 热配置 | [game/algorithm/packet_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/packet_generator.go) |
| TraceID 生成 | [settlement/service/trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) |
| TraceID Context 传播 | [common/trace/trace.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/trace/trace.go) |
| gRPC 拦截器 | [game/server/generic_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/server/generic_service.go) |
| 健康检查三端点 | [gateway/health/health.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/health/health.go) |
| Lua 错误码常量表 | [game/domain/lua_codes.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/domain/lua_codes.go) |
| 雪花 ID 生成器 | [common/idgen/snowflake.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go) |
| nodeID Redis 自动分配 | [common/idgen/node_allocator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/node_allocator.go) |
| nodeID 释放 Lua 脚本 | [common/idgen/release_node.lua.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/release_node.lua.go) |
| Redis key 单一真相源 | [common/rediskeys/keys.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/rediskeys/keys.go) |
| 字符串工具 | [common/strutil/url.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/strutil/url.go)、[common/strutil/hostport.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/strutil/hostport.go) |
| 签名校验 | [common/signature/signer.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/signature/signer.go) |
| 限流中间件 | [gateway/middleware/ratelimit.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/ratelimit.go) |
| Lua 脚本测试 | [game/infrastructure/persistence/redis/scripts/packet_test.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/redis/scripts/packet_test.go) |
| 雪花 ID 测试 | [common/idgen/snowflake_test.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake_test.go) |

---

## 附录 B：业界规范参考

本规范基于以下业界权威规范制定，开发者应熟悉这些原始文档：

### B.1 Go 官方与社区

- [Effective Go](https://go.dev/doc/effective_go) - Go 官方编程指南
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments) - Go 代码评审标准
- [Google Go Style Guide](https://google.github.io/styleguide/go/) - Google Go 风格指南（决策与指导）
- [Go Blog](https://go.dev/blog/) - Go 官方博客（含 error handling、context、pipelines 等）
- [Go Project Layout](https://github.com/golang-standards/project-layout) - Go 项目布局参考
- [Practical Go: Real world advice for writing maintainable Go programs](https://dave.cheney.net/practical-go) - Dave Cheney 实战建议

### B.2 架构与设计

- [Clean Architecture](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html) - Robert C. Martin
- [Domain-Driven Design](https://www.domainlanguage.com/ddd/) - Eric Evans
- [Twelve-Factor App](https://12factor.net/) - 现代云原生应用方法论
- [The Reactive Manifesto](https://www.reactivemanifesto.org/) - 响应式系统设计

### B.3 分布式系统

- [Designing Data-Intensive Applications](https://dataintensive.net/) - Martin Kleppmann
- [Distributed Systems Observability](https://www.oreilly.com/library/view/distributed-systems-observability/9781492033431/) - Cindy Sridharan
- [Kafka Best Practices](https://www.confluent.io/blog/kafka-best-practices/) - Confluent
- [Redis Best Practices](https://redis.io/docs/manual/patterns/) - Redis 官方

### B.4 可观测性

- [OpenTelemetry Go](https://opentelemetry.io/docs/instrumentation/go/) - 分布式追踪标准
- [Prometheus Best Practices](https://prometheus.io/docs/practices/naming/) - Metrics 命名与实践
- [Structured Logging](https://www.honeycomb.io/blog/structured-logging) - 结构化日志

### B.5 安全

- [OWASP Top 10](https://owasp.org/Top10/) - Web 应用安全风险
- [Go Security](https://go.dev/doc/security) - Go 官方安全指南
- [CWE - Common Weakness Enumeration](https://cwe.mitre.org/) - 通用缺陷枚举

### B.6 测试

- [Go Testing Blog](https://go.dev/blog/testing) - Go 官方测试博客
- [TableDrivenTests](https://github.com/golang/go/wiki/TableDrivenTests) - 表驱动测试
- [Test Doubles](https://martinfowler.com/bliki/TestDouble.html) - Martin Fowler 测试替身

### B.7 API 设计

- [Google API Design Guide](https://cloud.google.com/apis/design/) - Google API 设计指南
- [REST API Design Rulebook](https://www.oreilly.com/library/view/rest-api-design/9781449317904/) - REST API 设计规则
- [gRPC Best Practices](https://grpc.io/docs/guides/best-practices/) - gRPC 最佳实践

---

## 附录 C：项目技术债务清单（必须收敛项）

> 本附录列出当前代码库中不符合本规范的历史遗留问题，按优先级排列。新代码不得复刻这些不一致，重构时同步收敛。

### C.1 高优先级（资金/安全相关）

| 编号 | 文件 | 问题 | 应收敛为 |
|---|---|---|---|
| TD-1 | [settlement/service/deduct_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go) | `if exists, _ :=` 多处吞掉 error（11 处） | 显式检查 error 并返回 |
| TD-2 | [game/algorithm/straight.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/algorithm/straight.go) | 用 `math/rand` 涉及金额 | 改为 `crypto/rand` |
| TD-3 | [settlement/service/game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) | flat `5 * time.Second` 重试且不递增 retryCount | 指数退避 + `IncrementRetryCountWithNextRetryTime` |
| TD-4 | [settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) | `UpdateBillStatus` 等多处不带乐观锁 | 补 `WHERE status = ?` 条件 |
| TD-5 | [settlement/service/refund_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/refund_service.go) | service 直接持有 `*gorm.DB` 开事务 | 下沉到 `BillManager` + `Transaction.Execute` |
| TD-6 | [settlement/service/game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) | inline `fmt.Sprintf` 生成幂等键 | 下沉为 `TraceIDGenerator` 方法 |
| TD-7 | [settlement/service/game_settle_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/game_settle_service.go) | 只锁内检查无锁外预检 | 补齐锁外 cheap 检查 |

### C.2 中优先级（一致性/可维护性）

| 编号 | 文件 | 问题 | 应收敛为 |
|---|---|---|---|
| TD-8 | [common/message/push.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/push.go) 等 | 4 空格缩进 | `gofmt -w` 修复为 tab |
| TD-9 | [gateway/middleware/ratelimit.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/middleware/ratelimit.go) | 裸数字 `429` | `http.StatusTooManyRequests` |
| TD-10 | [stats/handler/stats_handler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/handler/stats_handler.go) | 多种响应信封并存 | 统一 `{"code","msg","data"}` |
| TD-11 | [stats/handler/stats_handler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/handler/stats_handler.go) | `strconv.Atoi(c.Query(...))` 手动解析 | `dto.PaginationReq` + `ShouldBindQuery` |
| TD-12 | [cmd/stats/main.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/cmd/stats/main.go) | 装配塞进 main | 重构为 `bootstrap/` 包 |
| TD-13 | [game/bootstrap/container.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/container.go) | `NewContainer` 24 个参数 | options struct |
| TD-14 | [game/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) | 用 `panic` 处理启动错误；缺 `Wait()` | `logger.Fatal`；补 `Wait()` |
| TD-15 | [common/limiter/limiter.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/limiter/limiter.go) | 内联 Lua + `redis.Eval` | 收敛到 `redis.Script` |
| TD-16 | [game/infrastructure/persistence/mysql/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/infrastructure/persistence/mysql/) | Repository 命名三种风格并存 | 统一 `gorm<Domain>Repository` |
| TD-17 | [settlement/service/user_id_convert_service.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/user_id_convert_service.go) | struct 名为 `UserService` 但同时是接口 | 改名 `UserIDConverter` |
| TD-18 | [settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) | `ExistsByRoundAndType` 与 `ExistsRoundSettlement` 并存 | 统一为 `ExistsByRoundSettlement` |
| TD-19 | [settlement/service/bill_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/bill_manager.go) | `CreateBillsInTransaction` 与 `CreateBillsOnly` 字节级重复 | 删除其一 |
| TD-20 | [gateway/service/game.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/game.go) 与 [gateway/service/test.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/test.go) | `saveUserAndGetInternalID` 重复 | 抽取共享方法 |
| TD-21 | [game/scheduler/timeout_scheduler.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/scheduler/timeout_scheduler.go) | `ClearAllTimeouts` 与 `ClearAllRoomTimeouts` 重复 | 删除其一 |
| TD-22 | [gateway/server/server.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/server/server.go) | 命令式 `setupRoutes()` | `RegisterRoutes` 模式 |

### C.3 低优先级（命名/注释）

| 编号 | 文件 | 问题 | 应收敛为 |
|---|---|---|---|
| TD-23 | [common/message/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/errors.go) | 西语注释 | 英文注释 |
| TD-24 | [common/message/types.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/types.go) | 中文分节注释 | 英文 |
| TD-25 | [settlement/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/) | 混合中英文注释 | 新代码用英文 |
| TD-26 | [common/message/broadcast.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/broadcast.go) vs [common/message/request.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/message/request.go) | `Marshal()` 与 `ToJSON()` 并存 | 统一 `ToJSON()` |
| TD-27 | [gateway/health/health.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/health/health.go) | `HealthStatus.Uptime` 字段未赋值 | 赋值或删除 |
| TD-28 | [stats/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/stats/config/config.go) | 默认端口 8081 与 gateway 冲突 | 改为 8082 |
| TD-29 | [gateway/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/config.go) 等 | `RedisConfig` 重复声明 | 复用 `common/config.RedisConfig` |
| TD-30 | [common/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/config.go) | `Load` 加载 algorithm.yaml，`LoadFromContent` 不会 | 对称化 |

---

## 附录 D：变更记录

| 日期 | 变更 |
|---|---|
| 2026-07-04 | 初版，基于 backend/ 全量代码（171 文件）分析制定 |
| 2026-07-04 | 新增 §16 字符串拼接规约（SC-1~SC-10）；§4.3 补充 `%w` vs `%v` 说明；§7.1 补充 `common/rediskeys` 统一包说明、Prefix 尾随冒号约定、Lua 孤儿 key 禁止规则 |
| 2026-07-04 | 新增 §17 调度器规约（SCH-1~SCH-12）；附录 A 调度器参考实现指向 `common/scheduler/base.go` |
| 2026-07-04 | 新增 §18 分布式锁与事务规约（DL-1~DL-12、TX-1~TX-12）；附录 A 补充分布式锁框架与锁释放 Lua 脚本参考实现 |
| 2026-07-04 | 新增 §19 Lua 脚本规约（分层）（L-1~L-8 通用层、L-B1~L-B2 业务层、L-G1~L-G2 通用脚本层） |
| 2026-07-05 | 新增 §21 TraceID 传播规约（TP-1~TP-12） |
| 2026-07-05 | **重大重构**：基于业界最佳实践全面重组文档结构，提升至高级开发工程师水准。主要变更：<br>1. 新增 §0 前言、§1 总则与核心原则，明确设计目标与质量红线<br>2. 新增 §13 测试规范（表驱动、Mock、覆盖率、并发测试、基准测试）<br>3. 新增 §14 安全规范（输入验证、SQL 注入、敏感数据、加密随机数、凭证管理、CORS、签名校验）<br>4. 新增 §15 性能与资源管理（内存、连接池、超时、缓存、N+1、异步化）<br>5. 新增 §18 格式化与工具链（golangci-lint、pre-commit、依赖管理、死代码）<br>6. 合并原 §17/§18/§19/§20/§21 到 §10 分布式系统统一编排<br>7. 附录 B 新增业界规范参考（Google Go Style Guide、Effective Go、Clean Architecture、Twelve-Factor App 等）<br>8. 附录 C 将原散落各处的"必须收敛"项集中为项目技术债务清单，按优先级分级<br>9. 反模式章节扩充（命名、错误处理、并发、数据库、Redis、HTTP、配置、重复、注释、序列化、测试、安全）<br>10. 引用业界权威规范作为兜底（Google Go Style Guide、Go Code Review Comments、OWASP 等） |
