# Stats & Dashboard 重构方案

## 一、现状分析

### 架构概览
- **后端**: 独立 stats 微服务 (Gin + GORM), 6 个 REST API 端点, 直接查询 MySQL
- **前端**: Vue 3 + ECharts + Axios 单页看板, Vite 构建

### 已识别的问题 (共 22 项)

---

#### 后端问题 (15 项)

**P0 - 性能/正确性**

1. **GetDashboardStats 巨型子查询**: 使用 9 个独立子查询, 18 个重复日期参数, 对同一天的数据全表扫描 9 次
   - 位置: `repository/stats_repository.go:24-35`
   - 影响: 查询耗时随数据量线性增长, 无缓存下每次请求都是重查询

2. **GetRoomRanking 关联子查询**: SELECT 中 3 个 correlated subquery (`player_count`, `straight_count`, `leopard_count`), 每行结果触发额外查询
   - 位置: `repository/stats_repository.go:121-138`
   - 影响: O(N*M) 复杂度, 房间越多越慢

3. **SystemPacketStats 使用 DATE() 函数**: `DATE(created_at) = ?` 破坏索引使用
   - 位置: `repository/stats_repository.go:161`
   - 影响: 全表扫描

4. **NetProfit 在 Go 代码中计算**: 应由 SQL 完成, 当前从 DB 取 3 个字段再在 Go 中相加
   - 位置: `repository/stats_repository.go:41`

**P1 - 可维护性**

5. **Handler 日期解析重复**: 6 个方法中有 5 个复制了相同的 start_date/end_date 解析逻辑 (~15 行重复代码)
   - 位置: `handler/stats_handler.go:43-56, 73-86, 103-116, 133-146, 163-176`

6. **Service 层纯透传**: `StatsService` 仅做方法转发, 无任何业务逻辑 (仅 `GetRoomRanking` 有 limit 默认值校验)
   - 位置: `service/stats_service.go` 全文

7. **金额分布区间硬编码**: SQL 中 CASE WHEN 的阈值和标签写死, 无法配置
   - 位置: `repository/stats_repository.go:84-103`

8. **HourlyTrend 日期格式 hack**: `CONCAT('1970-01-01 ', LPAD(h, 2, '0'), ':00')` 只为生成时间字符串
   - 位置: `repository/stats_repository.go:53`

9. **日期范围参数不一致**: 5 个接口用 start_date+end_date, system-packets 用单个 date
   - 位置: `handler/stats_handler.go:196`

**P2 - 安全/健壮性**

10. ~~无认证鉴权~~: **暂不实现**, 后续迭代补充
11. **无请求校验**: start_date 可晚于 end_date, 无最大查询范围限制
12. **无缓存层**: 项目有 Redis 基础设施, 但 stats 未使用, 每次请求直接打 DB
13. **Room Ranking 无分页**: 仅有 limit 无 offset, 无法翻页
14. **错误码不结构化**: 返回 `{"code": 500, "message": "..."}`, 无业务错误码分类
15. **无优雅关停**: stats main.go 无 SIGTERM 处理

---

#### 前端问题 (7 项)

**P0 - 功能缺失**

1. **系统红包统计未展示**: 后端有 `/system-packets` API, 但前端 Dashboard 未消费该接口
   - 位置: `views/Dashboard.vue` 中无 systemPacket 相关代码

**P1 - 体验/一致性**

2. **金额转换不一致**: Dashboard.vue 中 `fetchDailyTrend` 手动 `/100`, 而 StatCard 通过 `type="money"` 调用 `formatMoney`. 同样是金额, 处理方式不统一
   - 位置: `views/Dashboard.vue:194-198` vs `components/cards/StatCard.vue:28-29`

3. **无错误提示**: API 失败只 `console.error`, 用户无感知
   - 位置: `views/Dashboard.vue:222-224`

4. **无局部加载态**: 单一 `loading` 覆盖所有模块, 刷新时整个页面 loading
   - 位置: `views/Dashboard.vue:108`

5. **BarChart 不可复用**: 硬编码 `红包数量` 系列, y 轴字段名和系列名固定
   - 位置: `components/charts/BarChart.vue:94-103`

6. **DatePicker yesterday bug**: `new Date().setDate()` 直接修改 Date 对象, 后续 `formatDate(today)` 可能受影响
   - 位置: `components/DatePicker.vue:73-74`

7. **概览卡片不完整**: Dashboard 数据含 `total_commission`、`penalty_income`、`system_packet_cost`、`system_packet_count`, 但概览只展示 5 项, 关键财务指标未展示

---

## 二、重构方案

### 2.1 后端重构

#### 2.1.1 Repository SQL 优化

**GetDashboardStats 重写**: 将 9 个子查询合并为 3 个主查询 + 1 次组合:

```go
func (r *StatsRepository) GetDashboardStats(ctx context.Context, startDate, endDate time.Time) (*dto.DashboardStats, error) {
    var stats dto.DashboardStats
    startDateStr := startDate.Format("2006-01-02")
    endDateStr := endDate.Format("2006-01-02")
    nextDay := endDate.AddDate(0, 0, 1).Format("2006-01-02")

    // 查询1: rounds 相关聚合 (sessions, rounds, commission, system_packet)
    err := r.db.WithContext(ctx).Raw(`
        SELECT 
            (SELECT COUNT(*) FROM game_sessions WHERE created_at >= ? AND created_at < ?) as today_sessions,
            (SELECT COUNT(*) FROM rounds WHERE created_at >= ? AND created_at < ?) as today_rounds,
            COALESCE(SUM(CASE WHEN 1=1 THEN commission ELSE 0 END), 0) as total_commission,
            COALESCE(SUM(CASE WHEN sender_type IN ('system', 'system_forced', 'system_resume') THEN total_amount ELSE 0 END), 0) as system_packet_cost,
            SUM(CASE WHEN sender_type IN ('system', 'system_forced', 'system_resume') THEN 1 ELSE 0 END) as system_packet_count
        FROM rounds 
        WHERE created_at >= ? AND created_at < ?
    `, startDateStr, nextDay, startDateStr, nextDay, startDateStr, nextDay).Scan(&stats).Error
    if err != nil {
        return nil, err
    }

    // 查询2: active_players + penalty_income (可并行)
    // 查询3: special_rewards (straight/leopard)
    // ... NetProfit 在 SQL 中用计算列完成
}
```

**GetRoomRanking 重写**: 消除 correlated subquery, 改用 JOIN + 预聚合:

```sql
SELECT 
    r.room_id,
    rm.config_name as room_name,
    COUNT(DISTINCT r.session_id) as session_count,
    COUNT(*) as round_count,
    COALESCE(sp.player_count, 0) as player_count,
    COALESCE(SUM(r.total_amount), 0) as total_amount,
    COALESCE(SUM(r.commission), 0) as commission,
    COALESCE(sr.straight_count, 0) as straight_count,
    COALESCE(lr.leopard_count, 0) as leopard_count
FROM rounds r
LEFT JOIN rooms rm ON r.room_id = rm.room_id
LEFT JOIN (
    SELECT room_id, COUNT(DISTINCT user_id) as player_count 
    FROM session_players 
    WHERE joined_at >= ? AND joined_at < ? 
    GROUP BY room_id
) sp ON r.room_id = sp.room_id
LEFT JOIN (
    SELECT room_id, COUNT(*) as straight_count 
    FROM special_rewards WHERE reward_type = 1 
    AND created_at >= ? AND created_at < ? 
    GROUP BY room_id
) sr ON r.room_id = sr.room_id
LEFT JOIN (
    SELECT room_id, COUNT(*) as leopard_count 
    FROM special_rewards WHERE reward_type = 2 
    AND created_at >= ? AND created_at < ? 
    GROUP BY room_id
) lr ON r.room_id = lr.room_id
WHERE r.created_at >= ? AND r.created_at < ?
GROUP BY r.room_id, rm.config_name, sp.player_count, sr.straight_count, lr.leopard_count
ORDER BY round_count DESC
LIMIT ? OFFSET ?
```

**SystemPacketStats 索引友好重写**: 用范围查询替代 DATE() 函数:

```sql
WHERE sender_type IN ('system', 'system_forced', 'system_resume')
AND created_at >= ? AND created_at < ? + INTERVAL 1 DAY
```

#### 2.1.2 Handler 日期解析抽取

提取通用日期范围解析中间件/辅助函数:

```go
// DateRange 从 query 参数解析日期范围, 返回 (startDate, endDate, bool)
// bool=false 表示参数非法, handler 应直接返回 400
func ParseDateRange(c *gin.Context, defaultOffset int) (time.Time, time.Time, bool) {
    endDateStr := c.Query("end_date")
    startDateStr := c.Query("start_date")

    var endDate, startDate time.Time
    var ok bool

    if endDate, ok = parseDate(endDateStr); !ok {
        endDate = time.Now()
    }
    if startDate, ok = parseDate(startDateStr); !ok {
        startDate = endDate.AddDate(0, 0, defaultOffset)
    }

    // 校验: start <= end
    if startDate.After(endDate) {
        return time.Time{}, time.Time{}, false
    }
    // 校验: 最多查 90 天
    if endDate.Sub(startDate).Hours() > 90*24 {
        return time.Time{}, time.Time{}, false
    }

    return startDate, endDate, true
}
```

#### 2.1.3 Service 层增强

Service 不再纯透传, 增加缓存逻辑:

```go
type StatsService struct {
    repo   *repository.StatsRepository
    cache  redis.Cmdable
}

func (s *StatsService) GetDashboardStats(ctx context.Context, startDate, endDate time.Time) (*dto.DashboardStats, error) {
    cacheKey := fmt.Sprintf("stats:dashboard:%s:%s", startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
    
    var stats dto.DashboardStats
    if err := s.cache.Get(ctx, cacheKey).Scan(&stats); err == nil {
        return &stats, nil
    }
    
    result, err := s.repo.GetDashboardStats(ctx, startDate, endDate)
    if err != nil {
        return nil, err
    }
    
    // 缓存 5 分钟
    s.cache.Set(ctx, cacheKey, result, 5*time.Minute)
    return result, nil
}
```

#### 2.1.4 金额分布区间可配置化

将硬编码的区间提取为配置:

```go
type AmountRange struct {
    Min   int64  `yaml:"min"`
    Max   int64  `yaml:"max"`
    Label string `yaml:"label"`
}

// config.yaml
amount_ranges:
  - min: 0    max: 1000   label: "0-10元"
  - min: 1000 max: 5000   label: "10-50元"
  - min: 5000 max: 10000  label: "50-100元"
  - min: 10000 max: 50000  label: "100-500元"
  - min: 50000 max: 0      label: "500元以上"
```

SQL 动态生成:

```go
func buildAmountDistributionSQL(ranges []config.AmountRange) (string, []interface{}) {
    // 动态拼接 CASE WHEN 和 GROUP BY
}
```

#### 2.1.5 API 接口统一和增强

| 变更 | 说明 |
|------|------|
| `/system-packets` 改用 start_date+end_date | 统一日期参数风格 |
| Room Ranking 增加 offset 参数 | 支持分页 |
| 增加统一错误响应格式 | `{"code": 40001, "message": "...", "detail": "..."}` |
| ~~增加认证中间件~~ | **暂不实现**, 后续迭代补充 JWT/API Key 校验 |

#### 2.1.6 数据流路径 (重构后)

```
Client Request
    |
    v
    Gin Router
    |
    v
Handler (ParseDateRange 统一解析 + 校验)
    |
    v
Service (Redis Cache -> Miss -> Repository)
    |
    v
Repository (优化后的 SQL, 索引友好)
    |
    v
MySQL (composite indexes on created_at)
```

---

### 2.2 前端重构

#### 2.2.1 Dashboard 概览卡片补全

增加 4 个指标卡片, 形成完整的运营概览:

```
| 局数 | 活跃玩家 | 总抽佣 | 罚金收入 | 平台收益 | 系统红包支出 | 顺子触发 | 豹子触发 | 系统红包数 |
  9 cards, grid: repeat(auto-fill, minmax(160px, 1fr))
```

#### 2.2.2 金额转换统一

所有金额字段统一由后端返回 `amount` (分), 前端统一使用 `formatMoney()` 转换. 删除 Dashboard.vue 中的手动 `/100` 逻辑, 改为在 fetch 方法中统一标记哪些字段是金额类型, 在渲染层统一处理.

方案: 在 `format.js` 中增加通用的数据转换函数:

```js
// 将分转为元的数值 (用于 ECharts 渲染)
export const toYuan = (value) => value / 100
```

Dashboard fetch 方法中:

```js
const fetchDailyTrend = async () => {
  const data = await statsApi.getDailyTrend(startDate.value, endDate.value)
  dailyTrend.value = data.map(item => ({
    ...item,
    date: formatDate(item.date),
    net_profit: toYuan(item.net_profit),
    total_commission: toYuan(item.total_commission),
    system_packet_cost: toYuan(item.system_packet_cost),
    penalty_income: toYuan(item.penalty_income),
  }))
}
```

#### 2.2.3 BarChart 复用化

将 BarChart 改为与 LineChart 同级的通用图表组件:

```vue
<script setup>
const props = defineProps({
  data: { type: Array, default: () => [] },
  title: { type: String, default: '' },
  xField: { type: String, default: '' },
  yFields: { type: Array, default: () => [] },
  yNames: { type: Array, default: () => [] },
})
</script>
```

#### 2.2.4 错误提示与局部加载

- 增加全局 toast/notification 组件, API 失败时展示给用户
- 将单一 `loading` 拆为按 section 的加载状态:

```js
const sectionLoading = ref({
  dashboard: false,
  hourlyTrend: false,
  dailyTrend: false,
  amountDistribution: false,
  roomRanking: false,
})
```

#### 2.2.5 系统红包统计卡片

新增系统红包统计 section, 消费 `/system-packets` API:

```vue
<section class="section">
  <div class="section-header"><h2>系统红包统计</h2></div>
  <div class="system-packets-grid">
    <div v-for="item in systemPacketStats" :key="item.sender_type" class="packet-type-card">
      <div class="type-label">{{ senderTypeMap[item.sender_type] }}</div>
      <div class="type-value">{{ item.send_count }} 次</div>
      <div class="type-amount">{{ formatMoney(item.total_amount) }}</div>
    </div>
  </div>
</section>
```

#### 2.2.6 DatePicker 修复

```js
case 'yesterday': {
  const yesterday = new Date(today)  // 不要直接修改 today
  yesterday.setDate(yesterday.getDate() - 1)
  start = end = yesterday
  break
}
```

---

## 三、受影响文件清单

### 后端

| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `backend/stats/repository/stats_repository.go` | 重写 | SQL 优化, 消除子查询, 索引友好 |
| `backend/stats/handler/stats_handler.go` | 重构 | 提取日期解析, 增加校验, 统一错误响应 |
| `backend/stats/service/stats_service.go` | 增强 | 增加 Redis 缓存逻辑 |
| `backend/stats/dto/stats_dto.go` | 修改 | 新增分页 DTO, 补充字段 |
| `backend/stats/config/config.go` | 修改 | 增加 Redis 配置, 金额区间配置 |
| `backend/cmd/stats/main.go` | 修改 | 初始化 Redis, 优雅关停 |

### 前端

| 文件 | 修改类型 | 说明 |
|------|----------|------|
| `dashboard/src/views/Dashboard.vue` | 重构 | 补全卡片, 局部加载, 错误提示, 系统红包 |
| `dashboard/src/components/charts/BarChart.vue` | 重构 | 通用化改造 |
| `dashboard/src/components/DatePicker.vue` | 修复 | yesterday bug |
| `dashboard/src/utils/format.js` | 增加 | toYuan 通用函数 |
| `dashboard/src/api/stats.js` | 修改 | system-packets 改用 start_date+end_date |

---

## 四、边界条件与异常处理

1. **日期范围超限**: 拒绝 >90 天的查询范围, 返回 400 + 明确错误信息
2. **start_date > end_date**: 返回 400, 提示日期范围无效
3. **缓存穿透**: 对空结果也缓存 (短 TTL 1 分钟), 防止重复击穿
4. **Redis 不可用**: 降级为直接查 DB, 不阻塞请求
5. **DB 查询超时**: 设置 context timeout (3 秒), 超时返回 503
6. **并发刷新**: 前端防抖, 后端 singleflight 防缓存击穿
7. **大 limit 值**: Room Ranking limit 上限 100, 超过截断

## 五、预期收益

| 维度 | 现状 | 重构后 |
|------|------|--------|
| Dashboard API 响应 | ~500ms (9 次子查询) | ~50ms (3 次聚合 + 缓存) |
| Room Ranking | O(N*M) 关联子查询 | O(N) JOIN 聚合 |
| Handler 重复代码 | 5 处 ~15 行重复 | 1 个 ParseDateRange 函数 |
| 金额处理 | 前端分散 /100 | 统一 toYuan + formatMoney |
| 运营指标可见性 | 5/9 指标展示 | 9/9 全部展示 |
| 系统红包数据 | 后端有 API, 前端未用 | 完整展示 |
| 缓存 | 无 | Redis 5 分钟 TTL |
| 认证 | 无 | 暂不实现, 后续补充 |
