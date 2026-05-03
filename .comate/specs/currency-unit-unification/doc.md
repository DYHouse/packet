# 金额单位统一重构方案

## 问题描述

当前系统中金额单位使用混乱：
- **平台 API** 接口使用**元**（字符串格式如 `"12.50"`）
- **游戏服务**内部统一使用**分**（int64，如 `1250`）
- **回传给前端**时仍为分，前端需要手动 `/ 100` 转换为元显示

转换逻辑分散在各处，没有统一处理：
- 后端转换函数 `FormatAmount`/`ParseAmount` 仅存在于 `api/platform/utils.go`，仅服务于平台 API 边界
- Stats API 直接返回 int64 分值，前端自行转换
- WebSocket 推送消息携带 int64 分值，客户端自行转换
- 前端 dashboard 有 `formatMoney`（分→"¥xx.xx"）和 `toYuan`（分→元数值）
- 测试页面 `test-ws.html` 有 10+ 处内联 `/ 100` 转换

## 核心思路

**内部保持分为单位，输出边界自动转换为元**。

引入 `currency.Money` 类型，封装 int64 分值，通过 `MarshalJSON` 在 JSON 序列化时自动输出元值（如 `1250` 分 → JSON `12.50`），从根源上消除散落的转换代码。

## 架构设计

### 1. 新建 `common/currency` 包

**文件**: `backend/common/currency/money.go`

```go
package currency

import (
    "encoding/json"
    "fmt"
    "math"
    "strconv"
    "strings"
)

// Money 金额类型，内部以分为单位存储(int64)
// JSON 序列化时自动输出为元的数值（如 1250 → 12.50）
type Money int64

// MarshalJSON 实现 json.Marshaler，输出元值（精确到小数点后2位）
func (m Money) MarshalJSON() ([]byte, error) {
    if m == 0 {
        return []byte("0.00"), nil
    }
    yuan := float64(m) / 100.0
    s := strconv.FormatFloat(yuan, 'f', 2, 64)
    return []byte(s), nil
}

// UnmarshalJSON 实现 json.Unmarshaler，从元值解析为分
func (m *Money) UnmarshalJSON(data []byte) error {
    // 尝试作为数值解析
    var yuan float64
    if err := json.Unmarshal(data, &yuan); err == nil {
        *m = Money(math.Round(yuan * 100))
        return nil
    }
    // 尝试作为字符串解析（兼容平台 API 返回的 "12.50" 格式）
    var s string
    if err := json.Unmarshal(data, &s); err == nil {
        parsed, err := ParseAmount(s)
        if err != nil {
            return fmt.Errorf("parse money string failed: %w", err)
        }
        *m = Money(parsed)
        return nil
    }
    return fmt.Errorf("money must be number or string, got: %s", string(data))
}

// Fen 返回分值(int64)
func (m Money) Fen() int64 {
    return int64(m)
}

// Yuan 返回元值(float64)
func (m Money) Yuan() float64 {
    return float64(m) / 100.0
}

// String 返回元值字符串（如 "12.50"）
func (m Money) String() string {
    yuan := int64(m) / 100
    fen := int64(m) % 100
    if fen < 0 {
        fen = -fen
    }
    if m < 0 && yuan == 0 {
        return fmt.Sprintf("-%d.%02d", yuan, fen)
    }
    return fmt.Sprintf("%d.%02d", yuan, fen)
}

// NewMoneyFromFen 从分创建 Money
func NewMoneyFromFen(fen int64) Money {
    return Money(fen)
}

// NewMoneyFromYuan 从元(float64)创建 Money
func NewMoneyFromYuan(yuan float64) Money {
    return Money(math.Round(yuan * 100))
}
```

**文件**: `backend/common/currency/convert.go`

将 `api/platform/utils.go` 中的 `FormatAmount`/`ParseAmount` 迁移到此包作为通用函数：

```go
package currency

import (
    "fmt"
    "strconv"
    "strings"
)

// ParseAmount 将元字符串(如 "12.50")解析为分(int64)
func ParseAmount(amountStr string) (int64, error) {
    amountStr = strings.TrimSpace(amountStr)
    if amountStr == "" {
        return 0, nil
    }
    parts := strings.Split(amountStr, ".")
    var cents int64
    intPart, err := strconv.ParseInt(parts[0], 10, 64)
    if err != nil {
        return 0, fmt.Errorf("parse integer part failed: %w", err)
    }
    cents = intPart * 100
    if len(parts) > 1 {
        decPart := parts[1]
        if len(decPart) > 2 {
            decPart = decPart[:2]
        }
        decValue, err := strconv.ParseInt(decPart, 10, 64)
        if err != nil {
            return 0, fmt.Errorf("parse decimal part failed: %w", err)
        }
        if len(decPart) == 1 {
            decValue *= 10
        }
        cents += decValue
    }
    return cents, nil
}

// FormatAmount 将分(int64)格式化为元字符串(如 "12.50")
func FormatAmount(cents int64) string {
    return NewMoneyFromFen(cents).String()
}
```

**文件**: `backend/common/currency/money_test.go`

覆盖核心场景的单元测试：
- 正数、零、负数的序列化/反序列化
- 整数元（如 1200 分 → "12.00"）
- 带分值（如 1250 分 → "12.50"）
- 单位分（如 1 分 → "0.01"）
- 从元数值反序列化
- 从元字符串反序列化
- ParseAmount/FormatAmount 兼容性

### 2. 更新 WebSocket 消息类型

**文件**: `backend/common/message/payload.go`

将所有金额字段从 `int64` 改为 `currency.Money`：

```go
// 修改前
type RoundResult struct {
    Amount int64 `json:"amount"`        // 分
}

// 修改后
type RoundResult struct {
    Amount currency.Money `json:"amount"` // 序列化时自动输出元
}
```

涉及的所有结构体和字段：

| 结构体 | 字段 | 原类型 | 新类型 |
|--------|------|--------|--------|
| `RoundResult` | `Amount` | `int64` | `currency.Money` |
| `GameResult` | `TotalProfit` | `int64` | `currency.Money` |
| `RoundStartPush` | `TotalAmount` | `int64` | `currency.Money` |
| `RoundStartPush` | `Commission` | `int64` | `currency.Money` |
| `RoundStartPush` | `ActualAmount` | `int64` | `currency.Money` |
| `PacketGrabbedPush` | `Amount` | `int64` | `currency.Money` |
| `RoundEndPush` | `TotalAmount` | `int64` | `currency.Money` |
| `RoundEndPush` | `Commission` | `int64` | `currency.Money` |
| `RoundEndPush` | `RewardAmount` | `int64` | `currency.Money` |
| `DistributeResult` | `Amount` | `int64` | `currency.Money` |
| `PenaltyPush` | `PenaltyAmount` | `int64` | `currency.Money` |
| `GameInterruptedPush` | `PenaltyShare` | `int64` | `currency.Money` |

### 3. 更新游戏服务中的消息构造

所有创建上述消息结构体的代码需要将 `int64` 赋值改为 `currency.NewMoneyFromFen(fenValue)` 或 `currency.Money(fenValue)`。

需要修改的文件（基于代码搜索）：
- `backend/game/application/game_service.go` — 构造 RoundStartPush, RoundEndPush 等
- `backend/game/application/grab_service.go` — 构造 PacketGrabbedPush
- `backend/game/application/penalty_service.go` — 构造 PenaltyPush
- `backend/game/application/seat_service.go` — 构造 AutoDistributePush
- `backend/settlement/service/reward_settler.go` — 构造 RewardAmount

### 4. 更新 Stats API DTO

**文件**: `backend/stats/dto/stats_dto.go`

将所有金额字段从 `int64` 改为 `currency.Money`：

| 结构体 | 字段 |
|--------|------|
| `DashboardStats` | `TotalCommission`, `PenaltyIncome`, `SystemPacketCost`, `NetProfit` |
| `HourlyTrend` | `Commission` |
| `AmountDistribution` | `TotalAmount`, `AvgAmount` |
| `RoomRanking` | `TotalAmount`, `Commission` |
| `SystemPacketStats` | `TotalAmount`, `AvgAmount` |
| `DailyTrend` | `TotalCommission`, `PenaltyIncome`, `SystemPacketCost`, `NetProfit` |

### 5. 更新 `api/platform/utils.go`

使其委托到 `common/currency` 包：

```go
package platform

import "github.com/cashparty/backend/common/currency"

// ParseAmount 解析元字符串为分（委托到 currency 包）
var ParseAmount = currency.ParseAmount

// FormatAmount 格式化分为元字符串（委托到 currency 包）
var FormatAmount = currency.FormatAmount
```

这样所有现有的 `platform.FormatAmount()` / `platform.ParseAmount()` 调用无需修改。

### 6. 更新前端 Dashboard

**文件**: `dashboard/src/utils/format.js`

```javascript
// formatMoney - 接收元值(数字或字符串)，格式化为 "¥xx.xx"
export const formatMoney = (value) => {
  if (!value && value !== 0) return '¥0.00'
  const num = typeof value === 'string' ? parseFloat(value) : value
  return '¥' + num.toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2
  })
}

// toYuan - 值已经是元，直接返回（保留函数以兼容调用方）
export const toYuan = (value) => {
  if (!value && value !== 0) return 0
  return typeof value === 'string' ? parseFloat(value) : value
}
```

**文件**: `dashboard/src/views/Dashboard.vue`

移除所有 `toYuan()` 调用（值已经是元，不需要转换）：
- `commission: toYuan(item.commission)` → `commission: item.commission`
- `net_profit: toYuan(item.net_profit)` → `net_profit: item.net_profit`
- 类似处理其他字段

### 7. 更新测试页面

**文件**: `backend/test-ws.html`

移除所有 `/ 100` 内联转换（值已经是元）：
- `(pushData.total_amount / 100).toFixed(2)` → `pushData.total_amount.toFixed(2)`
- 类似处理其他 10+ 处

## 数据流路径

### 改造前
```
平台API(元字符串) → ParseAmount → 游戏服务(分int64) → WebSocket/StatsAPI(分int64) → 前端(/ 100 → 元)
```

### 改造后
```
平台API(元字符串) → currency.ParseAmount → 游戏服务(分int64) → currency.Money MarshalJSON → 前端(直接使用元值)
```

## 边界条件与异常处理

1. **零值处理**: `Money(0).MarshalJSON()` 输出 `0.00`，前端 `formatMoney(0)` 输出 `¥0.00`
2. **负数处理**: 惩罚等场景可能有负数，`Money(-500).MarshalJSON()` 输出 `-5.00`
3. **精度问题**: 使用 `strconv.FormatFloat` 的 `'f'` 格式保证2位小数，避免浮点精度丢失
4. **向后兼容**: `UnmarshalJSON` 同时支持数值和字符串输入，确保 `ParseAmount` 逻辑完全保留
5. **平台 API 兼容**: `platform.FormatAmount`/`platform.ParseAmount` 通过变量委托保持 API 不变

## 预期成果

1. 所有金额在 JSON 输出时统一为元，前端无需手动转换
2. 转换逻辑集中在 `common/currency` 包，单一职责
3. `Money` 类型提供类型安全，避免 int64 金额字段与普通 int64 混淆
4. 现有平台 API 调用零改动（通过委托保持兼容）
5. 前端代码大幅简化，消除所有 `/ 100` 转换
