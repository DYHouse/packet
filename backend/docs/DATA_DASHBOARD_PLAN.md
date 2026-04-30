# 红包游戏数据大屏看板方案

> 基于现有代码实现的数据分析与可视化方案（纯数据库查询版）

---

## 一、业务需求梳理与数据来源映射

| 业务需求 | 数据来源 | 可行性 | 备注 |
|---------|---------|--------|------|
| 房间进入人数 | `session_players` 表 + `rooms.player_count` | ✅ 已有数据 | 每局游戏的参与玩家数 |
| 游戏局数 | `game_sessions` 表 + `rounds` 表 | ✅ 已有数据 | Session数 + Round数 |
| 红包金额分布 | `packets` 表 + `round_grab_records` 表 | ✅ 已有数据 | 红包金额、抢到金额分布 |
| 平台抽佣 | `rounds.commission` + `round_settlement.commission` | ✅ 已有数据 | 每回合抽佣金额 |
| 顺子触发次数 | `round_settlement.reward_type = 1` | ✅ 已有数据 | 已记录奖励类型 |
| 豹子触发次数 | `round_settlement.reward_type = 2` | ✅ 已有数据 | 已记录奖励类型 |
| 平台发红包次数/金额 | `rounds.sender_type IN ('system','system_forced','system_resume')` | ✅ 已有数据 | 已区分发送者类型 |
| 平台收益 | `rounds.commission` 汇总 - 系统红包支出 | ✅ 可计算 | 需聚合计算 |

---

## 二、数据大屏布局方案

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           🎮 红包游戏数据大屏                                │
│                        数据监控与分析看板                                     │
├─────────────────────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐      │
│  │ 今日局数  │  │ 活跃玩家  │  │ 平台收益  │  │ 顺子触发  │  │ 豹子触发  │      │
│  │  1,234   │  │   567    │  │ ¥12,345  │  │   89次   │  │   12次   │      │
│  │ ↑ 15%    │  │ ↑ 8%     │  │ ↑ 22%    │  │ ↑ 5%     │  │ ↑ 2%     │      │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘  └──────────┘      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────┐  ┌─────────────────────────────┐          │
│  │    📈 游戏趋势（24小时）     │  │    💰 红包金额分布           │          │
│  │                             │  │                             │          │
│  │  局数折线图                  │  │  金额区间柱状图              │          │
│  │  玩家数折线图                │  │  - 0-10元                   │          │
│  │  收益折线图                  │  │  - 10-50元                  │          │
│  │                             │  │  - 50-100元                 │          │
│  │  [按小时聚合展示]            │  │  - 100-500元                │          │
│  │                             │  │  - 500元以上                │          │
│  └─────────────────────────────┘  └─────────────────────────────┘          │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                             │
│  ┌─────────────────────────────┐  ┌─────────────────────────────┐          │
│  │    🎁 平台红包统计           │  │    📊 抽佣与收益分析         │          │
│  │                             │  │                             │          │
│  │  发红包次数: 45次            │  │  总抽佣金额: ¥25,000         │          │
│  │  发红包总额: ¥22,500         │  │  系统红包支出: ¥22,500       │          │
│  │                             │  │  净收益: ¥2,500              │          │
│  │  触发原因分布:               │  │                             │          │
│  │  - 首回合: 20次              │  │  抽佣率: 5%                  │          │
│  │  - 豹子奖励: 15次            │  │                             │          │
│  │  - 强制/恢复: 10次           │  │  收益率趋势图                │          │
│  └─────────────────────────────┘  └─────────────────────────────┘          │
│                                                                             │
├─────────────────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │    🏆 房间热度排行 (TOP 10)                                          │   │
│  │                                                                     │   │
│  │  排名 │ 房间名称 │ 今日局数 │ 累计玩家 │ 总流水 │ 顺子 │ 豹子        │   │
│  │   1  │ VIP房    │   234   │   56    │ ¥45,000│  12  │   3         │   │
│  │   2  │ 普通房   │   189   │   89    │ ¥23,000│   8  │   2         │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 三、核心指标 SQL 查询

### 1. 房间进入人数

```sql
-- 今日活跃玩家数（去重）
SELECT COUNT(DISTINCT user_id) as active_players
FROM session_players
WHERE DATE(joined_at) = CURDATE();

-- 今日进入次数
SELECT COUNT(*) as total_entries
FROM session_players
WHERE DATE(joined_at) = CURDATE();

-- 指定日期范围的玩家统计
SELECT 
    DATE(joined_at) as date,
    COUNT(DISTINCT user_id) as unique_players,
    COUNT(*) as total_entries
FROM session_players
WHERE joined_at >= :start_date AND joined_at < :end_date
GROUP BY DATE(joined_at)
ORDER BY date;
```

---

### 2. 游戏局数

```sql
-- 今日 Session 数（完整游戏局数）
SELECT COUNT(*) as session_count
FROM game_sessions 
WHERE DATE(started_at) = CURDATE();

-- 今日 Round 数（回合数）
SELECT COUNT(*) as round_count
FROM rounds 
WHERE DATE(created_at) = CURDATE();

-- 按房间统计今日局数
SELECT 
    r.room_id,
    rm.config_name as room_name,
    COUNT(DISTINCT r.session_id) as session_count,
    COUNT(*) as round_count
FROM rounds r
LEFT JOIN rooms rm ON r.room_id = rm.room_id
WHERE DATE(r.created_at) = CURDATE()
GROUP BY r.room_id, rm.config_name;

-- 按小时统计趋势（最近24小时）
SELECT 
    DATE_FORMAT(created_at, '%Y-%m-%d %H:00') as hour,
    COUNT(*) as round_count
FROM rounds
WHERE created_at >= DATE_SUB(NOW(), INTERVAL 24 HOUR)
GROUP BY DATE_FORMAT(created_at, '%Y-%m-%d %H:00')
ORDER BY hour;
```

---

### 3. 红包金额分布

```sql
-- 红包金额分布（按区间统计）
SELECT 
    CASE 
        WHEN amount < 1000 THEN '0-10元'
        WHEN amount < 5000 THEN '10-50元'
        WHEN amount < 10000 THEN '50-100元'
        WHEN amount < 50000 THEN '100-500元'
        ELSE '500元以上'
    END as amount_range,
    COUNT(*) as packet_count,
    SUM(amount) as total_amount,
    ROUND(AVG(amount), 2) as avg_amount
FROM packets
WHERE DATE(created_at) = CURDATE()
GROUP BY 
    CASE 
        WHEN amount < 1000 THEN '0-10元'
        WHEN amount < 5000 THEN '10-50元'
        WHEN amount < 10000 THEN '50-100元'
        WHEN amount < 50000 THEN '100-500元'
        ELSE '500元以上'
    END
ORDER BY MIN(amount);

-- 抢到的金额分布
SELECT 
    CASE 
        WHEN amount < 1000 THEN '0-10元'
        WHEN amount < 5000 THEN '10-50元'
        WHEN amount < 10000 THEN '50-100元'
        WHEN amount < 50000 THEN '100-500元'
        ELSE '500元以上'
    END as amount_range,
    COUNT(*) as grab_count,
    COUNT(DISTINCT user_id) as player_count
FROM round_grab_records
WHERE DATE(grabbed_at) = CURDATE()
GROUP BY amount_range
ORDER BY MIN(amount);

-- 手气最佳（最小红包）统计
SELECT 
    COUNT(*) as min_packet_count,
    SUM(amount) as total_min_amount,
    ROUND(AVG(amount), 2) as avg_min_amount
FROM round_grab_records
WHERE is_min = 1
AND DATE(grabbed_at) = CURDATE();
```

---

### 4. 平台抽佣

```sql
-- 今日总抽佣
SELECT 
    COUNT(*) as round_count,
    SUM(commission) as total_commission,
    ROUND(AVG(commission), 2) as avg_commission
FROM rounds
WHERE DATE(created_at) = CURDATE()
AND commission > 0;

-- 按房间统计抽佣
SELECT 
    r.room_id,
    rm.config_name as room_name,
    COUNT(*) as round_count,
    SUM(r.commission) as total_commission,
    ROUND(AVG(r.commission), 2) as avg_commission
FROM rounds r
LEFT JOIN rooms rm ON r.room_id = rm.room_id
WHERE DATE(r.created_at) = CURDATE()
AND r.commission > 0
GROUP BY r.room_id, rm.config_name;

-- 抽佣趋势（按天）
SELECT 
    DATE(created_at) as date,
    COUNT(*) as round_count,
    SUM(commission) as daily_commission
FROM rounds
WHERE created_at >= DATE_SUB(CURDATE(), INTERVAL 7 DAY)
AND commission > 0
GROUP BY DATE(created_at)
ORDER BY date;
```

---

### 5. 顺子触发次数

```sql
-- 今日顺子触发统计
SELECT 
    COUNT(*) as straight_count,
    SUM(reward_amount) as total_reward,
    ROUND(AVG(reward_amount), 2) as avg_reward
FROM round_settlement
WHERE reward_type = 1
AND DATE(created_at) = CURDATE();

-- 按房间统计顺子
SELECT 
    rs.room_id,
    rm.config_name as room_name,
    COUNT(*) as straight_count,
    SUM(rs.reward_amount) as total_reward
FROM round_settlement rs
LEFT JOIN rooms rm ON rs.room_id = rm.room_id
WHERE rs.reward_type = 1
AND DATE(rs.created_at) = CURDATE()
GROUP BY rs.room_id, rm.config_name;

-- 顺子触发趋势（按天）
SELECT 
    DATE(created_at) as date,
    COUNT(*) as straight_count,
    SUM(reward_amount) as total_reward
FROM round_settlement
WHERE reward_type = 1
AND created_at >= DATE_SUB(CURDATE(), INTERVAL 7 DAY)
GROUP BY DATE(created_at)
ORDER BY date;
```

---

### 6. 豹子触发次数

```sql
-- 今日豹子触发统计
SELECT 
    COUNT(*) as leopard_count,
    SUM(reward_amount) as total_reward,
    ROUND(AVG(reward_amount), 2) as avg_reward
FROM round_settlement
WHERE reward_type = 2
AND DATE(created_at) = CURDATE();

-- 按房间统计豹子
SELECT 
    rs.room_id,
    rm.config_name as room_name,
    COUNT(*) as leopard_count,
    SUM(rs.reward_amount) as total_reward
FROM round_settlement rs
LEFT JOIN rooms rm ON rs.room_id = rm.room_id
WHERE rs.reward_type = 2
AND DATE(rs.created_at) = CURDATE()
GROUP BY rs.room_id, rm.config_name;

-- 豹子触发后系统发红包统计
SELECT 
    COUNT(*) as leopard_triggered_count
FROM round_settlement
WHERE reward_type = 2
AND DATE(created_at) = CURDATE();
```

---

### 7. 平台发红包次数和总金额

```sql
-- 今日平台发红包统计（按类型）
SELECT 
    sender_type,
    COUNT(*) as send_count,
    SUM(total_amount) as total_amount,
    ROUND(AVG(total_amount), 2) as avg_amount
FROM rounds
WHERE sender_type IN ('system', 'system_forced', 'system_resume')
AND DATE(created_at) = CURDATE()
GROUP BY sender_type;

-- 今日平台发红包总计
SELECT 
    COUNT(*) as total_send_count,
    SUM(total_amount) as total_amount
FROM rounds
WHERE sender_type IN ('system', 'system_forced', 'system_resume')
AND DATE(created_at) = CURDATE();

-- 平台发红包趋势（按天）
SELECT 
    DATE(created_at) as date,
    sender_type,
    COUNT(*) as send_count,
    SUM(total_amount) as total_amount
FROM rounds
WHERE sender_type IN ('system', 'system_forced', 'system_resume')
AND created_at >= DATE_SUB(CURDATE(), INTERVAL 7 DAY)
GROUP BY DATE(created_at), sender_type
ORDER BY date, sender_type;
```

**发送者类型说明：**

| sender_type | 说明 | 触发场景 |
|-------------|------|---------|
| `system` | 系统发红包 | 首回合、豹子奖励 |
| `system_forced` | 强制发红包 | 惩罚场景（玩家未按时发红包） |
| `system_resume` | 恢复发红包 | 游戏中断后恢复 |

---

### 8. 平台收益

```sql
-- 今日平台收益汇总
SELECT 
    (SELECT COALESCE(SUM(commission), 0) FROM rounds WHERE DATE(created_at) = CURDATE()) as total_commission,
    (SELECT COALESCE(SUM(total_amount), 0) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(created_at) = CURDATE()) as system_packet_cost,
    (SELECT COALESCE(SUM(commission), 0) FROM rounds WHERE DATE(created_at) = CURDATE()) - 
    (SELECT COALESCE(SUM(total_amount), 0) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(created_at) = CURDATE()) as net_profit;

-- 收益明细（更详细的查询）
SELECT 
    COUNT(*) as round_count,
    SUM(total_amount) as total_bet,
    SUM(commission) as total_commission,
    ROUND(SUM(commission) * 100.0 / NULLIF(SUM(total_amount), 0), 2) as commission_rate,
    (SELECT COALESCE(SUM(total_amount), 0) FROM rounds r2 WHERE r2.sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(r2.created_at) = CURDATE()) as system_packet_cost,
    SUM(commission) - (SELECT COALESCE(SUM(total_amount), 0) FROM rounds r2 WHERE r2.sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(r2.created_at) = CURDATE()) as net_profit
FROM rounds r1
WHERE DATE(created_at) = CURDATE();

-- 收益趋势（按天）
SELECT 
    DATE(created_at) as date,
    SUM(commission) as daily_commission,
    (SELECT COALESCE(SUM(total_amount), 0) 
     FROM rounds r2 
     WHERE DATE(r2.created_at) = DATE(r1.created_at)
     AND r2.sender_type IN ('system', 'system_forced', 'system_resume')) as system_cost,
    SUM(commission) - (SELECT COALESCE(SUM(total_amount), 0) 
     FROM rounds r2 
     WHERE DATE(r2.created_at) = DATE(r1.created_at)
     AND r2.sender_type IN ('system', 'system_forced', 'system_resume')) as net_profit
FROM rounds r1
WHERE created_at >= DATE_SUB(CURDATE(), INTERVAL 7 DAY)
GROUP BY DATE(created_at)
ORDER BY date;
```

---

## 四、综合查询（大屏核心数据）

### 4.1 大屏首页核心指标

```sql
-- 一次性获取所有核心指标
SELECT 
    -- 游戏局数
    (SELECT COUNT(*) FROM game_sessions WHERE DATE(started_at) = CURDATE()) as today_sessions,
    (SELECT COUNT(*) FROM rounds WHERE DATE(created_at) = CURDATE()) as today_rounds,
    
    -- 活跃玩家
    (SELECT COUNT(DISTINCT user_id) FROM session_players WHERE DATE(joined_at) = CURDATE()) as active_players,
    
    -- 平台收益
    (SELECT COALESCE(SUM(commission), 0) FROM rounds WHERE DATE(created_at) = CURDATE()) as total_commission,
    (SELECT COALESCE(SUM(total_amount), 0) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(created_at) = CURDATE()) as system_packet_cost,
    
    -- 特殊牌型
    (SELECT COUNT(*) FROM round_settlement WHERE reward_type = 1 AND DATE(created_at) = CURDATE()) as straight_count,
    (SELECT COUNT(*) FROM round_settlement WHERE reward_type = 2 AND DATE(created_at) = CURDATE()) as leopard_count,
    
    -- 平台红包
    (SELECT COUNT(*) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(created_at) = CURDATE()) as system_packet_count;
```

### 4.2 房间排行榜

```sql
-- 房间热度排行（今日）
SELECT 
    r.room_id,
    rm.config_name as room_name,
    COUNT(DISTINCT r.session_id) as session_count,
    COUNT(*) as round_count,
    COUNT(DISTINCT sp.user_id) as player_count,
    SUM(r.total_amount) as total_amount,
    SUM(r.commission) as total_commission,
    (SELECT COUNT(*) FROM round_settlement rs WHERE rs.room_id = r.room_id AND rs.reward_type = 1 AND DATE(rs.created_at) = CURDATE()) as straight_count,
    (SELECT COUNT(*) FROM round_settlement rs WHERE rs.room_id = r.room_id AND rs.reward_type = 2 AND DATE(rs.created_at) = CURDATE()) as leopard_count
FROM rounds r
LEFT JOIN rooms rm ON r.room_id = rm.room_id
LEFT JOIN session_players sp ON sp.room_id = r.room_id AND DATE(sp.joined_at) = CURDATE()
WHERE DATE(r.created_at) = CURDATE()
GROUP BY r.room_id, rm.config_name
ORDER BY round_count DESC
LIMIT 10;
```

---

## 五、技术实现方案

### 5.1 代码结构

```
backend/
├── stats/                          # 新增统计模块
│   ├── repository/
│   │   └── stats_repository.go    # 统计数据仓库（数据库查询）
│   ├── service/
│   │   └── stats_service.go       # 统计服务
│   ├── dto/
│   │   └── stats_dto.go           # 数据传输对象
│   └── handler/
│       └── stats_handler.go       # HTTP处理器
```

### 5.2 数据模型定义

```go
package dto

import "time"

type DashboardStats struct {
    TodaySessions      int64 `json:"today_sessions"`
    TodayRounds        int64 `json:"today_rounds"`
    ActivePlayers      int64 `json:"active_players"`
    TotalCommission    int64 `json:"total_commission"`
    SystemPacketCost   int64 `json:"system_packet_cost"`
    NetProfit          int64 `json:"net_profit"`
    StraightCount      int64 `json:"straight_count"`
    LeopardCount       int64 `json:"leopard_count"`
    SystemPacketCount  int64 `json:"system_packet_count"`
}

type HourlyTrend struct {
    Hour       string `json:"hour"`
    RoundCount int64  `json:"round_count"`
    Commission int64  `json:"commission"`
}

type AmountDistribution struct {
    Range       string `json:"range"`
    Count       int64  `json:"count"`
    TotalAmount int64  `json:"total_amount"`
    AvgAmount   int64  `json:"avg_amount"`
}

type RoomRanking struct {
    RoomID        int64  `json:"room_id"`
    RoomName      string `json:"room_name"`
    SessionCount  int64  `json:"session_count"`
    RoundCount    int64  `json:"round_count"`
    PlayerCount   int64  `json:"player_count"`
    TotalAmount   int64  `json:"total_amount"`
    Commission    int64  `json:"commission"`
    StraightCount int64  `json:"straight_count"`
    LeopardCount  int64  `json:"leopard_count"`
}

type SystemPacketStats struct {
    SenderType  string `json:"sender_type"`
    SendCount   int64  `json:"send_count"`
    TotalAmount int64  `json:"total_amount"`
    AvgAmount   int64  `json:"avg_amount"`
}

type DailyTrend struct {
    Date             string `json:"date"`
    RoundCount       int64  `json:"round_count"`
    TotalCommission  int64  `json:"total_commission"`
    SystemPacketCost int64  `json:"system_packet_cost"`
    NetProfit        int64  `json:"net_profit"`
}
```

### 5.3 统计仓库实现

```go
package repository

import (
    "context"
    "time"
    
    "github.com/cashparty/backend/stats/dto"
    "gorm.io/gorm"
)

type StatsRepository struct {
    db *gorm.DB
}

func NewStatsRepository(db *gorm.DB) *StatsRepository {
    return &StatsRepository{db: db}
}

func (r *StatsRepository) GetDashboardStats(ctx context.Context, date time.Time) (*dto.DashboardStats, error) {
    var stats dto.DashboardStats
    dateStr := date.Format("2006-01-02")
    
    err := r.db.WithContext(ctx).Raw(`
        SELECT 
            (SELECT COUNT(*) FROM game_sessions WHERE DATE(started_at) = ?) as today_sessions,
            (SELECT COUNT(*) FROM rounds WHERE DATE(created_at) = ?) as today_rounds,
            (SELECT COUNT(DISTINCT user_id) FROM session_players WHERE DATE(joined_at) = ?) as active_players,
            (SELECT COALESCE(SUM(commission), 0) FROM rounds WHERE DATE(created_at) = ?) as total_commission,
            (SELECT COALESCE(SUM(total_amount), 0) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(created_at) = ?) as system_packet_cost,
            (SELECT COUNT(*) FROM round_settlement WHERE reward_type = 1 AND DATE(created_at) = ?) as straight_count,
            (SELECT COUNT(*) FROM round_settlement WHERE reward_type = 2 AND DATE(created_at) = ?) as leopard_count,
            (SELECT COUNT(*) FROM rounds WHERE sender_type IN ('system', 'system_forced', 'system_resume') AND DATE(created_at) = ?) as system_packet_count
    `, dateStr, dateStr, dateStr, dateStr, dateStr, dateStr, dateStr, dateStr).Scan(&stats).Error
    
    if err != nil {
        return nil, err
    }
    
    stats.NetProfit = stats.TotalCommission - stats.SystemPacketCost
    
    return &stats, nil
}

func (r *StatsRepository) GetHourlyTrend(ctx context.Context, hours int) ([]dto.HourlyTrend, error) {
    var trends []dto.HourlyTrend
    
    err := r.db.WithContext(ctx).Raw(`
        SELECT 
            DATE_FORMAT(created_at, '%Y-%m-%d %H:00') as hour,
            COUNT(*) as round_count,
            SUM(commission) as commission
        FROM rounds
        WHERE created_at >= DATE_SUB(NOW(), INTERVAL ? HOUR)
        GROUP BY DATE_FORMAT(created_at, '%Y-%m-%d %H:00')
        ORDER BY hour
    `, hours).Scan(&trends).Error
    
    return trends, err
}

func (r *StatsRepository) GetAmountDistribution(ctx context.Context, date time.Time) ([]dto.AmountDistribution, error) {
    var distributions []dto.AmountDistribution
    dateStr := date.Format("2006-01-02")
    
    err := r.db.WithContext(ctx).Raw(`
        SELECT 
            CASE 
                WHEN amount < 1000 THEN '0-10元'
                WHEN amount < 5000 THEN '10-50元'
                WHEN amount < 10000 THEN '50-100元'
                WHEN amount < 50000 THEN '100-500元'
                ELSE '500元以上'
            END as range,
            COUNT(*) as count,
            SUM(amount) as total_amount,
            AVG(amount) as avg_amount
        FROM packets
        WHERE DATE(created_at) = ?
        GROUP BY 
            CASE 
                WHEN amount < 1000 THEN '0-10元'
                WHEN amount < 5000 THEN '10-50元'
                WHEN amount < 10000 THEN '50-100元'
                WHEN amount < 50000 THEN '100-500元'
                ELSE '500元以上'
            END
        ORDER BY MIN(amount)
    `, dateStr).Scan(&distributions).Error
    
    return distributions, err
}

func (r *StatsRepository) GetRoomRanking(ctx context.Context, date time.Time, limit int) ([]dto.RoomRanking, error) {
    var rankings []dto.RoomRanking
    dateStr := date.Format("2006-01-02")
    
    err := r.db.WithContext(ctx).Raw(`
        SELECT 
            r.room_id,
            rm.config_name as room_name,
            COUNT(DISTINCT r.session_id) as session_count,
            COUNT(*) as round_count,
            COUNT(DISTINCT sp.user_id) as player_count,
            SUM(r.total_amount) as total_amount,
            SUM(r.commission) as commission,
            (SELECT COUNT(*) FROM round_settlement rs WHERE rs.room_id = r.room_id AND rs.reward_type = 1 AND DATE(rs.created_at) = ?) as straight_count,
            (SELECT COUNT(*) FROM round_settlement rs WHERE rs.room_id = r.room_id AND rs.reward_type = 2 AND DATE(rs.created_at) = ?) as leopard_count
        FROM rounds r
        LEFT JOIN rooms rm ON r.room_id = rm.room_id
        LEFT JOIN session_players sp ON sp.room_id = r.room_id AND DATE(sp.joined_at) = ?
        WHERE DATE(r.created_at) = ?
        GROUP BY r.room_id, rm.config_name
        ORDER BY round_count DESC
        LIMIT ?
    `, dateStr, dateStr, dateStr, dateStr, limit).Scan(&rankings).Error
    
    return rankings, err
}

func (r *StatsRepository) GetSystemPacketStats(ctx context.Context, date time.Time) ([]dto.SystemPacketStats, error) {
    var stats []dto.SystemPacketStats
    dateStr := date.Format("2006-01-02")
    
    err := r.db.WithContext(ctx).Raw(`
        SELECT 
            sender_type,
            COUNT(*) as send_count,
            SUM(total_amount) as total_amount,
            AVG(total_amount) as avg_amount
        FROM rounds
        WHERE sender_type IN ('system', 'system_forced', 'system_resume')
        AND DATE(created_at) = ?
        GROUP BY sender_type
    `, dateStr).Scan(&stats).Error
    
    return stats, err
}

func (r *StatsRepository) GetDailyTrend(ctx context.Context, days int) ([]dto.DailyTrend, error) {
    var trends []dto.DailyTrend
    
    err := r.db.WithContext(ctx).Raw(`
        SELECT 
            DATE(created_at) as date,
            COUNT(*) as round_count,
            SUM(commission) as total_commission,
            (SELECT COALESCE(SUM(total_amount), 0) 
             FROM rounds r2 
             WHERE DATE(r2.created_at) = DATE(r1.created_at)
             AND r2.sender_type IN ('system', 'system_forced', 'system_resume')) as system_packet_cost,
            SUM(commission) - (SELECT COALESCE(SUM(total_amount), 0) 
             FROM rounds r2 
             WHERE DATE(r2.created_at) = DATE(r1.created_at)
             AND r2.sender_type IN ('system', 'system_forced', 'system_resume')) as net_profit
        FROM rounds r1
        WHERE created_at >= DATE_SUB(CURDATE(), INTERVAL ? DAY)
        GROUP BY DATE(created_at)
        ORDER BY date
    `, days).Scan(&trends).Error
    
    return trends, err
}
```

### 5.4 统计服务

```go
package service

import (
    "context"
    "time"
    
    "github.com/cashparty/backend/stats/dto"
    "github.com/cashparty/backend/stats/repository"
)

type StatsService struct {
    repo *repository.StatsRepository
}

func NewStatsService(repo *repository.StatsRepository) *StatsService {
    return &StatsService{repo: repo}
}

func (s *StatsService) GetDashboardStats(ctx context.Context) (*dto.DashboardStats, error) {
    return s.repo.GetDashboardStats(ctx, time.Now())
}

func (s *StatsService) GetHourlyTrend(ctx context.Context, hours int) ([]dto.HourlyTrend, error) {
    if hours <= 0 {
        hours = 24
    }
    return s.repo.GetHourlyTrend(ctx, hours)
}

func (s *StatsService) GetAmountDistribution(ctx context.Context) ([]dto.AmountDistribution, error) {
    return s.repo.GetAmountDistribution(ctx, time.Now())
}

func (s *StatsService) GetRoomRanking(ctx context.Context, limit int) ([]dto.RoomRanking, error) {
    if limit <= 0 {
        limit = 10
    }
    return s.repo.GetRoomRanking(ctx, time.Now(), limit)
}

func (s *StatsService) GetSystemPacketStats(ctx context.Context) ([]dto.SystemPacketStats, error) {
    return s.repo.GetSystemPacketStats(ctx, time.Now())
}

func (s *StatsService) GetDailyTrend(ctx context.Context, days int) ([]dto.DailyTrend, error) {
    if days <= 0 {
        days = 7
    }
    return s.repo.GetDailyTrend(ctx, days)
}
```

### 5.5 HTTP 处理器

```go
package handler

import (
    "net/http"
    "strconv"
    "time"
    
    "github.com/cashparty/backend/stats/dto"
    "github.com/cashparty/backend/stats/service"
    "github.com/gin-gonic/gin"
)

type StatsHandler struct {
    statsService *service.StatsService
}

func NewStatsHandler(statsService *service.StatsService) *StatsHandler {
    return &StatsHandler{statsService: statsService}
}

func (h *StatsHandler) RegisterRoutes(r *gin.RouterGroup) {
    stats := r.Group("/stats")
    {
        stats.GET("/dashboard", h.GetDashboard)
        stats.GET("/trend/hourly", h.GetHourlyTrend)
        stats.GET("/trend/daily", h.GetDailyTrend)
        stats.GET("/distribution", h.GetAmountDistribution)
        stats.GET("/rooms/ranking", h.GetRoomRanking)
        stats.GET("/system-packets", h.GetSystemPacketStats)
    }
}

func (h *StatsHandler) GetDashboard(c *gin.Context) {
    stats, err := h.statsService.GetDashboardStats(c.Request.Context())
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "code":    500,
            "message": err.Error(),
        })
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "code": 0,
        "data": stats,
    })
}

func (h *StatsHandler) GetHourlyTrend(c *gin.Context) {
    hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
    
    trends, err := h.statsService.GetHourlyTrend(c.Request.Context(), hours)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "code":    500,
            "message": err.Error(),
        })
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "code": 0,
        "data": trends,
    })
}

func (h *StatsHandler) GetDailyTrend(c *gin.Context) {
    days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
    
    trends, err := h.statsService.GetDailyTrend(c.Request.Context(), days)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "code":    500,
            "message": err.Error(),
        })
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "code": 0,
        "data": trends,
    })
}

func (h *StatsHandler) GetAmountDistribution(c *gin.Context) {
    distributions, err := h.statsService.GetAmountDistribution(c.Request.Context())
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "code":    500,
            "message": err.Error(),
        })
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "code": 0,
        "data": distributions,
    })
}

func (h *StatsHandler) GetRoomRanking(c *gin.Context) {
    limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
    
    rankings, err := h.statsService.GetRoomRanking(c.Request.Context(), limit)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "code":    500,
            "message": err.Error(),
        })
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "code": 0,
        "data": rankings,
    })
}

func (h *StatsHandler) GetSystemPacketStats(c *gin.Context) {
    stats, err := h.statsService.GetSystemPacketStats(c.Request.Context())
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{
            "code":    500,
            "message": err.Error(),
        })
        return
    }
    
    c.JSON(http.StatusOK, gin.H{
        "code": 0,
        "data": stats,
    })
}
```

---

## 六、API 接口文档

### 6.1 获取大屏核心数据

**请求：**
```
GET /api/v1/stats/dashboard
```

**响应：**
```json
{
    "code": 0,
    "data": {
        "today_sessions": 100,
        "today_rounds": 500,
        "active_players": 200,
        "total_commission": 500000,
        "system_packet_cost": 100000,
        "net_profit": 400000,
        "straight_count": 15,
        "leopard_count": 3,
        "system_packet_count": 10
    }
}
```

### 6.2 获取小时趋势

**请求：**
```
GET /api/v1/stats/trend/hourly?hours=24
```

**响应：**
```json
{
    "code": 0,
    "data": [
        {
            "hour": "2024-01-01 00:00",
            "round_count": 20,
            "commission": 20000
        },
        {
            "hour": "2024-01-01 01:00",
            "round_count": 15,
            "commission": 15000
        }
    ]
}
```

### 6.3 获取每日趋势

**请求：**
```
GET /api/v1/stats/trend/daily?days=7
```

**响应：**
```json
{
    "code": 0,
    "data": [
        {
            "date": "2024-01-01",
            "round_count": 500,
            "total_commission": 50000,
            "system_packet_cost": 10000,
            "net_profit": 40000
        }
    ]
}
```

### 6.4 获取金额分布

**请求：**
```
GET /api/v1/stats/distribution
```

**响应：**
```json
{
    "code": 0,
    "data": [
        {
            "range": "0-10元",
            "count": 100,
            "total_amount": 5000,
            "avg_amount": 50
        },
        {
            "range": "10-50元",
            "count": 80,
            "total_amount": 24000,
            "avg_amount": 300
        }
    ]
}
```

### 6.5 获取房间排行

**请求：**
```
GET /api/v1/stats/rooms/ranking?limit=10
```

**响应：**
```json
{
    "code": 0,
    "data": [
        {
            "room_id": 1,
            "room_name": "VIP房",
            "session_count": 50,
            "round_count": 234,
            "player_count": 56,
            "total_amount": 4500000,
            "commission": 225000,
            "straight_count": 12,
            "leopard_count": 3
        }
    ]
}
```

### 6.6 获取平台红包统计

**请求：**
```
GET /api/v1/stats/system-packets
```

**响应：**
```json
{
    "code": 0,
    "data": [
        {
            "sender_type": "system",
            "send_count": 20,
            "total_amount": 100000,
            "avg_amount": 5000
        },
        {
            "sender_type": "system_forced",
            "send_count": 5,
            "total_amount": 25000,
            "avg_amount": 5000
        }
    ]
}
```

---

## 七、现有数据表支持情况

| 数据表 | 已有字段 | 统计用途 | 相关代码 |
|--------|---------|---------|---------|
| `rooms` | player_count, current_round, status | 房间状态、玩家数 | [room.go](file:///e:/demo/demo-room/backend/game/model/room.go) |
| `game_sessions` | player_count, actual_rounds, status | 游戏局数统计 | [session.go](file:///e:/demo/demo-room/backend/game/model/session.go) |
| `rounds` | total_amount, commission, sender_type | 红包金额、抽佣、发送者 | [round.go](file:///e:/demo/demo-room/backend/game/model/round.go) |
| `round_settlement` | reward_type, reward_amount | 顺子/豹子统计 | [bill.go#L44](file:///e:/demo/demo-room/backend/settlement/model/bill.go#L44) |
| `session_players` | user_id, total_send, total_grab | 玩家统计 | [session.go#L34](file:///e:/demo/demo-room/backend/game/model/session.go#L34) |
| `packets` | amount | 红包金额分布 | [packet.go](file:///e:/demo/demo-room/backend/game/model/packet.go) |
| `round_grab_records` | amount, is_min | 抢红包统计 | [round.go#L39](file:///e:/demo/demo-room/backend/game/model/round.go#L39) |
| `bill_record` | bill_type, amount | 账单统计 | [bill.go](file:///e:/demo/demo-room/backend/settlement/model/bill.go) |

---

## 八、配置说明

### 8.1 抽佣配置

```yaml
# 默认抽佣率：5%
# 配置位置：game/domain/game_state.go
type CommissionConfig struct {
    Rate float64 `json:"rate"`  // 默认 0.05
}
```

### 8.2 奖励控制配置

```yaml
# config/algorithm.yaml
min_packet_amount: 1
straight_probability: 0.08      # 顺子概率 8%
leopard_probability: 0.002      # 豹子概率 0.2%

reward_control:
  global_switch_enabled: false
  profit_ratio_threshold: 0.05  # 盈利率阈值 5%
  room_configs:
    1776078870944425019:
      guarantee_enabled: true
      guarantee_straight: true   # 保底顺子
      guarantee_leopard: true    # 保底豹子
      probability_enabled: false
      straight_probability: 0.08
      leopard_probability: 0.002
```

---

## 九、实现优先级

### P0 - 核心指标（立即实现）
- [ ] 今日局数统计
- [ ] 活跃玩家数
- [ ] 平台收益
- [ ] 大屏首页接口

### P1 - 趋势分析（一周内）
- [ ] 24小时趋势图
- [ ] 红包金额分布
- [ ] 顺子/豹子触发统计
- [ ] 平台红包统计

### P2 - 深度分析（两周内）
- [ ] 房间热度排行
- [ ] 每日趋势对比
- [ ] 历史数据查询

---

## 十、总结

### 优势
1. ✅ 所有需求指标的数据**均已存在**于现有数据库表中
2. ✅ 关键业务字段（reward_type, sender_type, commission）设计合理，便于统计
3. ✅ 纯数据库查询，实现简单，无需额外中间件
4. ✅ 数据表结构完善，无需修改现有业务逻辑
5. ✅ 页面刷新即可获取最新数据，无需实时推送

### 需要新增
1. 统计模块（stats package）
2. 数据仓库层（repository）
3. HTTP 接口（handler）
4. 前端大屏页面（独立前端项目）

### 技术栈建议
- **后端：** Go + Gin + GORM（复用现有技术栈）
- **前端：** Vue3 + ECharts（页面刷新获取数据）
- **部署：** 可集成到现有 gateway 服务，或独立部署

---

## 附录：关键代码位置

| 功能模块 | 文件路径 |
|---------|---------|
| 房间模型 | [game/model/room.go](file:///e:/demo/demo-room/backend/game/model/room.go) |
| 回合模型 | [game/model/round.go](file:///e:/demo/demo-room/backend/game/model/round.go) |
| 会话模型 | [game/model/session.go](file:///e:/demo/demo-room/backend/game/model/session.go) |
| 红包模型 | [game/model/packet.go](file:///e:/demo/demo-room/backend/game/model/packet.go) |
| 抽佣配置 | [game/domain/game_state.go#L81](file:///e:/demo/demo-room/backend/game/domain/game_state.go#L81) |
| 发送者类型 | [game/domain/sender_type.go](file:///e:/demo/demo-room/backend/game/domain/sender_type.go) |
| 顺子生成器 | [game/algorithm/straight.go](file:///e:/demo/demo-room/backend/game/algorithm/straight.go) |
| 豹子生成器 | [game/algorithm/leopard.go](file:///e:/demo/demo-room/backend/game/algorithm/leopard.go) |
| 奖励控制器 | [game/algorithm/reward_controller.go](file:///e:/demo/demo-room/backend/game/algorithm/reward_controller.go) |
| 奖励类型定义 | [game/algorithm/model.go](file:///e:/demo/demo-room/backend/game/algorithm/model.go) |
| 结算模型 | [settlement/model/bill.go](file:///e:/demo/demo-room/backend/settlement/model/bill.go) |
