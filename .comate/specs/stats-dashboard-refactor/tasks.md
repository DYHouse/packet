# Stats & Dashboard 重构任务计划

- [x] Task 1: 后端 - 提取日期解析公共函数 + 请求校验
    - 1.1: 在 handler 包中创建 `parse_date_range.go`, 实现 `ParseDateRange()` 函数 (统一解析 start_date/end_date, 校验 start<=end, 最大 90 天范围)
    - 1.2: 重构所有 Handler 方法, 用 `ParseDateRange()` 替换重复的日期解析代码
    - 1.3: 统一 system-packets 接口改用 start_date+end_date 参数风格

- [x] Task 2: 后端 - Repository SQL 优化
    - 2.1: 重写 GetDashboardStats, 合并 9 个子查询为 3 次聚合查询, NetProfit 在 SQL 中计算
    - 2.2: 重写 GetRoomRanking, 消除 correlated subquery, 改用 JOIN + 预聚合子查询
    - 2.3: 重写 GetSystemPacketStats, 用范围查询替代 DATE() 函数, 保持索引友好
    - 2.4: 修复 HourlyTrend 的日期格式 hack, 改用 LPAD(h,2,'0') 直接输出 HH:00

- [x] Task 3: 后端 - 金额分布区间可配置化
    - 3.1: 在 config.go 中增加 AmountRange 结构和 YAML 配置字段
    - 3.2: 在 Repository 中实现动态 SQL 生成函数, 替换硬编码的 CASE WHEN

- [x] Task 4: 后端 - Service 层增加 Redis 缓存
    - 4.1: 在 config.go 中增加 Redis 配置项, main.go 中初始化 Redis 连接
    - 4.2: StatsService 增加 redis.Cmdable 依赖, 实现缓存读取/写入逻辑 (5 分钟 TTL)
    - 4.3: 空结果缓存 (1 分钟短 TTL) 防穿透, Redis 不可用时降级直接查 DB

- [x] Task 5: 后端 - DTO 补充 + 统一错误响应 + 分页
    - 5.1: dto 中增加 PaginationReq/PaginationResp, RoomRanking 请求增加 offset 参数
    - 5.2: Handler 中统一错误响应格式 `{"code": 4xxxx/5xxxx, "message": "..."}`
    - 5.3: Room Ranking limit 上限截断为 100

- [x] Task 6: 后端 - main.go 优雅关停
    - 6.1: 增加 os.Signal 监听 SIGTERM/SIGINT, 优雅关闭 HTTP Server 和 Redis 连接

- [x] Task 7: 前端 - format.js 增加 toYuan + Dashboard 金额转换统一
    - 7.1: format.js 中增加 `toYuan(value)` 函数
    - 7.2: Dashboard.vue 中 fetchDailyTrend/fetchHourlyTrend 统一使用 toYuan 替代手动 /100

- [x] Task 8: 前端 - Dashboard 概览卡片补全 + 系统红包统计
    - 8.1: 概览区域增加总抽佣、罚金收入、系统红包支出、系统红包数 4 个卡片, grid 改为 auto-fill 响应式布局
    - 8.2: 新增系统红包统计 section, 消费 /system-packets API, 展示各类型系统红包的次数和金额

- [x] Task 9: 前端 - BarChart 通用化改造
    - 9.1: BarChart 增加 xField/yFields/yNames props, 替换硬编码的字段名和系列名

- [x] Task 10: 前端 - 局部加载态 + 错误提示
    - 10.1: Dashboard.vue 将单一 loading 拆为 sectionLoading 对象, 各 section 独立 loading
    - 10.2: API 失败时在页面展示错误提示 (inline message 或 toast), 替代 console.error

- [x] Task 11: 前端 - DatePicker yesterday bug 修复
    - 11.1: 修复 yesterday 分支中直接修改 Date 对象的问题, 改用 new Date() 拷贝

- [x] Task 12: 前端 - stats.js API 适配后端变更
    - 12.1: getSystemPacketStats 改用 start_date+end_date 参数
    - 12.2: getRoomRanking 增加 offset 参数
