# Backend 代码架构重构方案

> 本文聚焦**代码架构层面**(分层、模块边界、依赖、横切关注点、可测试性),与已有的 [ARCHITECTURE_REFACTOR_PLAN.md](../ARCHITECTURE_REFACTOR_PLAN.md)(基础设施层面:Redis Sentinel / MySQL / Kafka / 广播)互补,不重复其内容。
>
> 所有结论均带文件路径与行号依据,可逐条复核。

## 目录

- [一、重构概述](#一重构概述)
- [二、架构总览与核心诊断](#二架构总览与核心诊断)
- [三、分层架构问题(DDD)](#三分层架构问题ddd)
- [四、模块边界与依赖](#四模块边界与依赖)
- [五、横切关注点一致性](#五横切关注点一致性)
- [六、跨服务组织与重复设计](#六跨服务组织与重复设计)
- [七、重构路线图](#七重构路线图)
- [附录:问题清单汇总表](#附录问题清单汇总表)

---

## 一、重构概述

### 1.1 审查范围

`packet/backend` 下 4 个服务:

| 服务 | 入口 | 包路径 | 运行形态 |
|------|------|--------|----------|
| game | `cmd/game/main.go` | `game/` | 独立进程(gRPC) |
| gateway | `cmd/gateway/main.go` | `gateway/` | 独立进程(WebSocket + HTTP) |
| stats | `cmd/stats/main.go` | `stats/` | 独立进程(gin HTTP) |
| settlement | **无独立 cmd** | `settlement/` | **被 game 进程内嵌** |

### 1.2 重构目标

1. 厘清分层边界,消除跨层依赖与基础设施泄漏
2. 消除模块间重复代码与隐性耦合(共享 key / 共享 model)
3. 统一横切关注点(配置、错误、日志、类型转换、金额)
4. 提升可测试性,为核心资金逻辑补齐单测保护
5. 明确 settlement 的服务定位(独立 vs 内嵌)

### 1.3 与已有工作的关系

- 已完成的 P0 修复(robot_pool 类型一致、虚拟余额 Lua 原子化、robot key 统一 `cashparty:` 前缀、抢包限流恢复、settlement 事务回滚)见 `.trae/specs/fix-game-optimization-issues/`,本文不再重复。
- 本文关注的是这些点修复之后**仍然存在的结构性问题**。

---

## 二、架构总览与核心诊断

### 2.1 目标分层架构

```
cmd/                 进程入口,只做 bootstrap 调用
  └─ bootstrap/      组装(统一骨架:Config → Logger → Redis → MySQL → Kafka → DI → Run)
       └─ application/   应用编排(薄:取数据→调领域→存数据→发事件)
            └─ domain/    领域模型(实体/值对象 + Repository 接口 + 业务错误)
                 ↑ 接口反转
            infrastructure/  基础设施实现(redis/mysql/kafka/messaging)
```

### 2.2 当前实际形态(诊断)

```
game:
  domain ──import──→ model            ❌ 方向倒置 + 常量重复
  domain ──定义──→ Lua 错误码          ❌ 基础设施泄漏
  application ──import──→ redis/scripts ❌ 跨层依赖(102 处)
  application ──持有──→ settlement 具体struct ❌ 无接口

settlement:
  无 domain 层                          ❌ 分层缺失
  service ──import──→ redis / gorm     ❌ 混合编排+数据访问
  service ──import──→ game/model       ❌ 违反循环依赖约束

gateway:
  测试代码混入生产目录并被生产 Container 实例化  ❌ 安全风险

横切:
  三套配置加载 / 两套错误模型 / utils 杂物间   ❌ 不一致
  全项目仅 2 个 _test.go                  ❌ 无回归保护
```

### 2.3 核心矛盾一句话

**"形似微服务、实为单体"**:目录按 4 个服务划分、settlement 有完整 DDD 目录结构,但运行时是单进程、跨层依赖、共享 model、共享 key,既享受不到独立部署的好处,又承担了双份基础设施代码的维护成本,且没有任何测试保护。

---

## 三、分层架构问题(DDD)

### A-01 [严重] application 层大规模直接依赖 infrastructure

**依据**:`game/application/` 8 个文件中共 **102 处**对 `redis.` / `scripts.` / `gorm.` 的引用。

- [game_app_service.go](../game/application/game_app_service.go):第 21-23 行 import `messaging` / `redis` / `scripts`;第 175 行 `redis.SendPacketLockKey(...)`、第 519 行 `scripts.TryStartGame.Run(...)`、第 895 行 `scripts.SettleRound.Run(...)`、第 1146 行 `scripts.EndGame.Run(...)`
- [grab_service.go](../game/application/grab_service.go):第 14-15 行 import;第 50 行 `scripts.GrabPacket.Run`、第 236 行 `scripts.SendPacket.Run`
- [seat_app_service.go](../game/application/seat_app_service.go):第 13-14 行 import;第 240 行 `scripts.PlayerReady.Run`

**为什么是问题**:application 层直接持有 `*cRedis.Client`、直接调 Lua 脚本并解析 `[]interface{}` 返回值,与 Redis 强耦合,无法替换存储实现做单测。

**反例即标杆**:[room_app_service.go](../game/application/room_app_service.go) 第 12-16 行只 import `domain` / `scheduler` / `settlement`,通过 `s.repo`(domain.RoomRepository 接口)操作,**零** infrastructure 直接依赖。同一代码库内标准不一致,正说明当前跨层依赖是可治理却未治理。

**解决方案**:在 `game/domain/repository.go` 为抢红包、发红包、自动分配、结算等行为补充领域接口(如 `PacketRepository` / `RoundStateRepository`),把 Lua 调用与返回值解析全部下沉到 `game/infrastructure/persistence/redis` 的实现中,application 层只调用接口返回的强类型结果。以 `room_app_service.go` 为标杆统一所有 app service 写法。

---

### A-02 [严重] settlement 完全缺失 domain 层,无 Repository 接口抽象

**依据**:`settlement/` 下只有 `config/` `dto/` `infrastructure/` `model/` `scheduler/` `service/`,**无 `domain/` 目录**。

- [bill_manager.go](../settlement/service/bill_manager.go):第 12-18 行 `BillManager` 直接持有 `*gorm.DB`,所有方法(第 20 行 `CreateBill`、第 113 行 `GetRoundSettlementByRoundID`)直接写 GORM 查询
- [settlement_service.go](../settlement/service/settlement_service.go):第 14 行 import redis;第 73 行 `redis.SettleRoundLockKey(...)` 在 service 中构造 Redis key
- [deduct_service.go](../settlement/service/deduct_service.go):第 15 行 import redis;第 71 / 407 / 415 行直接用 `redis.FirstRoundDeductLockKey` 等
- 全量统计:`settlement/service/` 10 个文件中共 **41 处**对 `redis.` / `gorm.` 的引用

**为什么是问题**:service 层同时承担 application 编排、domain 业务规则、infrastructure 数据访问三重职责。没有 Repository 接口意味着无法在不连真实 MySQL/Redis 的情况下单测 `SettleRound` / `DeductForFirstRound` 等核心结算逻辑。

**解决方案**:为 settlement 新建 `domain/` 层,定义 `BillRepository` / `RoundSettlementRepository` / `RefundRepository` 接口(纯 domain 类型签名);将 `BillManager` 的 GORM 实现下沉到 `infrastructure/persistence/mysql/`;service 层只持有接口并做编排。Redis 锁 key 由 infrastructure 包装为 `LockProvider` 接口暴露。

---

### A-03 [严重] Lua 脚本细节泄漏到 domain,错误翻译职责倒置

**依据**:

- [lua_errors.go](../game/domain/lua_errors.go):全文定义 `LuaSuccess` / `LuaErrRoomNotFound` 等 Redis Lua 脚本返回码常量
- [errors.go](../game/domain/errors.go):第 5-75 行 `MapLuaError(code int)`,第 3 行 import `common/message`
- 调用方向倒置:[repository.go](../game/infrastructure/persistence/redis/repository.go) 第 192-196 / 247-250 / 283-287 行,infrastructure 实现反向调用 `domain.MapLuaError(code)`
- application 层也直接调用:[grab_service.go](../game/application/grab_service.go) 第 58 / 127 行;[game_app_service.go](../game/application/game_app_service.go) 第 908 / 1159 行

**为什么是问题**:domain 层本应不依赖任何基础设施技术,但现在 domain 包含了 Lua 错误码这一 Redis 专属概念。正确方向应是 infrastructure 把 Lua 返回码翻译成 domain 错误,而非反过来。`domain/errors.go` 依赖 `common/message` 传输层错误码,也让 domain 反向依赖表示层。

**解决方案**:删除 `game/domain/lua_errors.go` 与 `domain/errors.go` 中的 `MapLuaError`;在 infrastructure 的 redis repository 实现内私有化 Lua 错误码 → domain sentinel error 的翻译;domain 层只定义业务错误变量(如 `var ErrSeatOccupied = errors.New(...)`)。

---

### A-04 [严重] 领域模型严重贫血,核心业务逻辑堆在 service 层

**依据**:

- [domain/room.go](../game/domain/room.go):`RoomMeta`(12-28 行)、`Player`(30-37 行)全是纯字段 struct,`Player` 仅 `IsOnline()`(55-57)与 `CanGrab()`(59-61)两个 trivial getter
- [domain/grab.go](../game/domain/grab.go):`GrabResult` / `PacketInfo` / `PlayerResult` / `DistributeResult`(12-56 行)全部纯数据无方法
- [domain/penalty.go](../game/domain/penalty.go):`DefaultPenaltyPolicy()`(68-75 行)把惩罚金额硬编码为 0
- [settlement/model/bill.go](../settlement/model/bill.go):`BillRecord`(5-41 行)、`RoundSettlement`(47-80 行)是带 gorm tag 的纯数据 struct
- 真正业务逻辑全在 [game_app_service.go](../game/application/game_app_service.go)(单文件 **1733 行**):`settleRound`(869-1115,约 246 行)内嵌佣金计算(944 行)、判断游戏结束;`initRoundAndDeduct`(1529-1606)、`initLaterRoundAndDeduct`(1608-1671)、`initSystemRoundAndDeduct`(1673-1732)三段高度重复

**为什么是问题**:贫血模型是 DDD 反模式。业务规则(首回合房费平摊、佣金按 5% 计算、最低金额玩家成为下一轮发送者、抢完最后一个包触发结算)散落在 application service 里,导致规则重复、无法单测、service 持续膨胀。

**解决方案**:把领域规则迁移到实体/值对象。`RoomMeta` 增加 `NextRoundNo()` / `IsFirstRound()`;新建 `Round` 领域实体封装 `Commission()` / `RoomFeePerPlayer(n)` / `NextSender(results)` 等行为;`PenaltyPolicy` 真正承担惩罚金额计算与踢人决策。application service 只做薄编排。

---

### A-05 [高] game 的 domain 与 model 职责重叠,常量重复定义

**依据**:

- `RoomStatus` 双份定义:[domain/room.go](../game/domain/room.go) 第 3-10 行 与 [model/room.go](../game/model/room.go) 第 5-12 行 完全相同
- 房间实体双重定义:`domain.RoomMeta`(Redis 运行态,RoomID 为 string)与 `model.Room`(GORM 持久化,RoomID 为 int64),字段大量重叠
- [db_repository.go](../game/domain/db_repository.go) 第 7 行 `import "game/model"`,第 10-53 行所有接口方法返回 `*model.Room` / `*model.Round`,**domain 反向依赖 model**

**为什么是问题**:常量重复违反 DRY,改一处必须同步改另一处;domain 依赖 model 使 domain 不再独立可测,违背"domain 不依赖任何外层"的 DDD 原则。

**解决方案**:让 domain 成为唯一领域模型来源,model 只保留纯持久化 DTO(通过 mapper 转换);db_repository 接口返回 domain 类型,infrastructure 的 mysql 实现负责 `model.Xxx` ↔ `domain.Xxx` 转换。**必须消除常量重复**(model 引用 domain 的常量)且 domain 不得 import model。

---

### A-06 [高] GameAppService 过厚(1733 行),职责过载且存在重复编排

**依据**:见 A-04。单一 struct 承担发红包、抢红包、三类超时处理、惩罚、踢人替补、轮次初始化与扣款、结算编排、Lua 调用、事件发布、广播。`initRoundAndDeduct` / `initLaterRoundAndDeduct` / `initSystemRoundAndDeduct` 三段逻辑高度雷同。

**解决方案**:按领域拆分为 `RoundService`(轮次初始化/扣款编排)、`SettlementOrchestrator`(结算编排)、`TimeoutHandler`(各类超时);把三个 initXxxAndDeduct 合并为参数化单一方法(用 `SendScenario` 区分)。

---

### A-07 [中] application / settlement 依赖具体 struct 而非接口

**依据**:

- [game_app_service.go](../game/application/game_app_service.go) 第 30-50 行:`GameAppService` 字段全是具体 struct 指针(`*messaging.GameEventPublisher`、`*settlementService.SettlementService`、`*settlementService.DeductService` 等)
- settlement/service 14 个文件中**只有 `RobotChecker` 一个接口**([robot_checker.go](../settlement/service/robot_checker.go) 第 13-15 行定义、第 23-25 行构造函数返回接口)
- `SettlementService` 持有 11 个依赖([settlement_service.go](../settlement/service/settlement_service.go) 第 18-31 行),仅 `robotChecker` 是接口

**为什么是问题**:无法用 mock 替换 settlement 服务做 game 单测;game 与 settlement 强耦合,任何 settlement 内部重构都波及 game。

**解决方案**:settlement 在新建的 `port` 包中定义出站接口(`SettlementPort` / `DeductPort` / `BalancePort`),由 service 实现;game.application 依赖接口。`robot_checker.go` 是现成的依赖倒置范例,应推广到所有跨服务依赖。

---

### A-08 [低] domain 事件 payload 使用 `interface{}` 弱类型

**依据**:[events.go](../game/domain/events.go) 第 34 行 `Payload interface{}`、第 104 行 `Data interface{}`。`GameEvent.Data` 承载 `SessionStartData` / `PacketCreatedData` 等多种类型但用 `interface{}`,消费者需类型断言。

**解决方案**:使用泛型事件 `GameEvent[T any]` 或为每类事件定义独立 struct,获得编译期类型检查。

---

## 四、模块边界与依赖

### B-01 [严重] settlement 反向 import game/model,违反循环依赖约束

**依据**:[user_id_convert_service.go](../settlement/service/user_id_convert_service.go) 第 8 行 `gameModel "github.com/cashparty/backend/game/model"`,第 12-14 行 `UserService` 接口方法返回 `*gameModel.User`。

**为什么是问题**:project memory 硬约束明确"Settlement package cannot import game package to prevent circular dependencies"。当前因 `game/model` 是叶子包(仅 import `common/idgen`)未触发编译期循环,但构成**逻辑双向耦合**(game → settlement 17 处 + settlement → game/model 1 处)。settlement 把 game 的 ORM 实体当领域模型在接口里返回,game 的 DB schema 演变会直接破坏 settlement 接口契约。

**解决方案**:settlement 侧定义最小自洽类型(`settlement/model` 加 `PlatformUser{ PlatformUserID string }`),`UserService` 接口返回 settlement 自己的类型;game 侧写适配器把 `gameModel.User` 翻译过去。彻底切断 `settlement → game` 的 import。

---

### B-02 [严重] settlement 没有独立进程入口,被 game 进程完全内嵌 — 架构定位错乱

**依据**:

- `cmd/` 下无 `cmd/settlement/`
- [game/bootstrap/app.go](../game/bootstrap/app.go) 第 25-26 行 import settlement 包;第 126-149 行在 game 进程内 new 了 11 个 settlement 服务实例
- [game/bootstrap/container.go](../game/bootstrap/container.go) 第 45-55 行 Container 持有 5 个 settlement scheduler 字段;第 295-308 行 `initSettlementSchedulers()`;第 329-361 行 `StartSchedulers()` 启动
- `settlement/` 目录却按独立服务组织(完整 DDD 分层 + 独立 Lua 脚本 + 独立 Redis keys + `SETTLEMENT_SOLUTION.md`)

**为什么是问题**:"形似服务、实为库"。部署/扩缩容无法独立;settlement 的重试/补偿调度器只能随 game 进程生死;项目记忆的循环依赖约束被反向绕过(实际是 game→settlement 单方向紧耦合,而非真正解耦)。

**解决方案**(二选一):

1. 若 settlement 真是独立业务能力 → 给它 `cmd/settlement/main.go`,通过 Kafka/gRPC 与 game 解耦,各自独立部署
2. 若 settlement 永远内嵌 game → 把 `settlement/` 内联到 `game/settlement/` 下,删除独立 config/keys/scripts 副本,消除"独立服务"假象

**当前形态两头不靠**,既享受不到独立部署的好处,又承担双份基础设施代码维护成本。需先做这个决策,它决定了后续 A-01/A-02 重构的方向。

---

### B-03 [高] game↔settlement 的"Kafka 事件协作"实为 game 私有自产自销

**依据**:

- 事件契约定义在 [game/domain/events.go](../game/domain/events.go) 第 89-179 行(`GameEvent` 等全属 `package domain` 即 game 包内)
- 发布方 [game_event_publisher.go](../game/infrastructure/messaging/game_event_publisher.go) 第 11 行 import game/domain
- 消费方 [game_event_consumer.go](../game/infrastructure/messaging/game_event_consumer.go) 第 13-17 行**同属 game 包**(同时 import settlement/dto + settlement/service);第 333-362 行构造 `settlementDto.RoundSettleRequest` 后**同步进程内调用** `c.settlementService.SettleRound(ctx, settleReq)`

**为什么是问题**:settlement 包完全不知道 `GameEvent` 的存在,也不订阅任何 topic。Kafka 在这里只是 game 的**内部事件溯源/重试缓冲**,不是 game 与 settlement 的跨服务通信通道。架构名义上是事件驱动,实际是单体进程内调用。

**解决方案**:若保持 in-process,明确文档:settlement/dto 才是契约面,Kafka topic 属 game 内部实现细节;若意图解耦,把跨上下文事件契约抽到独立 proto/shared 包,双方共享并版本化。

---

### B-04 [高] settlement/dto 作为契约被 game 直接依赖,无序列化标签且类型摩擦大

**依据**:

- [settlement/dto/request.go](../settlement/dto/request.go) 全文无任何 json tag,字段全为 int64
- game 侧 5 个 application 服务 + 1 个 messaging 直接 import settlement/dto + settlement/service([game_app_service.go:26-27](../game/application/game_app_service.go)、[room_app_service.go:14-15](../game/application/room_app_service.go)、[seat_app_service.go:16-17](../game/application/seat_app_service.go)、[penalty_service.go:15-16](../game/application/penalty_service.go)、[history_service.go:13](../game/application/history_service.go)、[game_event_consumer.go:16-17](../game/infrastructure/messaging/game_event_consumer.go))
- 类型摩擦:`RoundSettleRequest` 字段全 int64,而 `GameEvent.RoomID/SessionID` 是 string,[game_event_consumer.go:343](../game/infrastructure/messaging/game_event_consumer.go) 靠 `parseInt64` 做 string→int64 转换,consumer 中此类转换出现十几次

**为什么是问题**:game 直接依赖 settlement 入参结构作为契约,settlement 改字段名/类型会编译波及 game 多个 application 服务;类型不一致导致边界处大量手工转换,易错且无类型保护。

**解决方案**:game 应通过自己的端口接口(port)调用,由独立适配层做类型翻译,而非 application 层直接 import settlement/dto。

---

### B-05 [中] common 包偏"大杂烩",业务语义已渗入

**依据**:grep 确认 common 不反向依赖业务包(方向正确),但 `common/message` 定义了游戏命令码(被 [generic_service.go:79+](../game/server/generic_service.go) 的 `message.CmdJoinRoom` 等使用),`common/broadcast` 含游戏广播常量。

**为什么是问题**:common 已不是纯粹技术通用库,混入游戏领域语义。stats/gateway 等其它服务会被迫拉入这些与己无关的游戏命令码定义。

**解决方案**:维持 common 为纯技术库(kafka/redis/mysql/lock/logger/idgen);把 `message`(业务命令码)、`broadcast` 常量等业务语义下沉到对应业务模块或独立 `shared-contract` 包。

---

### B-06 [正向] gateway ↔ game 边界干净,可作为重构目标参考

**依据**:grep 确认 gateway 代码完全不 import game 包(唯一匹配是文档)。[proto/common/generic.proto](../proto/common/generic.proto) 第 7-10 行定义 `Forward` / `SaveUser` RPC;[gateway/discovery/discovery.go](../gateway/discovery/discovery.go) 第 19 行定义 `Forward` 接口,第 167-168 行经 Nacos 服务发现连 game;[gateway/service/game.go](../gateway/service/game.go) 第 17-19 行用 `UserSaver` 接口做依赖反转。

**结论**:这是整个仓库最干净的边界 — 纯 gRPC + Nacos 服务发现 + 接口反转。建议把此模式作为 game↔settlement 解耦的目标架构。

---

## 五、横切关注点一致性

### C-01 [严重] 配置文件敏感信息明文入库且跨文件重复

**依据**:

- [config/game.yaml:47](../config/game.yaml) `merchant_secret: "aca5d11a-e481-4163-9505-194564558ae3"`
- [config/gateway.yaml:61](../config/gateway.yaml) 同一 `merchant_secret` 字面量重复
- [config/game.yaml:27](../config/game.yaml) DB 密码 `123456`、第 69 行 Nacos 密码 `nacos`
- [config/gateway.yaml:66](../config/gateway.yaml) JWT `secret_key: "your-256-bit-secret-key-here-min-32-characters"`

**为什么是问题**:商户密钥/DB 密码/Nacos 密码/JWT 密钥均明文提交仓库;同一 `merchant_secret` 双份硬编码,改一处忘另一处会导致签名校验失败。

**解决方案**:敏感信息迁出 yaml,走环境变量 / Nacos 加密配置;`merchant_secret` 由 common/config 单点定义,gateway 经 nacos 拉取同一份。配合 C-04 的统一配置加载,启用 `AutomaticEnv` 支持 Secret 走环境变量。

---

### C-02 [严重] 错误处理策略分裂:game 用 `message.Error`,settlement 全用裸 `fmt.Errorf`

**依据**:

- [common/message/errors.go:251-270](../common/message/errors.go) 定义 `type Error struct{Code, Msg}` + `NewError(code)`
- [game/domain/errors.go:5-75](../game/domain/errors.go) `MapLuaError` 统一映射 Lua 错误码 → `*message.Error`
- settlement/service 共 40+ 处 `fmt.Errorf("...: %w", err)`([settlement_service.go:77,80,89](../settlement/service/settlement_service.go)、[deduct_service.go:121,205,216](../settlement/service/deduct_service.go)、[refund_service.go:68,72,76,107...](../settlement/service/refund_service.go)、[game_settle_service.go:65,68,86...](../settlement/service/game_settle_service.go)),**无一处使用 `message.Error`**;settlement 目录无 `errors.go`

**为什么是问题**:同一项目两套错误模型。game 的业务错误可被网关通过 `IsGameError` 识别并翻译为前端错误码,settlement 的 `fmt.Errorf` 全部退化为 `CodeSystemError`(5000),前端无法区分"余额不足/单据已退款/平台调用失败"等业务语义。

**解决方案**:settlement 引入 `settlement/domain/errors.go`,对可恢复业务错误(已退款、单据状态非 pending、余额不足)用 `message.NewError(code)` 或自定义带 `Is/Unwrap` 的错误类型。

---

### C-03 [严重] `message.Error` 未实现 `Is()` / `Unwrap()`,`errors.Is` 无法用于业务错误

**依据**:[common/message/errors.go:251-270](../common/message/errors.go) `type Error` 仅有 `Error() string`,无 `Is` / `Unwrap`;第 301-313 行用类型断言实现 `IsGameError` / `IsErrorCode`。全局 grep `errors.Is|errors.As` 仅 8 处,**全部针对 `gorm.ErrRecordNotFound` / `goredis.Nil`**,无一处用于 `message.Error`。

**为什么是问题**:Go 标准错误惯用法 `errors.Is(err, message.ErrInsufficientBalance)` 完全失效;第三方库用 `fmt.Errorf("...: %w", message.NewError(code))` 包装后类型断言丢失,业务错误码被吞。

**解决方案**:为 `message.Error` 增加 `Is(target error) bool`(按 Code 比较)与可选 `Unwrap`;为高频业务码提供 sentinel 变量(`var ErrInsufficientBalance = message.NewError(message.CodeInsufficientBalance)`),统一用 `errors.Is`。

---

### C-04 [严重] 三套配置加载机制并存

**依据**:

- [common/config/config.go:230-253](../common/config/config.go):viper + `mapstructure` 标签 + `AutomaticEnv` + 自动加载 algorithm.yaml
- [gateway/config/config.go:117-131](../gateway/config/config.go):`os.ReadFile` + `yaml.Unmarshal` + `yaml` 标签(完全自建)
- [stats/config/config.go:56-70](../stats/config/config.go):第二套自建
- [settlement/config/config.go:7-23](../settlement/config/config.go):仅 `PlatformConfig = config.PlatformConfig` 别名 + 桥接
- `LogConfig` / `RedisConfig` / `MySQLConfig` 等在 common / gateway / stats 三处重复定义;`RedisConfig` 字段还不一致(common 含 sentinel,gateway/stats 无)

**为什么是问题**:`AutomaticEnv` 的环境变量替换在 gateway/stats 中丢失(无法支持 C-01 的 Secret 走环境变量);新增配置项需改多处;RedisConfig 行为分裂。

**解决方案**:gateway/stats 改为复用 `common/config`(或 common 抽出更小"基础配置"子集 + 各服务扩展),统一 viper + mapstructure + `AutomaticEnv`。

---

### C-05 [高] common/config 偏 game 服务,gateway/stats 无法复用

**依据**:[common/config/config.go:22-28](../common/config/config.go) `GameServiceConfig` / `AlgorithmConfig` / `RobotConfig`(含 Scheduler/Behavior/Account,44-75 行)/ `LuaConfig` / `AvatarConfig` 均为 game 专属。gateway/stats 各自重写 Config 不引用 common/config。

**为什么是问题**:"common"语义被破坏 — 实际是"game 的 config"却命名为公共包。

**解决方案**:将 game 专属配置下沉到 `game/config`;common/config 只保留真正共享的基础设施配置(Server/Redis/MySQL/Kafka/Nacos/Log)。

---

### C-06 [高] 类型转换违规:生产代码绕过 `common/converter` 且吞掉错误

**依据**:

- [user_service.go:118](../game/application/user_service.go) `strconv.FormatInt(id, 10)` 应为 `converter.FormatID(id)`
- [room_repository.go:145](../game/infrastructure/persistence/mysql/room_repository.go) `strconv.FormatInt(roomID, 10)` 应为 `converter.FormatID(roomID)`
- 更严重:[repository.go:75-100](../game/infrastructure/persistence/redis/repository.go) 共 7 处 `_, _ = strconv.ParseInt(v, 10, 64)` / `strconv.Atoi(v)`,**解析错误全部吞掉**:

  ```
  75: meta.ConfigID, _ = strconv.ParseInt(v, 10, 64)
  81: meta.RoomFee, _ = strconv.ParseInt(v, 10, 64)
  84: meta.MaxPlayers, _ = strconv.Atoi(v)
  ...
  ```

**为什么是问题**:既绕过 `converter.ParseIDStrict`(project memory 明确要求),又吞掉错误。若 Redis 中 `config_id` 被脏写为非数字,`ConfigID` 静默变 0,下游拿 0 去查配置/扣费,难以定位。

**解决方案**:统一改用 `converter.ParseIDStrict`(返回 error 并记录)或 `converter.ParseInt64(v)`;`FormatInt` → `converter.FormatID`。

---

### C-07 [高] 多处 error 被显式吞掉,关键业务路径静默失败

**依据**:

- [robot_player.go:173](../game/application/robot_player.go) `_, _ = p.seatAppService.CancelSeat(ctx, ...)`(取消座位错误吞掉,座位残留)
- [robot_scheduler_service.go:170](../game/application/robot_scheduler_service.go) `existingRobots, _ = s.robotSchedulerRedis.GetRoomRobots(...)`(读机器人列表失败 → 当无机器人,调度决策错误)
- [generic_service.go:657](../game/server/generic_service.go) `dataBytes, _ = json.Marshal(data)`(序列化失败 → 空数据下发)

**为什么是问题**:结算/踢人/广播/座位等关键事件吞错后业务层以为成功,运维无日志可查。

**解决方案**:吞错处至少 `logger.Warn("...", "error", err)`;关键路径应把错误向上传播或写入死信。

---

### C-08 [中] `common/utils` 是"杂物间",与 `common/idgen` 职责重叠且含死代码

**依据**:[common/utils/utils.go:14-40](../common/utils/utils.go) 提供 4 个 ID 生成器,与 [common/idgen/snowflake.go:22](../common/idgen/snowflake.go) 重叠。grep 生产调用:`utils.GenerateConnID` 仅 1 处([gateway/server/server.go:192](../gateway/server/server.go));`utils.GenerateRoomID` / `GenerateOrderNo` / `GenerateUUID` **在 .go 生产代码中 0 调用**。`utils.GenerateRoomID`(29 行,时间戳*1e6+随机)有碰撞风险且不递增,不利于 B+树索引。`ContainsInt64`/`ContainsString`(86-113 行)可被 `slices.Contains` 替代;`IsValidPlatform`(71-78 行)是业务校验不应放 common。

**为什么是问题**:两套 ID 生成机制并存;死代码增加维护面;"utils"命名本身是反模式。

**解决方案**:删除未使用的 `GenerateRoomID`/`GenerateOrderNo`/`GenerateUUID`;`ContainsXXX` 用 `slices.Contains`;`IsValidPlatform` 移到业务层;`GenerateConnID` 归入 idgen。

---

### C-09 [中] `common/lock` 残留服务特定 Redis key 常量(死代码)

**依据**:[distributed_lock.go:36](../common/lock/distributed_lock.go) `LockKeyRoom = "cashparty:lock:room:%s"`,grep 全代码库**无任何引用**(所有锁调用为 `lock.WithRedisLock(ctx, s.redis, lockKey, ...)` 传参形式)。

**为什么是问题**:违反 project memory"各服务在各自 keys.go 维护 Redis key 常量"约定;common 中定义业务 key 是职责越界;死常量易误导后续开发者绕过 keys.go。

**解决方案**:删除 `LockKeyRoom`;lock 包只提供锁机制,key 一律由调用方传入。

---

### C-10 [中] broadcast channel 字符串在配置层硬编码,与 common 常量重复

**依据**:[common/broadcast/constants.go:4](../common/broadcast/constants.go) `BroadcastChannelGateway = "cashparty:gateway:broadcast"`;[config/game.yaml:41](../config/game.yaml) 与 [config/gateway.yaml:57](../config/gateway.yaml) 三处定义同一字符串。

**为什么是问题**:改常量需同步改两个 yaml;若 yaml 拼错,producer/consumer 静默失联。

**解决方案**:yaml 中 channel 留空时由代码注入默认值(读 `broadcast.BroadcastChannelGateway`),或启动期校验 yaml channel == 常量。

---

### C-11 [中] `currency.Money` 仅作边界 DTO,内部金额全用裸 `int64`

**依据**:[common/currency/money.go:12](../common/currency/money.go) `type Money int64`(分),仅 API 边界用 `currency.NewMoneyFromFen`。内部计算/存储全用裸 int64:[algorithm/model.go:4,13](../game/algorithm/model.go)、[algorithm/packet_generator.go:188,242,254](../game/algorithm/packet_generator.go)、[algorithm/reward_controller.go:180](../game/algorithm/reward_controller.go)、[model/session.go:19](../game/model/session.go)、[game_app_service.go](../game/application/game_app_service.go) 13 处、[penalty_service.go:36,109](../game/application/penalty_service.go)、[grab_service.go:204](../game/application/grab_service.go)。`api/platform/utils.go:6,9` 又用 `var ParseAmount = currency.ParseAmount` 起别名,入口不统一。

**为什么是问题**:裸 int64 无法在编译期阻止"元 vs 分"误用、负数金额、溢出;`Money` 价值仅体现在 JSON 序列化,域内计算无保护。

**解决方案**:在域层逐步用 `currency.Money` 替换 int64 金额字段(至少 model/algorithm 层),让加减乘除经过类型方法;去掉 `api/platform/utils.go` 的别名,直接用 `currency.ParseAmount`。

---

### C-12 [低] cmd/stats 启动在 logger 初始化前用 `fmt.Printf` 输出错误

**依据**:[cmd/stats/main.go:31](../cmd/stats/main.go) `fmt.Printf("failed to load config: %v\n", err)`,随后 `logger.Init` 在第 35 行。其它服务启动失败用 panic(game)或 logger.Fatal(gateway),行为不统一。

**解决方案**:统一启动失败用 `log.Fatalf`(标准库,无需初始化)或先 `logger.Init` 默认配置再重载。

---

## 六、跨服务组织与重复设计

### D-01 [严重] gateway 测试代码混入生产目录并被生产 Container 实例化

**依据**:

- [gateway/service/test.go:10-20](../gateway/service/test.go) `TestService` 提供 `GenerateTestToken`(34 行)直接为任意 userID 签发 JWT
- [gateway/bootstrap/container.go:92](../gateway/bootstrap/container.go) `c.TestService = service.NewTestService(...)` — **生产 Container 无条件创建**;第 125 行作为参数传入 `NewServer` 注册路由
- [gateway/store/memory.go:10-15](../gateway/store/memory.go) `MemoryGameStore` 用内存 map 存储,`initSampleData()`(69-85 行)硬编码样本游戏
- [gateway/bootstrap/container.go:85](../gateway/bootstrap/container.go) `c.GameStore = store.NewMemoryGameStore()` — 生产 Container 用内存 store

**为什么是问题**:测试 token 签发接口暴露在生产网关,任何能访问网关的人都能为任意 userID 生成有效 JWT,等同于绕过认证;生产链路使用 mock 级别的内存 store,数据丢失即恢复样本值。即便有 `Config.Server.TestEnabled` 门控,TestService 与 MemoryGameStore 仍被构造,生产二进制带有测试路径。

**解决方案**:将 `test.go` / `memory.go` 移到 `gateway/test/` 子包,用 build tag `//go:build test` 隔离;生产 Container 不应无条件实例化,改由配置 + 编译标签双重门控;GameStore 应使用持久化实现(MySQL/Redis-backed),MemoryGameStore 仅留作单测 stub。

---

### D-02 [严重] 几乎没有单元测试 — 全项目仅 2 个 _test.go

**依据**:Glob `**/*_test.go` 仅命中 [gateway/keys_test.go](../gateway/keys_test.go) 与 [game/algorithm/straight_test.go](../game/algorithm/straight_test.go)。settlement/service(14 文件)、settlement/scheduler(6)、game/application(13)、game/infrastructure、gateway/service、gateway/handler、stats/service 等**核心包零测试**。

**为什么是问题**:涉及资金扣款、退款、补偿、机器人余额的核心逻辑(`CreditRetryService` / `RefundService` / `DeductService` / `SettlementCheckService` / `VirtualBalanceService`)没有任何回归保护。project memory 的 Lessons Learned 第 2 条记录过"非原子 check-rollback 导致资金不一致",却没有任何测试固化这些场景。

**解决方案**:优先为 settlement 4 个补偿服务补 table-driven 测试;引入接口抽象(见 A-07)后再补集成测试;Lua 脚本通过 miniredis 或 testcontainers 跑端到端验证。

---

### D-03 [高] VirtualBalanceService 在 game 与 settlement 双份实现,功能重叠且不一致

**依据**:

- game 版:[virtual_balance.go:14-117](../game/infrastructure/persistence/redis/virtual_balance.go),字段含 `repo *mysql.RobotAccountRepository`(17 行)
- settlement 版:[virtual_balance_service.go:17-69](../settlement/service/virtual_balance_service.go),字段仅 `redis *cRedis.Client`(18 行)
- `Credit` 实现不一致:game 版(29-38 行)用 `converter.FormatID(userID)`;settlement 版(43-52 行)用 `fmt.Sprintf("%d", userID)` — **正是 C-06 约束的反例**
- `GetBalance` 也分歧:game 版有 DB fallback(50-58 行),settlement 版只 warn 后返回 0(58-62 行)
- [game/bootstrap/container.go:142,248](../game/bootstrap/container.go) 同时持有两个实例
- **项目记忆勘误**:project memory 中"game 层 Deduct 是死代码应删除"已过期 — grep 确认 game 层已无 `Deduct` 方法,该条目可清理

**为什么是问题**:同一份"虚拟余额"业务能力两套代码实现,读写同一 Redis key,格式不一致会导致脏数据集合成员无法被对端 `SyncToDB` 正确解析;所有权模糊,两侧并发写同一 key 时易语义冲突。

**解决方案**:收敛到 settlement 包(Lua 在 settlement/scripts),game 层只保留对 settlement 接口的调用;或抽出 `domain/virtual_balance` 共享接口 / `common/robot_balance` 共享包。同时清理 project memory 过期条目。

---

### D-04 [高] game 与 settlement 的 keys.go 重复定义相同的机器人 Redis key

**依据**:

- [game/.../redis/keys.go:69-71](../game/infrastructure/persistence/redis/keys.go):`KeyRobotVirtualBalance` / `KeyRobotVirtualBalanceDirty` / `KeyRobotUserIDs`
- [settlement/.../redis/keys.go:23-25](../settlement/infrastructure/persistence/redis/keys.go):三个常量字符串值**逐字相同**,第 22 行注释自证"与 game 层保持一致的字符串值"
- 两边各自实现同名工厂函数(`RobotVirtualBalanceKey` 等),签名完全一致
- 同理 [gateway/keys.go:8-9,19-20](../gateway/keys.go) 的 `KeyPlayerRoom`/`KeyRoomPlayers`/`KeyRoomSpectators` 与 game 侧重复

**为什么是问题**:project memory"各服务各自维护 keys.go"约束被当成"必须复制粘贴"的理由,但原意是避免循环依赖,并非必须重复。同一字符串两处定义,任一方改格式会导致另一方读写错位,触发资金数据错乱,且**编译无任何告警** — 是最危险的隐性耦合。

**解决方案**:对跨服务共享 key(robot/room 相关),提取 `common/rediskeys` 子包放共享常量,各服务 keys.go 只保留本服务独有 key;或加跨服务 key 一致性测试(扩展 [gateway/keys_test.go](../gateway/keys_test.go) 到跨服务对比)。

---

### D-05 [高] scheduler 两套实现:settlement 用 BaseScheduler,game 没复用且无分布式锁

**依据**:

- [settlement/scheduler/base.go:23-79](../settlement/scheduler/base.go) `BaseScheduler`(含 LockKey/LockTTL + `lock.WithRedisLock`),5 个 settlement scheduler 全部复用
- [game/scheduler/](../game/scheduler) 无 base.go;[timeout_scheduler.go:52-60](../game/scheduler/timeout_scheduler.go) 自己实现 ctx/cancel/wg;[virtual_balance_sync.go:15-21](../game/scheduler/virtual_balance_sync.go) 又一次独立实现 + 自加 `defer recover`(67-72 行)
- 全项目搜 `BaseScheduler` 命中 17 行,**全部在 settlement/scheduler**,game 侧 0 命中

**为什么是问题**:game 的 `VirtualBalanceSyncScheduler` / `TimeoutScheduler` 没用分布式锁 — 多实例部署时多个 game 实例会并发跑 SyncToDB(触发 DB 重复写)与重复触发 timeout。两套调度器抽象使"加新 scheduler"无统一模板。

**解决方案**:把 `BaseScheduler` 提到 `common/scheduler/base.go`,game 与 settlement 共用;改造 game 两个 scheduler 补齐 LockKey;统一 `Stop()` 超时退出语义。

---

### D-06 [高] 三个服务的启动/组装方式完全不一致

**依据**:

- game:[bootstrap/app.go:159-160](../game/bootstrap/app.go) `NewContainer(...)` 参数列表**长达 26 个**,全部 settlement 服务在 app.go 顶层手工 new 完透传;[container.go:165-237](../game/bootstrap/container.go) 再 `InitAppServices()` 二次组装
- gateway:[bootstrap/app.go:73](../gateway/bootstrap/app.go) `NewContainer(cfg, redisClient, kafkaProducer, nodeID)` 只传 4 个,`container.go:64-103` `InitServices(...)` 内部 new
- stats:[cmd/stats/main.go:55-77](../cmd/stats/main.go) 完全无 bootstrap 抽象,直接在 `main()` 内联 repository→service→handler→gin

**为什么是问题**:三套截然不同的组装风格;game 26 参数极易错位传参(Go 无具名实参,13 个 `*settlementService.XxxService` 同类指针排错极难);stats 绕过 bootstrap 导致 nacos 注册/优雅关闭/统一日志等通用能力无法复用。

**解决方案**:抽出 `common/bootstrap` 骨架(Application 接口 + Run 通用入口 + Config/Nacos/Logger/Redis 通用初始化);三服务统一"轻构造 Container + Init*() 分阶段"的 gateway 风格;game 的 26 参数改为 functional-options 或 builder,把 settlement 服务群组聚合成 `SettlementServices` struct 传入。

---

### D-07 [中] settlement 补偿/重试/检查逻辑散弹枪式,缺统一事务编排抽象

**依据**:

- [exception_manager.go:11-17](../settlement/service/exception_manager.go) / [platform_call_manager.go:12-18](../settlement/service/platform_call_manager.go):名为 Manager,实为 db CRUD 包装(`Create`/`GetByID`/`UpdateStatus`),无编排能力,应叫 `*Repository`
- [credit_retry_service.go:35-45](../settlement/service/credit_retry_service.go):自行实现指数退避(`calculateNextRetryTime` 181 行),字段全具体类型,`retryCfg` 构造时写死 `DefaultCreditRetryConfig()`(66 行)
- [refund_service.go:18-27](../settlement/service/refund_service.go):独立实现退款申请-审批-执行,8 个字段全具体类型
- [settlement_check_service.go:113](../settlement/service/settlement_check_service.go):巡检里直接调 `refundSvc.ApplyForRefund`,把"巡检"与"退款执行"耦合
- [settlement_service.go:18-31](../settlement/service/settlement_service.go):`SettlementService` 聚合 11 个依赖,每步失败的处理策略(回滚/标记异常/触发退款)散落各处 if-err,无 Saga/TCC/Outbox 抽象

**为什么是问题**:重试策略散落各 service,无法统一监控"重试总次数/异常总量";Manager 层级错位;5 个补偿服务互相直接依赖,调用图网状,新增补偿类型必须改多个 service。

**解决方案**:抽出 `TransactionOrchestrator` / `SagaCoordinator` 接口(`Begin`/`Step(name,fn)`/`Compensate(name,fn)`/`Commit`);`ExceptionManager`/`PlatformCallManager` 重命名为 `*Repository` 并下沉 infrastructure;重试参数(MaxRetryCount/BaseDelay/Multiplier)外置 config,统一通过 `RetryPolicy` 接口注入。

---

### D-08 [中] stats 服务无 bootstrap,且与 game HistoryService 功能边界模糊

**依据**:[cmd/stats/main.go:55-77](../cmd/stats/main.go) 完全内联,无 nacos 注册、无 bootstrap 抽象。功能上 `stats/repository` 与 [game/application/history_service.go](../game/application/history_service.go)、[settlement/service/bill_manager.go](../settlement/service/bill_manager.go) 都是"查询历史/账单",三者各自直连 MySQL,SQL 可能重复、模型可能不一致。

**解决方案**:给 stats 加 `bootstrap/app.go`(复用 common/bootstrap 骨架);抽出共享只读查询层,game/settlement 写入,stats 读取,共享 SQL 与 DTO。

---

### D-09 [低] gateway 的 keys.go 放置位置不一致

**依据**:[gateway/keys.go](../gateway/keys.go) 在包根目录,而 game/settlement 的 keys.go 在 `infrastructure/persistence/redis/keys.go`。gateway 根目录还混着 4 个 md 文档。

**解决方案**:统一 keys.go 一律放 `<service>/infrastructure/persistence/redis/keys.go`;gateway 的 4 个 md 移到 `docs/`。

---

## 七、重构路线图

> 原则:**先决策后重构,先安全后结构,先抽象后迁移**。每阶段保持可编译可回滚。

### 阶段 0:关键决策与安全基线(P0,先行)

| 决策 | 内容 | 必要性 |
|------|------|--------|
| **D1 settlement 定位** | 独立服务 vs 永久内嵌,二选一(见 B-02) | 决定 A-01/A-02 重构方向 |
| **D2 测试代码下线** | D-01:TestService/MemoryGameStore 用 build tag 隔离,移出生产 Container | 安全基线 |
| **D3 敏感信息迁出** | C-01:merchant_secret/DB/JWT 走环境变量或 Nacos 加密 | 安全基线 |

### 阶段 1:横切关注点统一(P0-P1)

- C-04/C-05:三套配置统一到 common/config,game 专属配置下沉 `game/config`
- C-02/C-03:settlement 引入错误类型;`message.Error` 增加 `Is/Unwrap`,提供 sentinel error
- C-06/C-07:converter 违规与吞错统一修复(配合已有 P0 修复经验)
- C-08/C-09/C-10:清理 utils 死代码、lock 死常量、broadcast channel 重复

### 阶段 2:分层与依赖治理(P1)

- A-03:删除 `domain/lua_errors.go` 与 `MapLuaError`,错误翻译下沉 infrastructure
- A-05:消除 domain/model 常量重复,domain 不再 import model
- A-01:以 `room_app_service.go` 为标杆,把 redis/scripts 调用下沉到 infrastructure(优先 grab/seat 这两个最高频路径)
- B-01:切断 settlement → game/model,引入 settlement 自有 User 类型 + game 适配器

### 阶段 3:settlement 分层补齐与可测试性(P1-P2)

- A-02:settlement 新建 `domain/` 层 + Repository 接口,BillManager GORM 实现下沉
- A-07/D-07:settlement 定义 port 接口,Manager 改名 Repository 下沉,引入 RetryPolicy/TransactionOrchestrator
- D-02:为 settlement 4 个补偿服务补 table-driven 测试(接口抽象后成本大降)

### 阶段 4:重复消除与统一骨架(P2)

- D-03:VirtualBalanceService 收敛(配合 D1 决策)
- D-04:共享 Redis key 提取 `common/rediskeys`
- D-05:`BaseScheduler` 提到 `common/scheduler`,game scheduler 补分布式锁
- D-06:抽出 `common/bootstrap`,三服务统一组装风格
- A-04/A-06:领域模型充血 + GameAppService 拆分(最高风险,最后做,需充分测试保护)

### 阶段 5:边界解耦(可选,依 D1 决策)

- 若 D1 选独立:settlement 建 `cmd/settlement/main.go`,B-03/B-04 的 in-process 契约改为 proto,走 Kafka/gRPC
- 若 D1 选内嵌:`settlement/` 内联 `game/settlement/`,删除独立 config/keys/scripts 副本

### 风险控制

| 风险 | 应对 |
|------|------|
| 阶段 4 领域模型重构改动面大、易引入资金 bug | 必须在 D-02 测试补齐后进行;每步小步提交 + 回归 |
| settlement 定位决策拖延 | 阶段 0 必须先定,否则阶段 2/3 方向不定 |
| 接口抽象后性能担忧 | 接口仅在跨层/跨服务边界引入,热路径内部仍可用具体类型 |

---

## 附录:问题清单汇总表

| 编号 | 严重度 | 主题 | 维度 |
|------|--------|------|------|
| A-01 | 严重 | application 跨层依赖 infrastructure(102 处) | DDD |
| A-02 | 严重 | settlement 缺失 domain 层 | DDD |
| A-03 | 严重 | Lua 错误码泄漏到 domain,职责倒置 | DDD |
| A-04 | 严重 | 领域模型贫血,逻辑堆在 service | DDD |
| A-05 | 高 | domain 与 model 职责重叠,常量重复 | DDD |
| A-06 | 高 | GameAppService 1733 行职责过载 | DDD |
| A-07 | 中 | 依赖具体 struct 而非接口 | DDD |
| A-08 | 低 | 事件 payload 用 interface{} | DDD |
| B-01 | 严重 | settlement 反向 import game/model | 依赖 |
| B-02 | 严重 | settlement 无独立 cmd,定位错乱 | 依赖 |
| B-03 | 高 | Kafka 事件实为 game 自产自销 | 依赖 |
| B-04 | 高 | settlement/dto 被直接依赖,类型摩擦 | 依赖 |
| B-05 | 中 | common 大杂烩,业务语义渗入 | 依赖 |
| B-06 | 正向 | gateway↔game 边界干净(参考) | 依赖 |
| C-01 | 严重 | 配置敏感信息明文+重复 | 横切 |
| C-02 | 严重 | 错误模型分裂(game vs settlement) | 横切 |
| C-03 | 严重 | message.Error 不支持 errors.Is | 横切 |
| C-04 | 严重 | 三套配置加载机制 | 横切 |
| C-05 | 高 | common/config 偏 game | 横切 |
| C-06 | 高 | 绕过 converter 且吞错 | 横切 |
| C-07 | 高 | 关键路径 error 被吞 | 横切 |
| C-08 | 中 | utils 杂物间+死代码 | 横切 |
| C-09 | 中 | lock 残留死常量 | 横切 |
| C-10 | 中 | broadcast channel 三处定义 | 横切 |
| C-11 | 中 | Money 退化为 DTO,内部裸 int64 | 横切 |
| C-12 | 低 | stats 启动用 fmt.Printf | 横切 |
| D-01 | 严重 | gateway 测试代码混入生产 | 组织 |
| D-02 | 严重 | 全项目仅 2 个 _test.go | 组织 |
| D-03 | 高 | VirtualBalanceService 双份不一致 | 组织 |
| D-04 | 高 | 跨服务 robot key 重复 | 组织 |
| D-05 | 高 | scheduler 两套,game 无分布式锁 | 组织 |
| D-06 | 高 | 三服务组装方式不一致 | 组织 |
| D-07 | 中 | 补偿逻辑散弹枪,缺事务编排 | 组织 |
| D-08 | 中 | stats 无 bootstrap,边界模糊 | 组织 |
| D-09 | 低 | gateway keys.go 放置不一致 | 组织 |

---

**文档版本**:v1.0
**审查范围**:`packet/backend` 全量(game / settlement / gateway / stats / common)
**定位**:代码架构层面,补充 [ARCHITECTURE_REFACTOR_PLAN.md](../ARCHITECTURE_REFACTOR_PLAN.md)(基础设施层面)
