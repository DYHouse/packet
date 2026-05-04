# 入账逻辑重构总结

## 问题

会话级结算（`netSettlePlayers`）使用净额入账逻辑：`platform.Credit(payout - bet)`，仅当净额 > 0 时才调用 Credit。但 `platform.Debit(bet)` 在扣款阶段已经从玩家钱包扣除了 bet，导致玩家被双重扣除 bet 金额。

**数值示例**：玩家扣款 100，抢到 200。期望余额 = 初始 - 100 + 200 = 初始 + 100。实际代码：初始 - 100 + (200-100) = 初始，少了 100。

## 修改内容

### 1. `backend/settlement/dto/constants.go`
- `BillTypeNetSettlement = 12` → `BillTypeSessionCredit = 12`（值不变，语义更新）

### 2. `backend/settlement/service/game_settle_service.go`（核心变更）
- `netSettlePlayers` → `creditSessionPayouts`：入账金额从 `payout - bet` 改为 `payout`，条件从 `netAmount <= 0` 改为 `payout <= 0`，移除不再需要的 `betMap` 参数
- `netSettlePlayer` → `creditSessionPayout`：入参从 `netAmount` 改为 `payOut`，更新 traceID/BizOrderNo 前缀、Remark
- `executeNetCredit` → `executeSessionCredit`：仅重命名和注释更新
- `SettleGame` 中调用更新，注释更新

### 3. `backend/settlement/service/bill_manager.go`
- `AggregatePayOutBySession` 中常量引用 `BillTypeNetSettlement` → `BillTypeSessionCredit`

### 4. `backend/settlement/service/settlement_service.go`
- Remark `"抢红包收入(待局级净额结算)"` → `"抢红包收入(待会话级入账)"`

### 5. `backend/settlement/service/reward_settler.go`
- Remark `"系统奖励收入(待局级净额结算)"` → `"系统奖励收入(待会话级入账)"`

## 验证

- `go build ./settlement/...` 编译通过
- 全局搜索确认无 `BillTypeNetSettlement` 或 `netSettle` 遗留引用
- `BillTypeSessionCredit = 12` 值与原 `BillTypeNetSettlement` 一致，数据库已有记录兼容
