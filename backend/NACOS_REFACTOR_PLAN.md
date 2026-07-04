# Nacos 模块完整重构方案与规约

> 输入：[NACOS_REVIEW.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/NACOS_REVIEW.md) 已识别 12 类问题 + 配置加载风格补充审查
> 输出：本文档给出目标架构、详细规约、分阶段重构步骤、文件级改动清单、验证方案
> 遵循：[CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md)
>
> **更新历史**：
> - 2026-07-04 v1：初版，覆盖 12 类 nacos 模块问题
> - 2026-07-04 v2：补充配置加载风格审查，新增 §3.10/3.11、Phase 2.5/4.5、附录 D/E/F
> - 2026-07-04 v3：修正"主配置需要热更新"误判——主配置仅在启动时拉取一次
> - 2026-07-04 v4：修正"NacosConfig 全放 common"误判——采用分层设计：common 只放共用基础，各服务独有 DataID 放各自 config 包；game 创建自己的 `game/config` 包（与 gateway/stats 对齐）

---

## 目录

1. [重构目标与原则](#一重构目标与原则)
2. [目标架构](#二目标架构)
3. [详细规约](#三详细规约)
4. [分阶段重构方案](#四分阶段重构方案)
5. [文件级改动清单](#五文件级改动清单)
6. [验证方案](#六验证方案)
7. [回滚预案](#七回滚预案)
8. [风险与权衡](#八风险与权衡)
9. [执行检查清单](#九执行检查清单)
10. [附录](#十附录)

---

## 一、重构目标与原则

### 1.1 目标

| # | 目标 | 衡量标准 |
|---|---|---|
| 1 | 消除 NacosConfig 重复定义 | 共用基础字段仅 1 处定义（`common/config`）；各服务独有 DataID 在各自 config 包扩展 |
| 2 | 消除 gRPC 连接管理重复实现 | 全仓库仅 1 套 ServiceDiscovery 实现 |
| 3 | 消除 bootstrap 装配风格分歧 | game/gateway bootstrap 均有 `initNacos` + `reloadXxxConfigFromNacos` helper |
| 4 | 删除所有死代码 | `grpc_manager.go`、`SubscribeService`、`GetOneInstance`、`PublishConfig`、`listeners` 字段、`DefaultClientConfig` 全部删除 |
| 5 | 统一错误处理与日志风格 | 错误一律 `fmt.Errorf("...: %w", err)` 包装后 return，不在底层 log 又 return |
| 6 | 配置可观测性 | nacos sdk 的 `TimeoutMs`/`LogLevel`/`LogDir`/`CacheDir` 可通过 yaml 配置 |
| 7 | 统一配置加载风格 | 全仓库配置加载统一用 viper；`Load`/`LoadFromContent` 入口对称；每个服务有自己的 `Config` 聚合 struct |
| 8 | 统一热更新回调注册 | 模块专属配置（algorithm/rateLimiter）的 `ListenConfig` 回调注册统一封装，错误处理统一 Warn。**主配置不热更新**（仅在启动时拉取一次） |
| 9 | game 拥有自己的 config 包 | `game/config` 与 `gateway/config`、`stats/config` 对齐，不再直接用 `common/config.Config` |

### 1.2 原则

- **P0 优先删除死代码**：删除不可逆风险最低，先把死代码清掉再重构。
- **P1 分层收敛**：`common/config` 只放**所有服务共用**的子结构（NacosConfig 基础、RedisConfig、MySQLConfig 等）和加载工具；各服务独有配置放各自 config 包。
- **P2 行为保持**：重构不改变外部可观察行为（注册的服务名、监听的 DataID、配置加载时机都不变）。
- **每阶段独立可发布**：每个 P 阶段结束后都能 `go build ./...` 通过，可独立合并。
- **配置加载对称性**：`Load(path)` 与 `LoadFromContent(content)` 必须对称——前者能做的事后者也要能做。
- **NacosConfig 分层**：共用字段（连接、注册、主配置 DataID、sdk 调优）在 `common/config.NacosConfig`；服务独有 DataID 通过嵌入扩展（`game/config.GameNacosConfig`、`gateway/config.GatewayNacosConfig`）。

---

## 二、目标架构

### 2.1 目标目录结构

```
backend/
├── common/
│   ├── config/
│   │   ├── nacos.go          # NacosConfig（共用基础）+ SetNacosDefaults
│   │   ├── redis.go          # RedisConfig + SetRedisDefaults
│   │   ├── mysql.go          # MySQLConfig + SetMySQLDefaults
│   │   ├── log.go            # LogConfig + SetLogDefaults
│   │   ├── server.go         # ServerConfig + SetServerDefaults
│   │   ├── loader.go         # LoadYAML/LoadYAMLFromContent（viper 工具函数）
│   │   └── types.go          # 其他共用子结构（Timeout/Broadcast/Kafka/Platform/Avatar/GameService/IDGenerator/Lua）
│   │   (删除: config.go 中的聚合 Config struct、defaults.go 中的聚合 setDefaults)
│   ├── nacos/
│   │   ├── client.go         # NewClient(*config.NacosConfig)，精简 API
│   │   └── errors.go         # 哨兵错误
│   │   (删除: config.go, grpc_manager.go)
│   └── discovery/            # 新增：从 gateway/discovery 移过来
│       └── discovery.go      # ServiceDiscovery + nacosResolver（公共复用）
├── game/
│   ├── config/               # 新增：game 自己的 config 包（与 gateway/stats 对齐）
│   │   ├── config.go         # Config 聚合 struct + Load/LoadFromContent
│   │   ├── nacos.go          # GameNacosConfig（嵌入 config.NacosConfig + AlgorithmDataID/AlgorithmGroup）
│   │   ├── algorithm.go      # AlgorithmConfig + LoadAlgorithm/LoadAlgorithmFromContent
│   │   └── defaults.go       # setDefaults（调用共用 SetXxxDefaults + game 独有默认值）
│   └── bootstrap/
│       ├── app.go            # 用 initNacos + reloadMainConfigFromNacos + loadAlgorithmConfigFromNacos
│       ├── config_listener.go # registerAlgorithmConfigListener
│       └── container.go
├── gateway/
│   ├── bootstrap/
│   │   ├── app.go            # 用 initNacos + reloadMainConfigFromNacos + loadRouterConfigFromNacos + loadRateLimiterConfigFromNacos
│   │   ├── config_listener.go # registerRateLimiterConfigListener
│   │   └── container.go
│   └── config/
│       ├── config.go         # Config 聚合 struct + Load/LoadFromContent（改为 viper）
│       ├── nacos.go          # GatewayNacosConfig（嵌入 config.NacosConfig + RouterDataID/RateLimiterDataID）
│       ├── router.go         # LoadRouterConfig/LoadRouterConfigFromContent
│       ├── rate_limiter.go   # RateLimiterConfig + LoadRateLimiterFromContent
│       └── defaults.go       # setDefaults
└── stats/
    └── config/
        ├── config.go         # Config 聚合 struct + Load（改为 viper，复用 common 子结构）
        └── defaults.go       # setDefaults
```

### 2.2 NacosConfig 分层设计

```
common/config.NacosConfig（共用基础）
├── Enabled, ServerAddr, Namespace, Group, Username, Password
├── ServiceName, ServiceAddr, ServicePort (uint64)
├── ConfigDataID, ConfigGroup（主配置，所有服务都可能用）
└── TimeoutMs, LogLevel, LogDir, CacheDir（sdk 调优）

game/config.GameNacosConfig（game 扩展）
└── 嵌入 common/config.NacosConfig
    ├── AlgorithmDataID
    └── AlgorithmGroup

gateway/config.GatewayNacosConfig（gateway 扩展）
└── 嵌入 common/config.NacosConfig
    ├── RouterDataID, RouterGroup
    └── RateLimiterDataID, RateLimiterGroup

stats：不用 nacos，无 NacosConfig
```

### 2.3 组件职责

| 组件 | 职责 | 不做什么 |
|---|---|---|
| `common/config.NacosConfig` | nacos 共用配置字段 | 不含服务独有 DataID |
| `common/config.LoadYAML/LoadYAMLFromContent` | 通用 yaml 加载工具（viper） | 不绑定具体 Config struct |
| `common/config.SetXxxDefaults` | 各共用子结构的默认值函数 | 不含服务独有默认值 |
| `common/nacos.Client` | 封装 nacos sdk：注册、反注册、发现、配置拉取、配置监听 | 不管理 gRPC 连接；不关心服务独有 DataID |
| `common/discovery.ServiceDiscovery` | 基于 nacos 的 gRPC 服务发现 | 不直接持有 nacos.Client 之外的 sdk 对象 |
| `game/config.Config` | game 配置聚合 | 不含 gateway/stats 独有字段 |
| `game/config.GameNacosConfig` | game 的 nacos 配置扩展 | 不含 router/rateLimiter DataID |
| `gateway/config.Config` | gateway 配置聚合 | 不含 game/stats 独有字段 |
| `gateway/config.GatewayNacosConfig` | gateway 的 nacos 配置扩展 | 不含 algorithm DataID |
| `bootstrap.initNacos` | 构造 `*nacos.Client`，注册自身服务 | 不加载业务配置 |
| `bootstrap.reloadMainConfigFromNacos` | 从 nacos 拉取主配置并覆盖 | 不注册 ListenConfig |

### 2.4 依赖方向

```
game/config ──────┐
gateway/config ───┼─→ common/config ──（无外部依赖）
stats/config ─────┘                    ↑
game/bootstrap ─┐                      │
gateway/bootstrap ─┤──→ common/nacos ──┘
common/discovery ─┘──→ common/config ──┘
```

- `common/config` 是最底层，无外部依赖
- 各服务 config 包依赖 `common/config` 获取共用子结构
- `common/nacos` 依赖 `common/config.NacosConfig`（只用共用基础）
- `common/discovery` 依赖 `common/nacos`
- 各服务 bootstrap 依赖自己的 config 包 + `common/nacos`

---

## 三、详细规约

### 3.1 NacosConfig 分层定义

#### 3.1.1 共用基础（common/config/nacos.go）

```go
package config

// NacosConfig 是 nacos 共用配置基础。各服务独有的 DataID 通过嵌入扩展。
// Used by: game (via GameNacosConfig), gateway (via GatewayNacosConfig).
// Not used by: stats.
type NacosConfig struct {
    // --- 连接 ---
    Enabled     bool   `mapstructure:"enabled" yaml:"enabled"`
    ServerAddr  string `mapstructure:"server_addr" yaml:"server_addr"`
    Namespace   string `mapstructure:"namespace" yaml:"namespace"`
    Group       string `mapstructure:"group" yaml:"group"`
    Username    string `mapstructure:"username" yaml:"username"`
    Password    string `mapstructure:"password" yaml:"password"`

    // --- 服务注册 ---
    ServiceName string `mapstructure:"service_name" yaml:"service_name"`
    ServiceAddr string `mapstructure:"service_addr" yaml:"service_addr"`
    ServicePort uint64 `mapstructure:"service_port" yaml:"service_port"` // 统一 uint64

    // --- 主配置 DataID（所有服务都可能用）---
    ConfigDataID string `mapstructure:"config_data_id" yaml:"config_data_id"`
    ConfigGroup  string `mapstructure:"config_group" yaml:"config_group"`

    // --- sdk 调优（避免硬编码）---
    TimeoutMs uint64 `mapstructure:"timeout_ms" yaml:"timeout_ms"`
    LogLevel  string `mapstructure:"log_level" yaml:"log_level"`
    LogDir    string `mapstructure:"log_dir" yaml:"log_dir"`
    CacheDir  string `mapstructure:"cache_dir" yaml:"cache_dir"`
}
```

**规约**：
- `NacosConfig` 只含所有使用 nacos 的服务都需要的字段。
- **禁止**在此结构中添加服务独有的 DataID（如 `AlgorithmDataID`、`RouterDataID`）。
- `ServicePort` 统一 `uint64`，禁止 `int`。
- 所有字段必须同时带 `mapstructure` 和 `yaml` tag。

#### 3.1.2 game 扩展（game/config/nacos.go）

```go
package config

import commonconfig "github.com/cashparty/backend/common/config"

// GameNacosConfig 扩展共用 NacosConfig，添加 game 独有的 DataID。
type GameNacosConfig struct {
    commonconfig.NacosConfig `mapstructure:",squash" yaml:",inline"`
    AlgorithmDataID          string `mapstructure:"algorithm_data_id" yaml:"algorithm_data_id"`
    AlgorithmGroup           string `mapstructure:"algorithm_group" yaml:"algorithm_group"`
}
```

#### 3.1.3 gateway 扩展（gateway/config/nacos.go）

```go
package config

import commonconfig "github.com/cashparty/backend/common/config"

// GatewayNacosConfig 扩展共用 NacosConfig，添加 gateway 独有的 DataID。
type GatewayNacosConfig struct {
    commonconfig.NacosConfig `mapstructure:",squash" yaml:",inline"`
    RouterDataID             string `mapstructure:"router_data_id" yaml:"router_data_id"`
    RouterGroup              string `mapstructure:"router_group" yaml:"router_group"`
    RateLimiterDataID        string `mapstructure:"rate_limiter_data_id" yaml:"rate_limiter_data_id"`
    RateLimiterGroup         string `mapstructure:"rate_limiter_group" yaml:"rate_limiter_group"`
}
```

**规约**：
- 各服务通过**嵌入**（`mapstructure:",squash"` / `yaml:",inline"`）扩展共用基础。
- 扩展结构命名：`<Service>NacosConfig`（如 `GameNacosConfig`、`GatewayNacosConfig`）。
- `nacos.NewClient` 只接收 `*common/config.NacosConfig`（基础部分），通过 `&cfg.Nacos.NacosConfig` 传入。

### 3.2 默认值分层

#### 3.2.1 共用默认值（common/config/nacos.go）

```go
// SetNacosDefaults 设置共用 NacosConfig 的默认值。
func SetNacosDefaults(cfg *NacosConfig) {
    if cfg.ServerAddr == "" {
        cfg.ServerAddr = "127.0.0.1:8848"
    }
    if cfg.Group == "" {
        cfg.Group = "DEFAULT_GROUP"
    }
    if cfg.Username == "" {
        cfg.Username = "nacos"
    }
    if cfg.Password == "" {
        cfg.Password = "nacos"
    }
    if cfg.ConfigGroup == "" {
        cfg.ConfigGroup = "DEFAULT_GROUP"
    }
    if cfg.TimeoutMs == 0 {
        cfg.TimeoutMs = 5000
    }
    if cfg.LogLevel == "" {
        cfg.LogLevel = "warn"
    }
    if cfg.LogDir == "" {
        cfg.LogDir = "/tmp/nacos/log"
    }
    if cfg.CacheDir == "" {
        cfg.CacheDir = "/tmp/nacos/cache"
    }
}
```

#### 3.2.2 game 独有默认值（game/config/defaults.go）

```go
func setDefaults(cfg *Config) {
    commonconfig.SetServerDefaults(&cfg.Server)
    commonconfig.SetRedisDefaults(&cfg.Redis)
    commonconfig.SetMySQLDefaults(&cfg.MySQL)
    commonconfig.SetLogDefaults(&cfg.Log)
    commonconfig.SetNacosDefaults(&cfg.Nacos.NacosConfig) // 传入嵌入的基础部分

    // game 独有
    if cfg.Nacos.AlgorithmGroup == "" {
        cfg.Nacos.AlgorithmGroup = "DEFAULT_GROUP"
    }
    // ... 其他 game 独有默认值
}
```

#### 3.2.3 gateway 独有默认值（gateway/config/defaults.go）

```go
func setDefaults(cfg *Config) {
    commonconfig.SetServerDefaults(&cfg.Server)
    commonconfig.SetRedisDefaults(&cfg.Redis)
    commonconfig.SetLogDefaults(&cfg.Log)
    commonconfig.SetNacosDefaults(&cfg.Nacos.NacosConfig)

    // gateway 独有
    if cfg.Nacos.RouterGroup == "" {
        cfg.Nacos.RouterGroup = "DEFAULT_GROUP"
    }
    if cfg.Nacos.RateLimiterGroup == "" {
        cfg.Nacos.RateLimiterGroup = "DEFAULT_GROUP"
    }
    setRateLimiterDefaults(&cfg.RateLimiter)
    // ... 其他 gateway 独有默认值
}
```

**规约**：
- 共用子结构默认值函数命名：`Set<Struct>Defaults`（如 `SetNacosDefaults`、`SetRedisDefaults`），定义在 `common/config`。
- 各服务 `setDefaults` 调用共用函数 + 设置服务独有默认值。
- **禁止**在多处定义同一子结构的默认值。

### 3.3 nacos.Client 接口规约

**位置**：[common/nacos/client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)

**精简后的 API**（仅保留实际被调用的方法 + Close）：

```go
package nacos

type Client struct {
    configClient config_client.IConfigClient
    namingClient naming_client.INamingClient
    cfg          *config.NacosConfig
    serviceName  string
    serviceAddr  string
    servicePort  uint64
    mu           sync.Mutex
    closed       bool  // 新增：closed 状态保护
}

// NewClient 构造 nacos 客户端。cfg 必须非 nil，外部已通过 SetNacosDefaults 填充默认值。
// 只接收共用基础 NacosConfig，不关心服务独有 DataID。
func NewClient(cfg *config.NacosConfig) (*Client, error)

// RegisterService 注册自身服务到 nacos。
func (c *Client) RegisterService() error

// DeregisterService 反注册自身服务。
func (c *Client) DeregisterService() error

// DiscoverService 查询指定服务的健康实例列表。
func (c *Client) DiscoverService(serviceName string) ([]model.Instance, error)

// GetConfig 从配置中心拉取一次配置。
func (c *Client) GetConfig(dataID, group string) (string, error)

// ListenConfig 订阅配置变更。onChange 在配置变化时被调用。
func (c *Client) ListenConfig(dataID, group string, onChange func(content string)) error

// Close 反注册服务并释放资源。重复调用返回 nil。
func (c *Client) Close() error
```

**已删除的 API**（无调用方）：
- `GetOneInstance` — 删除
- `PublishConfig` — 删除
- `SubscribeService` — 删除
- `DefaultClientConfig()` — 删除
- `listeners` 字段 — 删除

### 3.4 错误处理规约

| 方法 | 失败行为 |
|---|---|
| `NewClient` | sdk 创建失败 → `return nil, fmt.Errorf("create nacos config client failed: %w", err)` |
| `RegisterService` | 失败 → `return fmt.Errorf("register service %s failed: %w", c.serviceName, err)`，**不 log** |
| `DeregisterService` | 失败 → `return fmt.Errorf("deregister service %s failed: %w", c.serviceName, err)`，**不 log** |
| `DiscoverService` | 失败 → `return nil, fmt.Errorf("discover service %s failed: %w", serviceName, err)` |
| `GetConfig` | 失败 → `return "", fmt.Errorf("get config %s/%s failed: %w", dataID, group, err)` |
| `ListenConfig` | 失败 → `return fmt.Errorf("listen config %s/%s failed: %w", dataID, group, err)` |
| `Close` | 透传 `DeregisterService` 的 error；已 closed 时直接 `return nil` |

**规约**：
- 所有 error 必须用 `fmt.Errorf("<action> failed: %w", err)` 包装。
- **禁止** "log 又 return" 模式。底层方法只 return，由调用方决定 log 级别。
- 哨兵错误：`var ErrServiceNameEmpty = errors.New("service name is empty")`，放在 `common/nacos/errors.go`。

### 3.5 日志规约

`nacos.Client` 内部仅保留 Info 日志：
- `RegisterService` 成功后：`logger.Info("service registered to nacos", ...)`
- `DeregisterService` 成功后：`logger.Info("service deregistered from nacos", ...)`
- `ListenConfig` callback 触发时：`logger.Info("config changed", ...)`

**禁止** Error 级别日志在 `nacos.Client` 内部出现。

### 3.6 bootstrap 装配规约

每个使用 nacos 的服务 bootstrap 必须提供以下 helper（统一命名）：

```go
// initNacos 构造 nacos 客户端并注册自身服务。
// 注册失败时 logger.Warn 后继续启动（降级：服务发现不可用，但本地功能正常）。
// 返回 nil client 表示 nacos 未启用或初始化失败（已 Warn）。
func initNacos(cfg *commonconfig.NacosConfig) *nacos.Client

// reloadMainConfigFromNacos 从 nacos 拉取主配置并解析覆盖。
// 解析失败时 logger.Warn 后返回 oldCfg（保持现有配置继续运行）。
func reloadMainConfigFromNacos(nacosClient *nacos.Client, oldCfg *Config) *Config

// loadXxxConfigFromNacos 各模块按需实现，签名统一为：
//   func loadXxxConfigFromNacos(nacosClient *nacos.Client, cfg *Config) (*XxxConfig, error)
```

**调用顺序**（`NewApplicationWithConfig` 中）：
1. `initNacos(&cfg.Nacos.NacosConfig)` → `nacosClient`（传入嵌入的共用基础）
2. 若 `nacosClient != nil && cfg.Nacos.ConfigDataID != ""`：`cfg = reloadMainConfigFromNacos(nacosClient, cfg)`
3. 若 `nacosClient != nil && cfg.Nacos.AlgorithmDataID != ""`（game）：`loadAlgorithmConfigFromNacos(...)`
4. 若 `nacosClient != nil && cfg.Nacos.RouterDataID != ""`（gateway）：`loadRouterConfigFromNacos(...)`
5. 若 `nacosClient != nil && cfg.Nacos.RateLimiterDataID != ""`（gateway）：`loadRateLimiterConfigFromNacos(...)`
6. `NewContainer(..., nacosClient, ...)`
7. 在 `Start()` 中注册模块专属配置的 `ListenConfig` 回调（仅 algorithm/rateLimiter，**不含主配置**；见 §3.11）

### 3.7 ServiceDiscovery 规约

**位置**：`common/discovery/discovery.go`（从 `gateway/discovery/` 移过来）

**API 保持不变**（[gateway/discovery/discovery.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/discovery/discovery.go) 现有实现已符合规范，仅需移位置 + 改 import path）：

```go
package discovery

type ServiceDiscovery struct {
    nacosClient *nacos.Client
    connections sync.Map
    clients     sync.Map
    builder     *nacosResolverBuilder
}

func NewServiceDiscovery(nacosClient *nacos.Client) *ServiceDiscovery
func (d *ServiceDiscovery) GetClient(serviceName string) (ServiceClient, error)
func (d *ServiceDiscovery) Close() error
```

**规约**：
- gRPC 服务发现唯一实现，禁止再出现 `GRPCConnManager`。
- 负载均衡交给 gRPC `round_robin` policy，禁止手写轮询。

### 3.8 配置加载规约

#### 3.8.1 加载库统一

全仓库配置加载统一用 **viper**（通过 `common/config.LoadYAML`/`LoadYAMLFromContent` 工具函数）。

```go
// common/config/loader.go

// LoadYAML 用 viper 从文件加载 yaml 到 v。
func LoadYAML(path string, v interface{}) error {
    f := viper.New()
    f.SetConfigFile(path)
    f.SetConfigType("yaml")
    f.AutomaticEnv()
    if err := f.ReadInConfig(); err != nil {
        return fmt.Errorf("read config file failed: %w", err)
    }
    if err := f.Unmarshal(v, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
        return fmt.Errorf("parse config failed: %w", err)
    }
    return nil
}

// LoadYAMLFromContent 用 viper 从内容加载 yaml 到 v。
func LoadYAMLFromContent(content string, v interface{}) error {
    f := viper.New()
    f.SetConfigType("yaml")
    f.AutomaticEnv()
    if err := f.ReadConfig(strings.NewReader(content)); err != nil {
        return fmt.Errorf("read config content failed: %w", err)
    }
    if err := f.Unmarshal(v, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc())); err != nil {
        return fmt.Errorf("parse config failed: %w", err)
    }
    return nil
}
```

#### 3.8.2 各服务 Load/LoadFromContent 对称

每个服务的 `Load(path)` 与 `LoadFromContent(content)` 必须对称：

```go
// game/config/config.go
func Load(path string) (*Config, error) {
    var cfg Config
    if err := commonconfig.LoadYAML(path, &cfg); err != nil {
        return nil, err
    }
    setDefaults(&cfg)
    return &cfg, nil
}

func LoadFromContent(content string) (*Config, error) {
    var cfg Config
    if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
        return nil, err
    }
    setDefaults(&cfg)
    return &cfg, nil
}
```

#### 3.8.3 子配置加载对称

```go
// game/config/algorithm.go
func LoadAlgorithm(path string) (*AlgorithmConfig, error) {
    var cfg AlgorithmConfig
    if err := commonconfig.LoadYAML(path, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

func LoadAlgorithmFromContent(content string) (*AlgorithmConfig, error) {
    var cfg AlgorithmConfig
    if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

```go
// gateway/config/router.go
func LoadRouterConfig(path string) (*router.RouterConfig, error) { ... }
func LoadRouterConfigFromContent(content string) (*router.RouterConfig, error) { ... }

// gateway/config/rate_limiter.go
func LoadRateLimiterFromContent(content string) (*RateLimiterConfig, error) { ... }
```

#### 3.8.4 tag 风格统一

所有配置结构字段必须同时带 `mapstructure` 和 `yaml` tag，确保 viper 和直接 yaml 解析都兼容。

#### 3.8.5 错误信息统一

```go
return nil, fmt.Errorf("read config file failed: %w", err)
return nil, fmt.Errorf("parse config failed: %w", err)
return nil, fmt.Errorf("parse algorithm config failed: %w", err)
return nil, fmt.Errorf("parse router config failed: %w", err)
return nil, fmt.Errorf("parse rate limiter config failed: %w", err)
```

### 3.9 配置聚合 struct 规约

每个服务有自己的 `Config` 聚合 struct，组合共用子结构 + 服务独有字段：

```go
// game/config/config.go
type Config struct {
    Server      commonconfig.ServerConfig      `mapstructure:"server" yaml:"server"`
    Timeout     commonconfig.TimeoutConfig     `mapstructure:"timeout" yaml:"timeout"`
    Redis       commonconfig.RedisConfig       `mapstructure:"redis" yaml:"redis"`
    MySQL       commonconfig.MySQLConfig       `mapstructure:"mysql" yaml:"mysql"`
    Kafka       commonconfig.KafkaConfig       `mapstructure:"kafka" yaml:"kafka"`
    Broadcast   commonconfig.BroadcastConfig   `mapstructure:"broadcast" yaml:"broadcast"`
    Platform    commonconfig.PlatformConfig    `mapstructure:"platform" yaml:"platform"`
    Log         commonconfig.LogConfig         `mapstructure:"log" yaml:"log"`
    GameService commonconfig.GameServiceConfig `mapstructure:"game_service" yaml:"game_service"`
    Nacos       GameNacosConfig                `mapstructure:"nacos" yaml:"nacos"` // game 扩展
    Algorithm   AlgorithmConfig                `mapstructure:"algorithm" yaml:"algorithm"`
    IDGenerator commonconfig.IDGeneratorConfig `mapstructure:"id_generator" yaml:"id_generator"`
    Avatar      commonconfig.AvatarConfig      `mapstructure:"avatar" yaml:"avatar"`
    Robot       commonconfig.RobotConfig       `mapstructure:"robot" yaml:"robot"`
    Lua         commonconfig.LuaConfig         `mapstructure:"lua" yaml:"lua"`
}
```

```go
// gateway/config/config.go
type Config struct {
    Server      commonconfig.ServerConfig      `mapstructure:"server" yaml:"server"`
    Gateway     GatewayConfig                  `mapstructure:"gateway" yaml:"gateway"`
    Redis       commonconfig.RedisConfig       `mapstructure:"redis" yaml:"redis"`
    Nacos       GatewayNacosConfig             `mapstructure:"nacos" yaml:"nacos"` // gateway 扩展
    Kafka       commonconfig.KafkaConfig       `mapstructure:"kafka" yaml:"kafka"`
    Broadcast   commonconfig.BroadcastConfig   `mapstructure:"broadcast" yaml:"broadcast"`
    Merchant    MerchantConfig                 `mapstructure:"merchant" yaml:"merchant"`
    Token       TokenConfig                    `mapstructure:"token" yaml:"token"`
    Log         commonconfig.LogConfig         `mapstructure:"log" yaml:"log"`
    RateLimiter RateLimiterConfig              `mapstructure:"rate_limiter" yaml:"rate_limiter"`
}
```

```go
// stats/config/config.go
type Config struct {
    Server       commonconfig.ServerConfig `mapstructure:"server" yaml:"server"`
    MySQL        commonconfig.MySQLConfig  `mapstructure:"mysql" yaml:"mysql"`
    Redis        commonconfig.RedisConfig  `mapstructure:"redis" yaml:"redis"`
    Log          commonconfig.LogConfig    `mapstructure:"log" yaml:"log"`
    AmountRanges []AmountRange             `mapstructure:"amount_ranges" yaml:"amount_ranges"`
}
```

**规约**：
- `common/config` **不**提供聚合 `Config` struct（删除现有的 `common/config.Config`）。
- 每个服务的 `Config` struct 只包含该服务需要的字段。
- 共用子结构从 `common/config` 导入，服务独有字段在本地定义。

### 3.10 热更新回调注册规约

**问题**：当前 game 和 gateway 的 `ListenConfig` 回调注册都是 inline 在 `Start()` 里的匿名 goroutine，风格不一致，且错误级别用 `Error`（应为 `Warn`）。

**主配置不热更新**：主配置（`ConfigDataID`）只在启动时通过 `reloadMainConfigFromNacos` 拉取一次，**不注册 `ListenConfig`**。原因：
- 主配置中的 Redis/MySQL 连接池、gRPC server 端口等字段不可热更新。
- 主配置变更通常是低频操作，重启服务可接受。
- 仅模块专属配置（algorithm/rateLimiter）支持热更新。

**规约**：每个服务 bootstrap 必须把热更新回调注册抽成 helper：

```go
// registerConfigListeners 注册所有 nacos 配置热更新回调。
// 在 Start() 中调用，失败时 Warn 但不阻塞启动。
// 注意：主配置不在此注册——主配置仅在启动时通过 reloadMainConfigFromNacos 拉取一次。
func registerConfigListeners(nacosClient *nacos.Client, cfg *Config, container *Container) {
    if nacosClient == nil {
        return
    }
    // 模块专属热更新（仅这些配置支持热更新）
    registerAlgorithmConfigListener(nacosClient, cfg, container)  // game
    registerRateLimiterConfigListener(nacosClient, cfg, container) // gateway
}
```

**规约**：
- 每个 `ListenConfig` 必须在独立 goroutine 中调用（nacos sdk 的 ListenConfig 是阻塞的）。
- goroutine 必须经 `AsyncTaskRunner` 管理（或至少有 panic recovery）。
- 回调内解析失败必须 `Warn` 不 `Error`，且**不阻塞**后续变更通知。
- `ListenConfig` 调用失败必须 `Warn` 不 `Error`。
- **禁止**对主配置（`ConfigDataID`）注册 `ListenConfig`——主配置变更通过重启服务生效。

---

## 四、分阶段重构方案

### Phase 0：准备工作（无代码改动）

- 通读本文档，确认目标和规约。
- 确认 [NACOS_REVIEW.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/NACOS_REVIEW.md) 中的问题清单。

### Phase 1：P0 删除死代码

**目标**：删除无调用方的代码，零行为变化。

**步骤**：
1. 删除 [common/nacos/grpc_manager.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/grpc_manager.go) 整个文件。
2. 删除 [common/nacos/client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go) 中的：
   - `listeners` 字段（line 24）
   - `NewClient` 中 `listeners: make(...)` 初始化
   - `GetOneInstance` 方法
   - `PublishConfig` 方法
   - `SubscribeService` 方法
3. 删除 [common/nacos/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/config.go) 中的 `DefaultClientConfig()` 函数（保留 `ClientConfig` 结构体，Phase 2 会处理）。

**验证**：
- `go build ./...` 通过。
- `go vet ./...` 通过。
- 启动 game/gateway，功能正常。

### Phase 2：P1.1 统一 NacosConfig 定义（分层）

**目标**：建立 `common/config.NacosConfig` 共用基础 + 各服务扩展。

**步骤**：
1. 在 [common/config/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/config/) 新建 `nacos.go`：
   - 定义 `NacosConfig`（共用基础，见 §3.1.1）。
   - 定义 `SetNacosDefaults`（见 §3.2.1）。

2. 修改 [common/nacos/client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)：
   - `NewClient` 签名改为 `NewClient(cfg *config.NacosConfig) (*Client, error)`。
   - 删除 `common/nacos/config.go` 中的 `ClientConfig` 结构体（已由 `common/config.NacosConfig` 替代）。
   - 删除 `common/nacos/config.go` 整个文件。

3. 修改 [gateway/config/config.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/config/config.go)：
   - 拆分 `NacosConfig` 为 `gateway/config/nacos.go` 中的 `GatewayNacosConfig`（嵌入 `common/config.NacosConfig` + gateway 独有 DataID）。
   - `Config.Nacos` 字段类型改为 `GatewayNacosConfig`。
   - `ServicePort` 从 `int` 改为 `uint64`（消除类型转换）。

4. 修改 [gateway/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go)：
   - `initNacos` 中 `nacos.NewClient(&cfg.Nacos.NacosConfig)`（传入嵌入的基础部分）。
   - 删除 `uint64(cfg.Nacos.ServicePort)` 类型转换（已是 uint64）。

**验证**：
- `go build ./...` + `go vet ./...` 通过。
- 启动 gateway，检查 nacos 控制台能否看到服务注册。
- 检查能否从 nacos 拉取主配置 + router 配置 + rateLimiter 配置。

### Phase 2.5：P1.2 统一配置加载风格 + 创建 game/config 包

**目标**：
1. 创建 `game/config` 包（与 gateway/stats 对齐）。
2. `common/config` 拆分为子结构文件 + 加载工具函数。
3. 全仓库配置加载统一用 viper。
4. `Load`/`LoadFromContent` 对称。

**步骤**：
1. **拆分 common/config**：
   - 将 `common/config/config.go` 中的聚合 `Config` struct **删除**。
   - 将各子结构拆分到独立文件：`nacos.go`（Phase 2 已建）、`redis.go`、`mysql.go`、`log.go`、`server.go`、`types.go`（Timeout/Broadcast/Kafka/Platform/GameService/IDGenerator/Avatar/Robot/Lua）。
   - 将 `common/config/defaults.go` 中的聚合 `setDefaults` 拆分为 `SetRedisDefaults`、`SetMySQLDefaults`、`SetLogDefaults`、`SetServerDefaults` 等独立函数。
   - 新建 `common/config/loader.go`：提供 `LoadYAML`/`LoadYAMLFromContent` 工具函数（见 §3.8.1）。

2. **创建 game/config 包**：
   - 新建 `game/config/config.go`：定义 `Config` 聚合 struct（见 §3.9）+ `Load`/`LoadFromContent`。
   - 新建 `game/config/nacos.go`：定义 `GameNacosConfig`（见 §3.1.2）。
   - 新建 `game/config/algorithm.go`：移入 `AlgorithmConfig` 及相关结构 + `LoadAlgorithm`/`LoadAlgorithmFromContent`。
   - 新建 `game/config/defaults.go`：`setDefaults` 调用共用 `SetXxxDefaults` + game 独有默认值。

3. **修改 game/bootstrap/app.go**：
   - import 从 `common/config` 改为 `game/config`。
   - `config.Load` → `gameconfig.Load`（或通过 import alias）。
   - `config.LoadAlgorithmFromContent` → `gameconfig.LoadAlgorithmFromContent`。
   - `initNacos` 改为 `initNacos(&cfg.Nacos.NacosConfig)`。
   - `cfg.Nacos.AlgorithmDataID` 现在是 `GameNacosConfig` 的字段（通过嵌入访问）。

4. **修改 gateway/config**：
   - 将 yaml.v3 改为 viper（通过 `commonconfig.LoadYAML`/`LoadYAMLFromContent`）。
   - 拆分 `config.go` 为 `config.go`（聚合 + Load）、`nacos.go`（Phase 2 已建）、`router.go`、`rate_limiter.go`、`defaults.go`。
   - 所有字段添加 `mapstructure` tag（保留 `yaml` tag）。

5. **修改 stats/config**：
   - 将 yaml.v3 改为 viper（通过 `commonconfig.LoadYAML`）。
   - `Config` struct 改用 `commonconfig.ServerConfig`/`RedisConfig`/`MySQLConfig`/`LogConfig`（见 §3.9）。
   - 默认端口从 8081 改为 8082（避免与 gateway 冲突）。

6. **修改所有引用 common/config.Config 的地方**：
   - game 内所有 `config.Config` 引用改为 `gameconfig.Config`。
   - gateway 内 `gatewayConfig.Config` 已是本地定义，无需改。
   - settlement 包如果引用 `common/config.PlatformConfig`，保持不变（子结构仍从 common/config 导入）。

**验证**：
- `go build ./...` + `go vet ./...` 通过。
- 启动 game，检查配置加载正确（特别是 algorithm.yaml）。
- 启动 gateway，检查配置加载正确。
- 启动 stats，检查配置加载正确，端口 8082。

### Phase 3：P1.3 统一 ServiceDiscovery 位置

**目标**：将 `gateway/discovery/` 移到 `common/discovery/`，全仓库复用。

**步骤**：
1. 将 [gateway/discovery/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/discovery/) 整个目录移动到 `common/discovery/`。
2. 修改 import path：`github.com/cashparty/backend/gateway/discovery` → `github.com/cashparty/backend/common/discovery`。
3. 修改 [gateway/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go) 中 import。
4. 修改 [gateway/service/*.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/service/)、[gateway/handler/*.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/handler/) 中 import。

**验证**：
- `go build ./...` 通过。
- 启动 gateway，检查通过 ServiceDiscovery 调 game 的 gRPC 正常。

### Phase 4：P1.4 统一 bootstrap 装配

**目标**：game/gateway bootstrap 都用 `initNacos` + `reloadXxxConfigFromNacos` helper 风格。

**步骤**：
1. 修改 [game/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go)：
   - 抽出 `initNacos`、`reloadMainConfigFromNacos`、`loadAlgorithmConfigFromNacos` 三个 helper。
   - `NewApplicationWithConfig` 改为调用这三个 helper（见 §3.6 调用顺序）。
   - `RegisterService` 失败从 `logger.Error` 改为 `logger.Warn`（可恢复）。

2. 修改 [gateway/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go)：
   - `reloadConfigFromNacos` 重命名为 `reloadMainConfigFromNacos`。
   - 抽出 `loadRateLimiterConfigFromNacos` helper。
   - `RegisterService` 失败从 `logger.Error` 改为 `logger.Warn`。

**验证**：
- `go build ./...` 通过。
- 启动 game，检查 nacos 注册成功 + 主配置 + algorithm 配置从 nacos 加载。
- 模拟 nacos 不可用：停止 nacos，启动 game，检查是否 Warn 后继续启动。

### Phase 4.5：P1.5 统一热更新回调注册

**目标**：消除热更新回调的 inline 风格分歧。**不补齐主配置热更新**——主配置仅在启动时拉取一次。

**步骤**：
1. 在 [game/bootstrap/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/) 新建 `config_listener.go`（见附录 C）：
   - `registerConfigListeners` 只调用 `registerAlgorithmConfigListener`（**不**调用主配置 listener）。
   - 有 panic recovery。
   - 错误级别统一 `Warn`。

2. 在 [gateway/bootstrap/](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/) 新建 `config_listener.go`：
   - `registerConfigListeners` 只调用 `registerRateLimiterConfigListener`。
   - 同样有 panic recovery。

3. 在 `Application.Start()` 中调用 `registerConfigListeners(a.nacos, a.config, a.Container)`，替代 inline 的 `go func() { a.nacos.ListenConfig(...) }()`。

**验证**：
- `go build ./...` 通过。
- 启动 game，修改 nacos 上的 algorithm 配置，检查日志有 "algorithm config reloaded"。
- 启动 gateway，修改 nacos 上的 rateLimiter 配置，检查日志有 "rate limiter config reloaded"。
- 模拟配置解析失败（写错误 yaml），检查日志是 Warn 不是 Error，且服务继续运行。

### Phase 5：P2 代码质量

**目标**：错误处理、Close 生命周期、错误包装全部合规。

**步骤**：
1. [common/nacos/client.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/client.go)：
   - `Close()` 改为 `Close() error`，透传 `DeregisterService` 的 error。
   - 新增 `closed bool` 字段，`Close` 重复调用返回 nil。
   - `DeregisterService` 删除 log（只 return error）。
   - `ServerAddr` 解析改用 `net.SplitHostPort` 或简单 strings.Split（删除 `fmt.Sscanf`）。

2. [common/nacos/errors.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/common/nacos/errors.go) 新建：
   ```go
   package nacos
   import "errors"
   var ErrServiceNameEmpty = errors.New("service name is empty")
   ```

3. [game/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/game/bootstrap/app.go) `Stop()`：
   - `a.nacos.Close()` 改为 `if err := a.nacos.Close(); err != nil { logger.Warn(...) }`。

4. [gateway/bootstrap/app.go](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/gateway/bootstrap/app.go) `Stop()`：同上。

**验证**：
- `go build ./...` + `go vet ./...` 通过。
- 重复调用 `nacosClient.Close()`，第二次返回 nil，无 panic。
- game/gateway 正常停止，nacos 控制台实例下线。

---

## 五、文件级改动清单

| Phase | 文件 | 改动 | 行数变化 |
|---|---|---|---|
| P1 | `common/nacos/grpc_manager.go` | **删除整个文件** | -220 行 |
| P1 | `common/nacos/client.go` | 删除 `listeners` 字段、`GetOneInstance`、`PublishConfig`、`SubscribeService` | -80 行 |
| P1 | `common/nacos/config.go` | 删除 `DefaultClientConfig()` | -15 行 |
| P2 | `common/config/nacos.go` | **新建**：`NacosConfig`（共用基础）+ `SetNacosDefaults` | +60 行 |
| P2 | `common/nacos/client.go` | `NewClient` 签名改为 `NewClient(cfg *config.NacosConfig)` | +5 行 -10 行 |
| P2 | `common/nacos/config.go` | **删除整个文件**（`ClientConfig` 已被 `common/config.NacosConfig` 替代） | -40 行 |
| P2 | `gateway/config/nacos.go` | **新建**：`GatewayNacosConfig`（嵌入 + gateway 独有 DataID） | +20 行 |
| P2 | `gateway/config/config.go` | 删除本地 `NacosConfig`，`Config.Nacos` 改为 `GatewayNacosConfig`，`ServicePort` 改 `uint64` | -20 行 +2 行 |
| P2 | `gateway/bootstrap/app.go` | `initNacos` 改为 `NewClient(&cfg.Nacos.NacosConfig)`，删除类型转换 | +1 行 -2 行 |
| P2.5 | `common/config/config.go` | **删除聚合 Config struct**，拆分子结构到独立文件 | -200 行 |
| P2.5 | `common/config/redis.go` | **新建**：`RedisConfig` + `SetRedisDefaults` | +30 行 |
| P2.5 | `common/config/mysql.go` | **新建**：`MySQLConfig` + `SetMySQLDefaults` | +20 行 |
| P2.5 | `common/config/log.go` | **新建**：`LogConfig` + `SetLogDefaults` | +20 行 |
| P2.5 | `common/config/server.go` | **新建**：`ServerConfig` + `SetServerDefaults` | +20 行 |
| P2.5 | `common/config/types.go` | **新建**：其他共用子结构（Timeout/Broadcast/Kafka/Platform/GameService/IDGenerator/Avatar/Robot/Lua） | +80 行 |
| P2.5 | `common/config/loader.go` | **新建**：`LoadYAML`/`LoadYAMLFromContent` | +30 行 |
| P2.5 | `common/config/defaults.go` | **删除聚合 setDefaults**，拆分已移到各子结构文件 | -180 行 |
| P2.5 | `game/config/config.go` | **新建**：`Config` 聚合 + `Load`/`LoadFromContent` | +60 行 |
| P2.5 | `game/config/nacos.go` | **新建**：`GameNacosConfig` | +15 行 |
| P2.5 | `game/config/algorithm.go` | **新建**：`AlgorithmConfig` + `LoadAlgorithm`/`LoadAlgorithmFromContent` | +50 行 |
| P2.5 | `game/config/defaults.go` | **新建**：`setDefaults` | +40 行 |
| P2.5 | `game/bootstrap/app.go` | import 改为 `game/config`，调用方式调整 | +5 行 -5 行 |
| P2.5 | `game/**/*.go` | `config.Config` 引用改为 `gameconfig.Config`（如有） | ~10 处 |
| P2.5 | `gateway/config/config.go` | yaml.v3 改 viper，拆分子文件，字段加 `mapstructure` tag | +20 行 -10 行 |
| P2.5 | `gateway/config/router.go` | **新建**：`LoadRouterConfig`/`LoadRouterConfigFromContent` | +20 行 |
| P2.5 | `gateway/config/rate_limiter.go` | **新建**：`RateLimiterConfig` + `LoadRateLimiterFromContent` | +30 行 |
| P2.5 | `gateway/config/defaults.go` | **新建**：`setDefaults` | +40 行 |
| P2.5 | `stats/config/config.go` | yaml.v3 改 viper，复用 common 子结构 | +10 行 -30 行 |
| P2.5 | `stats/config/defaults.go` | **新建**：`setDefaults` | +30 行 |
| P3 | `gateway/discovery/` | **删除整个目录** | -205 行 |
| P3 | `common/discovery/discovery.go` | **新建**（从 gateway/discovery 移过来） | +205 行 |
| P3 | `gateway/bootstrap/app.go`、`gateway/service/*.go`、`gateway/handler/*.go` | import path 改 | ~5 行 |
| P4 | `game/bootstrap/app.go` | 抽 3 个 helper | +60 行 -40 行 |
| P4 | `gateway/bootstrap/app.go` | 重命名 helper + 抽 `loadRateLimiterConfigFromNacos` | +20 行 -10 行 |
| P4.5 | `game/bootstrap/config_listener.go` | **新建**：`registerConfigListeners` + `registerAlgorithmConfigListener`（不含主配置 listener） | +50 行 |
| P4.5 | `gateway/bootstrap/config_listener.go` | **新建**：`registerConfigListeners` + `registerRateLimiterConfigListener`（不含主配置 listener） | +50 行 |
| P4.5 | `game/bootstrap/app.go` | `Start()` 中删除 inline `ListenConfig`，改为调用 `registerConfigListeners` | -25 行 +1 行 |
| P4.5 | `gateway/bootstrap/app.go` | `Start()` 中删除 inline `ListenConfig`，改为调用 `registerConfigListeners` | -25 行 +1 行 |
| P5 | `common/nacos/client.go` | `Close() error` + `closed` 字段 + 删除 log + `ServerAddr` 解析重构 | +15 行 -10 行 |
| P5 | `common/nacos/errors.go` | **新建** | +5 行 |
| P5 | `game/bootstrap/app.go` | `Stop()` 调 `nacosClient.Close()` 处理 error | +3 行 |
| P5 | `gateway/bootstrap/app.go` | `Stop()` 调 `nacosClient.Close()` 处理 error | +3 行 |

**净变化**：删除 ~990 行，新增 ~780 行，净减少 ~210 行。

---

## 六、验证方案

### 6.1 编译验证

每个 Phase 结束后：
```bash
cd backend && go build ./... && go vet ./... && gofmt -l .
```

### 6.2 集成测试矩阵

| 用例 | 期望行为 |
|---|---|
| nacos 正常，启动 game | 日志有 "service registered to nacos"；nacos 控制台可见 game 实例 |
| nacos 正常，启动 gateway | 日志有 "service registered to nacos"；gateway 从 nacos 拉到主配置、router 配置、rateLimiter 配置 |
| nacos 正常，gateway 调 game | gateway 通过 ServiceDiscovery 找到 game 实例，gRPC 调用成功 |
| nacos 主配置变更 | **不触发热更新**——主配置仅在启动时拉取一次；变更需重启服务生效 |
| nacos algorithm 配置变更 | game 触发 ListenConfig callback，日志有 "algorithm config reloaded"，PacketGenerator 更新 |
| nacos rateLimiter 配置变更 | gateway 触发 ListenConfig callback，日志有 "rate limiter config reloaded"，RateLimiter 更新 |
| nacos 不可用，启动 game | Warn 后继续启动；nacos 控制台无实例（但 game 自身功能正常） |
| nacos 不可用，启动 gateway | Warn 后继续启动；gateway 用本地 yaml 配置 |
| nacos 不可用，启动 stats | stats 不用 nacos，正常启动 |
| game 正常停止 | 日志有 "service deregistered from nacos"；nacos 控制台实例下线 |
| gateway 正常停止 | 同上 |
| 重复调用 `nacosClient.Close()` | 第二次返回 nil，无 panic（Phase 5 后的行为） |
| nacos 上 algorithm 配置 yaml 格式错误 | game Warn 后用旧 algorithm 配置继续运行 |
| nacos 上主配置 yaml 格式错误 | game/gateway 启动时 `reloadMainConfigFromNacos` Warn 后用本地 yaml 配置继续运行 |
| listener goroutine panic | 日志有 "panic" + stack，服务继续运行（Phase 4.5 后的行为） |
| nacos 未启用，启动 game | algorithm.yaml 从本地加载 |
| stats 启动 | 本地 yaml 配置正确加载，端口 8082（不是 8081） |

### 6.3 回归测试

- game 创建房间、抢红包、结算流程正常。
- gateway WebSocket 连接、token 验证、路由转发正常。
- stats 统计查询正常。

---

## 七、回滚预案

### 7.1 各阶段回滚

- Phase 1（删死代码）几乎无风险，不需要回滚预案。
- Phase 2（统一 NacosConfig 分层）若出问题，回滚 PR 即可。
- Phase 2.5（统一配置加载 + 创建 game/config）若出问题，回滚 PR 即可。注意 viper 与 yaml.v3 行为差异（如环境变量覆盖、key 大小写）。
- Phase 3（移动 discovery）若出问题，回滚 PR 即可，纯文件移动。
- Phase 4（统一 bootstrap）若出问题，回滚 PR 即可。注意 Phase 4 引入的行为变化（algorithm 配置失败从 fatal 改 Warn）需要单独评估。
- Phase 4.5（统一热更新回调）若出问题，回滚 PR 即可。本阶段不引入主配置热更新，仅统一模块专属配置的回调注册风格。
- Phase 5（Close 生命周期）若出问题，回滚 PR 即可。

### 7.2 紧急回滚

如果重构合并后线上出问题：
1. `git revert <merge-commit>` 回滚对应 PR。
2. 重新部署。
3. 检查 nacos 控制台服务实例状态。

### 7.3 灰度策略

- Phase 1 可直接合并（纯删除）。
- Phase 2 + 2.5 一起合并，测试环境跑 2 天（配置加载是核心功能）。
- Phase 3 单独合并，测试环境验证 gRPC 调用。
- Phase 4 单独合并，测试环境验证 bootstrap 行为。
- Phase 4.5 单独合并，测试环境跑 1 天。
- Phase 5 单独合并，测试环境验证 Close 生命周期。

---

## 八、风险与权衡

### 8.1 风险

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| Phase 2 中 NacosConfig 嵌入（squash/inline）tag 不兼容导致配置加载失败 | 中 | 高 | 测试环境验证 viper 和 yaml.v3 都能正确解析嵌入结构 |
| Phase 2.5 中 viper 与 yaml.v3 行为差异（如环境变量覆盖、key 大小写、duration 解析） | 高 | 高 | 测试环境全面验证；duration 字段需要 `mapstructure.StringToTimeDurationHookFunc` |
| Phase 2.5 中 `Load` 不再附带加载 algorithm.yaml | 中 | 中 | game/bootstrap 显式调用 `LoadAlgorithm`，测试环境验证 |
| Phase 2.5 中 game 引用 `common/config.Config` 改为 `game/config.Config` 遗漏 | 中 | 中 | `go build ./...` 兜底；全量 grep `common/config.Config` 确认无遗漏 |
| Phase 2.5 中 stats `Config` struct 改用 `common/config` 子结构 | 中 | 中 | stats 独有的 `AmountRanges` 保留在本地 |
| Phase 4 中 algorithm 配置失败从 fatal 改 Warn | 低 | 高 | Warn 日志必须醒目；监控 Warn 告警 |
| Phase 3 中 discovery 移动后 import 遗漏 | 低 | 低 | `go build ./...` + `go vet ./...` 兜底 |
| Phase 5 中 `Close() error` 签名变化导致接口不兼容 | 低 | 低 | `nacos.Client` 是具体类型不是接口 |
| nacos sdk 内部 goroutine 在 `Close` 后未退出导致泄漏 | 中 | 中 | Phase 5 验证时用 `runtime.NumGoroutine()` 对比 |

### 8.2 权衡决策

#### 决策 1：保留 `SubscribeService` 还是删除？

- **选择**：删除。
- **理由**：无调用方，且 `DiscoverService` + `nacosResolver` 10s 轮询已满足需求。

#### 决策 2：`ServiceDiscovery` 是否支持多 nacos 集群？

- **选择**：不支持，单 nacos 集群。
- **理由**：当前所有服务用同一 nacos 集群。

#### 决策 3：`loadAlgorithmConfigFromNacos` 失败时 fatal 还是 Warn？

- **选择**：Warn + 继续用旧配置（与 gateway 行为对齐）。
- **理由**：nacos 短暂故障不应导致服务起不来。

#### 决策 4：`common/config` 是否保留聚合 `Config` struct？

- **选择**：**不保留**。`common/config` 只提供共用子结构和工具函数。
- **理由**：
  - 每个服务需要的字段不同（game 需要 Algorithm/Robot，gateway 需要 Merchant/Token/RateLimiter，stats 需要 AmountRanges）。
  - 强行聚合会导致 `common/config.Config` 膨胀，且各服务用不到的字段会造成困惑。
  - 各服务有自己的 `Config` struct 更清晰，符合"单一职责"。
- **代价**：每个服务需要写自己的 `Config` struct + `Load`/`LoadFromContent`。但通过 `common/config.LoadYAML` 工具函数复用加载逻辑，重复代码很少。

#### 决策 5：`Load` 是否附带加载 algorithm.yaml？

- **选择**：**不附带**。`Load` 只加载主配置；algorithm 配置通过 `LoadAlgorithm` 显式加载。
- **理由**：
  - 附带加载导致 `LoadFromContent`（nacos 用）行为不一致。
  - 显式调用 `LoadAlgorithm` 更清晰。

#### 决策 6：viper 还是 yaml.v3？

- **选择**：viper。
- **理由**：
  - `common/config` 已用 viper，统一即可。
  - viper 支持环境变量覆盖、多种格式。
- **代价**：gateway/stats 需要从 yaml.v3 迁移到 viper。

#### 决策 7：主配置是否需要热更新？

- **选择**：**不热更新**。主配置仅在启动时通过 `reloadMainConfigFromNacos` 拉取一次。
- **理由**：
  - 主配置中的 Redis/MySQL 连接池、gRPC server 端口等字段不可热更新。
  - 主配置变更通常是低频操作，重启服务可接受。
  - 仅模块专属配置（algorithm/rateLimiter）支持热更新。

#### 决策 8：NacosConfig 放 common 还是各服务？

- **选择**：**分层**。共用基础放 `common/config.NacosConfig`，服务独有 DataID 通过嵌入扩展。
- **理由**：
  - `AlgorithmDataID` 只有 game 用，`RouterDataID`/`RateLimiterDataID` 只有 gateway 用。
  - 全放 common 会导致 `NacosConfig` 字段膨胀，且语义不清（game 看到不属于自己的字段）。
  - 嵌入扩展既复用共用部分，又保持服务独有字段的归属清晰。
- **代价**：`nacos.NewClient` 需要传入 `&cfg.Nacos.NacosConfig`（嵌入的基础部分），略繁琐但清晰。

#### 决策 9：stats 是否应该复用 `common/config` 的子结构？

- **选择**：复用子结构，保留本地 `Config` struct。
- **理由**：
  - stats 不需要 nacos/algorithm/robot 等字段。
  - 但 stats 的 `RedisConfig`/`MySQLConfig`/`LogConfig`/`ServerConfig` 应该复用 `common/config` 的定义，避免重复。
  - stats 独有的 `AmountRanges` 保留在本地。

---

## 九、执行检查清单

每个阶段合并前必须确认：

- [ ] `go build ./...` 通过
- [ ] `go vet ./...` 通过
- [ ] `gofmt -l .` 无输出
- [ ] 新代码符合 [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) 相关条款
- [ ] 集成测试矩阵（§6.2）全部通过
- [ ] PR 描述包含：改动文件清单、行为变化说明、回滚方式
- [ ] 至少 1 人 code review 通过

全部阶段完成后确认：

- [ ] [NACOS_REVIEW.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/NACOS_REVIEW.md) 中 12 类问题全部解决
- [ ] 配置加载风格统一（viper + 对称 Load/LoadFromContent + 分层 NacosConfig）
- [ ] 热更新回调风格统一（不含主配置 listener）
- [ ] [CODING_STANDARD.md](file:///Users/aaron.pan/Desktop/party/RedPacket-master/backend/CODING_STANDARD.md) 的"必须收敛"项中 nacos + config 相关条目可标记为已完成
- [ ] 更新 [project_memory.md](file:///Users/aaron.pan/.trae-cn/memory/projects/-Users-aaron-pan-Desktop-party/project_memory.md) 记录重构完成

---

## 十、附录

### 附录 A：目标 common/config/nacos.go 完整骨架

```go
package config

// NacosConfig 是 nacos 共用配置基础。各服务独有的 DataID 通过嵌入扩展。
type NacosConfig struct {
    // --- 连接 ---
    Enabled     bool   `mapstructure:"enabled" yaml:"enabled"`
    ServerAddr  string `mapstructure:"server_addr" yaml:"server_addr"`
    Namespace   string `mapstructure:"namespace" yaml:"namespace"`
    Group       string `mapstructure:"group" yaml:"group"`
    Username    string `mapstructure:"username" yaml:"username"`
    Password    string `mapstructure:"password" yaml:"password"`

    // --- 服务注册 ---
    ServiceName string `mapstructure:"service_name" yaml:"service_name"`
    ServiceAddr string `mapstructure:"service_addr" yaml:"service_addr"`
    ServicePort uint64 `mapstructure:"service_port" yaml:"service_port"`

    // --- 主配置 DataID ---
    ConfigDataID string `mapstructure:"config_data_id" yaml:"config_data_id"`
    ConfigGroup  string `mapstructure:"config_group" yaml:"config_group"`

    // --- sdk 调优 ---
    TimeoutMs uint64 `mapstructure:"timeout_ms" yaml:"timeout_ms"`
    LogLevel  string `mapstructure:"log_level" yaml:"log_level"`
    LogDir    string `mapstructure:"log_dir" yaml:"log_dir"`
    CacheDir  string `mapstructure:"cache_dir" yaml:"cache_dir"`
}

// SetNacosDefaults 设置共用 NacosConfig 的默认值。
func SetNacosDefaults(cfg *NacosConfig) {
    if cfg.ServerAddr == "" {
        cfg.ServerAddr = "127.0.0.1:8848"
    }
    if cfg.Group == "" {
        cfg.Group = "DEFAULT_GROUP"
    }
    if cfg.Username == "" {
        cfg.Username = "nacos"
    }
    if cfg.Password == "" {
        cfg.Password = "nacos"
    }
    if cfg.ConfigGroup == "" {
        cfg.ConfigGroup = "DEFAULT_GROUP"
    }
    if cfg.TimeoutMs == 0 {
        cfg.TimeoutMs = 5000
    }
    if cfg.LogLevel == "" {
        cfg.LogLevel = "warn"
    }
    if cfg.LogDir == "" {
        cfg.LogDir = "/tmp/nacos/log"
    }
    if cfg.CacheDir == "" {
        cfg.CacheDir = "/tmp/nacos/cache"
    }
}
```

### 附录 B：目标 game/config 包完整骨架

```go
// game/config/nacos.go
package config

import (
    commonconfig "github.com/cashparty/backend/common/config"
)

// GameNacosConfig 扩展共用 NacosConfig，添加 game 独有的 DataID。
type GameNacosConfig struct {
    commonconfig.NacosConfig `mapstructure:",squash" yaml:",inline"`
    AlgorithmDataID          string `mapstructure:"algorithm_data_id" yaml:"algorithm_data_id"`
    AlgorithmGroup           string `mapstructure:"algorithm_group" yaml:"algorithm_group"`
}
```

```go
// game/config/config.go
package config

import (
    commonconfig "github.com/cashparty/backend/common/config"
)

type Config struct {
    Server      commonconfig.ServerConfig      `mapstructure:"server" yaml:"server"`
    Timeout     commonconfig.TimeoutConfig     `mapstructure:"timeout" yaml:"timeout"`
    Redis       commonconfig.RedisConfig       `mapstructure:"redis" yaml:"redis"`
    MySQL       commonconfig.MySQLConfig       `mapstructure:"mysql" yaml:"mysql"`
    Kafka       commonconfig.KafkaConfig       `mapstructure:"kafka" yaml:"kafka"`
    Broadcast   commonconfig.BroadcastConfig   `mapstructure:"broadcast" yaml:"broadcast"`
    Platform    commonconfig.PlatformConfig    `mapstructure:"platform" yaml:"platform"`
    Log         commonconfig.LogConfig         `mapstructure:"log" yaml:"log"`
    GameService commonconfig.GameServiceConfig `mapstructure:"game_service" yaml:"game_service"`
    Nacos       GameNacosConfig                `mapstructure:"nacos" yaml:"nacos"`
    Algorithm   AlgorithmConfig                `mapstructure:"algorithm" yaml:"algorithm"`
    IDGenerator commonconfig.IDGeneratorConfig `mapstructure:"id_generator" yaml:"id_generator"`
    Avatar      commonconfig.AvatarConfig      `mapstructure:"avatar" yaml:"avatar"`
    Robot       commonconfig.RobotConfig       `mapstructure:"robot" yaml:"robot"`
    Lua         commonconfig.LuaConfig         `mapstructure:"lua" yaml:"lua"`
}

func Load(path string) (*Config, error) {
    var cfg Config
    if err := commonconfig.LoadYAML(path, &cfg); err != nil {
        return nil, err
    }
    setDefaults(&cfg)
    return &cfg, nil
}

func LoadFromContent(content string) (*Config, error) {
    var cfg Config
    if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
        return nil, err
    }
    setDefaults(&cfg)
    return &cfg, nil
}
```

```go
// game/config/algorithm.go
package config

import (
    commonconfig "github.com/cashparty/backend/common/config"
)

type AlgorithmConfig struct {
    MinPacketAmount     *int64               `mapstructure:"min_packet_amount" yaml:"min_packet_amount"`
    StraightProbability *float64             `mapstructure:"straight_probability" yaml:"straight_probability"`
    LeopardProbability  *float64             `mapstructure:"leopard_probability" yaml:"leopard_probability"`
    RewardControl       *RewardControlConfig `mapstructure:"reward_control" yaml:"reward_control"`
}

// ... (RewardControlConfig, RoomRewardConfig 同现有)

func LoadAlgorithm(path string) (*AlgorithmConfig, error) {
    var cfg AlgorithmConfig
    if err := commonconfig.LoadYAML(path, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

func LoadAlgorithmFromContent(content string) (*AlgorithmConfig, error) {
    var cfg AlgorithmConfig
    if err := commonconfig.LoadYAMLFromContent(content, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}
```

```go
// game/config/defaults.go
package config

import (
    commonconfig "github.com/cashparty/backend/common/config"
)

func setDefaults(cfg *Config) {
    commonconfig.SetServerDefaults(&cfg.Server)
    commonconfig.SetRedisDefaults(&cfg.Redis)
    commonconfig.SetMySQLDefaults(&cfg.MySQL)
    commonconfig.SetLogDefaults(&cfg.Log)
    commonconfig.SetNacosDefaults(&cfg.Nacos.NacosConfig) // 传入嵌入的基础部分

    // game 独有
    if cfg.Nacos.AlgorithmGroup == "" {
        cfg.Nacos.AlgorithmGroup = "DEFAULT_GROUP"
    }
}
```

### 附录 C：目标 game/bootstrap/config_listener.go 完整骨架

```go
package bootstrap

import (
    "runtime/debug"

    gameconfig "github.com/cashparty/backend/game/config"
    "github.com/cashparty/backend/common/logger"
    "github.com/cashparty/backend/common/nacos"
)

// registerConfigListeners registers all nacos config hot-reload listeners.
// Called in Start(), failures are Warned but do not block startup.
//
// 注意：主配置（ConfigDataID）不在此注册——主配置仅在启动时通过
// reloadMainConfigFromNacos 拉取一次，不热更新。主配置变更需重启服务生效。
func registerConfigListeners(nacosClient *nacos.Client, cfg *gameconfig.Config, container *Container) {
    if nacosClient == nil {
        return
    }
    registerAlgorithmConfigListener(nacosClient, cfg, container)
}

func registerAlgorithmConfigListener(nacosClient *nacos.Client, cfg *gameconfig.Config, container *Container) {
    if cfg.Nacos.AlgorithmDataID == "" {
        return
    }
    go func() {
        defer func() {
            if r := recover(); r != nil {
                logger.Error("algorithm config listener panic",
                    "data_id", cfg.Nacos.AlgorithmDataID,
                    "panic", r,
                    "stack", string(debug.Stack()))
            }
        }()
        if err := nacosClient.ListenConfig(
            cfg.Nacos.AlgorithmDataID,
            cfg.Nacos.AlgorithmGroup,
            func(content string) {
                algoCfg, err := gameconfig.LoadAlgorithmFromContent(content)
                if err != nil {
                    logger.Warn("parse algorithm config from nacos failed, keep old config",
                        "data_id", cfg.Nacos.AlgorithmDataID,
                        "error", err)
                    return
                }
                newConfig := convertAlgorithmConfig(algoCfg)
                container.PacketGenerator.UpdateConfig(newConfig)
                logger.Info("algorithm config reloaded from nacos",
                    "data_id", cfg.Nacos.AlgorithmDataID)
            },
        ); err != nil {
            logger.Warn("listen algorithm config failed",
                "data_id", cfg.Nacos.AlgorithmDataID,
                "error", err)
        }
    }()
}
```

### 附录 D：目标 common/nacos/client.go 关键骨架

```go
package nacos

import (
    "errors"
    "fmt"
    "net"
    "strconv"
    "sync"

    "github.com/cashparty/backend/common/config"
    "github.com/cashparty/backend/common/logger"
    "github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
    "github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
    "github.com/nacos-group/nacos-sdk-go/v2/model"
    "github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type Client struct {
    configClient config_client.IConfigClient
    namingClient naming_client.INamingClient
    cfg          *config.NacosConfig
    serviceName  string
    serviceAddr  string
    servicePort  uint64
    mu           sync.Mutex
    closed       bool
}

func NewClient(cfg *config.NacosConfig) (*Client, error) {
    if cfg == nil {
        return nil, errors.New("nacos config is nil")
    }
    host, port := parseServerAddr(cfg.ServerAddr)
    serverConfigs := []constant.ServerConfig{
        {IpAddr: host, Port: port},
    }
    clientConfig := constant.ClientConfig{
        NamespaceId:         cfg.Namespace,
        Username:            cfg.Username,
        Password:            cfg.Password,
        TimeoutMs:           cfg.TimeoutMs,
        NotLoadCacheAtStart: true,
        LogDir:              cfg.LogDir,
        CacheDir:            cfg.CacheDir,
        LogLevel:            cfg.LogLevel,
    }
    // ... sdk 初始化
    return &Client{
        cfg:         cfg,
        serviceName: cfg.ServiceName,
        serviceAddr: cfg.ServiceAddr,
        servicePort: cfg.ServicePort,
        // ...
    }, nil
}

// parseServerAddr 解析 ServerAddr（host 或 host:port）。
// 使用 net.SplitHostPort 替代 fmt.Sscanf，更健壮。
func parseServerAddr(addr string) (string uint64) {
    if !strings.Contains(addr, ":") {
        return addr, 8848
    }
    host, portStr, err := net.SplitHostPort(addr)
    if err != nil {
        return addr, 8848
    }
    port, err := strconv.ParseUint(portStr, 10, 64)
    if err != nil {
        return host, 8848
    }
    return host, port
}

func (c *Client) RegisterService() error {
    if c.serviceName == "" {
        return ErrServiceNameEmpty
    }
    success, err := c.namingClient.RegisterInstance(vo.RegisterInstanceParam{
        Ip:          c.serviceAddr,
        Port:        c.servicePort,
        ServiceName: c.serviceName,
        GroupName:   c.cfg.Group,
        Healthy:     true,
        Enable:      true,
        Weight:      1.0,
    })
    if err != nil {
        return fmt.Errorf("register service %s failed: %w", c.serviceName, err)
    }
    if !success {
        return fmt.Errorf("register service %s failed: sdk returned false", c.serviceName)
    }
    logger.Info("service registered to nacos",
        "service", c.serviceName,
        "address", fmt.Sprintf("%s:%d", c.serviceAddr, c.servicePort),
        "group", c.cfg.Group)
    return nil
}

func (c *Client) DeregisterService() error {
    if c.serviceName == "" {
        return nil
    }
    success, err := c.namingClient.DeregisterInstance(vo.DeregisterInstanceParam{
        Ip:          c.serviceAddr,
        Port:        c.servicePort,
        ServiceName: c.serviceName,
        GroupName:   c.cfg.Group,
    })
    if err != nil {
        return fmt.Errorf("deregister service %s failed: %w", c.serviceName, err)
    }
    if !success {
        return fmt.Errorf("deregister service %s failed: sdk returned false", c.serviceName)
    }
    logger.Info("service deregistered from nacos", "service", c.serviceName)
    return nil
}

func (c *Client) Close() error {
    c.mu.Lock()
    defer c.mu.Unlock()
    if c.closed {
        return nil
    }
    c.closed = true
    return c.DeregisterService()
}
```

### 附录 E：目标 bootstrap helper 骨架（game 示例）

```go
// game/bootstrap/nacos_helper.go
package bootstrap

import (
    "fmt"

    gameconfig "github.com/cashparty/backend/game/config"
    "github.com/cashparty/backend/common/logger"
    "github.com/cashparty/backend/common/nacos"
)

func initNacos(cfg *gameconfig.Config) *nacos.Client {
    if !cfg.Nacos.Enabled {
        return nil
    }
    client, err := nacos.NewClient(&cfg.Nacos.NacosConfig) // 传入嵌入的基础部分
    if err != nil {
        logger.Warn("failed to create nacos client, nacos disabled", "error", err)
        return nil
    }
    return client
}

func reloadMainConfigFromNacos(client *nacos.Client, oldCfg *gameconfig.Config) *gameconfig.Config {
    content, err := client.GetConfig(oldCfg.Nacos.ConfigDataID, oldCfg.Nacos.ConfigGroup)
    if err != nil {
        logger.Warn("get main config from nacos failed, keep local config",
            "data_id", oldCfg.Nacos.ConfigDataID, "error", err)
        return oldCfg
    }
    newCfg, err := gameconfig.LoadFromContent(content)
    if err != nil {
        logger.Warn("parse main config from nacos failed, keep local config",
            "data_id", oldCfg.Nacos.ConfigDataID, "error", err)
        return oldCfg
    }
    // 保留 nacos 运行时配置（不能被远程覆盖）
    newCfg.Nacos.NacosConfig = oldCfg.Nacos.NacosConfig
    logger.Info("main config loaded from nacos",
        "data_id", oldCfg.Nacos.ConfigDataID)
    return newCfg
}

func loadAlgorithmConfigFromNacos(client *nacos.Client, cfg *gameconfig.Config) (*gameconfig.AlgorithmConfig, error) {
    if client == nil || cfg.Nacos.AlgorithmDataID == "" {
        return nil, nil
    }
    content, err := client.GetConfig(cfg.Nacos.AlgorithmDataID, cfg.Nacos.AlgorithmGroup)
    if err != nil {
        return nil, fmt.Errorf("get algorithm config from nacos failed: %w", err)
    }
    algoCfg, err := gameconfig.LoadAlgorithmFromContent(content)
    if err != nil {
        return nil, fmt.Errorf("parse algorithm config from nacos failed: %w", err)
    }
    logger.Info("algorithm config loaded from nacos",
        "data_id", cfg.Nacos.AlgorithmDataID)
    return algoCfg, nil
}
```
