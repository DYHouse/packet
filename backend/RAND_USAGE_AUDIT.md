# Backend `math/rand` vs `crypto/rand` 使用审查报告

> 审查范围：`backend/` 全量 Go 代码（171 文件）
> 审查依据：`CODING_STANDARD.md` §6.4（随机数 MUST）、§19.6（L-6 Lua 随机数）、§20.2（SID-12）
> 审查日期：2026-07-05
> 审查方法：`grep -rn "math/rand|crypto/rand"` 全量扫描 + 逐文件读取上下文 + 对照规约判断

---

## 0. 规约依据

### 6.4 随机数（MUST）

> 涉及**金额、红包拆分、奖励生成**的随机数必须使用 `crypto/rand`，**禁止** `math/rand`。

### 19.6 L-6：Lua 内禁止 math.random（MUST）

> 涉及随机数场景（红包拆分、奖励生成）必须在 Go 侧用 `crypto/rand` 生成后通过 ARGV 传入 Lua。

### 20.2 SID-12

> 需要随机数的场景（如 `GenerateReconcileNo`）MUST 使用 `crypto/rand`，禁止用雪花 ID 取模（低 12 位是 sequence，碰撞概率高）。

---

## 1. 总览

| 类型 | 使用文件数 | 合规 | 违规 |
|------|------------|------|------|
| `crypto/rand` | 5 | 5 | 0 |
| `math/rand` | 8 | 6 | 2 |

**结论**：`crypto/rand` 全部合规；`math/rand` 有 2 处违规（涉及红包金额/抢红包结果），6 处合规（3 处 jitter/重连退避 + 3 处机器人 AI 行为）。

> **判定原则**：规约 §6.4 仅强制"金额、红包拆分、奖励生成"使用 `crypto/rand`。机器人 AI 行为（选座、延迟、跳过概率）不涉及资金，且 Go 1.20+ `math/rand` 顶层包已自动种子化，预测难度足够，使用 `math/rand` 是合规且合理的选择。

---

## 2. `crypto/rand` 使用清单（全部合规）

### 2.1 `common/utils/utils.go`

| 函数 | 行 | 用途 | 合规 |
|------|----|------|------|
| `GenerateConnID` | 14-18 | 生成 WebSocket 连接 ID（`conn_<ts><rand>`） | ✅ |
| `GenerateRoomID` | 21-25 | 生成房间 ID（时间戳+roomType+随机数） | ✅ |
| `GenerateOrderNo` | 28-32 | 生成订单号（`<prefix>_<ts>_<rand>`） | ✅ |
| `RandomInt64` | 116-122 | 工具函数：返回 `[0, max)` 随机 int64 | ✅ |
| `ShuffleInt64` | 125-130 | Fisher-Yates 洗牌（依赖 `RandomInt64`） | ✅ |

**说明**：工具包统一封装 `crypto/rand`，供其他模块复用。

### 2.2 `common/utils/avatar.go`

| 函数 | 行 | 用途 | 合规 |
|------|----|------|------|
| `GetRandomAvatar` | 9-18 | 随机选择头像 URL（`<baseURL>/<n>.png`） | ✅ |

**说明**：用户可见资源，虽不涉及金额，但用 `crypto/rand` 无害。

### 2.3 `settlement/service/trace_id_generator.go`

| 函数 | 行 | 用途 | 合规 |
|------|----|------|------|
| `GenerateReconcileNo` | 55-62 | 生成对账单号 `REC_<ts>_<4位随机>` | ✅ |

**说明**：直接对应 SID-12 规约，明确用 `crypto/rand` 避免雪花 ID 低位 sequence 碰撞。

### 2.4 `game/algorithm/reward_controller.go`

| 函数 | 行 | 用途 | 合规 |
|------|----|------|------|
| `randomFloat` | 210-213 | 奖励控制器随机数（`[0, 1)`） | ✅ |

**说明**：奖励生成场景，规约 §6.4 强制要求 `crypto/rand`。`crand.Reader` 并发安全，无需加锁。

### 2.5 `game/algorithm/packet_generator.go`

| 函数 | 行 | 用途 | 合规 |
|------|----|------|------|
| `randomInt` | 215-221 | 红包生成器随机整数 | ✅ |
| `randomRange` | 223-231 | 红包生成器区间随机 | ✅ |

**说明**：红包拆分场景，规约 §6.4 强制要求 `crypto/rand`。

---

## 3. `math/rand` 使用清单（含合规判定）

### 3.1 ✅ 合规：jitter / 重连退避（3 处）

#### 3.1.1 `common/kafka/retry.go:63`

```go
// processWithRetry 重试退避 jitter
backoff := cfg.RetryBackoff * time.Duration(1<<uint(attempt))
jitter := time.Duration(rand.Intn(100)) * time.Millisecond
```

- **用途**：Kafka 消费重试的指数退避 + 0~100ms 随机 jitter，避免消费风暴
- **合规**：✅ 不涉及金额/奖励，仅用于分布式系统去同步化
- **依据**：jitter 是 `math/rand` 的标准使用场景，无需 `crypto/rand`

#### 3.1.2 `common/broadcast/redis_pubsub_consumer.go:65, 96`

```go
// 重连退避 jitter
case <-time.After(backoff + time.Duration(rand.Intn(100))*time.Millisecond):
```

- **用途**：Redis PubSub 重连的指数退避 + jitter
- **合规**：✅ 同 3.1.1

#### 3.1.3 `gateway/connection/manager.go:238, 258`

```go
// 重连退避 jitter
case <-time.After(backoff + time.Duration(rand.Intn(100))*time.Millisecond):
```

- **用途**：Gateway 连接管理器重连退避 jitter
- **合规**：✅ 同 3.1.1

> **注意**：`math/rand` 顶层包在 Go 1.20+ 默认已自动种子化（`rand.Seed` 已废弃），不再需要显式 `rand.Seed(time.Now().UnixNano())`。这三处使用无需任何修改。

---

### 3.2 ❌ 违规：金额/红包/抢红包（3 处，对应 P0-6, P0-7）

#### 3.2.1 `game/algorithm/straight.go:5, 48, 50` 【P0-6】

```go
import "math/rand"

r := rand.New(rand.NewSource(time.Now().UnixNano()))
indices := r.Perm(int(n))
```

- **用途**：顺子红包金额分配，对金额索引进行随机置换
- **违规**：❌ **直接涉及红包金额拆分**，违反 §6.4 MUST 规约
- **风险**：`time.Now().UnixNano()` 种子可被预测，攻击者可推断红包金额分布
- **修复方案**：参考 `packet_generator.go:215` 的 `randomInt` 实现，用 `crypto/rand` 生成 Perm；或在 `common/utils` 提供 `CryptoRandPerm(n int) ([]int, error)` 封装
- **规约引用**：CODING_STANDARD.md §6.4 明确点名此文件为"必须收敛"项

#### 3.2.2 `game/application/grab_service.go:6, 98` 【P0-7】

```go
import "math/rand"

// GetAvailablePacketID: 机器人选红包
return packetIDs[rand.Intn(len(packetIDs))], nil
```

- **用途**：机器人随机选择可用红包 ID
- **违规**：❌ **影响抢红包结果**，违反 §6.4 MUST 规约
- **风险**：种子可预测时，攻击者可推断机器人选包策略
- **修复方案**：用 `utils.RandomInt64(int64(len(packetIDs)))` 替换

#### 3.2.3 `game/application/grab_service.go:6, 123` 【P0-7】

```go
// RobotGrabPacket: 随机起始偏移传入 Lua
rand.Intn(1000),
```

- **用途**：机器人抢红包时预生成随机起始偏移，Lua 侧 `% packetCount` 取模
- **违规**：❌ **影响抢红包结果**
- **风险**：种子可预测时偏移可被推断
- **修复方案**：用 `utils.RandomInt64(1000)` 替换

---

### 3.3 ✅ 合规：机器人 AI 行为（3 处）

> **判定依据**：规约 §6.4 仅强制"金额、红包拆分、奖励生成"使用 `crypto/rand`。机器人 AI 行为（选座、延迟、跳过概率）属于游戏 AI 模拟，不涉及资金分配，且 Go 1.20+ `math/rand` 顶层包已自动种子化（`rand.Seed` 已废弃），预测难度足够。业界游戏 AI 行为普遍使用 `math/rand`，`crypto/rand` 留给安全敏感场景。

#### 3.3.1 `game/application/robot_player.go:6, 260, 269`

```go
import "math/rand"

// pickRandomEmptySeat: 机器人随机选座
return emptySeats[rand.Intn(len(emptySeats))]

// randomDelay: 机器人随机延迟
return min + time.Duration(rand.Int63n(int64(max-min)))
```

- **用途**：机器人随机选座 + 随机行为延迟
- **合规**：✅ 机器人 AI 行为，不涉及资金
- **分析**：选座虽影响抢红包顺序，但不直接决定金额分配；延迟仅影响游戏节奏

#### 3.3.2 `game/application/robot_behavior.go:6, 321, 327`

```go
import "math/rand"

// randomDelay: 机器人随机延迟
return min + time.Duration(rand.Int63n(int64(max-min)))

// shouldSkipGrab: 机器人是否跳过抢红包
return rand.Float64() < prob
```

- **用途**：机器人随机延迟 + 概率跳过抢红包
- **合规**：✅ 机器人 AI 行为，不涉及资金
- **分析**：`shouldSkipGrab` 仅决定机器人是否参与本轮抢红包，不影响红包金额分配算法

#### 3.3.3 `game/application/robot_scheduler_service.go:5, 531`

```go
import "math/rand"

// randomDelay: 机器人调度随机延迟
return min + time.Duration(rand.Int63n(int64(max-min)))
```

- **用途**：机器人调度器随机延迟
- **合规**：✅ 机器人 AI 行为，不涉及资金
- **分析**：调度延迟仅影响机器人触发节奏，对游戏公平性无影响

---

## 4. 修复优先级

| 优先级 | 编号 | 文件 | 问题 | 修复方案 |
|--------|------|------|------|----------|
| **P0** | P0-6 | `game/algorithm/straight.go` | 红包金额随机分配用 `math/rand` | 改用 `crypto/rand` 生成 Perm |
| **P0** | P0-7 | `game/application/grab_service.go` | 机器人选包+随机偏移用 `math/rand` | 改用 `utils.RandomInt64` |
| 无需修改 | — | `game/application/robot_player.go` | 机器人选座+延迟 | `math/rand` 合规（AI 行为） |
| 无需修改 | — | `game/application/robot_behavior.go` | 机器人延迟+跳过概率 | `math/rand` 合规（AI 行为） |
| 无需修改 | — | `game/application/robot_scheduler_service.go` | 机器人调度延迟 | `math/rand` 合规（AI 行为） |
| 无需修改 | — | `common/kafka/retry.go` | 重试 jitter | `math/rand` 合规 |
| 无需修改 | — | `common/broadcast/redis_pubsub_consumer.go` | 重连 jitter | `math/rand` 合规 |
| 无需修改 | — | `gateway/connection/manager.go` | 重连 jitter | `math/rand` 合规 |

---

## 5. 建议的修复方案

### 5.1 在 `common/utils` 新增 `CryptoRandPerm` 封装

```go
// common/utils/rand.go (新增文件)
package utils

import (
    "crypto/rand"
    "math/big"
)

// CryptoRandPerm 返回 [0, n) 的随机置换切片，使用 crypto/rand。
// 用于红包金额索引随机化等安全敏感场景（规约 §6.4）。
func CryptoRandPerm(n int) ([]int, error) {
    if n <= 0 {
        return nil, fmt.Errorf("n must be positive")
    }
    perm := make([]int, n)
    for i := 0; i < n; i++ {
        perm[i] = i
    }
    // Fisher-Yates with crypto/rand
    for i := n - 1; i > 0; i-- {
        j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
        if err != nil {
            return nil, fmt.Errorf("crypto rand perm failed: %w", err)
        }
        perm[i], perm[j.Int64()] = perm[j.Int64()], perm[i]
    }
    return perm, nil
}
```

### 5.2 `straight.go` 修复示例

```go
// 修改前
r := rand.New(rand.NewSource(time.Now().UnixNano()))
indices := r.Perm(int(n))

// 修改后
indices, err := utils.CryptoRandPerm(int(n))
if err != nil {
    return nil, fmt.Errorf("generate random perm failed: %w", err)
}
```

### 5.3 `grab_service.go` 修复示例

```go
// 修改前 (line 98)
return packetIDs[rand.Intn(len(packetIDs))], nil

// 修改后
return packetIDs[utils.RandomInt64(int64(len(packetIDs)))], nil

// 修改前 (line 123)
rand.Intn(1000),

// 修改后
utils.RandomInt64(1000),
```

---

## 6. 性能考量

`crypto/rand` 相比 `math/rand` 性能下降约 10-50 倍（取决于系统熵池），但在本项目的使用场景下：

- **红包生成**：每回合 1 次，QPS < 10，性能影响可忽略
- **机器人选包**：单次调用，QPS < 100，性能影响可忽略
- **重试 jitter**：保持 `math/rand`（已是合规用法）

**结论**：性能下降在本项目场景下完全可接受。

---

## 7. 验证清单

修复完成后执行以下验证：

```bash
# 1. 编译验证
cd backend && go build ./...

# 2. grep 验证 math/rand 残留（应剩 6 处合规场景：3 处 jitter + 3 处机器人 AI 行为）
grep -rn "math/rand" --include="*.go" game/ settlement/ gateway/ common/
# 预期输出：
# common/broadcast/redis_pubsub_consumer.go:6
# common/kafka/retry.go:6
# gateway/connection/manager.go:7
# game/application/robot_behavior.go:6
# game/application/robot_player.go:6
# game/application/robot_scheduler_service.go:5

# 3. grep 验证 game/algorithm 与 game/application/grab_service.go 无 math/rand
grep -rn "math/rand" --include="*.go" game/algorithm/
# 预期输出：空
grep -rn "math/rand" --include="*.go" game/application/grab_service.go
# 预期输出：空

# 4. 单元测试
go test ./game/... ./settlement/... -race -count=1
```

---

**审查人**：TRAE 自动审查
**审查日期**：2026-07-05
