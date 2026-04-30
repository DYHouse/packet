# Settlement 模块重构方案 V2

> 文档版本：v2.0  
> 创建日期：2026-04-11  
> 基于现有代码：`/Users/aaron.pan/Desktop/demo/backend/settlement`

---

## 目录

- [一、现有问题分析](#一现有问题分析)
- [二、重构目标](#二重构目标)
- [三、TraceID 体系设计](#三traceid-体系设计)
- [四、表结构设计](#四表结构设计)
- [五、扣款逻辑设计](#五扣款逻辑设计)
- [六、对账调度设计](#六对账调度设计)
- [七、退款审核设计](#七退款审核设计)
- [八、幂等性保证](#八幂等性保证)
- [九、事务性保证](#九事务性保证)
- [十、代码实现方案](#十代码实现方案)
- [十一、实施计划](#十一实施计划)

---

## 一、现有问题分析

### 1.1 TraceID 职责定义混乱

**问题表现：**

- TraceID 在多处生成（[settlement_service.go:46](file:///Users/aaron.pan/Desktop/demo/backend/settlement/service/settlement_service.go#L46)、[settlement_service.go:110](file:///Users/aaron.pan/Desktop/demo/backend/settlement/service/settlement_service.go#L110)），无统一规范
- TraceID 既用于发红包扣款，也用于回合结算，语义不清晰
- 缺少业务流水号概念，无法唯一标识一笔业务操作
- 平台接口使用 `orderNo` 作为幂等键，与 TraceID 混用

**代码示例：**

```go
// settlement_service.go:46
traceID := idgen.GenerateString()
if req.TraceID != "" {
    traceID = req.TraceID
}

// settlement_service.go:110
traceID := idgen.GenerateString()
if req.TraceID != "" {
    traceID = req.TraceID
}
```

**影响：**
- 难以追踪完整的业务流程
- 幂等性保证不完善
- 对账困难

### 1.2 表设计缺陷

**BillRecord 表问题：**

```go
// model/bill.go:18-35
type BillRecord struct {
    ID            int64     `gorm:"primaryKey;autoIncrement" json:"id"`
    TraceID       string    `gorm:"index;size:32;not null" json:"trace_id"`  // ❌ 不是唯一索引
    BillType      int       `gorm:"index;not null" json:"bill_type"`
    // ... 其他字段
}
```

问题列表：
- ❌ TraceID 不是唯一索引，可能导致重复记录
- ❌ 缺少扣款类型字段（首回合平摊 vs 后续回合最低金额玩家扣款）
- ❌ 缺少退款状态和退款记录字段
- ❌ 缺少对账状态字段
- ❌ 缺少业务流水号（用于平台接口幂等）
- ❌ 缺少批次ID（用于首回合批量扣款）

**RoundSettlement 表问题：**

```go
// model/bill.go:41-58
type RoundSettlement struct {
    ID          int64      `gorm:"primaryKey;autoIncrement" json:"id"`
    TraceID     string     `gorm:"uniqueIndex;size:32;not null" json:"trace_id"`
    // ... 其他字段
}
```

问题列表：
- ❌ 缺少扣款类型字段
- ❌ 缺少退款状态字段
- ❌ 缺少首回合批量扣款的汇总信息
- ❌ 缺少扣款和结算两个阶段的字段区分

### 1.3 扣款逻辑缺失

**当前逻辑：**

```go
// settlement_service.go:35-69
func (s *SettlementService) DeductForSendPacket(ctx context.Context, req *model.SendPacketRequest) error {
    // 只有发红包扣款
    // 没有区分首回合平摊扣款和后续回合最低金额玩家扣款
}
```

**业务需求：**

```
每个轮次（10回合）：
├─ 首回合：每个玩家平摊房费（5个玩家必须全部扣款成功）
└─ 后续回合：抢红包金额最低玩家扣款房费
```

### 1.4 缺少对账调度

**当前状态：**

```go
// scheduler/retry_scheduler.go
type RetryScheduler struct {
    // 只有重试调度器
    // 没有对账调度器
}
```

**业务需求：**

```
扣款发红包后，如果游戏回合过程中出错：
├─ 需要人工审核退款
├─ 退款必须人工审核确认
└─ 不可以系统自动退款
```

### 1.5 事务性不足

**问题：**

```go
// bill_manager.go:21-23
func (m *BillManager) CreateBill(ctx context.Context, bill *model.BillRecord) error {
    return m.db.Create(bill).Error  // ❌ 单表操作，没有事务保证
}

// settlement_service.go:63-68
if err := s.billMgr.CreateBill(ctx, bill); err != nil {
    return err
}
return s.executeDeduct(ctx, bill, req.RoomFee, req.RoundID)  // ❌ 账单创建和扣款没有原子性
```

### 1.6 幂等性保证不完善

**当前实现：**

```
1. Redis 分布式锁 ✓
2. RoundID + BillType 检查 ✓
3. TraceID 唯一索引 ✗（不是唯一索引）
```

**缺失：**
- 业务流水号唯一性保证
- 平台接口幂等性保证
- 数据库唯一约束

---

## 二、重构目标

### 2.1 核心目标

1. **TraceID 体系清晰化**：建立层次化的 TraceID 体系，明确职责
2. **表结构完善化**：补充缺失字段，支持完整业务流程
3. **扣款逻辑完善**：实现首回合批量扣款和后续回合扣款
4. **对账调度实现**：自动检测异常，生成退款申请
5. **退款审核流程**：人工审核退款，保证资金安全
6. **幂等性保证**：多层幂等性设计，防止重复扣款
7. **事务性保证**：完善事务处理，保证数据一致性

### 2.2 非功能性目标

- 高并发支持：支持多服务部署场景
- 数据安全：防止重复扣款和重复退款
- 可追溯性：完整的资金流转追踪
- 可维护性：代码结构清晰，易于维护

---

## 三、TraceID 体系设计

### 3.1 TraceID 层次定义

```
TraceID 体系：
├─ round_trace_id（回合追踪ID）
│   └─ 格式：RT_会话ID_回合编号
│   └─ 示例：RT_1234567890_1
│   └─ 作用：追踪回合的资金流转
│   └─ 可以解析出：session_id、round_no
│
└─ biz_order_no（业务流水号）
    └─ 格式：业务类型_时间戳_用户ID_随机数
    └─ 示例：DEDUCT_FIRST_20250411120000_1001_001
    └─ 作用：平台接口幂等、对账
    └─ 特点：全局唯一
```

### 3.2 TraceID 职责划分

| ID类型 | 作用域 | 用途 | 示例 |
|--------|--------|------|------|
| round_trace_id | Round 级别 | 追踪单个回合的资金流转 | RT_1234567890_1 |
| biz_order_no | 操作级别 | 平台接口幂等、对账 | DEDUCT_FIRST_20250411120000_1001_001 |

### 3.3 TraceID 生成规范

```go
package trace

import (
    "fmt"
    "time"
    "github.com/cashparty/backend/common/idgen"
)

type TraceIDGenerator struct {
    idGen *idgen.SnowflakeIDGenerator
}

func NewTraceIDGenerator(idGen *idgen.SnowflakeIDGenerator) *TraceIDGenerator {
    return &TraceIDGenerator{idGen: idGen}
}

func (g *TraceIDGenerator) GenerateRoundTraceID(sessionID int64, roundNo int) string {
    return fmt.Sprintf("RT_%d_%d", sessionID, roundNo)
}

func (g *TraceIDGenerator) GenerateBizOrderNo(bizType string, userID int64) string {
    timestamp := time.Now().Format("20060102150405")
    random := g.idGen.NextID() % 1000
    return fmt.Sprintf("%s_%s_%d_%03d", bizType, timestamp, userID, random)
}

func ParseRoundTraceID(roundTraceID string) (sessionID int64, roundNo int, err error) {
    parts := strings.Split(roundTraceID, "_")
    if len(parts) != 3 || parts[0] != "RT" {
        return 0, 0, fmt.Errorf("invalid round_trace_id format")
    }
    
    sessionID, err = strconv.ParseInt(parts[1], 10, 64)
    if err != nil {
        return 0, 0, err
    }
    
    roundNo, err = strconv.Atoi(parts[2])
    if err != nil {
        return 0, 0, err
    }
    
    return sessionID, roundNo, nil
}
```

---

## 四、表结构设计

### 4.1 bill_record 表（账单记录表）

```sql
CREATE TABLE `bill_record` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID',
    
    -- 追踪ID
    `round_trace_id` VARCHAR(32) NOT NULL COMMENT 'RT_会话ID_回合编号',
    `biz_order_no` VARCHAR(64) NOT NULL COMMENT '业务流水号（平台幂等键）',
    `platform_trans_id` VARCHAR(64) DEFAULT '' COMMENT '平台交易ID',
    
    -- 账单类型
    `bill_type` TINYINT NOT NULL COMMENT '1=首回合扣款,2=后续扣款,3=抢红包收入,4=惩罚罚款,5=惩罚分红',
    `deduct_scene` TINYINT DEFAULT 0 COMMENT '扣款场景:1=首回合平摊,2=后续最低金额',
    
    -- 业务关联
    `room_id` BIGINT NOT NULL COMMENT '房间ID',
    `session_id` BIGINT NOT NULL COMMENT '会话ID',
    `round_id` BIGINT NOT NULL COMMENT '回合ID',
    `round_no` INT NOT NULL DEFAULT 0 COMMENT '回合编号',
    `user_id` BIGINT NOT NULL COMMENT '用户ID',
    `batch_id` VARCHAR(32) DEFAULT '' COMMENT '批次ID（首回合批量扣款）',
    
    -- 金额信息
    `amount` BIGINT NOT NULL COMMENT '金额(正数=收入,负数=支出)',
    `balance_before` BIGINT NOT NULL DEFAULT 0 COMMENT '操作前余额',
    `balance_after` BIGINT NOT NULL DEFAULT 0 COMMENT '操作后余额',
    
    -- 状态管理
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '状态:0=处理中,1=成功,2=失败,3=已退款',
    `reconcile_status` TINYINT NOT NULL DEFAULT 0 COMMENT '对账状态:0=未对账,1=已对账,2=对账异常',
    
    -- 退款信息
    `refund_status` TINYINT NOT NULL DEFAULT 0 COMMENT '退款状态:0=无需退款,1=待退款,2=退款审核中,3=已退款,4=退款拒绝',
    `refund_order_no` VARCHAR(64) DEFAULT '' COMMENT '退款流水号',
    `refund_amount` BIGINT NOT NULL DEFAULT 0 COMMENT '退款金额',
    `refund_reason` VARCHAR(256) DEFAULT '' COMMENT '退款原因',
    `refund_applied_at` DATETIME DEFAULT NULL COMMENT '退款申请时间',
    `refund_approved_at` DATETIME DEFAULT NULL COMMENT '退款审核时间',
    `refund_approved_by` BIGINT DEFAULT 0 COMMENT '退款审核人ID',
    
    -- 重试信息
    `retry_count` INT NOT NULL DEFAULT 0 COMMENT '重试次数',
    `next_retry_at` DATETIME DEFAULT NULL COMMENT '下次重试时间',
    
    -- 错误信息
    `error_code` VARCHAR(32) DEFAULT '' COMMENT '错误码',
    `error_message` VARCHAR(512) DEFAULT '' COMMENT '错误信息',
    
    -- 备注信息
    `remark` VARCHAR(256) DEFAULT '' COMMENT '备注',
    
    -- 时间戳
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_biz_order_no` (`biz_order_no`),
    UNIQUE KEY `uk_platform_trans` (`platform_trans_id`),
    KEY `idx_round_trace` (`round_trace_id`),
    KEY `idx_bill_type` (`bill_type`),
    KEY `idx_room_id` (`room_id`),
    KEY `idx_session_id` (`session_id`),
    KEY `idx_round_id` (`round_id`),
    KEY `idx_user_id` (`user_id`),
    KEY `idx_status` (`status`),
    KEY `idx_reconcile_status` (`reconcile_status`),
    KEY `idx_refund_status` (`refund_status`),
    KEY `idx_batch_id` (`batch_id`),
    KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账单记录表（本地事务表）';
```

### 4.2 round_settlement 表（回合结算表）

```sql
CREATE TABLE `round_settlement` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID',
    
    -- 追踪ID
    `round_trace_id` VARCHAR(32) NOT NULL COMMENT 'RT_会话ID_回合编号',
    
    -- 业务关联
    `room_id` BIGINT NOT NULL COMMENT '房间ID',
    `session_id` BIGINT NOT NULL COMMENT '会话ID',
    `round_id` BIGINT NOT NULL COMMENT '回合ID',
    `round_no` INT NOT NULL COMMENT '回合编号',
    
    -- ========== 扣款阶段字段 ==========
    `deduct_scene` TINYINT NOT NULL COMMENT '扣款场景:1=首回合平摊,2=后续最低金额',
    `deduct_amount` BIGINT NOT NULL DEFAULT 0 COMMENT '扣款总金额',
    `deduct_user_count` INT NOT NULL DEFAULT 0 COMMENT '需要扣款用户数',
    `deduct_success_count` INT NOT NULL DEFAULT 0 COMMENT '扣款成功用户数',
    `deducted_at` DATETIME DEFAULT NULL COMMENT '扣款完成时间',
    
    -- ========== 结算阶段字段 ==========
    `settle_amount` BIGINT NOT NULL DEFAULT 0 COMMENT '结算总金额',
    `settle_user_count` INT NOT NULL DEFAULT 0 COMMENT '需要结算用户数',
    `settle_success_count` INT NOT NULL DEFAULT 0 COMMENT '结算成功用户数',
    `settled_at` DATETIME DEFAULT NULL COMMENT '结算完成时间',
    
    -- ========== 状态字段 ==========
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '0=扣款中,1=扣款完成,2=结算中,3=成功,4=部分成功,5=失败',
    `reconcile_status` TINYINT NOT NULL DEFAULT 0 COMMENT '对账状态:0=未对账,1=已对账,2=对账异常',
    
    -- ========== 退款信息 ==========
    `refund_status` TINYINT NOT NULL DEFAULT 0 COMMENT '退款状态:0=无需退款,1=待退款,2=退款审核中,3=已退款',
    `refund_reason` VARCHAR(256) DEFAULT '' COMMENT '退款原因',
    
    -- ========== 其他信息 ==========
    `sender_id` BIGINT NOT NULL DEFAULT 0 COMMENT '发红包者ID',
    `sender_type` VARCHAR(20) NOT NULL DEFAULT '' COMMENT 'platform/player',
    `min_player_id` BIGINT NOT NULL DEFAULT 0 COMMENT '最低金额玩家ID',
    `total_amount` BIGINT NOT NULL DEFAULT 0 COMMENT '红包总金额',
    `commission` BIGINT NOT NULL DEFAULT 0 COMMENT '平台佣金',
    `error_message` VARCHAR(512) DEFAULT '' COMMENT '错误信息',
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_round_trace` (`round_trace_id`),
    UNIQUE KEY `uk_round_id` (`round_id`),
    KEY `idx_session_id` (`session_id`),
    KEY `idx_status` (`status`),
    KEY `idx_reconcile_status` (`reconcile_status`),
    KEY `idx_refund_status` (`refund_status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='回合结算表（本地事务表）';
```

### 4.3 refund_audit 表（退款审核表）

```sql
CREATE TABLE `refund_audit` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID',
    
    -- 退款申请信息
    `refund_order_no` VARCHAR(64) NOT NULL COMMENT '退款流水号',
    `round_trace_id` VARCHAR(32) DEFAULT '' COMMENT '回合追踪ID',
    `batch_id` VARCHAR(32) DEFAULT '' COMMENT '批次ID',
    
    -- 业务关联
    `room_id` BIGINT NOT NULL COMMENT '房间ID',
    `session_id` BIGINT NOT NULL COMMENT '会话ID',
    `round_id` BIGINT DEFAULT 0 COMMENT '回合ID',
    `user_id` BIGINT NOT NULL COMMENT '用户ID',
    
    -- 关联账单
    `bill_id` BIGINT NOT NULL COMMENT '关联账单ID',
    `bill_order_no` VARCHAR(64) NOT NULL COMMENT '原业务流水号',
    
    -- 退款信息
    `refund_amount` BIGINT NOT NULL COMMENT '退款金额',
    `refund_reason` VARCHAR(512) NOT NULL COMMENT '退款原因',
    `refund_type` TINYINT NOT NULL COMMENT '退款类型:1=首回合扣款失败退款,2=游戏异常退款,3=其他退款',
    
    -- 审核信息
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '状态:0=待审核,1=审核通过,2=审核拒绝,3=已退款',
    `applied_at` DATETIME NOT NULL COMMENT '申请时间',
    `applied_by` BIGINT NOT NULL DEFAULT 0 COMMENT '申请人ID（系统为0）',
    
    -- 审核结果
    `approved_at` DATETIME DEFAULT NULL COMMENT '审核时间',
    `approved_by` BIGINT DEFAULT 0 COMMENT '审核人ID',
    `approve_remark` VARCHAR(256) DEFAULT '' COMMENT '审核备注',
    
    -- 退款执行
    `refunded_at` DATETIME DEFAULT NULL COMMENT '退款完成时间',
    `platform_trans_id` VARCHAR(64) DEFAULT '' COMMENT '平台退款交易ID',
    `error_message` VARCHAR(512) DEFAULT '' COMMENT '错误信息',
    
    -- 时间戳
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_refund_order_no` (`refund_order_no`),
    KEY `idx_round_trace` (`round_trace_id`),
    KEY `idx_bill_id` (`bill_id`),
    KEY `idx_user_id` (`user_id`),
    KEY `idx_status` (`status`),
    KEY `idx_batch_id` (`batch_id`),
    KEY `idx_applied_at` (`applied_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='退款审核表';
```

### 4.4 reconcile_record 表（对账记录表）

```sql
CREATE TABLE `reconcile_record` (
    `id` BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID',
    
    -- 对账范围
    `reconcile_no` VARCHAR(32) NOT NULL COMMENT '对账批次号',
    `reconcile_type` TINYINT NOT NULL COMMENT '对账类型:1=定时对账,2=异常对账,3=手动对账',
    `reconcile_scope` TINYINT NOT NULL COMMENT '对账范围:1=会话级,2=回合级,3=账单级',
    
    -- 业务关联
    `round_trace_id` VARCHAR(32) DEFAULT '' COMMENT '回合追踪ID',
    `bill_id` BIGINT DEFAULT 0 COMMENT '账单ID',
    `biz_order_no` VARCHAR(64) DEFAULT '' COMMENT '业务流水号',
    
    -- 对账结果
    `status` TINYINT NOT NULL DEFAULT 0 COMMENT '状态:0=对账中,1=一致,2=不一致',
    `expected_amount` BIGINT NOT NULL DEFAULT 0 COMMENT '预期金额',
    `actual_amount` BIGINT NOT NULL DEFAULT 0 COMMENT '实际金额',
    `diff_amount` BIGINT NOT NULL DEFAULT 0 COMMENT '差异金额',
    
    -- 对账详情
    `local_data` TEXT COMMENT '本地数据',
    `remote_data` TEXT COMMENT '远程数据',
    `diff_detail` TEXT COMMENT '差异详情',
    
    -- 处理信息
    `handled` TINYINT NOT NULL DEFAULT 0 COMMENT '是否已处理:0=未处理,1=已处理',
    `handle_type` TINYINT DEFAULT 0 COMMENT '处理方式:1=自动修复,2=人工处理',
    `handle_remark` VARCHAR(256) DEFAULT '' COMMENT '处理备注',
    `handled_at` DATETIME DEFAULT NULL COMMENT '处理时间',
    `handled_by` BIGINT DEFAULT 0 COMMENT '处理人ID',
    
    -- 时间戳
    `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP COMMENT '创建时间',
    `updated_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT '更新时间',
    
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_reconcile_no` (`reconcile_no`),
    KEY `idx_round_trace` (`round_trace_id`),
    KEY `idx_bill_id` (`bill_id`),
    KEY `idx_status` (`status`),
    KEY `idx_handled` (`handled`),
    KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='对账记录表';
```

### 4.5 表关系说明

```
game_session（游戏会话）
    │
    ├─ session_id: 1234567890
    └─ max_rounds: 10
         │
         ├─── round_settlement（回合结算汇总）
         │    ├─ round_id: 1001 (第1回合)
         │    ├─ deduct_scene: 1 (首回合平摊)
         │    ├─ deduct_user_count: 5
         │    ├─ deduct_success_count: 5
         │    └─ status: 3 (成功)
         │         │
         │         ├─── bill_record（账单明细）- 玩家A
         │         │    ├─ round_id: 1001
         │         │    ├─ user_id: 10001
         │         │    ├─ bill_type: 1 (首回合扣款)
         │         │    └─ amount: -100
         │         │
         │         ├─── bill_record（账单明细）- 玩家B
         │         │    ├─ round_id: 1001
         │         │    ├─ user_id: 10002
         │         │    ├─ bill_type: 1 (首回合扣款)
         │         │    └─ amount: -100
         │         │
         │         └─── ... (其他玩家账单)
         │
         └─── round_settlement（回合结算汇总）
              ├─ round_id: 1002 (第2回合)
              ├─ deduct_scene: 2 (后续回合最低金额)
              ├─ min_player_id: 10003
              └─ status: 3 (成功)
                   │
                   ├─── bill_record（账单明细）- 扣款
                   │    ├─ round_id: 1002
                   │    ├─ user_id: 10003 (最低金额玩家)
                   │    ├─ bill_type: 2 (后续回合扣款)
                   │    └─ amount: -100
                   │
                   ├─── bill_record（账单明细）- 玩家A收入
                   │    ├─ round_id: 1002
                   │    ├─ user_id: 10001
                   │    ├─ bill_type: 3 (抢红包收入)
                   │    └─ amount: 50
                   │
                   └─── ... (其他玩家收入账单)
```

---

## 五、扣款逻辑设计

### 5.1 扣款场景定义

```go
const (
    DeductSceneFirstRoundShare  = 1 // 首回合平摊扣款
    DeductSceneLaterRoundMin    = 2 // 后续回合最低金额玩家扣款
)

const (
    BillTypeFirstRoundDeduct    = 1 // 首回合平摊扣款
    BillTypeLaterRoundDeduct    = 2 // 后续回合房费扣款
    BillTypeGrabPacketCredit    = 3 // 抢红包收入
    BillTypePenaltyDeduct       = 4 // 惩罚罚款
    BillTypePenaltyShare        = 5 // 惩罚分红
)

const (
    RoundStatusDeducting   = 0  // 扣款中
    RoundStatusDeducted    = 1  // 扣款完成，等待发红包
    RoundStatusSettling    = 2  // 结算中
    RoundStatusSuccess     = 3  // 全部成功
    RoundStatusPartial     = 4  // 部分成功
    RoundStatusFailed      = 5  // 失败（需要退款）
)
```

### 5.2 首回合批量扣款流程

```
首回合扣款流程：
├─ 1. 创建 round_settlement（状态：扣款中）
├─ 2. 预检查所有玩家余额
├─ 3. 批量创建 bill_record（扣款账单）
├─ 4. 执行批量扣款（并发）
├─ 5. 检查扣款结果
│    ├─ 全部成功 → 更新状态为"扣款完成"
│    └─ 部分失败 → 创建退款申请，更新状态为"失败"
└─ 6. 等待发红包
```

### 5.3 后续回合扣款流程

```
后续回合扣款流程：
├─ 1. 创建 round_settlement（状态：扣款中）
├─ 2. 创建 bill_record（最低金额玩家扣款账单）
├─ 3. 执行扣款
│    ├─ 成功 → 更新状态为"扣款完成"
│    └─ 失败 → 标记失败，等待重试
└─ 4. 等待发红包
```

### 5.4 回合结算流程

```
回合结算流程：
├─ 1. 更新 round_settlement（状态：结算中）
├─ 2. 批量创建 bill_record（结算账单）
├─ 3. 执行批量结算（并发）
├─ 4. 检查结算结果
│    ├─ 全部成功 → 更新状态为"成功"
│    ├─ 部分成功 → 更新状态为"部分成功"
│    └─ 全部失败 → 更新状态为"失败"
└─ 5. 完成结算
```

---

## 六、对账调度设计

### 6.1 对账调度器设计

```go
type ReconcileScheduler struct {
    billMgr      *BillManager
    settleSvc    *SettlementService
    platform     platform.Client
    traceIDGen   *trace.TraceIDGenerator
    interval     time.Duration
    stopCh       chan struct{}
}

func (s *ReconcileScheduler) Start() {
    ticker := time.NewTicker(s.interval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            s.runReconcile()
        case <-s.stopCh:
            return
        }
    }
}

func (s *ReconcileScheduler) runReconcile() {
    ctx := context.Background()
    
    // 1. 对账未对账的账单
    s.reconcilePendingBills(ctx)
    
    // 2. 对账异常会话
    s.reconcileAbnormalSessions(ctx)
    
    // 3. 检测需要退款的场景
    s.detectRefundNeeded(ctx)
}
```

### 6.2 对账策略

```
对账策略：
├─ 定时对账：每小时执行一次
├─ 对账范围：最近24小时的账单
├─ 对账逻辑：
│    ├─ 查询状态为 Success 的账单
│    ├─ 调用平台接口查询交易状态
│    ├─ 对比金额是否一致
│    └─ 发现不一致则记录并告警
└─ 异常处理：
     ├─ 自动重试
     ├─ 人工介入
     └─ 生成退款申请
```

### 6.3 异常检测

```
异常检测场景：
├─ 首回合扣款失败
│    └─ 检测：部分玩家扣款成功，部分失败
│    └─ 处理：为成功扣款的玩家创建退款申请
│
├─ 游戏异常中断
│    └─ 检测：扣款成功但游戏未正常结束
│    └─ 处理：创建退款申请
│
└─ 结算失败
     └─ 检测：部分玩家结算失败
     └─ 处理：重试或人工处理
```

---

## 七、退款审核设计

### 7.1 退款申请流程

```go
// ApplyForRefund 申请退款
func (s *SettlementService) ApplyForRefund(ctx context.Context, req *RefundApplyRequest) error {
    // 1. 验证账单状态
    bill, err := s.billMgr.GetBillByID(ctx, req.BillID)
    if err != nil {
        return fmt.Errorf("bill not found: %w", err)
    }
    
    if bill.Status != model.BillStatusSuccess {
        return fmt.Errorf("bill status is not success, cannot refund")
    }
    
    if bill.RefundStatus == model.RefundStatusRefunded {
        return fmt.Errorf("bill already refunded")
    }
    
    // 2. 创建退款审核记录
    refundOrderNo := s.traceIDGen.GenerateBizOrderNo("REFUND", bill.UserID)
    refundAudit := &model.RefundAudit{
        RefundOrderNo:   refundOrderNo,
        RoundTraceID:    bill.RoundTraceID,
        BatchID:         bill.BatchID,
        RoomID:          bill.RoomID,
        SessionID:       bill.SessionID,
        RoundID:         bill.RoundID,
        UserID:          bill.UserID,
        BillID:          bill.ID,
        BillOrderNo:     bill.BizOrderNo,
        RefundAmount:    req.RefundAmount,
        RefundReason:    req.RefundReason,
        RefundType:      req.RefundType,
        Status:          model.RefundStatusPending,
        AppliedAt:       time.Now(),
        AppliedBy:       req.AppliedBy,
    }
    
    if err := s.billMgr.CreateRefundAudit(ctx, refundAudit); err != nil {
        return fmt.Errorf("create refund audit failed: %w", err)
    }
    
    // 3. 更新账单退款状态
    return s.billMgr.UpdateBillRefundStatus(ctx, bill.ID, model.RefundStatusPending, refundOrderNo)
}
```

### 7.2 退款审核流程

```go
// ApproveRefund 审核通过退款
func (s *SettlementService) ApproveRefund(ctx context.Context, req *RefundApproveRequest) error {
    // 1. 查询退款审核记录
    refund, err := s.billMgr.GetRefundAuditByOrderNo(ctx, req.RefundOrderNo)
    if err != nil {
        return fmt.Errorf("refund audit not found: %w", err)
    }
    
    if refund.Status != model.RefundStatusPending {
        return fmt.Errorf("refund status is not pending")
    }
    
    // 2. 分布式锁
    lockKey := redis.RefundLockKey(req.RefundOrderNo)
    return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
        // 3. 更新审核状态
        now := time.Now()
        if err := s.billMgr.UpdateRefundAuditStatus(ctx, refund.ID, model.RefundStatusApproved, 
            now, req.ApprovedBy, req.Remark); err != nil {
            return err
        }
        
        // 4. 执行退款
        return s.executeRefund(ctx, refund)
    })
}

// executeRefund 执行退款
func (s *SettlementService) executeRefund(ctx context.Context, refund *model.RefundAudit) error {
    // 1. 调用平台退款接口
    refundReq := &platform.RefundRequest{
        UserID:        refund.UserID,
        Amount:        refund.RefundAmount,
        OriginalOrder: refund.BillOrderNo,
        RefundOrderNo: refund.RefundOrderNo,
        Remark:        refund.RefundReason,
    }
    
    result, err := s.platform.Refund(ctx, refundReq)
    if err != nil {
        // 更新退款失败状态
        s.billMgr.UpdateRefundAuditError(ctx, refund.ID, err.Error())
        return fmt.Errorf("platform refund failed: %w", err)
    }
    
    // 2. 更新退款成功状态（事务）
    return s.billMgr.UpdateRefundSuccessInTransaction(ctx, refund.ID, result.TransactionID, time.Now())
}
```

### 7.3 退款类型定义

```go
const (
    RefundTypeFirstRoundFail  = 1 // 首回合扣款失败退款
    RefundTypeGameAbnormal    = 2 // 游戏异常退款
    RefundTypeOther           = 3 // 其他退款
)

const (
    RefundStatusPending   = 0 // 待审核
    RefundStatusApproved  = 1 // 审核通过
    RefundStatusRejected  = 2 // 审核拒绝
    RefundStatusRefunded  = 3 // 已退款
)
```

---

## 八、幂等性保证

### 8.1 多层幂等性设计

```
幂等性保证层次：
├─ 1. 业务流水号唯一索引（数据库层）
│    └─ uk_biz_order_no
│
├─ 2. RoundID + BillType + UserID 组合检查（业务层）
│    └─ 防止重复操作
│
├─ 3. Redis 分布式锁（并发层）
│    └─ 防止竞争
│
└─ 4. 平台接口幂等键（外部接口层）
     └─ 使用 biz_order_no 作为幂等键
```

### 8.2 幂等性实现

```go
// DeductWithIdempotent 带幂等性保证的扣款
func (s *SettlementService) DeductWithIdempotent(ctx context.Context, req *DeductRequest) error {
    // 1. 业务层幂等检查
    if s.billMgr.ExistsByRoundTypeAndUser(ctx, req.RoundID, req.BillType, req.UserID) {
        return nil
    }
    
    // 2. 分布式锁
    lockKey := redis.DeductLockKey(req.RoundID, req.BillType, req.UserID)
    return lock.WithRedisLock(ctx, s.redis, lockKey, 30, func() error {
        // 3. 双重检查
        if s.billMgr.ExistsByRoundTypeAndUser(ctx, req.RoundID, req.BillType, req.UserID) {
            return nil
        }
        
        // 4. 生成业务流水号
        bizOrderNo := s.traceIDGen.GenerateBizOrderNo("DEDUCT", req.UserID)
        
        // 5. 创建账单（数据库唯一索引保证）
        bill := &model.BillRecord{
            BizOrderNo:     bizOrderNo,
            RoundTraceID:   req.RoundTraceID,
            BillType:       req.BillType,
            // ... 其他字段
        }
        
        if err := s.billMgr.CreateBill(ctx, bill); err != nil {
            // 唯一索引冲突，说明已处理
            if isDuplicateKeyError(err) {
                return nil
            }
            return err
        }
        
        // 6. 执行扣款（平台接口使用 bizOrderNo 作为幂等键）
        return s.executeDeduct(ctx, bill, req.Amount)
    })
}
```

### 8.3 平台接口幂等性

```go
// 平台接口调用时使用业务流水号作为幂等键
func (c *HTTPClient) Deduct(ctx context.Context, req *DeductRequest) (*DeductResult, error) {
    url := fmt.Sprintf("%s/api/account/deduct", c.baseURL)
    
    platformReq := &platformDeductRequest{
        UserID:  req.UserID,
        Amount:  req.Amount,
        OrderNo: req.BizOrderNo,  // 使用业务流水号作为幂等键
        Remark:  req.Remark,
        TraceID: req.RoundTraceID,
    }
    
    resp, err := c.doRequest(ctx, "POST", url, platformReq)
    if err != nil {
        return nil, err
    }
    
    if resp.Code != 0 {
        return nil, fmt.Errorf("deduct failed: %s", resp.Msg)
    }
    
    return &DeductResult{
        UserID:         req.UserID,
        Amount:         req.Amount,
        BalanceAfter:   resp.Data.BalanceAfter,
        TransactionID:  resp.Data.TransactionID,
    }, nil
}
```

---

## 九、事务性保证

### 9.1 数据库事务封装

```go
// CreateBillsInTransaction 事务性创建多个账单
func (m *BillManager) CreateBillsInTransaction(ctx context.Context, bills []*model.BillRecord) error {
    return m.db.Transaction(func(tx *gorm.DB) error {
        for _, bill := range bills {
            if err := tx.Create(bill).Error; err != nil {
                return err
            }
        }
        return nil
    })
}

// UpdateRefundSuccessInTransaction 事务性更新退款成功
func (m *BillManager) UpdateRefundSuccessInTransaction(ctx context.Context, refundID int64, platformTransID string, refundedAt time.Time) error {
    return m.db.Transaction(func(tx *gorm.DB) error {
        // 1. 更新退款审核记录
        if err := tx.Model(&model.RefundAudit{}).
            Where("id = ?", refundID).
            Updates(map[string]interface{}{
                "status":           model.RefundStatusRefunded,
                "refunded_at":      refundedAt,
                "platform_trans_id": platformTransID,
            }).Error; err != nil {
            return err
        }
        
        // 2. 查询退款审核记录
        var refund model.RefundAudit
        if err := tx.Where("id = ?", refundID).First(&refund).Error; err != nil {
            return err
        }
        
        // 3. 更新账单退款状态
        if err := tx.Model(&model.BillRecord{}).
            Where("id = ?", refund.BillID).
            Updates(map[string]interface{}{
                "refund_status":    model.RefundStatusRefunded,
                "refund_amount":    refund.RefundAmount,
                "refund_order_no":  refund.RefundOrderNo,
                "status":           model.BillStatusRefunded,
            }).Error; err != nil {
            return err
        }
        
        return nil
    })
}
```

### 9.2 分布式事务考虑

```
对于跨服务的操作，采用最终一致性方案：
├─ 本地事务保证本地数据一致性
├─ 消息队列保证跨服务操作的最终一致性
└─ 对账机制发现和修复不一致
```

```go
// DeductAndNotify 扣款并发送通知
func (s *SettlementService) DeductAndNotify(ctx context.Context, req *DeductRequest) error {
    // 1. 本地事务创建账单
    bill, err := s.createBillInTransaction(ctx, req)
    if err != nil {
        return err
    }
    
    // 2. 调用平台扣款
    result, err := s.platform.Deduct(ctx, &platform.DeductRequest{
        BizOrderNo: bill.BizOrderNo,
        UserID:     bill.UserID,
        Amount:     -bill.Amount,
    })
    
    if err != nil {
        // 3. 扣款失败，更新账单状态
        s.billMgr.UpdateBillStatus(ctx, bill.ID, model.BillStatusFailed, err.Error())
        return err
    }
    
    // 4. 扣款成功，更新账单状态（本地事务）
    if err := s.billMgr.UpdateBillSuccess(ctx, bill.ID, result.BalanceBefore, result.BalanceAfter); err != nil {
        // 5. 更新失败，发送消息进行补偿
        s.sendCompensationMessage(ctx, bill.ID, result)
        return err
    }
    
    return nil
}
```

---

## 十、代码实现方案

### 10.1 目录结构

```
settlement/
├── model/
│   ├── bill.go                    # 账单模型（重构）
│   ├── request.go                 # 请求模型（重构）
│   ├── refund.go                  # 退款模型（新增）
│   └── reconcile.go               # 对账模型（新增）
├── service/
│   ├── settlement_service.go      # 核心结算服务（重构）
│   ├── bill_manager.go            # 账单管理器（重构）
│   ├── refund_service.go          # 退款服务（新增）
│   └── trace_id_generator.go      # TraceID生成器（新增）
├── scheduler/
│   ├── retry_scheduler.go         # 重试调度器（保留）
│   └── reconcile_scheduler.go     # 对账调度器（新增）
├── consumer/
│   └── event_consumer.go          # 结算事件消费者（保留）
└── infrastructure/
    └── persistence/
        └── redis/
            └── keys.go            # Redis键定义（扩展）
```

### 10.2 核心接口设计

```go
// SettlementService 结算服务接口
type SettlementService interface {
    // 扣款相关
    DeductForFirstRound(ctx context.Context, req *FirstRoundDeductRequest) (*FirstRoundDeductResult, error)
    DeductForLaterRound(ctx context.Context, req *LaterRoundDeductRequest) error
    
    // 结算相关
    SettleRound(ctx context.Context, req *RoundSettleRequest) error
    
    // 退款相关
    ApplyForRefund(ctx context.Context, req *RefundApplyRequest) error
    ApproveRefund(ctx context.Context, req *RefundApproveRequest) error
    RejectRefund(ctx context.Context, req *RefundRejectRequest) error
    
    // 查询相关
    GetBillByTraceID(ctx context.Context, traceID string) (*model.BillRecord, error)
    GetBillsByUserID(ctx context.Context, userID int64, limit, offset int) ([]*model.BillRecord, error)
    GetBillsByRoundID(ctx context.Context, roundID int64) ([]*model.BillRecord, error)
    GetRoundSettlement(ctx context.Context, roundID int64) (*model.RoundSettlement, error)
}

// BillManager 账单管理器接口
type BillManager interface {
    // 账单操作
    CreateBill(ctx context.Context, bill *model.BillRecord) error
    CreateBillsInTransaction(ctx context.Context, bills []*model.BillRecord) error
    UpdateBillStatus(ctx context.Context, billID int64, status int, errMsg string) error
    UpdateBillSuccess(ctx context.Context, billID int64, balanceBefore, balanceAfter int64) error
    UpdateBillRefundStatus(ctx context.Context, billID int64, refundStatus int, refundOrderNo string) error
    
    // 查询操作
    GetBillByID(ctx context.Context, billID int64) (*model.BillRecord, error)
    GetBillByBizOrderNo(ctx context.Context, bizOrderNo string) (*model.BillRecord, error)
    GetBillsByRoundID(ctx context.Context, roundID int64) ([]*model.BillRecord, error)
    GetBillsByBatchID(ctx context.Context, batchID string) ([]*model.BillRecord, error)
    ExistsByRoundTypeAndUser(ctx context.Context, roundID int64, billType int, userID int64) bool
    
    // 回合结算操作
    CreateRoundSettlement(ctx context.Context, settlement *model.RoundSettlement) error
    UpdateRoundSettlementStatus(ctx context.Context, roundTraceID string, status int, errMsg string) error
    UpdateRoundSettlementDeductSuccess(ctx context.Context, roundTraceID string, successCount int, deductedAt time.Time) error
    UpdateRoundSettlementSettleSuccess(ctx context.Context, roundTraceID string, successCount int, settledAt time.Time) error
    GetRoundSettlementByRoundID(ctx context.Context, roundID int64) (*model.RoundSettlement, error)
    
    // 退款操作
    CreateRefundAudit(ctx context.Context, refund *model.RefundAudit) error
    UpdateRefundAuditStatus(ctx context.Context, refundID int64, status int, approvedAt time.Time, approvedBy int64, remark string) error
    UpdateRefundSuccessInTransaction(ctx context.Context, refundID int64, platformTransID string, refundedAt time.Time) error
    GetRefundAuditByOrderNo(ctx context.Context, refundOrderNo string) (*model.RefundAudit, error)
}
```

### 10.3 请求模型设计

```go
// FirstRoundDeductRequest 首回合扣款请求
type FirstRoundDeductRequest struct {
    RoomID          int64
    SessionID       int64
    RoundID         int64
    RoundNo         int
    RoomFeePerPlayer int64
    Players         []*PlayerDeductInfo
}

type PlayerDeductInfo struct {
    UserID   int64
    Nickname string
}

// FirstRoundDeductResult 首回合扣款结果
type FirstRoundDeductResult struct {
    BatchID        string
    AllSuccess     bool
    SuccessCount   int
    FailedCount    int
    SuccessPlayers []int64
    FailedPlayers  []*FailedPlayerInfo
}

type FailedPlayerInfo struct {
    UserID    int64
    ErrorCode string
    ErrorMsg  string
}

// LaterRoundDeductRequest 后续回合扣款请求
type LaterRoundDeductRequest struct {
    RoomID         int64
    SessionID      int64
    RoundID        int64
    RoundNo        int
    RoomFee        int64
    MinPlayerID    int64
    SessionTraceID string
}

// RefundApplyRequest 退款申请请求
type RefundApplyRequest struct {
    BillID       int64
    RefundAmount int64
    RefundReason string
    RefundType   int
    AppliedBy    int64
}

// RefundApproveRequest 退款审核请求
type RefundApproveRequest struct {
    RefundOrderNo string
    ApprovedBy    int64
    Remark        string
}
```

---

## 十一、实施计划

### 11.1 阶段一：基础重构（1-2周）

**目标：** 完善基础设施，不影响现有功能

**任务：**
1. 创建新的数据库表（退款审核表、对账记录表）
2. 扩展现有表字段（新增字段，保留旧字段兼容）
3. 实现 TraceID 生成器
4. 实现业务流水号生成器
5. 完善幂等性检查机制

**验证：**
- 新表创建成功
- 旧功能正常运行
- 新字段可正常读写

### 11.2 阶段二：扣款逻辑重构（2-3周）

**目标：** 实现新的扣款逻辑

**任务：**
1. 实现首回合批量扣款逻辑
2. 实现后续回合扣款逻辑
3. 实现批量扣款失败退款申请
4. 完善事务性保证
5. 单元测试和集成测试

**验证：**
- 首回合扣款成功场景
- 首回合部分失败退款场景
- 后续回合扣款场景

### 11.3 阶段三：对账调度重构（1-2周）

**目标：** 实现对账和异常检测

**任务：**
1. 实现对账调度器
2. 实现异常检测机制
3. 实现自动退款申请
4. 完善日志和监控

**验证：**
- 对账调度正常运行
- 异常场景能被检测
- 自动退款申请生成

### 11.4 阶段四：退款审核流程（1-2周）

**目标：** 实现人工退款审核

**任务：**
1. 实现退款申请接口
2. 实现退款审核接口
3. 实现退款执行逻辑
4. 实现退款查询接口
5. 管理后台界面

**验证：**
- 退款申请流程
- 退款审核流程
- 退款执行流程

### 11.5 阶段五：灰度发布和监控（1周）

**目标：** 安全上线

**任务：**
1. 灰度发布策略
2. 监控告警配置
3. 数据迁移脚本
4. 回滚预案

**验证：**
- 灰度发布流程
- 监控告警正常
- 数据一致性

---

## 十二、风险点和注意事项

### 12.1 数据迁移风险

**风险：** 新旧表结构差异，数据迁移可能出错

**应对：**
- 先创建新表，不删除旧字段
- 编写数据迁移脚本并充分测试
- 准备回滚脚本
- 灰度发布，逐步切换

### 12.2 并发控制风险

**风险：** 高并发场景下可能出现重复扣款

**应对：**
- 多层幂等性保证
- 分布式锁超时设置合理
- 数据库唯一索引兜底
- 对账机制发现和修复

### 12.3 事务一致性风险

**风险：** 跨服务操作可能出现数据不一致

**应对：**
- 本地事务保证本地一致性
- 消息队列保证最终一致性
- 对账机制定期检查
- 人工审核机制兜底

### 12.4 性能风险

**风险：** 批量扣款可能影响性能

**应对：**
- 批量操作使用并发控制
- 数据库连接池配置合理
- Redis 连接池配置合理
- 监控性能指标

---

## 十三、总结

本重构方案从以下几个方面系统化解决了现有问题：

1. **TraceID 体系重构**：简化为 round_trace_id，明确职责
2. **表结构完善**：新增退款、对账相关字段和表，支持完整的业务流程
3. **扣款逻辑完善**：实现首回合批量扣款和后续回合扣款，满足业务需求
4. **对账调度**：实现自动对账和异常检测，及时发现问题
5. **退款审核**：实现人工退款审核流程，保证资金安全
6. **幂等性保证**：多层幂等性设计，防止重复扣款和退款
7. **事务性保证**：完善事务处理，保证数据一致性

通过分阶段实施，可以逐步完成重构，降低风险，确保系统稳定运行。
