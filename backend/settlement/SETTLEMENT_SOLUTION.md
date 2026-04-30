# Settlement 结算方案文档

## 一、系统概述

结算系统负责处理游戏中的所有资金流转，包括扣款、入账、退款等操作，并通过调度器确保数据的一致性和完整性。

***

## 二、核心原则

| 操作类型     | 失败处理             | 原因        |
| -------- | ---------------- | --------- |
| **扣款失败** | 重试3次，失败，直接标记异常   | 后续流程无法进行  |
| **入账失败** | 重试（指数退避），超限后标记异常 | 资金已扣，必须入账 |

***

## 三、系统账号处理

### 3.1 核心原则

**平台没有系统账号**，系统收入 = 玩家总扣款 - 玩家总收入（自动计算）。

系统相关账单（`UserID = 0`）**只记录，不调用平台接口**。

### 3.2 系统账单处理规则

| 场景 | 系统账单处理 | 平台接口调用 |
|------|-----------|---------|
| 佣金结算 | 直接标记 Success | 无 |
| 惩罚扣款到平台 | 直接标记 Success | 无（玩家扣款正常调用） |
| 惩罚分配 | 直接标记 Success | 无（玩家入账正常调用） |
| 系统奖励 | 直接标记 Success | 无（玩家入账正常调用） |
| 系统发红包 | 直接标记 Success | 无 |

### 3.3 系统收支统计

```sql
-- 系统总收入（UserID = 0 的正金额账单）
SELECT SUM(amount) FROM bill_records WHERE user_id = 0 AND amount > 0;

-- 系统总支出（UserID = 0 的负金额账单）
SELECT SUM(ABS(amount)) FROM bill_records WHERE user_id = 0 AND amount < 0;
```

***

## 四、账单类型

| BillType | 常量名                       | 场景说明   | 操作类型            |
| -------- | ------------------------- | ------ | --------------- |
| 1        | BillTypeSendPacket        | 发红包    | 玩家扣款            |
| 2        | BillTypeFirstRoundDeduct  | 首回合扣款  | 玩家批量扣款          |
| 3        | BillTypeGrabPacket        | 抢红包    | 玩家入账            |
| 4        | BillTypeLaterRoundDeduct  | 后续回合扣款 | 玩家扣款            |
| 7        | BillTypeCommission        | 佣金     | 系统记录（不调平台）      |
| 8        | BillTypePenaltyIncome     | 惩罚扣款   | 玩家扣款 + 系统记录（成对） |
| 9        | BillTypeSystemPacket      | 系统红包   | 系统记录 + 玩家入账（成对） |
| 10       | BillTypePenaltyDistribute | 惩罚分配   | 系统记录 + 玩家入账（成对） |
| 11       | BillTypeSystemReward      | 系统奖励   | 系统记录 + 玩家入账（成对） |

***

## 五、数据模型

### 5.1 BillRecord（账单记录）

```
账单记录表，记录每一笔扣款/入账操作
```

| 字段              | 类型          | 说明                           |
| --------------- | ----------- | ---------------------------- |
| ID              | int64       | 主键                           |
| RoundTraceID    | string      | 回合追踪ID                       |
| BizOrderNo      | string      | 业务订单号（唯一）                    |
| PlatformTransID | string      | 平台交易ID                       |
| BillType        | int         | 账单类型                         |
| DeductScene     | int         | 扣款场景                         |
| RoomID          | int64       | 房间ID                         |
| SessionID       | int64       | 会话ID                         |
| RoundID         | int64       | 回合ID                         |
| RoundNo         | int         | 回合序号                         |
| UserID          | int64       | 用户ID（0=系统账号）                 |
| BatchID         | string      | 批次ID                         |
| Amount          | int64       | 金额（负数扣款，正数入账）                |
| BalanceBefore   | int64       | 操作前余额                        |
| BalanceAfter    | int64       | 操作后余额                        |
| Status          | int         | 状态（0处理中/1成功/2失败/3已退款）        |
| ReconcileStatus | int         | 对账状态                         |
| RefundStatus    | int         | 退款状态（0无/1待处理/2已批准/3已退款/4已拒绝） |
| RetryCount      | int         | 重试次数                         |
| NextRetryAt     | \*time.Time | 下次重试时间                       |
| ErrorCode       | string      | 错误码                          |
| ErrorMessage    | string      | 错误信息                         |
| ExceptionID     | int64       | 关联异常记录ID                     |

### 5.2 RoundSettlement（回合结算）

```
回合结算表，记录每个回合的整体结算状态
```

| 字段                 | 类型     | 说明                               |
| ------------------ | ------ | -------------------------------- |
| ID                 | int64  | 主键                               |
| RoundTraceID       | string | 回合追踪ID（唯一）                       |
| RoomID             | int64  | 房间ID                             |
| SessionID          | int64  | 会话ID                             |
| RoundID            | int64  | 回合ID                             |
| RoundNo            | int    | 回合序号                             |
| DeductScene        | int    | 扣款场景                             |
| DeductAmount       | int64  | 扣款总额                             |
| DeductUserCount    | int    | 扣款用户数                            |
| DeductSuccessCount | int    | 扣款成功数                            |
| SettleAmount       | int64  | 结算总额                             |
| SettleUserCount    | int    | 结算用户数                            |
| SettleSuccessCount | int    | 结算成功数                            |
| SenderID           | int64  | 发送者ID                            |
| SenderType         | string | 发送者类型                            |
| TotalAmount        | int64  | 总金额                              |
| Commission         | int64  | 佣金                               |
| PlayerCount        | int    | 玩家数                              |
| MinPlayerID        | int64  | 最小玩家ID                           |
| RewardType         | int    | 奖励类型                             |
| RewardAmount       | int64  | 奖励金额                             |
| Status             | int    | 状态（0扣款中/1已扣款/2结算中/3成功/4部分成功/5失败） |

### 5.3 RefundAudit（退款审核）

```
退款审核表，记录退款申请和处理状态
```

| 字段            | 类型          | 说明                         |
| ------------- | ----------- | -------------------------- |
| ID            | int64       | 主键                         |
| RefundOrderNo | string      | 退款订单号（唯一）                  |
| RoundTraceID  | string      | 回合追踪ID                     |
| BillID        | int64       | 关联账单ID                     |
| UserID        | int64       | 用户ID                       |
| RefundAmount  | int64       | 退款金额                       |
| RefundReason  | string      | 退款原因                       |
| RefundType    | int         | 退款类型                       |
| Status        | int         | 状态（0无/1待处理/2已批准/3已退款/4已拒绝） |
| AppliedAt     | time.Time   | 申请时间                       |
| ApprovedAt    | \*time.Time | 审批时间                       |
| RefundedAt    | \*time.Time | 退款完成时间                     |

### 5.4 ExceptionRecord（异常记录）

```
异常记录表，记录所有需要人工处理的异常情况
```

| 字段              | 类型          | 说明                        |
| --------------- | ----------- | ------------------------- |
| ID              | int64       | 主键                        |
| ExceptionNo     | string      | 异常编号（唯一）                  |
| ExceptionType   | int         | 异常类型（1扣款失败/2入账重试超限/3结算缺失） |
| BillID          | int64       | 关联账单ID                    |
| RoundTraceID    | string      | 回合追踪ID                    |
| BillType        | int         | 账单类型                      |
| UserID          | int64       | 用户ID                      |
| Amount          | int64       | 金额                        |
| Status          | int         | 状态（0待处理/1处理中/2已解决/3已忽略）   |
| ExceptionDetail | string      | 异常详情                      |
| HandleType      | int         | 处理类型（1人工/2退款/3重试/4忽略）     |
| HandleRemark    | string      | 处理备注                      |
| HandledAt       | \*time.Time | 处理时间                      |
| HandledBy       | int64       | 处理人ID                     |

***

## 六、结算流程

### 6.1 发红包流程

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  发送请求   │───→│  扣款操作   │───→│  创建账单   │
└─────────────┘    └─────────────┘    └─────────────┘
                          │
                    ┌─────┴─────┐
                    │           │
                  成功         失败
                    │           │
                    ↓           ↓
              ┌───────────┐ ┌───────────┐
              │ 等待结算  │ │ 标记异常  │
              └───────────┘ └───────────┘
```

**调用方法**：`SettlementService.SendPacket()`

**处理逻辑**：

1. 生成 RoundTraceID
2. 创建扣款账单（BillType=1）
3. 调用平台扣款接口
4. 成功：返回等待结算
5. 失败：创建异常记录，不重试

### 6.2 首回合扣款流程

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  发送请求   │───→│ 批量扣款    │───→│ 检查结果    │
└─────────────┘    └─────────────┘    └─────────────┘
                          │
                    ┌─────┴─────┐
                    │           │
                全部成功     部分失败
                    │           │
                    ↓           ↓
              ┌───────────┐ ┌───────────────────┐
              │ 正常流程  │ │ 失败玩家标记异常  │
              └───────────┘ │ 成功玩家触发退款  │
                            └───────────────────┘
```

**调用方法**：`DeductService.FirstRoundDeduct()`

**处理逻辑**：

1. 生成批次ID
2. 为每个玩家创建扣款账单（BillType=2）
3. 并发调用平台扣款接口
4. 统计成功/失败数量
5. 如果有失败：
   - 失败玩家：创建异常记录
   - 成功玩家：自动创建退款申请

### 6.3 回合结算流程

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  结算请求   │───→│  扣款阶段   │───→│  结算阶段   │───→│  佣金处理   │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
                          │                  │                  │
                    ┌─────┴─────┐      ┌─────┴─────┐      ┌─────┴─────┐
                    │           │      │           │      │           │
                  成功         失败   成功         失败   直接记录    │
                    │           │      │           │      Success    │
                    ↓           ↓      ↓           ↓           │
              ┌───────────┐ ┌─────┐ ┌─────┐   ┌─────┐           │
              │ 继续结算  │ │标记│ │完成 │   │重试 │           │
              └───────────┘ │异常│ │     │   │入账 │           │
                            └─────┘ └─────┘   └─────┘           │
```

**调用方法**：`SettlementService.SettleRound()`

**处理逻辑**：

1. 创建 RoundSettlement 记录
2. 执行扣款阶段：
   - 后续回合扣款（BillType=4）：最小玩家扣款
   - 系统红包扣款（BillType=9）：系统记录，不调平台
3. 执行结算阶段：
   - 抢红包入账（BillType=3）：玩家入账
4. 执行佣金处理（BillType=7）：系统记录，不调平台
5. 更新 RoundSettlement 状态

### 6.4 惩罚扣款流程（成对账单）

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  惩罚请求   │───→│ 玩家扣款    │───→│ 系统记录    │
└─────────────┘    └─────────────┘    └─────────────┘
                          │                  │
                    ┌─────┴─────┐      ┌─────┴─────┐
                    │           │      │           │
                  成功         失败   直接标记     │
                    │           │    Success      │
                    ↓           ↓                 │
              ┌───────────┐ ┌─────┐               │
              │ 系统记录  │ │标记│               │
              │ (Success) │ │异常│               │
              └───────────┘ └─────┘               │
```

**调用方法**：`SettlementService.DeductPenaltyToPlatform()`

**处理逻辑**：

1. 创建玩家扣款账单（BillType=8, amount<0）
2. 执行玩家扣款（调用平台接口）
3. 成功：创建系统账单（BillType=8, amount>0），直接标记 Success
4. 失败：创建异常记录

### 6.5 惩罚分配流程（成对账单）

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  分配请求   │───→│ 系统记录    │───→│ 玩家入账    │
└─────────────┘    └─────────────┘    └─────────────┘
                          │                  │
                    ┌─────┴─────┐      ┌─────┴─────┐
                    │           │      │           │
                  直接标记     无    成功         失败
                  Success      需重试  │           │
                                      ↓           ↓
                                ┌─────┐   ┌───────────┐
                                │完成 │   │ 调度器重试│
                                └─────┘   └───────────┘
```

**调用方法**：`SettlementService.DistributePenaltyFromPlatform()`

**处理逻辑**：

1. 创建系统扣款账单（BillType=10, amount<0），直接标记 Success
2. 为每个接收者创建入账账单（BillType=10, amount>0）
3. 执行玩家入账（调用平台接口）
4. 入账失败：设置重试时间，由调度器处理

### 6.6 系统奖励流程（成对账单）

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  奖励请求   │───→│ 系统记录    │───→│ 玩家入账    │
└─────────────┘    └─────────────┘    └─────────────┘
                          │                  │
                    ┌─────┴─────┐      ┌─────┴─────┐
                    │           │      │           │
                  直接标记     无    成功         失败
                  Success      需重试  │           │
                                      ↓           ↓
                                ┌─────┐   ┌───────────┐
                                │完成 │   │ 调度器重试│
                                └─────┘   └───────────┘
```

**调用方法**：`RewardSettler.SettleReward()`

**处理逻辑**：

1. 创建系统扣款账单（BillType=11, amount<0），直接标记 Success
2. 为每个玩家创建入账账单（BillType=11, amount>0）
3. 执行玩家入账（调用平台接口）
4. 入账失败：设置重试时间，由调度器处理

***

## 七、退款流程

### 7.1 退款类型

| RefundType | 常量名                      | 说明      | 处理方式   |
| ---------- | ------------------------ | ------- | ------ |
| 1          | RefundTypeFirstRoundFail | 首回合扣款失败 | 自动审批退款 |
| 2          | RefundTypeGameAbnormal   | 游戏异常    | 人工审批   |
| 3          | RefundTypeOther          | 其他原因    | 人工审批   |

### 7.2 退款流程

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  申请退款   │───→│  创建记录   │───→│  审批退款   │───→│  执行退款   │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
                                            │
                                      ┌─────┴─────┐
                                      │           │
                                    自动         人工
                                      │           │
                                      ↓           ↓
                                ┌───────────┐ ┌───────────┐
                                │首回合失败 │ │ 等待审批  │
                                └───────────┘ └───────────┘
```

**调用方法**：

- 申请：`RefundService.ApplyForRefund()`
- 审批：`RefundService.ApproveRefund()`
- 拒绝：`RefundService.RejectRefund()`

***

## 八、调度器系统

### 8.1 调度器架构

```
┌─────────────────────────────────────────────────────────────────┐
│                      Scheduler Manager                           │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
│  │ CreditRetry      │  │ PairBillCheck    │  │ SettlementCheck  │
│  │ Scheduler        │  │ Scheduler        │  │ Scheduler        │
│  │ (入账重试)        │  │ (成对账单检查)    │  │ (结算完整性检查)  │
│  │ 30秒             │  │ 1分钟            │  │ 5分钟            │
│  └──────────────────┘  └──────────────────┘  └──────────────────┘
│                                                                  │
│  ┌──────────────────┐  ┌──────────────────┐                     │
│  │ RefundProcess    │  │ ExceptionHandle  │                     │
│  │ Scheduler        │  │ Scheduler        │                     │
│  │ (退款处理)        │  │ (异常处理)        │                     │
│  │ 1分钟            │  │ 5分钟            │                     │
│  └──────────────────┘  └──────────────────┘                     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 8.2 CreditRetryScheduler（入账重试调度器）

**频率**：30秒

**职责**：重试所有入账失败的账单

**处理逻辑**：

1. 查询需要重试的入账账单：
   - `amount > 0`（入账）
   - `status IN (Processing, Failed)`
   - `retry_count < 3`
   - `next_retry_at IS NULL OR next_retry_at <= now`
2. 对每个账单：
   - 获取分布式锁
   - 执行入账操作
   - 成功：更新状态为 Success
   - 失败：
     - `retry_count++`
     - 设置 `next_retry_at`（指数退避：5s → 10s → 20s → ...）
     - 如果 `retry_count >= 3`，创建异常记录

### 8.3 PairBillCheckScheduler（成对账单检查调度器）

**频率**：1分钟

**职责**：检查成对账单的扣款成功但入账失败的情况

**适用场景**：

- 惩罚扣款（BillType=8）
- 系统红包（BillType=9）
- 惩罚分配（BillType=10）
- 系统奖励（BillType=11）

**处理逻辑**：

1. 查询扣款成功但入账失败的成对账单
2. 对每个扣款成功的账单：
   - 查找对应的入账账单
   - 如果入账账单不存在：创建入账账单并触发重试
   - 如果入账账单状态为 Failed/Processing：
     - 如果 `retry_count < 3`：触发重试
     - 如果 `retry_count >= 3`：创建异常记录

### 8.4 SettlementCheckScheduler（结算完整性检查调度器）

**频率**：5分钟

**职责**：检查结算流程的完整性

**检查项**：

| 检查项     | 条件                                          | 处理方式      |
| ------- | ------------------------------------------- | --------- |
| 发红包无结算  | `bill_type=1` 且成功，超过30分钟无 `bill_type=3` 的入账 | 创建异常记录    |
| 首回合部分失败 | `RoundSettlement.status=Failed`             | 检查退款是否已处理 |

### 8.5 RefundProcessScheduler（退款处理调度器）

**频率**：1分钟

**职责**：自动处理退款

**处理逻辑**：

1. 查询待处理的退款记录（`status=Pending`）
2. 对每条退款记录：
   - 如果是首回合失败类型（`RefundType=1`）：自动审批并执行退款

### 8.6 ExceptionHandleScheduler（异常处理调度器）

**频率**：5分钟

**职责**：处理标记的异常记录

**处理逻辑**：

1. 查询待处理的异常记录（`status=Pending`）
2. 根据异常类型处理：
   - `ExceptionTypeSettlementMissing`：创建退款申请

***

## 九、异常处理

### 9.1 异常类型

| ExceptionType | 常量名                            | 说明     | 处理方式     |
| ------------- | ------------------------------ | ------ | -------- |
| 1             | ExceptionTypeDebitFailed       | 扣款失败   | 人工处理     |
| 2             | ExceptionTypeCreditRetryExceed | 入账重试超限 | 人工处理     |
| 3             | ExceptionTypeSettlementMissing | 结算账单缺失 | 自动创建退款申请 |

### 9.2 异常状态流转

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│   Pending   │───→│ Processing  │───→│  Resolved   │
│  (待处理)   │    │  (处理中)   │    │  (已解决)   │
└─────────────┘    └─────────────┘    └─────────────┘
       │                                     │
       │                                     │
       ↓                                     ↓
┌─────────────┐                       ┌─────────────┐
│   Ignored   │                       │   Ignored   │
│  (已忽略)   │                       │  (已忽略)   │
└─────────────┘                       └─────────────┘
```

***

## 十、服务接口

### 10.1 SettlementService（结算服务）

| 方法                    | 说明         |
| --------------------- | ---------- |
| `SendPacket()`        | 发红包扣款      |
| `SettleRound()`       | 回合结算       |
| `DeductPenaltyToPlatform()` | 惩罚扣款到平台 |
| `DistributePenaltyFromPlatform()` | 惩罚分配 |
| `GetBillByTraceID()`  | 根据追踪ID获取账单 |

### 10.2 DeductService（扣款服务）

| 方法                     | 说明     |
| ---------------------- | ------ |
| `FirstRoundDeduct()`   | 首回合扣款  |
| `LaterRoundDeduct()`   | 后续回合扣款 |
| `SystemPacketDeduct()` | 系统红包扣款（只记录） |
| `SingleDeduct()`       | 单笔扣款   |

### 10.3 RefundService（退款服务）

| 方法                 | 说明   |
| ------------------ | ---- |
| `ApplyForRefund()` | 申请退款 |
| `ApproveRefund()`  | 审批退款 |
| `RejectRefund()`   | 拒绝退款 |

### 10.4 BalanceService（余额服务）

| 方法                       | 说明       |
| ------------------------ | -------- |
| `CheckBalanceForReady()` | 检查余额是否足够 |
| `CalculateRequiredFee()` | 计算所需费用   |
| `CheckUserBalance()`     | 查询用户余额   |

### 10.5 RewardSettler（奖励结算器）

| 方法               | 说明   |
| ---------------- | ---- |
| `SettleReward()` | 结算奖励 |

### 10.6 UserIDConvertService（用户ID转换服务）

| 方法                    | 说明         |
| --------------------- | ---------- |
| `GetPlatformUserID()` | 内部用户ID转平台用户ID |

**注意**：系统账号（UserID=0）调用此方法会返回错误，因为系统账号不调用平台接口。

***

## 十一、状态机

### 11.1 BillRecord 状态机

```
┌─────────────┐    成功    ┌─────────────┐
│ Processing  │───────────→│   Success   │
│  (处理中)   │            │   (成功)    │
└─────────────┘            └─────────────┘
       │                         │
     失败                       退款
       │                         │
       ↓                         ↓
┌─────────────┐            ┌─────────────┐
│   Failed    │            │  Refunded   │
│   (失败)    │            │  (已退款)   │
└─────────────┘            └─────────────┘
```

### 11.2 RoundSettlement 状态机

```
┌─────────────┐    扣款完成  ┌─────────────┐    结算完成  ┌─────────────┐
│  Deducting  │────────────→│  Deducted   │────────────→│  Settling   │
│  (扣款中)   │             │  (已扣款)   │             │  (结算中)   │
└─────────────┘             └─────────────┘             └─────────────┘
                                  │                           │
                            部分失败                      全部成功
                                  │                           │
                                  ↓                           ↓
                            ┌─────────────┐             ┌─────────────┐
                            │   Partial   │             │   Success   │
                            │ (部分成功)  │             │   (成功)    │
                            └─────────────┘             └─────────────┘
                                  │
                              全部失败
                                  │
                                  ↓
                            ┌─────────────┐
                            │   Failed    │
                            │   (失败)    │
                            └─────────────┘
```

***

## 十二、分布式锁

### 12.1 锁 Key 定义

| Key                                                    | 说明        | TTL  |
| ------------------------------------------------------ | --------- | ---- |
| `settlement:deduct:lock:{roundID}:{billType}:{userID}` | 扣款锁       | 30s  |
| `settlement:bill:retry:lock:{billID}`                  | 账单重试锁     | 30s  |
| `settlement:pair_bill:check:lock:{roundTraceID}`       | 成对账单检查锁   | 30s  |
| `scheduler:credit_retry:lock`                          | 入账重试调度锁   | 60s  |
| `scheduler:pair_bill_check:lock`                       | 成对账单检查调度锁 | 120s |
| `scheduler:settlement_check:lock`                      | 结算检查调度锁   | 300s |
| `scheduler:refund_process:lock`                        | 退款处理调度锁   | 120s |
| `scheduler:exception_handle:lock`                      | 异常处理调度锁   | 300s |

***

## 十三、重试策略

### 13.1 入账重试配置

| 参数              | 值    | 说明     |
| --------------- | ---- | ------ |
| MaxRetryCount   | 3    | 最大重试次数 |
| BaseDelay       | 5s   | 基础延迟   |
| MaxDelay        | 5min | 最大延迟   |
| RetryMultiplier | 2.0  | 延迟倍数   |

### 13.2 重试时间计算

```
第1次重试: 5秒后
第2次重试: 10秒后 (5 * 2^1)
第3次重试: 20秒后 (5 * 2^2)
```

***

## 十四、文件结构

```
settlement/
├── config/
│   └── config.go              # 配置定义
├── dto/
│   ├── constants.go           # 常量定义
│   ├── request.go             # 请求结构体
│   └── response.go            # 响应结构体
├── infrastructure/
│   └── persistence/
│       └── redis/
│           └── keys.go        # Redis Key 定义
├── model/
│   ├── bill.go                # 账单模型
│   ├── exception_record.go    # 异常记录模型
│   └── refund.go              # 退款模型
├── scheduler/
│   ├── base.go                # 调度器骨架
│   ├── manager.go             # 调度器管理器
│   ├── credit_retry_scheduler.go        # 入账重试调度器
│   ├── pair_bill_check_scheduler.go     # 成对账单检查调度器
│   ├── settlement_check_scheduler.go    # 结算检查调度器
│   ├── refund_process_scheduler.go      # 退款处理调度器
│   └── exception_handle_scheduler.go    # 异常处理调度器
└── service/
    ├── balance_service.go     # 余额服务
    ├── bill_manager.go        # 账单管理器
    ├── credit_retry_service.go          # 入账重试服务
    ├── deduct_service.go      # 扣款服务
    ├── exception_manager.go   # 异常管理器
    ├── pair_bill_check_service.go       # 成对账单检查服务
    ├── refund_service.go      # 退款服务
    ├── reward_settler.go      # 奖励结算器
    ├── settlement_check_service.go      # 结算检查服务
    ├── settlement_service.go  # 结算服务
    ├── trace_id_generator.go  # 追踪ID生成器
    └── user_id_convert_service.go       # 用户ID转换服务
```

***

## 十五、完整性检查清单

| 检查项         | 状态 | 处理方式                                          |
| ----------- | -- | --------------------------------------------- |
| 发红包扣款失败     | ✅  | 创建异常记录（不重试）                                   |
| 发红包无结算      | ✅  | SettlementCheckScheduler → 创建异常 → 退款          |
| 首回合扣款失败     | ✅  | 创建异常记录（不重试）                                   |
| 首回合部分失败退款   | ✅  | 现有逻辑 + RefundProcessScheduler                 |
| 抢红包入账失败     | ✅  | CreditRetryScheduler 重试                       |
| 后续回合扣款失败    | ✅  | 创建异常记录（不重试）                                   |
| 佣金入账        | ✅  | 系统记录，不调平台接口                                   |
| 惩罚扣款-玩家扣款失败 | ✅  | 创建异常记录（不重试）                                   |
| 惩罚扣款-系统记录   | ✅  | 直接标记 Success                                  |
| 系统红包-系统记录   | ✅  | 直接标记 Success                                  |
| 系统红包-玩家入账失败 | ✅  | PairBillCheckScheduler → CreditRetryScheduler |
| 惩罚分配-系统记录   | ✅  | 直接标记 Success                                  |
| 惩罚分配-玩家入账失败 | ✅  | PairBillCheckScheduler → CreditRetryScheduler |
| 系统奖励-系统记录   | ✅  | 直接标记 Success                                  |
| 系统奖励-玩家入账失败 | ✅  | PairBillCheckScheduler → CreditRetryScheduler |
