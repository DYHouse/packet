# Nacos 实现风格审查报告

> 审查范围：`backend/common/nacos/`、`backend/common/config/`、`backend/game/bootstrap/`、`backend/gateway/bootstrap/`、`backend/gateway/discovery/`、`backend/gateway/config/`
> 审查目的：识别"同一功能不同风格"的问题，对应到 [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) 的相关条款。

---

## 一、总体结论

Nacos 的实现存在 **5 大类风格不一致**，全部违反刚制定的编码规范。问题集中在：

1. **配置结构重复定义 3 处**（最严重）
2. **配置默认值重复定义 2 处**
3. **bootstrap 装配风格两套**（game inline vs gateway 抽函数）
4. **gRPC 连接管理两套实现**（`GRPCConnManager` vs `ServiceDiscovery`）
5. **服务发现订阅两套机制**（`SubscribeService` callback vs `nacosResolver` 轮询）
6. **错误处理与日志风格不统一**

下面逐项展开。

---

## 二、详细问题清单

### 问题 1：NacosConfig 三处重复定义（严重）

**违反条款**：[CODING_STANDARD.md §11.1](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — "每个服务模块的 config 包定义自己的 Config struct，**聚合** common/config.*Config 子结构，不得重新声明同名字段"。

三处 `NacosConfig` 结构体字段重叠但又不完全一致：

| 位置 | 字段数 | 标签 | 字段差异 |
|---|---|---|---|
| [common/nacos/config.go:3-14](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/config.go) `nacos.ClientConfig` | 10 | `mapstructure + yaml` | 无 `Enabled`、无 `AlgorithmDataID/AlgorithmGroup`、无 `RouterDataID/RouterGroup`、无 `RateLimiterDataID/RateLimiterGroup`；`ServicePort uint64` |
| [common/config/config.go:183-197](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/config.go) `config.NacosConfig` | 13 | `mapstructure` | 有 `Enabled`、有 `AlgorithmDataID/AlgorithmGroup`；`ServicePort uint64`；无 `RouterDataID/RouterGroup`、无 `RateLimiterDataID/RateLimiterGroup` |
| [gateway/config/config.go:49-65](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/config.go) `gatewayConfig.NacosConfig` | 15 | `yaml` | 有 `Enabled`、有 `RouterDataID/RouterGroup`、有 `RateLimiterDataID/RateLimiterGroup`；**`ServicePort int`**（与上面两处的 `uint64` 类型不一致！）；无 `AlgorithmDataID/AlgorithmGroup` |

**直接后果**：
- `gateway/bootstrap/app.go:222` 必须做 `ServicePort: uint64(cfg.Nacos.ServicePort)` 类型转换，因为 gateway 用 `int`，而 `nacos.ClientConfig` 用 `uint64` —— 这是一个典型的"重复定义导致的耦合点"。
- game 模块支持 `AlgorithmDataID/AlgorithmGroup`，gateway 不支持；gateway 支持 `RouterDataID/RateLimiterDataID`，game 不支持。**同一套 nacos 客户端在不同服务下能力不对等**。
- 三套 tag 风格：`mapstructure+yaml`、纯 `mapstructure`、纯 `yaml` —— 反映出 common/config 用 viper，gateway/config 用 yaml.v3 两套配置加载库。

**修复方向**：保留 [common/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/config.go) 一处 `NacosConfig` 定义，把所有"可选 DataID/Group"字段都放进去（algorithm/router/rateLimiter 等）。`nacos.ClientConfig` 改为内嵌或直接被 `common/config.NacosConfig` 替换。`gateway/config` 复用 `common/config.NacosConfig`，删除本地定义。

---

### 问题 2：Nacos 默认值两处重复

**违反条款**：[CODING_STANDARD.md §11.2](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — "禁止在多个地方定义同一类默认值"。

| 位置 | 内容 |
|---|---|
| [common/nacos/config.go:16-24](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/config.go) `DefaultClientConfig()` | `ServerAddr="127.0.0.1:8848"`、`Group="DEFAULT_GROUP"`、`Username="nacos"`、`Password="nacos"`、`ConfigGroup="DEFAULT_GROUP"` |
| [common/config/defaults.go:105-120](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/defaults.go) `setDefaults` 的 Nacos 段 | `ServerAddr="127.0.0.1:8848"`、`Group="DEFAULT_GROUP"`、`Username="nacos"`、`Password="nacos"`、`ConfigGroup="DEFAULT_GROUP"` |

**字节级重复**，且 `DefaultClientConfig()` 在代码库中**从未被调用**（grep 全仓库无引用）—— 这是死代码。

**修复方向**：删除 `nacos.DefaultClientConfig()`，保留 `common/config/defaults.go` 一处。

---

### 问题 3：bootstrap 装配 nacos 的两种风格

**违反条款**：[CODING_STANDARD.md §12.1, §12.3](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — Application 生命周期应统一。

#### game/bootstrap/app.go（inline 风格）
[game/bootstrap/app.go:47-92](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) 把所有逻辑塞进 `NewApplicationWithConfig`：

```go
func NewApplicationWithConfig(cfg *config.Config) (*Application, error) {
    var nacosClient *nacos.Client
    if cfg.Nacos.Enabled {
        nacosClient, err = nacos.NewClient(&nacos.ClientConfig{...})  // inline 构造
        // ... 加载主配置
        // ... 加载 algorithm 配置
    }
    // ... 后续 100 行装配其它组件
}
```

- 没有 `initNacos` 辅助函数。
- 主配置从 nacos 加载失败时，`logger.Warn` 但**继续用旧 cfg**。
- algorithm 配置加载失败时，`logger.Warn` 也继续。
- 没有抽 `reloadConfigFromNacos` helper。

#### gateway/bootstrap/app.go（抽函数风格）
[gateway/bootstrap/app.go:40-95, 209-246](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go) 把 nacos 相关逻辑抽成 3 个辅助函数：

```go
func NewApplicationWithConfig(cfg *gatewayConfig.Config, routerPath string) (*Application, error) {
    nacosClient, err := initNacos(cfg)             // 抽函数
    if nacosClient != nil && cfg.Nacos.ConfigDataID != "" {
        cfg = reloadConfigFromNacos(nacosClient, cfg)  // 抽函数
    }
    // ...
    routerConfig, err := loadRouterConfig(nacosClient, cfg, routerPath)  // 抽函数
}
```

- 有 `initNacos`、`reloadConfigFromNacos`、`loadRouterConfig` 三个 helper。
- `reloadConfigFromNacos` 在解析失败时**返回旧 cfg**（更宽容），而 game 模块在 algorithm 配置解析失败时**返回 fatal error**。

**风格不一致点**：
1. game 没抽 helper，gateway 抽了 3 个。
2. game 把 nacos + algorithm 配置加载都放在 `NewApplicationWithConfig` 里；gateway 把 nacos + router 配置加载也放进去但抽成函数。
3. **错误处理策略相反**：game 对 nacos 配置解析失败"Warn 继续 + 主配置/algorithm 配置独立判断"；gateway 对 nacos 配置解析失败"统一 Warn + 返回旧 cfg"。
4. game 在 `NewApplicationWithConfig` 中检查 `cfg.Nacos.Enabled`；gateway 在 `initNacos` 内部检查。**检查点位置不一致**。

**修复方向**：统一采用 gateway 的"抽 helper"风格，每个服务 bootstrap 都有 `initNacos`、`reloadMainConfigFromNacos`、`loadXxxConfigFromNacos` 三个 helper。错误策略统一为"Warn + 返回旧 cfg"（更宽容，避免 nacos 短暂故障导致服务起不来）。

---

### 问题 4：gRPC 连接管理两套实现（严重）

**违反条款**：[CODING_STANDARD.md §2.1, §3.2](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — 同一功能不得有两种实现。

#### 实现 A：common/nacos/grpc_manager.go（`GRPCConnManager`）
[common/nacos/grpc_manager.go:16-162](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/grpc_manager.go)

- 手动管理 `[]*grpcConn` 切片。
- 用 `sync.RWMutex` 保护切片。
- 用 `atomic.AddUint64` 做轮询负载均衡（`GetConn`）。
- 通过 `client.SubscribeService` callback **被动推送**更新连接。
- `createConn` 自己拼 `grpc.DialOption`，keepalive 5min/1min。
- `Close` 关闭所有连接。

#### 实现 B：gateway/discovery/discovery.go（`ServiceDiscovery` + `nacosResolver`）
[gateway/discovery/discovery.go:103-161, 24-101](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/discovery/discovery.go)

- 用 gRPC 原生 `resolver.Resolver` 机制。
- `nacosResolver` 实现 `resolver.Resolver` 接口。
- 用 `sync.Map` 缓存连接。
- **主动轮询**（`time.NewTicker(10 * time.Second)`）调用 `DiscoverService` 更新地址列表。
- 用 `grpc.Dial("nacos:///<service>", ...)` + `grpc.WithResolvers(builder)` 让 gRPC 自己管理连接。
- 负载均衡交给 gRPC 的 `round_robin` policy。
- 没有自定义 keepalive。

**实际使用情况**：
- `GRPCConnManager`：grep 全仓库**无任何调用方** —— 这是死代码！
- `ServiceDiscovery`：被 `gateway/bootstrap/app.go:82` 使用。

**修复方向**：删除 [common/nacos/grpc_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/grpc_manager.go) 整个文件（死代码）。保留 `ServiceDiscovery` 作为唯一的 gRPC 服务发现实现。

---

### 问题 5：服务发现订阅两套机制

**违反条款**：[CODING_STANDARD.md §3.4](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — 同一行为应统一方法风格。

`nacos.Client` 提供两种订阅方式：

| 方式 | API | 实现位置 | 触发机制 |
|---|---|---|---|
| A | `SubscribeService(serviceName, callback)` | [common/nacos/client.go:203-219](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go) | nacos SDK 内部推送 |
| B | `DiscoverService(serviceName)` 主动查询 | [common/nacos/client.go:137-152](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go) + `nacosResolver` 10s 轮询 | 定时拉取 |

- 方式 A 仅被死代码 `GRPCConnManager.watchService` 调用。
- 方式 B 被 `ServiceDiscovery.nacosResolver.watch` 调用。

**修复方向**：删除 `GRPCConnManager` 后，`SubscribeService` 也无调用方，可一并删除（或保留作为公共能力但标注"暂未使用"）。

---

### 问题 6：ClientConfig 字段在 bootstrap 装配时手工逐字段拷贝

**违反条款**：[CODING_STANDARD.md §12.2](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — "NewXxx 参数超过 5 个时应使用 options struct"。

[game/bootstrap/app.go:52-61](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) 和 [gateway/bootstrap/app.go:214-223](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go) 都在调用 `nacos.NewClient` 时**手工逐字段拷贝** 8 个字段：

```go
// game
nacosClient, err = nacos.NewClient(&nacos.ClientConfig{
    ServerAddr:  cfg.Nacos.ServerAddr,
    Namespace:   cfg.Nacos.Namespace,
    Group:       cfg.Nacos.Group,
    Username:    cfg.Nacos.Username,
    Password:    cfg.Nacos.Password,
    ServiceName: cfg.Nacos.ServiceName,
    ServiceAddr: cfg.Nacos.ServiceAddr,
    ServicePort: cfg.Nacos.ServicePort,
})
```

**问题**：
- 8 行字段拷贝，重复 2 处。
- gateway 还要额外做 `uint64(cfg.Nacos.ServicePort)` 类型转换（因为 §问题1 中提到的类型不一致）。
- 一旦 `NacosConfig` 新增字段，两个 bootstrap 都要同步修改 —— 高维护成本。

**修复方向**：`nacos.NewClient` 直接接收 `*common/config.NacosConfig`（或 `nacos.ClientConfig` 与之合并），消除逐字段拷贝。

---

### 问题 7：错误处理与日志风格不一致

#### 7.1 RegisterService 失败处理不一致

| 位置 | 行为 |
|---|---|
| [game/bootstrap/app.go:211-214](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) | `logger.Error` 后**继续启动**（不 return error） |
| [gateway/bootstrap/app.go:109-113](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go) | `logger.Error` 后**继续启动**（不 return error） |

两处一致（都是 Warn-after-error 风格），但**都不返回 error** —— 这意味着 nacos 注册失败时服务照常启动，但其它服务无法发现它。**这是一个潜在的静默故障**。

**修复方向**：根据业务需求选择：
- 若"nacos 注册失败必须阻止启动"：return error 让 `Run()` 调 `logger.Fatal`。
- 若"nacos 注册失败可降级"：保持现状但**必须** `logger.Warn`（不是 `Error`），并在启动日志中显式标注"nacos 注册失败，服务发现不可用"。

按 [CODING_STANDARD.md §5.3](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) "可恢复失败用 Warn"，此处应改为 `Warn`。

#### 7.2 DeregisterService 错误处理违反规范

[common/nacos/client.go:116-135](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go) `DeregisterService`：

```go
if err != nil {
    logger.Error("failed to deregister service", "error", err)
    return err   // ❌ 已 log 又 return，违反"要么 log 要么 return"原则
}
```

**违反**：[CODING_STANDARD.md §4.3](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) —— 应该只 return 包装后的 error，由调用方决定是否 log。或者只 log 不 return（但这样调用方无法感知失败）。

而同文件 `RegisterService` ([client.go:89-114](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)) 在失败时**只 return error 不 log** —— 风格相反。

**修复方向**：统一为"包装 error 后 return，不 log"，由调用方根据场景决定 log 级别。即：
```go
if err != nil {
    return fmt.Errorf("failed to deregister service: %w", err)
}
```

#### 7.3 createConn 错误未包装

[common/nacos/grpc_manager.go:82-85](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/grpc_manager.go) `createConn` 直接 `return nil, err`，不包装。虽然这是死代码，但反映了"不包装 error"的反面风格。

---

### 问题 8：ServiceDiscovery 没有用 atomic.Pointer 但有并发读写

[gateway/discovery/discovery.go:103-115](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/discovery/discovery.go) `ServiceDiscovery` 用 `sync.Map` 存连接，这部分是 OK 的。

但 `nacosResolver.cc` 字段（[discovery.go:46-51](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/discovery/discovery.go)）在 `start()` 中赋值后只读，OK。

`nacosResolver.cancel` 字段在 `start()` 中赋值、`Close()` 中调用 —— 如果 `Close()` 与 `start()` 并发（理论上不会，因为 `Build` 同步调 `start`），可能有竞争。**这里不是大问题**，但说明没有显式的生命周期约束。

---

### 问题 9：Client.Close 的语义问题

[common/nacos/client.go:221-223](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)：

```go
func (c *Client) Close() {
    c.DeregisterService()
}
```

- `Close` 不返回 error，但 `DeregisterService` 返回 error —— **吞掉 error**。
- `Close` 只做 deregister，不做 config listener 清理、不做 naming subscribe 清理。

**违反**：[CODING_STANDARD.md §4.4](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) — 不得吞没 error。

**修复方向**：`Close() error`，返回 `DeregisterService` 的 error。

---

### 问题 10：nacos sdk 配置硬编码

[common/nacos/client.go:47-56](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go) `NewClient` 中 nacos sdk 的 `ClientConfig` 大量硬编码：

```go
clientConfig := constant.ClientConfig{
    NamespaceId:         cfg.Namespace,
    Username:            cfg.Username,
    Password:            cfg.Password,
    TimeoutMs:           5000,          // 硬编码
    NotLoadCacheAtStart: true,          // 硬编码
    LogDir:              "/tmp/nacos/log",     // 硬编码 Linux 路径
    CacheDir:            "/tmp/nacos/cache",   // 硬编码 Linux 路径
    LogLevel:            "warn",        // 硬编码
}
```

**问题**：
- `TimeoutMs=5000`、`LogLevel="warn"` 等不可配置。
- `LogDir`/`CacheDir` 硬编码 `/tmp/...`，macOS 开发环境虽然能用但不规范，生产环境可能无 `/tmp` 写权限。
- 违反 [CODING_STANDARD.md §3.6](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) "魔法数字禁止出现在业务逻辑里"。

**修复方向**：把 `TimeoutMs`、`LogLevel`、`LogDir`、`CacheDir` 加到 `NacosConfig`，并提供默认值。

---

### 问题 11：NewClient 中 ServerAddr 解析逻辑过度复杂

[common/nacos/client.go:28-45](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)：

```go
serverConfigs := []constant.ServerConfig{
    {IpAddr: cfg.ServerAddr, Port: 8848},
}

if len(cfg.ServerAddr) > 0 && cfg.ServerAddr[len(cfg.ServerAddr)-1] >= '0' && cfg.ServerAddr[len(cfg.ServerAddr)-1] <= '9' {
    for i := len(cfg.ServerAddr) - 1; i >= 0; i-- {
        if cfg.ServerAddr[i] == ':' {
            var port uint64
            fmt.Sscanf(cfg.ServerAddr[i+1:], "%d", &port)
            serverConfigs[0].IpAddr = cfg.ServerAddr[:i]
            serverConfigs[0].Port = port
            break
        }
    }
}
```

**问题**：
- 手写字符串解析 `host:port`，应该用 `net.SplitHostPort`。
- 用 `fmt.Sscanf` 解析整数，性能差且不检查 error。
- 末位字符判断 `>= '0' && <= '9'` 是为了区分"带端口"和"不带端口"，但 `net.SplitHostPort` + 错误处理更清晰。

**修复方向**：
```go
host, portStr, err := net.SplitHostPort(cfg.ServerAddr)
if err != nil {
    host = cfg.ServerAddr
    portStr = "8848"
}
port, _ := strconv.ParseUint(portStr, 10, 64)
serverConfigs := []constant.ServerConfig{{IpAddr: host, Port: port}}
```

---

### 问题 12：listeners 字段是死代码

[common/nacos/client.go:24](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go) `listeners []func(content string)` 字段：
- 在 `NewClient` 中初始化为空 slice（line 85）。
- grep 全仓库**无任何写入或读取** —— 死字段。

**修复方向**：删除。

---

## 三、风格不一致汇总表

| # | 问题 | 涉及位置 | 违反条款 | 严重度 |
|---|---|---|---|---|
| 1 | NacosConfig 三处重复定义 | nacos/config.go、common/config/config.go、gateway/config/config.go | §11.1 | 高 |
| 2 | Nacos 默认值两处重复 | nacos/config.go、common/config/defaults.go | §11.2 | 中 |
| 3 | bootstrap 装配 nacos 两种风格 | game/bootstrap/app.go、gateway/bootstrap/app.go | §12.1, §12.3 | 中 |
| 4 | gRPC 连接管理两套实现（一套死代码） | nacos/grpc_manager.go、gateway/discovery/discovery.go | §2.1, §3.2 | 高 |
| 5 | 服务发现订阅两套机制 | nacos/client.go SubscribeService vs DiscoverService+轮询 | §3.4 | 中 |
| 6 | ClientConfig 手工逐字段拷贝 | game/bootstrap/app.go:52-61、gateway/bootstrap/app.go:214-223 | §12.2 | 中 |
| 7.1 | RegisterService 失败处理不一致 | game/bootstrap/app.go:211-214、gateway/bootstrap/app.go:109-113 | §4.5, §5.3 | 中 |
| 7.2 | DeregisterService 错误处理违反"log 或 return" | nacos/client.go:128-131 | §4.3 | 中 |
| 7.3 | createConn 错误未包装 | nacos/grpc_manager.go:82-85 | §4.3 | 低（死代码） |
| 8 | nacosResolver 生命周期约束缺失 | gateway/discovery/discovery.go:46-58 | §6.1 | 低 |
| 9 | Client.Close 吞 error | nacos/client.go:221-223 | §4.4 | 中 |
| 10 | nacos sdk 配置硬编码 | nacos/client.go:47-56 | §3.6, §11.2 | 中 |
| 11 | ServerAddr 解析过度复杂 | nacos/client.go:28-45 | §15 Anti-Patterns | 中 |
| 12 | listeners 死字段 | nacos/client.go:24 | §15.8 重复代码 | 低 |

---

## 四、修复优先级建议

### P0（立即修复，影响正确性或为死代码）
1. **删除 `common/nacos/grpc_manager.go` 整个文件**（死代码，问题 4）
2. **删除 `nacos.Client.listeners` 字段**（死字段，问题 12）
3. **删除 `nacos.DefaultClientConfig()`**（死代码，问题 2）
4. **删除 `nacos.Client.SubscribeService`**（无调用方，问题 5）—— 或保留但加注释"预留能力"

### P1（重构，消除重复）
5. **统一 `NacosConfig` 定义**：保留 [common/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/config.go) 一处，把 algorithm/router/rateLimiter DataID/Group 全放进去；`nacos.ClientConfig` 改为 `common/config.NacosConfig` 的别名或直接被替换（问题 1）
6. **`nacos.NewClient` 直接接收 `*common/config.NacosConfig`**，消除 bootstrap 中的逐字段拷贝（问题 6）
7. **统一 game/gateway bootstrap 的 nacos 装配风格**：抽 `initNacos`、`reloadMainConfigFromNacos`、`loadXxxConfigFromNacos` helper（问题 3）

### P2（代码质量）
8. **`Client.Close() error`** 返回 deregister 的 error（问题 9）
9. **`DeregisterService`** 删除内部 log，只 return 包装后的 error（问题 7.2）
10. **`RegisterService` 失败时改 `logger.Warn`** 并在启动日志中显式标注（问题 7.1）
11. **`NewClient` 用 `net.SplitHostPort`** 替换手写解析（问题 11）
12. **nacos sdk 的 `TimeoutMs`/`LogLevel`/`LogDir`/`CacheDir`** 提取到 `NacosConfig`（问题 10）

---

## 五、参考实现推荐

修复后应保留的"唯一实现"：

| 主题 | 推荐保留位置 |
|---|---|
| NacosConfig 定义 | [common/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/config.go)（统一此处） |
| Nacos 默认值 | [common/config/defaults.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/defaults.go)（统一此处） |
| Nacos 客户端 | [common/nacos/client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)（删除死字段/死方法） |
| gRPC 服务发现 | [gateway/discovery/discovery.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/discovery/discovery.go)（唯一实现，考虑移到 `common/discovery/` 共享） |
| bootstrap 装配风格 | [gateway/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go) 的 `initNacos` + `reloadConfigFromNacos` + `loadRouterConfig` 模式（统一采用） |

---

## 六、结论

Nacos 模块是当前 backend 中**风格不一致最严重的子系统**之一：
- **3 处配置定义** + **2 处默认值** + **2 套 gRPC 连接管理**（其中 1 套是死代码）+ **2 种 bootstrap 装配风格** + **多个错误处理/日志违规**。
- 其中 **`common/nacos/grpc_manager.go` 整个文件是死代码**，应该立即删除。
- 修复后建议把 `gateway/discovery/discovery.go` 移到 `common/discovery/` 让 game 模块也能复用（目前 game 只注册自己，不做服务发现，但未来如果需要就应该复用同一套）。

修复时遵循 [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) 的 §11（配置）、§12（DI/生命周期）、§4（错误处理）、§5（日志）条款。
