# 雪花 ID（Snowflake ID）重构方案

> 版本：v2.1（采用成熟框架 bwmarrin/snowflake + Redis 自动分配 nodeID + 纪元 2026-01-01）
> 日期：2026-07-04
> 范围：`backend/` 全量代码（`common/idgen/`、`settlement/service/trace_id_generator.go`、所有调用方）
> 基于对 5 个核心文件 + 12 个调用方文件的完整阅读整理而成

---

## 0. 决策摘要

### 0.1 采用成熟框架 bwmarrin/snowflake

详见 §0.2 框架选型对比。选择理由：位分配兼容、生态成熟（JSON Marshal/Base 编码/ID 反解析）、社区验证（3.8k+ stars）、无外部依赖。封装层补齐时钟回拨检测。

### 0.2 多实例 nodeID 分配：Redis 自动分配（nacos 配置中心场景）

**问题背景**：配置文件统一存放在 nacos，多实例读同一份配置，若 nacos 中 `node_id` 写死，所有实例 nodeID 相同会产生 ID 冲突。

**解决方案**：nacos 配置 `node_id: 0` 表示自动分配，启动时通过 Redis INCR + SET NX 自动分配唯一 nodeID，心跳续约保活，实例下线后 TTL 过期回收。

**三种方案对比**：

| 方案 | 复杂度 | 唯一性保证 | 适用场景 | 采用 |
|---|---|---|---|---|
| A. 环境变量注入 | 低 | 强（人工保证） | k8s StatefulSet 部署 | 备选 |
| **B. Redis 自动分配 + 心跳续约** | **中** | **强（自动）** | **通用，无需编排系统** | **✓** |
| C. nacos metadata 协调 | 高 | 强（自动） | 利用现有 nacos 基础设施 | 不采用（复杂） |

**方案 B 详细流程**：

```
启动 → nacos 读 node_id 配置
  ├─ node_id > 0：使用配置值（兼容单实例/测试环境）
  └─ node_id = 0：触发 Redis 自动分配
       ├─ INCR cashparty:idgen:node_id_seq
       ├─ nodeID = (seq - 1) % 1024
       ├─ SET NX cashparty:idgen:node_id:assigned:<nodeID> <instanceID> EX 3600
       │    ├─ 成功：分配成功，启动心跳续约（每 5min EXPIRE）
       │    └─ 失败：INCR 重试，分配下一个（最多 1024 次）
       └─ 注册到 nacos 时 metadata.node_id = <nodeID>（可观测性）

实例正常下线 → DeregisterService 时 DEL 回收 nodeID
实例异常宕机 → 心跳停止 → TTL 1 小时过期 → nodeID 自动回收
```

**关键设计点**：
1. nacos 配置 `node_id: 0` 表示自动分配（默认值），单实例仍可显式配置（如 `node_id: 1`）
2. Redis INCR + SET NX 组合：INCR 保证递增循环，SET NX 保证独占
3. 心跳续约：每 5 分钟 `EXPIRE` 续约，1 小时 TTL 容忍网络抖动
4. 优雅退出：`DeregisterService` 时 `DEL` 回收 nodeID，避免等待 TTL
5. nacos metadata 标记：注册时带 `node_id`，便于运维排查 nodeID 冲突

### 0.3 自定义纪元改为 2026-01-01 00:00:00 UTC

**决策**：自定义纪元从 `2024-01-01`（旧实现）改为 `2026-01-01 00:00:00 UTC`（`1735689600000`）。

**理由**：
1. 项目 2026 年上线，纪元与上线时间对齐
2. 纪元更近，ID 数值更小（更易读、更短）
3. 理论可用年限从 2026 年起算 69 年（到 2095 年）

**影响评估**：

| 场景 | 影响 | 处理 |
|---|---|---|
| 新增 ID | 时间戳部分从 0 开始，ID 数值更小 | 正面 |
| 已有 ID 解析时间戳 | 用旧纪元解析新 ID → 时间戳错误（提前 2 年） | 项目未上线，无已有数据 |
| 已有 ID 作为唯一标识 | 无影响（ID 本身仍唯一） | 无需处理 |
| 数据库已有数据 | 无影响（ID 不重复生成） | 项目未上线，无生产数据 |

**结论**：项目未上线，无历史数据兼容问题，直接改为 `2026-01-01 00:00:00 UTC`。

### 0.4 框架选型对比

| 维度 | 自研（现状） | bwmarrin/snowflake（推荐） | sony/sonyflake |
|---|---|---|---|
| GitHub Stars | - | 3.8k+ | 2.5k+ |
| 维护状态 | 私有 | 稳定（BSD-2） | 活跃（MIT，2025-07 最新） |
| 位分配 | ts(41)+node(10)+seq(12) | ts(41)+node(10)+step(12) | ts(39)+seq(8)+machine(16) |
| 每毫秒单节点 ID 数 | 4096 | 4096 | 256（10ms 单位） |
| 最大节点数 | 1024 | 1024 | 65536 |
| `Generate` 返回 error | 否（缺陷） | 否 | **是** |
| 时钟回拨处理 | **无（会重复）** | **无（同样缺陷）** | **有（elapsedTime++ + sleep）** |
| JSON Marshal/Unmarshal | 无 | **有** | 无 |
| Base32/Base58/Base64 编码 | 无 | **有** | 无 |
| ID 反解析（时间/节点/序列） | 无 | **有** | **有（Decompose）** |
| 自定义位分配 | 无 | **有** | **有** |
| 自定义纪元 | 硬编码 | **有（Epoch 变量）** | **有（StartTime 字段）** |
| MachineID 自动获取 | 无 | 无 | **有（私有 IP 低 16 位）** |
| 单元测试 | 无 | **有（完善）** | **有（完善）** |
| 依赖 | 无 | 无外部依赖 | 无外部依赖 |

### 0.2 选型理由

**选择 bwmarrin/snowflake 的理由**：
1. **位分配完全兼容**：ts(41)+node(10)+step(12) 与现状一致，已有 ID 与新 ID 完全兼容，无数据迁移
2. **生态成熟**：JSON Marshal、Base32/58/64 编码、ID 反解析等特性，适合业务使用（暴露 ID 给前端、日志分析）
3. **自定义纪元**：`snowflake.Epoch = 1704067200000` 保持与现状一致
4. **社区验证**：3.8k+ stars，BSD-2 协议，稳定可靠
5. **无外部依赖**：仅依赖标准库

**不选 sonyflake 的理由**：
- 每毫秒仅 256 个 ID（10ms 单位 × 256 seq），单节点吞吐量低 16 倍
- 16 位 machineID 对当前规模过度设计
- 无 JSON Marshal，业务层需自己封装

**不选自研的理由**：
- 重复造轮子，缺少 JSON/Base 编码、ID 反解析等实用功能
- 时钟回拨缺陷需自己修复
- 无社区验证

### 0.3 bwmarrin/snowflake 的时钟回拨缺陷及修复

**重要发现**：bwmarrin/snowflake 的 `Generate()` 同样不处理时钟回拨（回拨时 `step = 0` 会生成重复 ID）。

**修复策略**：在我们的封装层 `SnowflakeGenerator` 中补齐时钟回拨检测：
- 检测 `now < lastTimestamp` 时返回 `ErrClockMovedBackwards`
- 小幅回拨（≤5ms）等待追上
- 大幅回拨（>5ms）拒绝生成

这样既享受成熟框架的生态，又修复其缺陷。

---

## 1. 现状分析

### 1.1 实现概览

| 维度 | 现状 |
|---|---|
| 实现方式 | **完全自研**（go.mod 无第三方雪花库） |
| 位分配 | timestamp(41) + nodeID(10) + sequence(12) = 63 位 + 1 符号位 |
| 自定义纪元 | `1704067200000`（2024-01-01 00:00:00 UTC） |
| 节点配置 | 仅 `NODE_ID` 环境变量，默认 `1`，无 yaml 配置字段 |
| 线程安全 | `sync.Mutex` 保护 `GenerateInt64` |
| 时钟回拨 | **未处理**（严重缺陷） |
| 单元测试 | **无任何测试** |
| 单例模式 | `sync.Once`（但 `Init`/`InitFromEnv` 共享同一 `once`，语义冲突） |

### 1.2 文件清单

#### 核心实现

| 文件 | 角色 | 行数 |
|---|---|---|
| [common/idgen/snowflake.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go) | 雪花算法核心（自研） | 155 |
| [settlement/service/trace_id_generator.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go) | 封装层（2 方法用雪花，8 方法确定性拼接） | 95 |
| [common/config/types.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/types.go#L136-L138) | `IDGeneratorConfig`（仅 `Enabled` 字段） | 3 |
| [game/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/config/config.go#L20) | Game 配置聚合 | 1 |
| [config/game.yaml](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/config/game.yaml#L127-L128) | YAML 配置（仅 `enabled: true`） | 2 |

#### 调用方（13 处直接调用 + 10 处间接调用）

| 文件 | 调用类型 | 业务用途 |
|---|---|---|
| [game/model/user.go:31](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/model/user.go#L31) | `idgen.GenerateInt64()` | 用户表主键 ID |
| [game/application/game_app_service.go:475](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L475) | `idgen.GenerateString()` | 游戏会话 sessionID |
| [game/application/game_app_service.go:515](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L515) | `idgen.GenerateString()` | SessionStart 事件 TraceID |
| [game/application/game_app_service.go:1046](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L1046) | `idgen.GenerateString()` | RoundSettle 事件 TraceID |
| [game/application/game_app_service.go:1220](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L1220) | `idgen.GenerateString()` | SessionEnd 事件 TraceID |
| [game/application/game_app_service.go:1504](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L1504) | `idgen.GenerateString()` | PacketCreated 事件 TraceID |
| [game/application/game_app_service.go:1542](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/application/game_app_service.go#L1542) | `idgen.GenerateInt64()` | 游戏回合记录 ID |
| [scripts/init_rooms.go:143](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/scripts/init_rooms.go#L143) | `idgen.GenerateInt64()` | 初始化房间 ID |
| [scripts/init_robot_accounts.go:36](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/scripts/init_robot_accounts.go#L36) | `idgen.InitFromEnv()` | 脚本初始化 |
| [game/bootstrap/app.go:78](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go#L78) | `idgen.InitFromEnv()` | game 服务启动初始化 |
| [game/bootstrap/app.go:108](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go#L108) | `idgen.GetDefaultGenerator()` | 注入 TraceIDGenerator |
| [game/bootstrap/app.go:163](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go#L163) | `idgen.GetNodeIDString()` | Kafka 消费者 group ID 后缀 |
| [gateway/bootstrap/app.go:70](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go#L70) | `idgen.GetNodeIDString()` | gateway 节点 ID |
| [settlement/service/deduct_service.go:102](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/deduct_service.go#L102) | `GenerateBatchID()` | 扣款批次 ID（`BATCH_<雪花>`） |
| [settlement/service/trace_id_generator.go:40](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go#L40) | `GenerateReconcileNo()` | 对账单号（`REC_<时间戳>_<雪花%10000>`） |

### 1.3 ID 类型分工

| ID 类型 | 生成方式 | 用途 | 示例 |
|---|---|---|---|
| **雪花 ID** | `idgen.GenerateInt64()` / `GenerateString()` | 业务实体唯一标识 | 用户 ID、房间 ID、回合 ID、sessionID |
| **确定性字符串** | `TraceIDGenerator.GenerateXxx()` | 业务订单号（幂等键） | `RT_<sessionID>_<roundNo>`、`BATCH_<雪花>` |
| **UUID** | `google/uuid` | 网关请求 ID、分布式锁 token | `req_<毫秒>_<uuid[:12]>` |

---

## 2. 问题清单

### 2.1 严重问题（P0，必须修复）

#### P0-1：时钟回拨未处理，会产生重复 ID

**文件**：[common/idgen/snowflake.go:71-80](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go#L71-L80)

**问题**：当 `now < g.timestamp`（NTP 校时、VM 迁移、容器时钟漂移）时，进入 `else` 分支，`sequence = 0`，使用回拨后的较小时间戳生成 ID。该 ID 与之前生成的 ID **完全相同**。

**影响**：`User.ID` 主键冲突、`Round.RoundID` 主键冲突、`sessionID` 重复。

**修复方案**：采用 bwmarrin/snowflake + 封装层补齐时钟回拨检测（详见 §3）。

#### P0-2：nodeID 仅靠环境变量，多实例默认冲突

**文件**：[common/idgen/snowflake.go:38-51](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go#L38-L51) + 所有 yaml 配置

**问题**：nodeID 完全依赖环境变量 `NODE_ID`，默认值为 `1`。多实例部署时若忘记设置，所有实例使用相同 nodeID=1，产生 ID 冲突。

**修复方案**：`IDGeneratorConfig` 新增 `NodeID` 字段，yaml 配置新增 `node_id`，启动时强制校验。

#### P0-3：无任何单元测试

**文件**：`common/idgen/` 目录下无 `_test.go` 文件

**修复方案**：新增 `common/idgen/snowflake_test.go`，覆盖并发、时钟回拨、序列号溢出、边界值。

### 2.2 中等问题（P1，应当修复）

#### P1-1：`GenerateReconcileNo` 随机性不足

**文件**：[settlement/service/trace_id_generator.go:40](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/settlement/service/trace_id_generator.go#L40)

**问题**：雪花 ID 低 12 位是 sequence（0-4095），`% 10000` 等同于 sequence 本身，碰撞概率高。

**修复方案**：改用 `crypto/rand` 生成 4 位随机数。

#### P1-2：`int64ToString` 重复造轮子

**文件**：[common/idgen/snowflake.go:92-118](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go#L92-L118)

**问题**：手写 27 行 int64 转 string，应直接使用 `strconv.FormatInt`。

**修复方案**：采用 bwmarrin/snowflake 后删除此函数，使用库提供的 `ID.String()`。

#### P1-3：`Init`/`InitFromEnv` 共享 `sync.Once` 语义冲突

**文件**：[common/idgen/snowflake.go:123-134](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go#L123-L134)

**问题**：`Init(nodeID)` 和 `InitFromEnv()` 共享同一个 `once`，第二次调用被静默忽略。

**修复方案**：删除 `InitFromEnv`，`Init` 在 `bootstrap` 层显式调用，传入完整配置。

#### P1-4：`GetDefaultGenerator` 潜在竞态

**文件**：[common/idgen/snowflake.go:136-141](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/idgen/snowflake.go#L136-L141)

**问题**：`if defaultGenerator == nil` 不是原子的，存在数据竞争。

**修复方案**：启动时强制初始化，`GetGenerator` 返回 error 或 panic if nil（fail-fast）。

### 2.3 设计层面问题（P2，建议优化）

#### P2-1：无 datacenter_id 概念

**评估**：当前规模下单数据中心 1024 节点足够。bwmarrin/snowflake 支持自定义位分配，未来可扩展。本次**不修改位分配**。

#### P2-2：游戏事件 TraceID 使用雪花字符串而非确定性

**评估**：这些 TraceID 用于事件追踪而非幂等键。**保持现状**，在 CODING_STANDARD.md 中明确"事件 TraceID 允许非确定性，幂等键 MUST 确定性"。

#### P2-3：`TraceIDGenerator` 依赖 `*SnowflakeGenerator` 具体类型

**修复方案**：定义 `IDGenerator` 接口，`TraceIDGenerator` 依赖接口，便于 mock 测试。

---

## 3. 重构方案

### 3.1 整体设计思路

#### 3.1.1 核心原则

1. **采用成熟框架**：使用 bwmarrin/snowflake，不自研
2. **封装层补齐缺陷**：在封装层 `SnowflakeGenerator` 中补齐时钟回拨检测
3. **fail-fast**：启动时校验配置，运行时拒绝生成错误 ID
4. **配置驱动**：nodeID 从 yaml 配置读取，环境变量作为 fallback
5. **接口抽象**：定义 `IDGenerator` 接口，业务代码依赖接口非具体类型
6. **显式初始化**：`bootstrap` 层显式调用 `Init`，禁止运行时懒加载
7. **测试覆盖**：核心算法 100% 测试覆盖，含并发、时钟回拨、边界值

#### 3.1.2 架构分层

```
┌─────────────────────────────────────────────────────────────┐
│                    业务调用方                                │
│  game/application  settlement/service  scripts              │
│  依赖 IDGenerator 接口 / TraceIDGenerator                   │
└──────────────────────────┬──────────────────────────────────┘
                           │ 依赖接口
┌──────────────────────────▼──────────────────────────────────┐
│              TraceIDGenerator（封装层）                       │
│  settlement/service/trace_id_generator.go                   │
│  依赖 IDGenerator 接口                                      │
│  生成确定性业务订单号 + 部分非确定性 ID（BatchID）            │
└──────────────────────────┬──────────────────────────────────┘
                           │ 依赖接口
┌──────────────────────────▼──────────────────────────────────┐
│              IDGenerator 接口（抽象层）                       │
│  common/idgen/generator.go                                  │
│  GenerateInt64() (int64, error)                             │
│  GenerateString() (string, error)                           │
│  GenerateID() (snowflake.ID, error)  ← 暴露 bwmarrin 类型   │
└──────────────────────────┬──────────────────────────────────┘
                           │ 实现
┌──────────────────────────▼──────────────────────────────────┐
│           SnowflakeGenerator（封装层，补齐缺陷）              │
│  common/idgen/snowflake.go                                  │
│  - 内部持有 bwmarrin/snowflake.Node                         │
│  - 时钟回拨检测（返回 ErrClockMovedBackwards）              │
│  - 序列号溢出由库处理（阻塞等待下一毫秒）                     │
│  - nodeID 从配置注入                                         │
│  - sync.Mutex 保护并发（包裹库的 Generate）                  │
└──────────────────────────┬──────────────────────────────────┘
                           │ 依赖
┌──────────────────────────▼──────────────────────────────────┐
│           bwmarrin/snowflake（第三方库）                      │
│  github.com/bwmarrin/snowflake                              │
│  - Twitter snowflake 算法实现                               │
│  - ID 类型（int64 别名）+ JSON Marshal + Base 编码           │
│  - ID 反解析（Time/Node/Step）                               │
│  - 自定义 Epoch、NodeBits、StepBits                         │
└─────────────────────────────────────────────────────────────┘
```

#### 3.1.3 关键设计决策

| 决策点 | 方案 | 理由 |
|---|---|---|
| 框架选型 | bwmarrin/snowflake | 位分配兼容、生态成熟、社区验证 |
| 时钟回拨处理 | 封装层补齐（返回 error） | 库本身不处理，封装层 fail-fast |
| 序列号溢出 | 库内置处理（阻塞等待） | bwmarrin 已实现 |
| `GenerateInt64` 返回值 | `(int64, error)` | 时钟回拨时返回 error |
| nodeID 配置来源 | yaml 配置优先，环境变量 fallback | 配置文件更易管理 |
| 接口抽象 | 定义 `IDGenerator` 接口 | 解耦，便于 mock 测试 |
| 初始化方式 | `bootstrap` 显式 `Init(cfg)` | 消除懒加载竞态 |
| 位分配 | timestamp(41) + nodeID(10) + sequence(12) | 保持现状，兼容已有数据 |
| 自定义纪元 | `snowflake.Epoch = 1704067200000` | 保持现状 |

### 3.2 文件结构

#### 3.2.1 重构后目录结构

```
backend/
├── common/
│   ├── idgen/
│   │   ├── generator.go          # 【新增】IDGenerator 接口定义 + 错误常量
│   │   ├── snowflake.go          # 【修改】封装 bwmarrin/snowflake + 时钟回拨检测
│   │   ├── snowflake_test.go     # 【新增】单元测试（并发、时钟回拨、边界值）
│   │   └── registry.go           # 【新增】全局实例注册（Init + GetGenerator）
│   ├── config/
│   │   └── types.go              # 【修改】IDGeneratorConfig 新增 NodeID 字段
│   └── ...
├── settlement/
│   └── service/
│       ├── trace_id_generator.go # 【修改】依赖 IDGenerator 接口 + 修复 GenerateReconcileNo
│       └── trace_id_generator_test.go  # 【新增】单元测试
├── game/
│   ├── bootstrap/
│   │   └── app.go                # 【修改】显式 idgen.Init(cfg.IDGenerator)
│   ├── application/
│   │   └── game_app_service.go   # 【修改】6 处调用方处理 error 返回值
│   └── config/
│       ├── config.go             # 【无变更】IDGenerator 字段已存在
│       └── defaults.go           # 【修改】setDefaults 校验 NodeID 范围
├── gateway/
│   └── bootstrap/
│       └── app.go                # 【修改】显式 idgen.Init(cfg.IDGenerator)
├── config/
│   ├── game.yaml                 # 【修改】新增 node_id 字段
│   └── gateway.yaml              # 【修改】新增 id_generator + node_id 字段
├── go.mod                        # 【修改】新增 bwmarrin/snowflake 依赖
└── CODING_STANDARD.md            # 【修改】新增 §20 雪花 ID 规约
```

#### 3.2.2 文件改动清单

| 文件 | 操作 | 改动说明 |
|---|---|---|
| `go.mod` | **修改** | 新增 `github.com/bwmarrin/snowflake` 依赖 |
| `common/idgen/generator.go` | **新增** | `IDGenerator` 接口 + 错误常量 |
| `common/idgen/snowflake.go` | **重写** | 封装 bwmarrin/snowflake + 时钟回拨检测 |
| `common/idgen/snowflake_test.go` | **新增** | 单元测试（6 个测试函数） |
| `common/idgen/registry.go` | **新增** | 全局实例注册 |
| `common/config/types.go` | **修改** | `IDGeneratorConfig` 新增 `NodeID` 字段 |
| `settlement/service/trace_id_generator.go` | **修改** | 依赖接口 + 修复 `GenerateReconcileNo` + 方法返回 error |
| `settlement/service/trace_id_generator_test.go` | **新增** | 单元测试 |
| `game/bootstrap/app.go` | **修改** | 显式 `idgen.Init(cfg.IDGenerator)` |
| `gateway/bootstrap/app.go` | **修改** | 显式 `idgen.Init(cfg.IDGenerator)` |
| `game/application/game_app_service.go` | **修改** | 6 处调用方处理 error |
| `game/config/defaults.go` | **修改** | 校验 NodeID 范围 |
| `config/game.yaml` | **修改** | 新增 `node_id: 1` |
| `config/gateway.yaml` | **修改** | 新增 `id_generator` 配置段 |
| `CODING_STANDARD.md` | **修改** | 新增 §20 雪花 ID 规约 |

---

## 4. 详细设计

### 4.1 `common/idgen/generator.go`（新增）

```go
package idgen

import (
	"errors"

	"github.com/bwmarrin/snowflake"
)

// IDGenerator 雪花 ID 生成器接口。
// 业务代码依赖此接口，不依赖具体 SnowflakeGenerator 类型，便于 mock 测试。
type IDGenerator interface {
	// GenerateInt64 生成 int64 类型的雪花 ID。
	// 当时钟回拨时返回 ErrClockMovedBackwards，调用方必须处理该错误。
	GenerateInt64() (int64, error)

	// GenerateString 生成字符串类型的雪花 ID（十进制表示）。
	// 当时钟回拨时返回 ErrClockMovedBackwards。
	GenerateString() (string, error)

	// GenerateID 生成 bwmarrin/snowflake.ID 类型，支持 JSON Marshal/Base 编码等。
	// 当时钟回拨时返回 ErrClockMovedBackwards。
	GenerateID() (snowflake.ID, error)

	// GetNodeID 返回生成器的 nodeID。
	GetNodeID() int64
}

// 雪花 ID 生成相关错误。
var (
	// ErrClockMovedBackwards 时钟回拨，拒绝生成 ID 以避免重复。
	// 调用方应记录告警日志并 retry，或返回错误给上游。
	ErrClockMovedBackwards = errors.New("clock moved backwards, refusing to generate id")

	// ErrNodeIDInvalid nodeID 超出合法范围 [0, 1023]。
	ErrNodeIDInvalid = errors.New("node_id must be in range [0, 1023]")

	// ErrGeneratorNotInitialized 生成器未初始化，必须先调用 Init。
	ErrGeneratorNotInitialized = errors.New("id generator not initialized, call Init first")
)
```

### 4.2 `common/idgen/snowflake.go`（重写）

```go
package idgen

import (
	"strconv"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
)

// 自定义纪元：2026-01-01 00:00:00 UTC
const (
	customEpoch        = int64(1735689600000) // 2026-01-01 00:00:00 UTC
	maxClockBackwardMs = int64(5)             // 时钟回拨最大容忍毫秒数
	nodeIDMax          = int64(1023)          // nodeID 最大值（10 位）
)

func init() {
	// 设置自定义纪元（必须在 NewNode 前设置）
	snowflake.Epoch = customEpoch
	// NodeBits=10, StepBits=12 与默认值一致，无需修改
}

// SnowflakeGenerator 雪花 ID 生成器，封装 bwmarrin/snowflake。
// 位分配：timestamp(41) + nodeID(10) + sequence(12) = 63 位 + 1 符号位。
// 在 bwmarrin/snowflake 基础上补齐时钟回拨检测。
type SnowflakeGenerator struct {
	mu            sync.Mutex
	node          *snowflake.Node
	nodeID        int64
	lastTimestamp int64 // 上次生成 ID 的毫秒时间戳，用于时钟回拨检测
}

// NewSnowflakeGenerator 创建雪花 ID 生成器。
// nodeID 必须在 [0, 1023] 范围内，否则返回 ErrNodeIDInvalid。
func NewSnowflakeGenerator(nodeID int64) (*SnowflakeGenerator, error) {
	if nodeID < 0 || nodeID > nodeIDMax {
		return nil, ErrNodeIDInvalid
	}
	node, err := snowflake.NewNode(nodeID)
	if err != nil {
		return nil, err
	}
	return &SnowflakeGenerator{
		node:   node,
		nodeID: nodeID,
	}, nil
}

// GenerateInt64 生成 int64 类型的雪花 ID。
// 当时钟回拨超过 maxClockBackwardMs 时返回 ErrClockMovedBackwards。
// 当同毫秒序列号溢出时由 bwmarrin/snowflake 阻塞等待到下一毫秒。
func (g *SnowflakeGenerator) GenerateInt64() (int64, error) {
	id, err := g.GenerateID()
	if err != nil {
		return 0, err
	}
	return int64(id), nil
}

// GenerateString 生成字符串类型的雪花 ID（十进制表示）。
func (g *SnowflakeGenerator) GenerateString() (string, error) {
	id, err := g.GenerateID()
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// GenerateID 生成 bwmarrin/snowflake.ID 类型。
// 支持JSON Marshal、Base32/58/64 编码、ID 反解析等特性。
func (g *SnowflakeGenerator) GenerateID() (snowflake.ID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	now := time.Now().UnixMilli()

	// 时钟回拨检测（bwmarrin/snowflake 不处理，封装层补齐）
	if now < g.lastTimestamp {
		diff := g.lastTimestamp - now
		if diff <= maxClockBackwardMs {
			// 小幅回拨，等待时钟追上
			time.Sleep(time.Duration(diff) * time.Millisecond)
			now = time.Now().UnixMilli()
		} else {
			// 大幅回拨，拒绝生成
			return 0, ErrClockMovedBackwards
		}
	}

	// 调用 bwmarrin/snowflake 生成 ID（库内部处理序列号溢出）
	id := g.node.Generate()

	g.lastTimestamp = now
	return id, nil
}

// GetNodeID 返回生成器的 nodeID。
func (g *SnowflakeGenerator) GetNodeID() int64 {
	return g.nodeID
}
```

**关键变更**：
1. **采用 bwmarrin/snowflake**：内部持有 `*snowflake.Node`，调用 `node.Generate()` 生成 ID
2. **补齐时钟回拨检测**：`GenerateID` 检测 `now < lastTimestamp`，小幅回拨等待，大幅回拨返回 error
3. **`GenerateInt64` 返回 `(int64, error)`**：调用方必须处理 error
4. **新增 `GenerateID` 方法**：返回 `snowflake.ID` 类型，支持 JSON Marshal、Base 编码、ID 反解析
5. **删除 `int64ToString`**：使用库提供的 `ID.String()`
6. **自定义纪元改为 2026-01-01**：`init()` 中设置 `snowflake.Epoch = 1735689600000`
7. **`NewSnowflakeGenerator` 返回 error**：校验 nodeID 范围

### 4.3 `common/idgen/node_allocator.go`（新增，Redis 自动分配 nodeID）

```go
package idgen

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/rediskeys"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/google/uuid"
)

const (
	// nodeID 分配相关常量
	nodeIDAllocRetryMax   = 1024 // 最大重试次数（覆盖所有可能的 nodeID）
	nodeIDAllocTTL         = 3600 // 分配记录 TTL（秒），1 小时
	nodeIDAllocRenewInterval = 5 * time.Minute // 心跳续约间隔
)

// NodeAllocator 通过 Redis 自动分配唯一 nodeID。
// 适用于 nacos 配置中心场景：多实例读同一份配置，node_id=0 触发自动分配。
type NodeAllocator struct {
	redis      *cRedis.Client
	instanceID string // 实例唯一标识（UUID）
	nodeID     int64
	cancel     context.CancelFunc
}

// NewNodeAllocator 创建 nodeID 分配器。
func NewNodeAllocator(redis *cRedis.Client) *NodeAllocator {
	return &NodeAllocator{
		redis:      redis,
		instanceID: uuid.NewString(),
	}
}

// Allocate 分配唯一 nodeID。
// 流程：INCR 获取候选 nodeID → SET NX 抢占 → 失败则重试。
func (a *NodeAllocator) Allocate(ctx context.Context) (int64, error) {
	for i := 0; i < nodeIDAllocRetryMax; i++ {
		// INCR 获取递增序列
		seq, err := a.redis.Incr(ctx, rediskeys.KeyIDGenNodeIDSeq).Result()
		if err != nil {
			return 0, fmt.Errorf("incr node_id_seq: %w", err)
		}

		// 候选 nodeID = (seq - 1) % 1024，确保在 [0, 1023] 范围内循环
		candidateID := (seq - 1) % (nodeIDMax + 1)

		// SET NX 抢占（key 带 TTL，实例宕机后自动回收）
		allocKey := rediskeys.IDGenNodeIDAllocKey(candidateID)
		ok, err := a.redis.SetNX(ctx, allocKey, a.instanceID, nodeIDAllocTTL*time.Second).Result()
		if err != nil {
			return 0, fmt.Errorf("setnx node_id_alloc %d: %w", candidateID, err)
		}
		if ok {
			// 抢占成功
			a.nodeID = candidateID
			logger.Info("node_id allocated", "node_id", candidateID, "instance_id", a.instanceID)
			return candidateID, nil
		}
		// 抢占失败，重试下一个
	}
	return 0, fmt.Errorf("no available node_id after %d retries", nodeIDAllocRetryMax)
}

// StartRenewal 启动心跳续约 goroutine。
// 每 5 分钟续约一次，防止 TTL 过期导致 nodeID 被回收。
func (a *NodeAllocator) StartRenewal(ctx context.Context) {
	renewCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	go func() {
		ticker := time.NewTicker(nodeIDAllocRenewInterval)
		defer ticker.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				allocKey := rediskeys.IDGenNodeIDAllocKey(a.nodeID)
				if err := a.redis.Expire(renewCtx, allocKey, nodeIDAllocTTL*time.Second).Err(); err != nil {
					logger.Warn("failed to renew node_id allocation", "node_id", a.nodeID, "error", err)
				}
			}
		}
	}()
}

// Release 释放 nodeID（优雅退出时调用）。
func (a *NodeAllocator) Release(ctx context.Context) error {
	if a.cancel != nil {
		a.cancel()
	}
	allocKey := rediskeys.IDGenNodeIDAllocKey(a.nodeID)

	// Lua 脚本：仅当 value == instanceID 时才 DEL，防止误删他人锁
	releaseScript := `
		if redis.call('GET', KEYS[1]) == ARGV[1] then
			return redis.call('DEL', KEYS[1])
		else
			return 0
		end
	`
	_, err := a.redis.Eval(ctx, releaseScript, []string{allocKey}, a.instanceID).Result()
	if err != nil {
		return fmt.Errorf("release node_id_alloc %d: %w", a.nodeID, err)
	}
	logger.Info("node_id released", "node_id", a.nodeID, "instance_id", a.instanceID)
	return nil
}

// GetNodeID 返回已分配的 nodeID。
func (a *NodeAllocator) GetNodeID() int64 {
	return a.nodeID
}

// GetInstanceID 返回实例 ID。
func (a *NodeAllocator) GetInstanceID() string {
	return a.instanceID
}
```

### 4.4 `common/rediskeys/keys.go`（修改，新增 nodeID 分配相关 key）

```go
// 雪花 ID 生成器 nodeID 分配相关 key
KeyIDGenNodeIDSeq     = KeyPrefix + ":idgen:node_id_seq"      // INCR 序列号
KeyIDGenNodeIDAllocPrefix = KeyPrefix + ":idgen:node_id:alloc:" // 分配记录前缀

// IDGenNodeIDAllocKey 生成 nodeID 分配记录 key。
// 用途：SET NX 抢占 nodeID，TTL 1 小时。
func IDGenNodeIDAllocKey(nodeID int64) string {
	return fmt.Sprintf("%s%d", KeyIDGenNodeIDAllocPrefix, nodeID)
}
```

### 4.5 `common/idgen/registry.go`（新增，全局实例注册）

```go
package idgen

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
)

// 全局实例注册，替代旧的 Init/InitFromEnv/GetDefaultGenerator。
var (
	globalGenerator IDGenerator
	globalNodeID    int64
	globalAllocator *NodeAllocator
	initOnce        sync.Once
)

// Init 初始化全局 ID 生成器（显式指定 nodeID，适用于单实例/测试环境）。
// 必须在 bootstrap 层启动时调用，禁止在业务代码中调用。
// nodeID 必须在 [0, 1023] 范围内。
func Init(nodeID int64) error {
	var initErr error
	initOnce.Do(func() {
		gen, err := NewSnowflakeGenerator(nodeID)
		if err != nil {
			initErr = err
			return
		}
		globalGenerator = gen
		globalNodeID = nodeID
		logger.Info("id generator initialized with explicit node_id", "node_id", nodeID)
	})
	return initErr
}

// InitWithAutoAlloc 初始化全局 ID 生成器（Redis 自动分配 nodeID，适用于多实例生产环境）。
// 流程：Redis 自动分配 nodeID → 创建 SnowflakeGenerator → 启动心跳续约。
// 返回 NodeAllocator 供调用方在退出时调用 Release。
func InitWithAutoAlloc(ctx context.Context, redis *cRedis.Client) (*NodeAllocator, error) {
	var initErr error
	var allocator *NodeAllocator
	initOnce.Do(func() {
		allocator = NewNodeAllocator(redis)
		nodeID, err := allocator.Allocate(ctx)
		if err != nil {
			initErr = fmt.Errorf("allocate node_id: %w", err)
			return
		}
		gen, err := NewSnowflakeGenerator(nodeID)
		if err != nil {
			initErr = err
			return
		}
		globalGenerator = gen
		globalNodeID = nodeID
		globalAllocator = allocator
		allocator.StartRenewal(ctx)
		logger.Info("id generator initialized with auto-allocated node_id", "node_id", nodeID)
	})
	return allocator, initErr
}

// GetGenerator 获取全局 ID 生成器。
// 未初始化时返回 ErrGeneratorNotInitialized（fail-fast，禁止懒加载）。
func GetGenerator() (IDGenerator, error) {
	if globalGenerator == nil {
		return nil, ErrGeneratorNotInitialized
	}
	return globalGenerator, nil
}

// GetNodeID 获取全局 nodeID。
func GetNodeID() (int64, error) {
	if globalGenerator == nil {
		return 0, ErrGeneratorNotInitialized
	}
	return globalNodeID, nil
}

// GetNodeIDString 获取全局 nodeID 的字符串表示。
// 未初始化时返回 "0"（向后兼容 gateway 等调用方）。
func GetNodeIDString() string {
	if globalGenerator == nil {
		return "0"
	}
	return strconv.FormatInt(globalNodeID, 10)
}

// Shutdown 优雅关闭，释放自动分配的 nodeID。
// 必须在 bootstrap 层退出时调用。
func Shutdown(ctx context.Context) error {
	if globalAllocator != nil {
		return globalAllocator.Release(ctx)
	}
	return nil
}
```

### 4.6 `common/config/types.go`（修改）

```go
// IDGeneratorConfig 雪花 ID 生成器配置。
type IDGeneratorConfig struct {
	Enabled bool  `mapstructure:"enabled" yaml:"enabled"`
	NodeID  int64 `mapstructure:"node_id" yaml:"node_id"` // 节点 ID，[0, 1023]，0 表示 Redis 自动分配（多实例推荐）
}
```

### 4.5 `settlement/service/trace_id_generator.go`（修改）

```go
package service

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/cashparty/backend/common/idgen"
)

type TraceIDGenerator struct {
	idGen idgen.IDGenerator // 依赖接口，非具体类型
}

func NewTraceIDGenerator(idGen idgen.IDGenerator) *TraceIDGenerator {
	return &TraceIDGenerator{idGen: idGen}
}

// GenerateBatchID 生成扣款批次 ID。
// 非确定性，使用雪花 ID。
func (g *TraceIDGenerator) GenerateBatchID() (string, error) {
	id, err := g.idGen.GenerateInt64()
	if err != nil {
		return "", fmt.Errorf("generate batch id: %w", err)
	}
	return fmt.Sprintf("BATCH_%d", id), nil
}

// GenerateReconcileNo 生成对账单号。
// 使用 crypto/rand 生成 4 位随机数，避免雪花 ID 低位 sequence 碰撞。
func (g *TraceIDGenerator) GenerateReconcileNo() (string, error) {
	timestamp := time.Now().Format("20060102150405")
	random, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", fmt.Errorf("generate reconcile no: %w", err)
	}
	return fmt.Sprintf("REC_%s_%04d", timestamp, random.Int64()), nil
}

// 确定性方法保持不变（GenerateRoundTraceID、GenerateBizOrderNo 等）
// ... 其他方法无变更 ...
```

**关键变更**：
1. `idGen` 字段类型从 `*idgen.SnowflakeGenerator` 改为 `idgen.IDGenerator` 接口
2. `GenerateBatchID` 返回 `(string, error)`，处理雪花 ID 生成错误
3. `GenerateReconcileNo` 改用 `crypto/rand` 生成随机数，修复 P1-1 问题

### 4.6 `game/bootstrap/app.go`（修改）

```go
// 旧代码：
// if cfg.IDGenerator.Enabled {
//     idgen.InitFromEnv()
// }

// 新代码：
if cfg.IDGenerator.Enabled {
	if err := idgen.Init(cfg.IDGenerator.NodeID); err != nil {
		return nil, fmt.Errorf("failed to init id generator: %w", err)
	}
	logger.Info("id generator initialized", "node_id", cfg.IDGenerator.NodeID)
}
```

### 4.7 `game/application/game_app_service.go`（修改）

推荐在构造函数注入 `IDGenerator` 接口：

```go
type GameAppService struct {
	// ... 其他字段
	idGen idgen.IDGenerator
}

func NewGameAppService(/* ... */, idGen idgen.IDGenerator) *GameAppService {
	return &GameAppService{
		// ...
		idGen: idGen,
	}
}

// 调用示例：
sessionID, err := s.idGen.GenerateString()
if err != nil {
    return fmt.Errorf("generate session id: %w", err)
}
```

### 4.8 `config/game.yaml`（修改）

```yaml
id_generator:
  enabled: true
  node_id: 1  # 节点 ID，[0, 1023]，多实例部署必须唯一
```

### 4.9 `go.mod`（修改）

```go
require (
	// ... 其他依赖
	github.com/bwmarrin/snowflake v0.3.0
)
```

---

## 5. 规约（CODING_STANDARD.md §20 新增）

### §20 雪花 ID 规约

#### 20.1 通用规约

| 编号 | 规约 | 级别 |
|---|---|---|
| SID-1 | 所有业务实体唯一标识（用户 ID、房间 ID、回合 ID、sessionID）MUST 使用雪花 ID，禁止使用 UUID 或数据库自增 | MUST |
| SID-2 | 雪花 ID 生成 MUST 通过 `idgen.IDGenerator` 接口调用，禁止直接实例化 `SnowflakeGenerator` 或直接使用 `bwmarrin/snowflake` | MUST |
| SID-3 | `GenerateInt64()` / `GenerateString()` / `GenerateID()` 返回 error，调用方 MUST 检查 error，禁止忽略 | MUST |
| SID-4 | 时钟回拨时返回 `ErrClockMovedBackwards`，调用方 MUST 记录 Warn 日志并 retry 或返回错误 | MUST |
| SID-5 | nodeID MUST 从 yaml 配置读取，禁止仅依赖环境变量 | MUST |
| SID-6 | nodeID MUST 在 [0, 1023] 范围内，多实例部署 MUST 唯一 | MUST |
| SID-7 | `idgen.Init` MUST 在 `bootstrap` 层启动时显式调用，禁止在业务代码中懒加载 | MUST |
| SID-8 | 业务订单号（BizOrderNo、RefundOrderNo、ExceptionNo 等幂等键）MUST 确定性生成，禁止使用雪花 ID | MUST |
| SID-9 | 事件 TraceID 允许使用雪花 ID（非确定性），但 MUST 与幂等键区分 | SHOULD |
| SID-10 | `TraceIDGenerator` MUST 依赖 `IDGenerator` 接口，禁止依赖具体类型 | MUST |
| SID-11 | 雪花 ID 生成器 MUST 有单元测试，覆盖并发、时钟回拨、序列号溢出、边界值 | MUST |
| SID-12 | 需要随机数的场景（如 `GenerateReconcileNo`）MUST 使用 `crypto/rand`，禁止用雪花 ID 取模 | MUST |
| SID-13 | 雪花 ID 框架 MUST 使用 `bwmarrin/snowflake`，禁止自研 | MUST |
| SID-14 | 自定义纪元 MUST 设置为 `1704067200000`（2024-01-01 00:00:00 UTC），禁止使用默认 Twitter 纪元 | MUST |

#### 20.2 调用方规约

| 编号 | 规约 | 级别 |
|---|---|---|
| SID-C1 | `GameAppService`、`GrabService` 等业务服务 SHOULD 在构造函数注入 `IDGenerator` 接口 | SHOULD |
| SID-C2 | 禁止在业务代码中调用 `idgen.GetGenerator()`（应在构造函数注入） | MUST |
| SID-C3 | `scripts/` 下的脚本工具可直接调用 `idgen.GetGenerator()`，但 MUST 检查 error | MUST |
| SID-C4 | `TraceIDGenerator` 的非确定性方法（`GenerateBatchID`、`GenerateReconcileNo`）返回 `(string, error)`，调用方 MUST 检查 error | MUST |
| SID-C5 | 需要暴露 ID 给前端或日志分析时，SHOULD 使用 `GenerateID()` 返回 `snowflake.ID` 类型，支持 JSON Marshal 和 Base 编码 | SHOULD |

#### 20.3 配置规约

| 编号 | 规约 | 级别 |
|---|---|---|
| SID-CFG1 | `IDGeneratorConfig.Enabled=true` 时，`NodeID` MUST 设置且在 [0, 1023] 范围内 | MUST |
| SID-CFG2 | `config/defaults.go` MUST 校验 `NodeID` 范围，超出时 panic（fail-fast） | MUST |
| SID-CFG3 | 多实例部署时，每个实例的 `node_id` MUST 唯一，禁止使用默认值 `1` | MUST |
| SID-CFG4 | 环境变量 `NODE_ID` 仅作为 fallback，配置文件优先 | SHOULD |

#### 20.4 测试规约

| 编号 | 规约 | 级别 |
|---|---|---|
| SID-T1 | `common/idgen/snowflake_test.go` MUST 覆盖：单线程唯一性、多线程并发唯一性（1000 goroutine × 10000 ID） | MUST |
| SID-T2 | MUST 覆盖时钟回拨场景：小幅回拨（≤5ms）等待追上、大幅回拨（>5ms）返回 `ErrClockMovedBackwards` | MUST |
| SID-T3 | MUST 覆盖序列号溢出：同毫秒生成 > 4096 个 ID 时阻塞等待下一毫秒 | MUST |
| SID-T4 | MUST 覆盖 nodeID 边界值：0、1023（合法）、-1、1024（非法，返回 error） | MUST |
| SID-T5 | MUST 验证时间戳单调递增（无回拨时） | MUST |
| SID-T6 | `TraceIDGenerator` 测试 MUST 覆盖确定性方法的幂等性（相同输入相同输出） | MUST |
| SID-T7 | MUST 验证 `snowflake.ID` 的 JSON Marshal/Unmarshal 正确性 | SHOULD |

---

## 6. 重构任务分解

### Phase 1：核心实现改造（P0）

| 任务 | 文件 | 说明 |
|---|---|---|
| 1.1 新增依赖 | `go.mod` | `go get github.com/bwmarrin/snowflake` |
| 1.2 新增 `generator.go` | `common/idgen/generator.go` | `IDGenerator` 接口 + 错误常量 |
| 1.3 重写 `snowflake.go` | `common/idgen/snowflake.go` | 封装 bwmarrin/snowflake + 时钟回拨检测 |
| 1.4 新增 `registry.go` | `common/idgen/registry.go` | 全局实例注册 |
| 1.5 新增 `snowflake_test.go` | `common/idgen/snowflake_test.go` | 6 个测试函数 |
| 1.6 修改 `config/types.go` | `common/config/types.go` | `IDGeneratorConfig` 新增 `NodeID` 字段 |

### Phase 2：封装层 + 调用方改造（P0）

| 任务 | 文件 | 说明 |
|---|---|---|
| 2.1 修改 `trace_id_generator.go` | `settlement/service/trace_id_generator.go` | 依赖接口 + 修复 `GenerateReconcileNo` + 方法返回 error |
| 2.2 新增 `trace_id_generator_test.go` | `settlement/service/trace_id_generator_test.go` | 单元测试 |
| 2.3 修改 `game/bootstrap/app.go` | `game/bootstrap/app.go` | 显式 `idgen.Init(cfg.IDGenerator)` |
| 2.4 修改 `gateway/bootstrap/app.go` | `gateway/bootstrap/app.go` | 显式 `idgen.Init(cfg.IDGenerator)` |
| 2.5 修改 `game_app_service.go` | `game/application/game_app_service.go` | 6 处调用方处理 error + 注入 `IDGenerator` |
| 2.6 修改 `scripts/init_robot_accounts.go` | `scripts/init_robot_accounts.go` | 适配新 `Init` 签名 |
| 2.7 修改 `settlement/service/deduct_service.go` | `settlement/service/deduct_service.go` | 适配 `GenerateBatchID` 返回 error |

### Phase 3：配置 + 文档（P1）

| 任务 | 文件 | 说明 |
|---|---|---|
| 3.1 修改 `config/game.yaml` | `config/game.yaml` | 新增 `node_id: 1` |
| 3.2 修改 `config/gateway.yaml` | `config/gateway.yaml` | 新增 `id_generator` 配置段 |
| 3.3 修改 `game/config/defaults.go` | `game/config/defaults.go` | 校验 `NodeID` 范围 |
| 3.4 修改 `CODING_STANDARD.md` | `CODING_STANDARD.md` | 新增 §20 雪花 ID 规约 |

### Phase 4：全局验证（P0）

| 任务 | 验证项 |
|---|---|
| 4.1 | `go build ./common/... ./game/... ./settlement/... ./gateway/...` 通过 |
| 4.2 | `go vet` 通过 |
| 4.3 | `gofmt -l` 无输出 |
| 4.4 | `go test ./common/idgen/... ./settlement/service/...` 全部通过 |
| 4.5 | grep `idgen.GenerateInt64()` / `idgen.GenerateString()` 包级函数调用为 0 |
| 4.6 | grep `int64ToString` 为 0 |
| 4.7 | grep `InitFromEnv` 为 0 |
| 4.8 | grep `getNodeIDFromEnv` 为 0 |
| 4.9 | grep `bwmarrin/snowflake` 在 `common/idgen/` 外为 0（仅封装层使用） |
| 4.10 | CODING_STANDARD.md 包含 §20 |

---

## 7. 风险评估

### 7.1 高风险

| 风险 | 影响 | 缓解措施 |
|---|---|---|
| `GenerateInt64` 返回值从 `int64` 改为 `(int64, error)` | 所有调用方需修改 | 13 处调用方逐一修改，编译器强制检查 |
| 删除包级 `GenerateInt64()`/`GenerateString()` 函数 | 所有直接调用方编译失败 | 编译器强制修改，无遗漏 |
| `TraceIDGenerator.GenerateBatchID` 返回 error | `deduct_service.go` 调用方需修改 | 编译器强制检查 |
| 新增 bwmarrin/snowflake 依赖 | go.mod 变更 | 库无外部依赖，BSD-2 协议，稳定 |

### 7.2 中风险

| 风险 | 影响 | 缓解措施 |
|---|---|---|
| nodeID 从默认 1 改为配置驱动 | 已部署实例需补充配置 | yaml 默认值 `1`，向后兼容 |
| 时钟回拨返回 error | 极端场景下业务调用失败 | 调用方 retry 逻辑 + Warn 日志告警 |
| bwmarrin/snowflake 的 `Generate()` 不返回 error | 封装层需补齐检测 | 已在 `GenerateID` 中实现回拨检测 |

### 7.3 低风险

| 风险 | 影响 | 缓解措施 |
|---|---|---|
| 删除 `int64ToString` | 无（内部函数） | 编译器检查 |
| `GenerateReconcileNo` 改用 `crypto/rand` | 对账号格式变化 | 兼容 `REC_<timestamp>_<4位>` 格式 |
| 自定义纪元设置方式变化 | `init()` 函数设置 `snowflake.Epoch` | 纪元值不变，兼容已有 ID |

---

## 8. 兼容性策略

### 8.1 数据兼容

- **已有雪花 ID**：位分配不变（timestamp(41) + nodeID(10) + sequence(12)），已有 ID 与新 ID 完全兼容
- **自定义纪元不变**：`snowflake.Epoch = 1704067200000`，ID 时间戳部分连续
- **已有 nodeID=1 实例**：yaml 默认值 `1`，无需修改配置即可继续运行
- **已有业务订单号**：确定性方法（`GenerateBizOrderNo` 等）签名不变，完全兼容

### 8.2 渐进式迁移

1. **Phase 1**：核心实现改造（`common/idgen/`），保持旧包级函数作为 deprecated wrapper
2. **Phase 2**：调用方逐一迁移到新接口
3. **Phase 3**：删除 deprecated wrapper
4. **Phase 4**：全局验证

**本次方案建议一次性完成**，不保留 deprecated wrapper，避免长期维护两套接口。编译器会强制所有调用方修改，无遗漏风险。

---

## 9. 附录

### 9.1 雪花 ID 位分配图

```
| 1 bit | 41 bits              | 10 bits   | 12 bits |
|-------|----------------------|-----------|---------|
| 0     | timestamp (ms)      | nodeID    | sequence|
| 符号  | 自定义纪元后毫秒数   | 节点 ID   | 序列号  |

- 纪元：2024-01-01 00:00:00 UTC (1704067200000)
- 最大支持节点数：1024
- 每毫秒每节点最大 ID 数：4096
- 理论可用年限：约 69 年（从 2024 年起）
```

### 9.2 bwmarrin/snowflake ID 类型特性

```go
id, _ := gen.GenerateID()

// 基础类型转换
id.Int64()        // int64
id.String()       // "1234567890123456"（十进制）

// 编码
id.Base32()       // z-base-32 编码（更短）
id.Base58()       // Base58 编码（更短）
id.Base64()       // Base64 编码
id.Base2()        // 二进制字符串

// JSON Marshal/Unmarshal
data, _ := id.MarshalJSON()  // "1234567890123456"
var id2 snowflake.ID
id2.UnmarshalJSON(data)

// ID 反解析
id.Time()         // 生成 ID 的时间戳（毫秒）
id.Node()         // 生成 ID 的节点 ID
id.Step()         // 生成 ID 的序列号

// 字节表示
id.Bytes()        // []byte
id.IntBytes()     // [8]byte（大端序）
```

### 9.3 ID 类型使用场景对照表

| 场景 | ID 类型 | 生成方式 | 确定性 | 示例 |
|---|---|---|---|---|
| 用户表主键 | 雪花 int64 | `IDGenerator.GenerateInt64()` | 否 | `1234567890123456` |
| 房间 ID | 雪花 int64 | `IDGenerator.GenerateInt64()` | 否 | `1234567890123457` |
| 回合 ID | 雪花 int64 | `IDGenerator.GenerateInt64()` | 否 | `1234567890123458` |
| sessionID | 雪花 string | `IDGenerator.GenerateString()` | 否 | `"1234567890123459"` |
| 事件 TraceID | 雪花 string | `IDGenerator.GenerateString()` | 否 | `"1234567890123460"` |
| 扣款批次 ID | 雪花 + 前缀 | `TraceIDGenerator.GenerateBatchID()` | 否 | `"BATCH_1234567890123461"` |
| 对账单号 | 时间戳 + 随机 | `TraceIDGenerator.GenerateReconcileNo()` | 否 | `"REC_20260704120000_5678"` |
| RoundTraceID | 确定性拼接 | `TraceIDGenerator.GenerateRoundTraceID()` | 是 | `"RT_123_1"` |
| BizOrderNo | 确定性拼接 | `TraceIDGenerator.GenerateBizOrderNo()` | 是 | `"RT_123_1_1_1001"` |
| RefundOrderNo | 确定性拼接 | `TraceIDGenerator.GenerateRefundOrderNo()` | 是 | `"REFUND_1001"` |
| ExceptionNo | 确定性拼接 | `TraceIDGenerator.GenerateExceptionNo()` | 是 | `"EXC_1001_TIMEOUT"` |
| 网关请求 ID | 时间戳 + UUID | `generateRequestID()` | 否 | `"req_1234567890_abc123"` |
| 分布式锁 token | UUID | `uuid.NewString()` | 否 | `"550e8400-e29b-41d4-a716-446655440000"` |

### 9.4 参考文献

- [bwmarrin/snowflake](https://github.com/bwmarrin/snowflake)（采用框架，BSD-2 协议）
- [sony/sonyflake](https://github.com/sony/sonyflake)（对比方案，MIT 协议）
- [Twitter Snowflake 原始论文](https://blog.twitter.com/2010/announcing-snowflake)
- [CODING_STANDARD.md §9 幂等性](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md)
- [CODING_STANDARD.md §16 字符串拼接](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md)

---

## 10. 变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1.0 | 2026-07-04 | 初版，自研方案 |
| v2.0 | 2026-07-04 | **改用 bwmarrin/snowflake 成熟框架**，封装层补齐时钟回拨检测 |
