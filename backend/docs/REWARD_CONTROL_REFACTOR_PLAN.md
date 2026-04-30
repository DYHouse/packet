# 顺子和豹子概率控制规则重构方案

## 一、需求概述

| 需求项 | 描述 |
|--------|------|
| **保底中奖** | 某些房间一个轮次内顺子和豹子可以各中一局，不受总开关控制 |
| **概率控制** | 支持按概率触发顺子和豹子 |
| **总开关** | 控制是否开启顺子和豹子的概率中奖 |
| **收益比例触发** | 平台收益达到一定比例才开启概率中奖 |

---

## 二、架构设计

```
┌─────────────────────────────────────────────────────────────────────┐
│                          PacketGenerator                             │
│  ┌───────────────────────────────────────────────────────────────┐  │
│  │                      RewardController                          │  │
│  │  ┌────────────────┐  ┌────────────────┐  ┌──────────────────┐ │  │
│  │  │ 保底中奖逻辑    │  │ 概率触发逻辑   │  │ 开关/收益控制    │ │  │
│  │  │ (不受开关控制)  │  │ (受开关控制)   │  │                  │ │  │
│  │  └────────────────┘  └────────────────┘  └──────────────────┘ │  │
│  └───────────────────────────────────────────────────────────────┘  │
│                                ↓                                     │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────────┐  │
│  │StraightGen   │  │LeopardGen    │  │ NormalGen                │  │
│  └──────────────┘  └──────────────┘  └──────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 三、数据来源说明

### 3.1 轮次局数 (MaxRounds)

**来源：房间缓存 `RoomMeta.MaxRounds`**

从 Redis 房间缓存中获取，无需单独配置：

```go
// 从房间缓存获取
roomMeta, _ := roomRepo.GetRoomMeta(ctx, roomID)
maxRounds := roomMeta.MaxRounds  // 如：10局
```

### 3.2 平台收益比例

**来源：实时统计 (Redis)**

复用现有的 RTPTracker 机制，实时统计平台收益：

```
收益比例 = (总投注 - 总派奖 - 总奖励) / 总投注
```

**数据流程：**

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  每局游戏    │ ──→ │ Redis实时   │ ──→ │ 收益比例    │
│  结算数据    │     │ 累加统计    │     │ 计算        │
└─────────────┘     └─────────────┘     └─────────────┘
```

**Redis Key 设计：**

| Key | 说明 | 过期时间 |
|-----|------|----------|
| `profit:daily:{date}` | 当日收益统计 Hash | 7天 |
| `profit:daily:{date}:total_bet` | 当日总投注 | - |
| `profit:daily:{date}:total_win` | 当日总派奖 | - |
| `profit:daily:{date}:total_reward` | 当日总奖励 | - |

**无需跑批**：数据在每局游戏结算时实时写入 Redis，查询时直接计算收益比例。

---

## 四、配置结构设计

### 4.1 配置文件 (YAML)

```yaml
# config/game.yaml
reward_control:
  # 总开关配置
  global_switch_enabled: false              # 总开关，默认关闭
  profit_ratio_threshold: 0.05              # 收益比例阈值(5%)，达到后才开启概率触发
  
  # 房间级别配置 (key 为 room_id)
  room_configs:
    1001:  # 房间ID = 1001
      guarantee_enabled: true               # 开启保底中奖
      guarantee_straight: true              # 保底顺子
      guarantee_leopard: true               # 保底豹子
      probability_enabled: true             # 开启概率触发
      straight_probability: 0.08            # 顺子概率
      leopard_probability: 0.002            # 豹子概率
    
    1002:  # 房间ID = 1002
      guarantee_enabled: true
      guarantee_straight: true
      guarantee_leopard: true
      probability_enabled: true
      straight_probability: 0.06
      leopard_probability: 0.001
    
    1003:  # 房间ID = 1003 - 只开概率，无保底
      guarantee_enabled: false
      probability_enabled: true
      straight_probability: 0.05
      leopard_probability: 0.001
    
    1004:  # 房间ID = 1004
      guarantee_enabled: true
      guarantee_straight: true
      guarantee_leopard: false
      probability_enabled: true
      straight_probability: 0.04
      leopard_probability: 0.0005
    
    1005:  # 房间ID = 1005 - 关闭所有奖励
      guarantee_enabled: false
      probability_enabled: false
      straight_probability: 0
      leopard_probability: 0
```

### 4.2 Go 配置结构

```go
// RewardControlConfig 奖励控制配置
type RewardControlConfig struct {
    GlobalSwitchEnabled  bool                        `yaml:"global_switch_enabled"`
    ProfitRatioThreshold float64                     `yaml:"profit_ratio_threshold"`
    RoomConfigs          map[int64]*RoomRewardConfig `yaml:"room_configs"`
}

// RoomRewardConfig 房间奖励配置
type RoomRewardConfig struct {
    GuaranteeEnabled    bool    `yaml:"guarantee_enabled"`    // 是否开启保底中奖
    GuaranteeStraight   bool    `yaml:"guarantee_straight"`   // 保底顺子
    GuaranteeLeopard    bool    `yaml:"guarantee_leopard"`    // 保底豹子
    ProbabilityEnabled  bool    `yaml:"probability_enabled"`  // 是否开启概率触发
    StraightProbability float64 `yaml:"straight_probability"` // 顺子概率
    LeopardProbability  float64 `yaml:"leopard_probability"`  // 豹子概率
}
```

---

## 五、Redis Key 设计

### 5.1 轮次中奖状态

| Key Pattern | 说明 | 过期时间 |
|-------------|------|----------|
| `reward:cycle:{room_id}:{session_id}:straight` | 轮次内顺子已中奖 (0/1) | 24小时 |
| `reward:cycle:{room_id}:{session_id}:leopard` | 轮次内豹子已中奖 (0/1) | 24小时 |
| `reward:cycle:{room_id}:{session_id}:rounds` | 轮次内已进行局数 | 24小时 |

### 5.2 收益统计

| Key Pattern | 说明 | 过期时间 |
|-------------|------|----------|
| `profit:daily:{date}` | 当日收益统计 Hash | 7天 |
| - `total_bet` | 总投注 | - |
| - `total_win` | 总派奖 | - |
| - `total_reward` | 总奖励 | - |

---

## 六、核心逻辑流程

### 6.1 奖励类型判定流程

```
                    ┌─────────────────┐
                    │   开始判定      │
                    └────────┬────────┘
                             │
                             ▼
                    ┌─────────────────┐
                    │ 获取房间配置     │
                    │ (根据 room_id)   │
                    └────────┬────────┘
                             │
              ┌──────────────┴──────────────┐
              │                             │
              ▼                             ▼
    ┌─────────────────┐           ┌─────────────────┐
    │ 1. 保底中奖检查  │           │ 2. 概率触发检查  │
    │ (优先级最高)     │           │                 │
    │ (不受开关控制)   │           │                 │
    └────────┬────────┘           └────────┬────────┘
             │                              │
             │                              ▼
             │                    ┌─────────────────┐
             │                    │ 检查总开关      │
             │                    │ global_switch   │
             │                    └────────┬────────┘
             │                             │
             │                             ▼
             │                    ┌─────────────────┐
             │                    │ 检查收益比例    │
             │                    │ >= 阈值?        │
             │                    └────────┬────────┘
             │                             │
             │                             ▼
             │                    ┌─────────────────┐
             │                    │ 根据配置概率判定  │
             │                    │ StraightProb    │
             │                    │ LeopardProb     │
             │                    └────────┬────────┘
             │                             │
             ▼                             ▼
    ┌─────────────────────────────────────────────┐
    │              返回奖励类型                    │
    │   (RewardTypeStraight / RewardTypeLeopard   │
    │    / RewardTypeNone)                        │
    └─────────────────────────────────────────────┘
```

### 6.2 保底中奖逻辑

```
┌─────────────────────────────────────────────────────────────────┐
│                     保底中奖判定流程                             │
└─────────────────────────────────────────────────────────────────┘

输入: roomID, sessionID, currentRoundNo, maxRounds, roomConfig

1. 检查顺子保底
   ├── 获取 Redis Key: reward:cycle:{roomID}:{sessionID}:straight
   ├── 如果已中奖 (值为1) → 跳过
   └── 如果未中奖 (值为0或不存在)
       ├── 计算剩余局数: remainingRounds = maxRounds - currentRoundNo + 1
       ├── 如果是最后一局 (remainingRounds == 1) → 必中顺子
       └── 否则按均匀分布概率触发: prob = 1 / remainingRounds

2. 检查豹子保底
   ├── 同上逻辑
   └── 返回结果

3. 中奖后设置 Redis 标记
   └── SET reward:cycle:{roomID}:{sessionID}:{type} 1 EX 86400
```

### 6.3 概率触发逻辑

```
┌─────────────────────────────────────────────────────────────────┐
│                     概率触发判定流程                             │
└─────────────────────────────────────────────────────────────────┘

输入: roomConfig, globalSwitchEnabled, profitRatioThreshold

1. 检查总开关
   └── if !globalSwitchEnabled → 返回 RewardTypeNone

2. 检查收益比例
   ├── 从 Redis 获取当日收益统计
   ├── 计算收益比例: profitRatio = (totalBet - totalWin - totalReward) / totalBet
   └── if profitRatio < profitRatioThreshold → 返回 RewardTypeNone

3. 概率判定
   ├── 生成随机数 randVal ∈ [0, 1)
   ├── if randVal < leopardProbability → 返回 RewardTypeLeopard
   ├── randVal -= leopardProbability
   ├── if randVal < straightProbability → 返回 RewardTypeStraight
   └── 否则返回 RewardTypeNone
```

---

## 七、代码实现

### 7.1 新增文件: `reward_controller.go`

```go
package algorithm

import (
    "context"
    "fmt"
    "time"

    cRedis "github.com/cashparty/backend/common/redis"
    crand "crypto/rand"
    "math/big"
    "sync"
)

const (
    TriggerTypeGuarantee   = 1 // 保底触发
    TriggerTypeProbability = 2 // 概率触发
)

type RewardController struct {
    config       *RewardControlConfig
    redis        *cRedis.Client
    rngMu        sync.Mutex
}

func NewRewardController(config *RewardControlConfig, redis *cRedis.Client) *RewardController {
    return &RewardController{
        config: config,
        redis:  redis,
    }
}

// DetermineRewardType 决定奖励类型
// roomID: 房间ID，用于匹配配置和生成 Redis key
// sessionID: 会话ID，用于标识轮次
// currentRoundNo: 当前局号 (1-based)，用于计算剩余局数
// maxRounds: 轮次总局数，从 roomMeta.MaxRounds 获取
func (c *RewardController) DetermineRewardType(
    ctx context.Context,
    roomID int64,
    sessionID int64,
    currentRoundNo int,
    maxRounds int,
) int {
    roomConfig := c.getRoomConfig(roomID)
    if roomConfig == nil {
        return RewardTypeNone
    }

    // 1. 优先检查保底中奖 (不受总开关控制)
    if roomConfig.GuaranteeEnabled {
        if rewardType := c.checkGuarantee(
            ctx, roomID, sessionID, currentRoundNo, maxRounds, roomConfig,
        ); rewardType != RewardTypeNone {
            return rewardType
        }
    }

    // 2. 检查概率触发 (受总开关控制)
    if roomConfig.ProbabilityEnabled && c.isProbabilityAllowed(ctx) {
        return c.checkProbability(roomConfig)
    }

    return RewardTypeNone
}

// checkGuarantee 检查保底中奖
func (c *RewardController) checkGuarantee(
    ctx context.Context,
    roomID, sessionID int64,
    currentRoundNo, maxRounds int,
    config *RoomRewardConfig,
) int {
    cycleKey := fmt.Sprintf("reward:cycle:%d:%d", roomID, sessionID)

    // 检查顺子保底
    if config.GuaranteeStraight {
        straightKey := cycleKey + ":straight"
        straightWon, _ := c.redis.Get(ctx, straightKey).Int()

        if straightWon == 0 {
            remainingRounds := maxRounds - currentRoundNo + 1
            if c.shouldTriggerGuarantee(ctx, remainingRounds) {
                c.redis.Set(ctx, straightKey, 1, 24*time.Hour)
                return RewardTypeStraight
            }
        }
    }

    // 检查豹子保底
    if config.GuaranteeLeopard {
        leopardKey := cycleKey + ":leopard"
        leopardWon, _ := c.redis.Get(ctx, leopardKey).Int()

        if leopardWon == 0 {
            remainingRounds := maxRounds - currentRoundNo + 1
            if c.shouldTriggerGuarantee(ctx, remainingRounds) {
                c.redis.Set(ctx, leopardKey, 1, 24*time.Hour)
                return RewardTypeLeopard
            }
        }
    }

    return RewardTypeNone
}

// shouldTriggerGuarantee 判断是否触发保底
func (c *RewardController) shouldTriggerGuarantee(ctx context.Context, remainingRounds int) bool {
    if remainingRounds <= 1 {
        return true
    }

    prob := 1.0 / float64(remainingRounds)
    return c.randomFloat() < prob
}

// isProbabilityAllowed 检查概率触发是否被允许
func (c *RewardController) isProbabilityAllowed(ctx context.Context) bool {
    if !c.config.GlobalSwitchEnabled {
        return false
    }

    if c.config.ProfitRatioThreshold > 0 {
        currentRatio, err := c.GetCurrentProfitRatio(ctx)
        if err != nil || currentRatio < c.config.ProfitRatioThreshold {
            return false
        }
    }

    return true
}

// checkProbability 概率检查
func (c *RewardController) checkProbability(config *RoomRewardConfig) int {
    randVal := c.randomFloat()

    if randVal < config.LeopardProbability {
        return RewardTypeLeopard
    }

    randVal -= config.LeopardProbability
    if randVal < config.StraightProbability {
        return RewardTypeStraight
    }

    return RewardTypeNone
}

// GetCurrentProfitRatio 获取当前收益比例
func (c *RewardController) GetCurrentProfitRatio(ctx context.Context) (float64, error) {
    today := time.Now().Format("2006-01-02")
    key := fmt.Sprintf("profit:daily:%s", today)

    data, err := c.redis.HGetAll(ctx, key).Result()
    if err != nil {
        return 0, err
    }

    var totalBet, totalWin, totalReward int64
    if v, ok := data["total_bet"]; ok {
        fmt.Sscanf(v, "%d", &totalBet)
    }
    if v, ok := data["total_win"]; ok {
        fmt.Sscanf(v, "%d", &totalWin)
    }
    if v, ok := data["total_reward"]; ok {
        fmt.Sscanf(v, "%d", &totalReward)
    }

    if totalBet == 0 {
        return 0, nil
    }

    profit := totalBet - totalWin - totalReward
    return float64(profit) / float64(totalBet), nil
}

// RecordProfit 记录收益数据
func (c *RewardController) RecordProfit(ctx context.Context, betAmount, winAmount, rewardAmount int64) error {
    today := time.Now().Format("2006-01-02")
    key := fmt.Sprintf("profit:daily:%s", today)

    pipe := c.redis.Pipeline()
    pipe.HIncrBy(ctx, key, "total_bet", betAmount)
    pipe.HIncrBy(ctx, key, "total_win", winAmount)
    pipe.HIncrBy(ctx, key, "total_reward", rewardAmount)
    pipe.Expire(ctx, key, 7*24*time.Hour)

    _, err := pipe.Exec(ctx)
    return err
}

// OnSessionEnd 会话结束时清理轮次状态
func (c *RewardController) OnSessionEnd(ctx context.Context, roomID, sessionID int64) {
    cycleKey := fmt.Sprintf("reward:cycle:%d:%d", roomID, sessionID)
    c.redis.Del(ctx, cycleKey+":straight", cycleKey+":leopard", cycleKey+":rounds")
}

func (c *RewardController) getRoomConfig(roomID int64) *RoomRewardConfig {
    if c.config == nil || c.config.RoomConfigs == nil {
        return nil
    }
    return c.config.RoomConfigs[roomID]
}

func (c *RewardController) randomFloat() float64 {
    c.rngMu.Lock()
    defer c.rngMu.Unlock()

    n, _ := crand.Int(crand.Reader, big.NewInt(1000000))
    return float64(n.Int64()) / 1000000.0
}
```

### 7.2 修改 `config.go`

```go
package algorithm

type Config struct {
    MinPacketAmount     int64   `yaml:"min_packet_amount"`
    CommissionRate      float64 `yaml:"commission_rate"`
    JackpotProbability  float64 `yaml:"jackpot_probability"`
    JackpotContribution float64 `yaml:"jackpot_contribution"`
    JackpotInitialPool  int64   `yaml:"jackpot_initial_pool"`
    TargetRTP           float64 `yaml:"target_rtp"`
    RTPAdjustThreshold  float64 `yaml:"rtp_adjust_threshold"`

    RewardControl *RewardControlConfig `yaml:"reward_control"`
}

type RewardControlConfig struct {
    GlobalSwitchEnabled  bool                        `yaml:"global_switch_enabled"`
    ProfitRatioThreshold float64                     `yaml:"profit_ratio_threshold"`
    RoomConfigs          map[int64]*RoomRewardConfig `yaml:"room_configs"`
}

type RoomRewardConfig struct {
    ConfigID            int64   `yaml:"config_id"`
    GuaranteeEnabled    bool    `yaml:"guarantee_enabled"`
    GuaranteeStraight   bool    `yaml:"guarantee_straight"`
    GuaranteeLeopard    bool    `yaml:"guarantee_leopard"`
    ProbabilityEnabled  bool    `yaml:"probability_enabled"`
    StraightProbability float64 `yaml:"straight_probability"`
    LeopardProbability  float64 `yaml:"leopard_probability"`
}

func DefaultConfig() *Config {
    return &Config{
        MinPacketAmount:     1,
        CommissionRate:      0.05,
        JackpotProbability:  0.0005,
        JackpotContribution: 0.01,
        JackpotInitialPool:  100000,
        TargetRTP:           0.95,
        RTPAdjustThreshold:  0.02,
        RewardControl: &RewardControlConfig{
            GlobalSwitchEnabled:  false,
            ProfitRatioThreshold: 0.05,
            RoomConfigs:          make(map[int64]*RoomRewardConfig),
        },
    }
}
```

### 7.3 修改 `packet_generator.go`

```go
// 在 PacketGenerator 结构体中添加
type PacketGenerator struct {
    config            *Config
    redis             *cRedis.Client
    db                *gorm.DB
    jackpotManager    *JackpotManager
    rtpTracker        *RTPTracker
    straightGenerator *StraightGenerator
    leopardGenerator  *LeopardGenerator
    rewardController  *RewardController  // 新增
    roomRepo          *redis.RoomRepository  // 新增：用于获取房间信息
    rngMu             sync.Mutex
}

// 修改构造函数
func NewPacketGenerator(
    config *Config,
    redis *cRedis.Client,
    db *gorm.DB,
    roomRepo *redis.RoomRepository,
) *PacketGenerator {
    g := &PacketGenerator{
        config:   config,
        redis:    redis,
        db:       db,
        roomRepo: roomRepo,
    }
    g.jackpotManager = NewJackpotManager(config, redis, db)
    g.rtpTracker = NewRTPTracker(redis, db)
    g.straightGenerator = NewStraightGenerator(config)
    g.leopardGenerator = NewLeopardGenerator(config)
    g.rewardController = NewRewardController(config.RewardControl, redis)
    return g
}

// 修改 Generate 方法
func (g *PacketGenerator) Generate(ctx context.Context, req *GenerateRequest) (*GenerateResult, error) {
    if err := g.validateRequest(req); err != nil {
        return nil, err
    }

    // 获取房间信息 (所有需要的信息都从这里获取，不需要在 request 中传递)
    roomMeta, err := g.roomRepo.GetRoomMeta(ctx, req.RoomID)
    if err != nil {
        return nil, err
    }

    // 缓存检查 (原有逻辑)
    cacheKey := redis.RoundPacketsKey(req.RoundID)
    cached, err := g.redis.Get(ctx, cacheKey).Result()
    if err == nil {
        var result GenerateResult
        if err := json.Unmarshal([]byte(cached), &result); err == nil {
            return &result, nil
        }
    }

    traceID := req.RoundID

    // 从 roomMeta 获取所有需要的信息
    roomID := idStringToInt64(req.RoomID)
    sessionID := idStringToInt64(roomMeta.CurrentSessionID)
    currentRoundNo := roomMeta.CurrentRound  // 当前局号，用于计算剩余局数
    maxRounds := roomMeta.MaxRounds          // 轮次总局数

    // 使用新的奖励控制器 (用 roomID 匹配配置)
    rewardType := g.rewardController.DetermineRewardType(
        ctx, roomID, sessionID, currentRoundNo, maxRounds,
    )

    var result *GenerateResult
    var genErr error

    switch rewardType {
    case RewardTypeStraight:
        result, genErr = g.generateStraightPackets(ctx, req, traceID)
        if genErr != nil {
            result, genErr = g.generateNormalPackets(ctx, req, traceID)
        }
    case RewardTypeLeopard:
        result, genErr = g.generateLeopardPackets(ctx, req, traceID)
        if genErr != nil {
            result, genErr = g.generateNormalPackets(ctx, req, traceID)
        }
    case RewardTypeJackpot:
        result, genErr = g.generateJackpotPackets(ctx, req, traceID)
    default:
        result, genErr = g.generateNormalPackets(ctx, req, traceID)
    }

    if genErr != nil {
        return nil, genErr
    }

    resultJSON, _ := json.Marshal(result)
    success, err := g.redis.SetNX(ctx, cacheKey, resultJSON, time.Hour).Result()
    if err == nil && !success {
        if cached, err = g.redis.Get(ctx, cacheKey).Result(); err == nil {
            json.Unmarshal([]byte(cached), &result)
        }
    }

    g.updateJackpotPool(ctx, req.TotalAmount)

    return result, nil
}

// 删除旧的 determineRewardType 方法，由 rewardController 接管
```

### 7.4 `model.go` 无需修改

**说明**：`GenerateRequest` 保持原有字段即可，所有需要的信息（sessionID、configID、currentRound、maxRounds）都从 `roomMeta` 获取，避免数据冗余和传递不一致。

```go
// GenerateRequest 保持原有结构，无需添加新字段
type GenerateRequest struct {
    TotalAmount int64  `json:"total_amount"`
    PacketCount int    `json:"packet_count"`
    RoomID      string `json:"room_id"`
    RoundID     string `json:"round_id"`
    // 其他字段都从 roomMeta 获取，无需在此传递
}
```

---

## 八、配置示例

### 8.1 完整配置文件示例

```yaml
# config/game.yaml
game:
  min_packet_amount: 1
  commission_rate: 0.05
  jackpot_probability: 0.0005
  jackpot_contribution: 0.01
  jackpot_initial_pool: 100000
  target_rtp: 0.95
  rtp_adjust_threshold: 0.02
  max_rounds: 10

reward_control:
  global_switch_enabled: false
  profit_ratio_threshold: 0.05

  room_configs:
    1001:  # room_id = 1001
      guarantee_enabled: true
      guarantee_straight: true
      guarantee_leopard: true
      probability_enabled: true
      straight_probability: 0.08
      leopard_probability: 0.002

    1002:  # room_id = 1002
      guarantee_enabled: true
      guarantee_straight: true
      guarantee_leopard: true
      probability_enabled: true
      straight_probability: 0.06
      leopard_probability: 0.001

    1003:  # room_id = 1003
      guarantee_enabled: false
      probability_enabled: true
      straight_probability: 0.05
      leopard_probability: 0.001

    1004:  # room_id = 1004
      guarantee_enabled: true
      guarantee_straight: true
      guarantee_leopard: false
      probability_enabled: true
      straight_probability: 0.04
      leopard_probability: 0.0005

    1005:  # room_id = 1005 - 关闭所有奖励
      guarantee_enabled: false
      probability_enabled: false
      straight_probability: 0
      leopard_probability: 0
```

### 8.2 配置说明

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `global_switch_enabled` | 总开关，控制概率触发是否生效 | `false` |
| `profit_ratio_threshold` | 收益比例阈值，达到后才开启概率触发 | `0.05` (5%) |
| `guarantee_enabled` | 是否开启保底中奖 | `false` |
| `guarantee_straight` | 保底顺子 | `false` |
| `guarantee_leopard` | 保底豹子 | `false` |
| `probability_enabled` | 是否开启概率触发 | `false` |
| `straight_probability` | 顺子概率 | `0` |
| `leopard_probability` | 豹子概率 | `0` |

---

## 九、实施步骤

| 步骤 | 任务 | 说明 |
|------|------|------|
| 1 | 修改 `config.go` | 添加新的配置结构 |
| 2 | 新增 `reward_controller.go` | 实现奖励控制逻辑 |
| 3 | 修改 `packet_generator.go` | 集成奖励控制器，注入 roomRepo |
| 4 | 更新配置文件 | 添加 reward_control 配置 |
| 5 | 单元测试 | 编写测试用例 |
| 6 | 集成测试 | 端到端测试 |

---

## 十、测试用例

### 10.1 保底中奖测试

```go
func TestGuaranteeReward(t *testing.T) {
    // 场景1: 10局轮次，最后一局必中
    // 场景2: 10局轮次，中间局按概率触发
    // 场景3: 已中奖后不再触发
}

func TestGuaranteeNotAffectedBySwitch(t *testing.T) {
    // 场景: 总开关关闭，保底仍生效
}
```

### 10.2 概率触发测试

```go
func TestProbabilityWithSwitch(t *testing.T) {
    // 场景1: 开关关闭，概率不触发
    // 场景2: 开关开启，收益比例未达标，概率不触发
    // 场景3: 开关开启，收益比例达标，概率触发
}

func TestProfitRatioCalculation(t *testing.T) {
    // 场景: 验证收益比例计算正确性
}
```

---

## 十一、注意事项

1. **保底中奖优先级最高**：不受总开关和收益比例控制
2. **轮次状态清理**：会话结束时需要清理 Redis 中的轮次状态
3. **并发安全**：使用 Redis 原子操作保证并发安全
4. **配置热更新**：支持配置文件热更新，无需重启服务
5. **监控告警**：建议添加奖励触发监控，便于运营分析
