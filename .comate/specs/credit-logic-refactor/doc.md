# 入账逻辑重构：净额入账 → 抢红包/奖励入账

## 问题描述

当前会话级结算（`netSettlePlayers`）使用**净额入账**逻辑：`platform.Credit(netAmount)` 其中 `netAmount = payout - bet`，仅当 `netAmount > 0` 时才调用 Credit。

这是**错误的**，因为 `platform.Debit(bet)` 在扣款阶段已经从玩家钱包扣除了 bet 金额。入账时如果只 Credit 净额，玩家会被**双重扣除 bet**。

### 数值验证

| 场景 | bet | payout | 期望余额 | 当前代码余额 | 差异 |
|------|-----|--------|----------|-------------|------|
| 赢家 | 100 | 200 | 初始 - 100 + 200 = 初始 + 100 | 初始 - 100 + (200-100) = 初始 | 少了 100 |
| 输家 | 100 | 50 | 初始 - 100 + 50 = 初始 - 50 | 初始 - 100 (netAmount=-50 不入账) | 少了 50 |
| 平局 | 100 | 100 | 初始 - 100 + 100 = 初始 | 初始 - 100 (netAmount=0 不入账) | 少了 100 |

**所有场景下玩家都被少入账了 bet 金额。**

## 重构方案

### 核心变更

将会话级入账从"净额入账"改为"抢红包/奖励入账"：
- **入账金额**：`payout`（玩家在会话中获得的全部正向收入），而非 `payout - bet`
- **入账条件**：`payout > 0`（只要有收入就入账），而非 `netAmount > 0`
- **入账语义**：玩家会话级抢红包/奖励入账，而非净额入账

### 影响范围

#### 1. `game_settle_service.go` — 核心入账逻辑

**`netSettlePlayers` 方法**（第251-282行）：
- 变更：`netAmount := payOut - betAmount` → 直接使用 `payOut` 作为入账金额
- 变更：`netAmount <= 0` 判断 → `payOut <= 0` 判断
- 变更：方法名 `netSettlePlayers` → `creditSessionPayouts`
- 变更：方法名 `netSettlePlayer` → `creditSessionPayout`

**`netSettlePlayer` 方法**（第284-319行）：
- 变更：入账金额从 `netAmount` 改为 `payOut`
- 变更：BillRecord 的 BillType 从 `BillTypeNetSettlement` 改为 `BillTypeSessionCredit`
- 变更：Remark 从 `"会话级净额入账,局ID:%d,净额:%d"` 改为 `"会话级抢红包/奖励入账,局ID:%d,入账:%d"`
- 变更：幂等检查从 `BillTypeNetSettlement` 改为 `BillTypeSessionCredit`
- 变更：traceID 前缀从 `NET_SETTLE` 改为 `SESSION_CREDIT`
- 变更：BizOrderNo 前缀从 `NET_CREDIT` 改为 `SESSION_CREDIT`

**`executeNetCredit` 方法**（第321-388行）：
- 变更：方法名 `executeNetCredit` → `executeSessionCredit`
- 无其他逻辑变更（仍然调用 `platform.Credit()`，只是金额不同）

**`SettleGame` 方法**（第51-155行）：
- 变更：调用从 `netSettlePlayers` 改为 `creditSessionPayouts`
- 变更：注释更新

#### 2. `constants.go` — 账单类型常量

- 变更：`BillTypeNetSettlement = 12` → `BillTypeSessionCredit = 12`
- 保持值不变（12），仅改名称和语义

#### 3. `bill_manager.go` — 聚合查询

**`AggregatePayOutBySession` 方法**（第420-441行）：
- 变更：排除条件从 `bill_type != BillTypeNetSettlement` 改为 `bill_type != BillTypeSessionCredit`
- 逻辑不变，只是常量名更新

**`GetBillsBySessionTypeAndUser` 方法**（第393-398行）：
- 无变更（调用方会自动使用新常量名）

#### 4. `settlement_service.go` — 无变更

- `creditRound` 方法仅做内部记账，不调用 `platform.Credit()`，逻辑正确
- Remark `"抢红包收入(待局级净额结算)"` 建议更新为 `"抢红包收入(待会话级入账)"`

#### 5. `reward_settler.go` — 无逻辑变更

- Remark `"系统奖励收入(待局级净额结算)"` 建议更新为 `"系统奖励收入(待会话级入账)"`

### 不变更的部分

1. **`settlePlayer` 方法**（Settle 上报）：继续上报完整的 `betAmount` 和 `payOut`，这是报告性质，不涉及资金移动
2. **`AggregateBetBySession` 方法**：逻辑不变，仍然正确聚合扣款金额
3. **扣款逻辑**（`deduct_service.go`）：逻辑正确，无需变更
4. **回合级记账**（`creditRound`）：仅做内部记账，不调用平台 API，逻辑正确

### 数据流对比

**重构前：**
```
扣款阶段: platform.Debit(bet) → 玩家钱包 -= bet
入账阶段: platform.Credit(payout - bet) → 玩家钱包 += max(payout - bet, 0)
结果:     玩家钱包 = 初始 - bet + max(payout - bet, 0)  ← 错误
```

**重构后：**
```
扣款阶段: platform.Debit(bet) → 玩家钱包 -= bet
入账阶段: platform.Credit(payout) → 玩家钱包 += payout (当 payout > 0)
结果:     玩家钱包 = 初始 - bet + payout  ← 正确
```

### 边界条件

1. **payout = 0 的玩家**：不调用 Credit（和之前 netAmount = 0 时不调用的行为一致）
2. **payout > 0 但 bet = 0 的玩家**（仅获得奖励无扣款）：Credit(payout)，正确
3. **payout < bet 的玩家**（净亏损）：Credit(payout)，玩家最终余额 = 初始 - bet + payout < 初始，正确反映亏损
4. **payout = bet 的玩家**（盈亏平衡）：Credit(payout)，玩家最终余额 = 初始，正确
5. **幂等性**：仍然通过 `GetBillsBySessionTypeAndUser` + `BillTypeSessionCredit` 检查，逻辑不变

### 数据库兼容性

- `BillTypeSessionCredit = 12` 保持与 `BillTypeNetSettlement` 相同的值
- 已存在的 `bill_type = 12` 的记录在数据库中无需迁移
- 新记录的 Remark 会不同，但 bill_type 值一致
