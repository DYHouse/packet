# Stats & Dashboard 重构总结

## 完成情况

全部 12 个任务已完成, 涵盖后端 6 项 + 前端 6 项重构。

---

## 后端变更

### 1. 日期解析公共函数 (`handler/parse_date_range.go`)
- 新增 `ParseDateRange()` 函数, 统一解析 start_date/end_date
- 内置校验: start <= end, 最大查询范围 90 天
- 消除 5 处 ~15 行重复代码

### 2. Handler 重构 (`handler/stats_handler.go`)
- 所有 6 个方法使用 `ParseDateRange()` 替代内联解析
- 新增 `respondError()` 统一错误响应格式 `{"code": 4xxxx/5xxxx, "message": "..."}`
- `/system-packets` 改用 start_date+end_date 参数风格
- Room Ranking 新增 offset 分页参数, limit 上限 100

### 3. Repository SQL 优化 (`repository/stats_repository.go`)
- **GetDashboardStats**: 9 个独立子查询 → 合并 rounds 聚合为 1 次子查询, NetProfit 在 SQL 中计算
- **GetRoomRanking**: 3 个 correlated subquery → JOIN + 预聚合子查询, O(N*M) → O(N)
- **GetSystemPacketStats**: `DATE(created_at) = ?` → `created_at >= ? AND created_at < ?` 索引友好
- **GetHourlyTrend**: 去掉 `CONCAT('1970-01-01 ', ...)` hack, 改用 `LPAD(h, 2, '0')`
- 提取 `dateRange()` 辅助函数, 统一用 nextDay 替代 `+ INTERVAL 1 DAY`
- 所有日期范围查询统一为 `created_at >= ? AND created_at < ?` 格式

### 4. 金额分布区间可配置化
- `config.go` 新增 `AmountRange` 结构和 YAML 配置
- 默认值保持与原硬编码一致
- Repository 中新增 `buildAmountCaseWhen()` 动态生成 CASE WHEN

### 5. Service 层 Redis 缓存 (`service/stats_service.go`)
- 所有 6 个方法增加 Redis 缓存逻辑, TTL 5 分钟
- 空结果短 TTL 1 分钟防穿透
- Redis 不可用时静默降级为直接查 DB
- 使用 JSON 序列化, 泛型 `getCache[T]` 辅助函数

### 6. 配置与启动 (`config/config.go`, `cmd/stats/main.go`)
- 新增 Redis 配置项 (addr, password, db, pool_size)
- main.go 初始化 Redis, 连接失败降级为无缓存模式
- 优雅关停: 关闭 HTTP Server + Redis 连接

---

## 前端变更

### 7. 金额转换统一 (`utils/format.js`)
- 新增 `toYuan(value)` 函数, 用于 ECharts 等需要原始数值的场景
- Dashboard.vue 中 fetchDailyTrend/fetchHourlyTrend 统一使用 toYuan 替代手动 `/100`

### 8. Dashboard 概览卡片补全 + 系统红包统计 (`views/Dashboard.vue`)
- 概览从 5 项扩展到 9 项: 新增总抽佣、罚金收入、系统红包支出、系统红包数
- grid 布局改为 `auto-fill, minmax(160px, 1fr)` 响应式
- 新增系统红包统计 section, 消费 `/system-packets` API
- 展示各类型 (系统/强制/续场) 红包的次数、总金额、均值

### 9. BarChart 通用化改造 (`components/charts/BarChart.vue`)
- 新增 xField/yFields/yNames props, 与 LineChart 接口对齐
- 支持多系列柱状图, 自动 legend
- Dashboard.vue 中 BarChart 传入 x-field="range", y-fields="['count']", y-names="['红包数量']"

### 10. 局部加载态 + 错误提示 (`views/Dashboard.vue`)
- 单一 `loading` 拆为 `sectionLoading` 对象, 各 section 独立
- 新增 `errorMessage` ref, API 失败时在页面顶部显示错误横幅
- 替代原有 console.error

### 11. DatePicker bug 修复 (`components/DatePicker.vue`)
- yesterday 分支使用 `new Date(today)` 拷贝, 避免修改原对象
- last7days/last30days 同步修复

### 12. stats.js API 适配 (`api/stats.js`)
- `getSystemPacketStats` 改用 start_date+end_date 参数
- `getRoomRanking` 增加 offset 参数

---

## 受影响文件清单

| 文件 | 变更类型 |
|------|----------|
| `backend/stats/handler/parse_date_range.go` | 新增 |
| `backend/stats/handler/stats_handler.go` | 重写 |
| `backend/stats/service/stats_service.go` | 重写 |
| `backend/stats/repository/stats_repository.go` | 重写 |
| `backend/stats/config/config.go` | 修改 |
| `backend/stats/dto/stats_dto.go` | 修改 |
| `backend/cmd/stats/main.go` | 修改 |
| `dashboard/src/utils/format.js` | 修改 |
| `dashboard/src/views/Dashboard.vue` | 重写 |
| `dashboard/src/components/charts/BarChart.vue` | 重写 |
| `dashboard/src/components/DatePicker.vue` | 修改 |
| `dashboard/src/api/stats.js` | 修改 |
