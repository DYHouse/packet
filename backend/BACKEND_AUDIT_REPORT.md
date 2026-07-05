# Backend 全量代码审查报告与重构方案

> 审查范围：`backend/` 全量 Go 代码（171 文件）。
> 审查方法：4 个搜索代理并行逐文件阅读，对照 `CODING_STANDARD.md` 第 2–20 章规约逐条核对，每条问题均附 `file:line` 证据，未凭直觉。
> 审查日期：2026-07-05。

---

## 0. 概览

| 级别 | 数量 | 含义 |
|------|------|------|
| P0 | 10 | 资金安全 / 数据正确性 / 安全漏洞，**必须立即修复** |
| P1 | 42 | 架构 / 可维护性 / 可观测性，**本迭代修复** |
| P2 | 28 | 命名 / 注释 / 测试覆盖，**择机收敛** |
| **总计** | **80** | （已对 4 份子报告去重） |

按模块分布：

| 模块 | 问题数 | 主要问题类型 |
|------|--------|--------------|
| settlement/ | 22 | 资金正确性、事务边界、错误吞没 |
| game/ | 14 | math/rand 滥用、错误包装、DI 参数过多 |
| common/ | 18 | 锁未用 Lua、限流 fail-open、Kafka panic |
| gateway/ | 13 | HTTP 信封不一致、context.Background、配置重复 |
| stats/ + scripts/ | 13 | 响应信封、硬编码凭据、手动解析参数 |

---

## 1. P0 级问题（资金 / 安全 / 正确性）

### P0-1 [TX-1/§8.3] refund_service 跨表更新 bill.refund_status 缺失乐观锁
- **位置**：`settlement/service/refund_service.go:116-121`
- **现状**：在 service 层直接开事务更新 `bill_record.refund_status` 时，WHERE 子句只有 `id = ?`，没有 `AND refund_status = ?`，也没有检查 `RowsAffected==0`。并发退款可重复扣款。
- **应该**：调用 `BillManager.CreateRefundAuditAndUpdateBillRefundStatusInTransaction`（`bill_manager.go:394-415` 已实现乐观锁）。
- **证据**：
```go
if err := tx.Model(&model.BillRecord{}).
    Where("id = ?", bill.ID).  // 缺少 AND refund_status = ?
    Updates(map[string]interface{}{
        "refund_status":   dto.RefundStatusPending,
        "refund_order_no": refundOrderNo,
    }).Error; err != nil {
```

### P0-2 [TX-3/§18.15] executeRefund 失败路径不置 Failed，退款单永久卡 Processing
- **位置**：`settlement/service/refund_service.go:182, 209`
- **现状**：RPC 失败时调用 `UpdateRefundAuditError` 只更新 `error_message`，不更新 `status`。退款单停留在 `Processing`，而 `RefundProcessScheduler` 只查 `RefundStatusPending`（`refund_process_scheduler.go:45`），导致失败退款永不重试。
- **应该**：失败时将 status 置为 `Failed`（或回退到 `Approved` 以便重试），并记录 error_message。
- **证据**：
```go
s.billMgr.UpdateRefundAuditError(ctx, refund.ID, dto.RefundStatusProcessing, err.Error())
// UpdateRefundAuditError 只 Update("error_message", errMsg)，不改 status
```

### P0-3 [TX-9/§18.21] BillRecord 复合唯一索引 GORM 标签残缺
- **位置**：`settlement/model/bill.go:7, 10, 16`
- **现状**：只有 `RoundTraceID` 带 `uniqueIndex:idx_bill_record_round_type_user`，`BillType` 和 `UserID` 均无此标签。GORM AutoMigrate 会创建单列唯一索引而非复合索引，导致同一 `round_trace_id` 无法创建多条 bill（grab+commission+reward 等会冲突）。
- **应该**：三字段均加 `uniqueIndex:idx_round_trace_bill_user,priority:1/2/3`。
- **证据**：
```go
RoundTraceID string `gorm:"index;size:64;uniqueIndex:idx_bill_record_round_type_user" json:"round_trace_id"`
BillType     int    `gorm:"index;not null" json:"bill_type"`     // 缺 uniqueIndex
UserID       int64  `gorm:"index;not null" json:"user_id"`       // 缺 uniqueIndex
```

### P0-4 [TX-2/§8.2] refund_service 三处直接在 service 层持有 *gorm.DB 开事务
- **位置**：`settlement/service/refund_service.go:112-127`（applyForRefundLocked）、`254-256`（RejectRefund）、`settlement/service/deduct_service.go:363-368`（handleFirstRoundDeductFailure）
- **现状**：Service 层直接 `s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { ... })`，违反"禁止 service 直接持有 *gorm.DB 开事务"规约。
- **应该**：事务通过 `domain.Transaction.Execute(ctx, func(txCtx context.Context) error { ... })` 调用，或下沉到 `BillManager`。
- **证据**：
```go
if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
    if err := tx.Create(refundAudit).Error; err != nil {
```

### P0-5 [安全] scripts 硬编码生产凭据
- **位置**：`scripts/check_tables.go:12`、`scripts/init_rooms.go:52,66`、`scripts/test_game_apis.go:18-21`、`scripts/test_websocket.go:29,33`
- **现状**：硬编码数据库 root 密码 `123456`、merchant secret `aca5d11a-e481-4163-9505-194564558ae3`、生产 IP `43.135.35.31`。
- **应该**：从环境变量或配置文件读取。
- **证据**：
```go
dsn := "root:123456@tcp(127.0.0.1:3306)/cashparty?charset=utf8mb4&parseTime=True&loc=Local"
const merchantSecret = "aca5d11a-e481-4163-9505-194564558ae3"
```

### P0-6 [§6.4] 算法层使用 math/rand 进行红包金额随机分配
- **位置**：`game/algorithm/straight.go:5, 48, 50`
- **现状**：导入 `math/rand`，用 `rand.NewSource(time.Now().UnixNano())` 创建随机源，用 `r.Perm(int(n))` 对红包金额索引进行随机置换。`time.Now().UnixNano()` 作为种子可被预测，攻击者可推断金额分布。
- **应该**：红包金额分配属于金额拆分场景，规约 §6.4 强制要求使用 `crypto/rand`。
- **证据**：
```go
import "math/rand"
r := rand.New(rand.NewSource(time.Now().UnixNano()))
indices := r.Perm(int(n))
```

### P0-7 [§6.4] grab_service 使用 math/rand 选择红包 ID 与生成随机偏移
- **位置**：`game/application/grab_service.go:6, 98, 123`
- **现状**：`GetAvailablePacketID` 用 `rand.Intn(len(packetIDs))` 选择红包；`RobotGrabPacket` 用 `rand.Intn(1000)` 生成抢红包随机起始偏移传入 Lua 脚本。影响红包抢取结果。
- **应该**：使用 `crypto/rand`。
- **证据**：
```go
return packetIDs[rand.Intn(len(packetIDs))], nil  // line 98
rand.Intn(1000),                                  // line 123
```

### P0-8 [§6 并发安全] UserLimiter AllowGrab/AllowJoin/AllowCreate 共享指针 data race
- **位置**：`common/limiter/limiter.go:98-114`
- **现状**：`ul.configs["grab"]` 返回共享 `*LimitConfig` 指针，每次调用都修改 `cfg.Key = fmt.Sprintf("grab:%s", userID)`。多 goroutine 并发调用同一方法时，共享指针的 Key 字段被同时读写，造成 data race，会导致限流 key 错乱（A 用户的限流计数落到 B 用户的 key 上）。
- **应该**：每次调用构造新的 `LimitConfig` 值（非指针），或在 `Allow` 入参用值类型传递 Key。
- **证据**：
```go
func (ul *UserLimiter) AllowGrab(ctx context.Context, userID string) (bool, error) {
    cfg := ul.configs["grab"]                // 共享指针
    cfg.Key = fmt.Sprintf("grab:%s", userID) // 写共享字段
    return ul.limiter.Allow(ctx, cfg)
}
```

### P0-9 [§4.6 panic recovery] Kafka consumer processWithRetry 缺 panic recovery
- **位置**：`common/kafka/consumer.go:112`（注释）与 `common/kafka/retry.go:42-79`（实现）
- **现状**：`consumer.go:112` 注释明确写 "processWithRetry 内部对 handler 做 panic recovery"，但 `retry.go` 的 `processWithRetry` 函数体没有任何 `defer recover()`。handler panic 会冒泡到 `Consumer.Start` 的 defer recover，导致整个消费循环退出，消费者永久停止消费。
- **应该**：在 `processWithRetry` 内部加 `defer func(){ if r := recover(); r != nil { lastErr = fmt.Errorf("handler panic: %v\n%s", r, debug.Stack()) } }()`，并修正注释。
- **证据**：
```go
// consumer.go:112
// processWithRetry 内部对 handler 做 panic recovery
if err := processWithRetry(ctx, c.handler, msg, c.cfg); err != nil {

// retry.go:42-79 processWithRetry 函数体无 defer recover()
```

### P0-10 [§4.5 fail-closed] Gateway 限流器 Redis 出错时 fail-open 放行
- **位置**：`gateway/middleware/ratelimit.go:97-108` + `common/limiter/limiter.go:33-44`
- **现状**：`slidingWindowAllow` 在 Redis 调用出错时 `return true, nil`，即限流器自身故障时放行所有请求。`common/limiter/limiter.go:40` 也存在同样的 fail-open。game 层的 `GrabService` 等依赖 `UserLimiter`，Redis 故障时限流失效，所有抢红包请求都会打到后端。
- **应该**：Redis 不可用时返回 `(false, error)`，由调用方决定是否放行（通常应拒绝以保护后端）。
- **证据**：
```go
if err != nil {
    logger.Error("rate limiter error", "key", key, "error", err)
    return true, nil  // fail-open
}
```

---

## 2. P1 级问题（架构 / 可维护性 / 可观测性）

### 资金 & 幂等

#### P1-1 [TX-7/§18.19] DeductPenaltyToPlatform 失败不递增 retry_count 也不创建 Exception
- **位置**：`settlement/service/settlement_service.go:274, 283, 310`
- **现状**：三处 `UpdateBillStatus(..., BillStatusProcessing, BillStatusFailed, ...)` 调用均不调用 `IncrementRetryCountWithNextRetryTime`，也不调用 `CreateDebitFailedException`（对比 `deduct_service.go:264,292` 会创建异常）。失败的 penalty bill 既无重试也无人工升级。
- **应该**：至少调用 `creditRetrySvc.CreateDebitFailedException(ctx, bill)` 升级人工处理。

#### P1-2 [TX-8/§18.20] DistributePenaltyFromPlatform 幂等检查仅覆盖 Success
- **位置**：`settlement/service/settlement_service.go:347-350`
- **现状**：`existingBill.Status == dto.BillStatusSuccess` 仅检查终态。
- **应该**：改为 `existingBill.Status != dto.BillStatusFailed`（与 `DeductPenaltyToPlatform` line 230 一致），覆盖所有非终态。

### 错误处理 & 可观测性

#### P1-3 [TX-11/§4.3] bill_manager.go 大量 Get* 方法裸返回 err 不包装
- **位置**：`settlement/service/bill_manager.go:75,84,93,100,110,120,171,179,189,197,242,276,288,499,514,545,555,575,605,621,677,687,721,734`
- **现状**：所有 Get/Exists/Aggregate 方法直接 `return nil, err`，不包装上下文。
- **应该**：`return nil, fmt.Errorf("get bill by trace id failed: %w", err)` 等。

#### P1-4 [TX-11/§4.3] exception_manager.go 全部方法裸返回 err 不包装
- **位置**：`settlement/service/exception_manager.go:20,27,38,51`

#### P1-5 [TX-11/§4.3] platform_call_manager.go 裸返回 err 不包装
- **位置**：`settlement/service/platform_call_manager.go:38,67,76,87`

#### P1-6 [TX-12/§4.4] 多处 UpdateBillStatus 返回值被丢弃（错误吞没）
- **位置**：`settlement/service/deduct_service.go:253,263,291`；`game_settle_service.go:412,421,448`；`settlement_service.go:274,283,310`；`refund_service.go:182,209`；`credit_retry_service.go:128,155`
- **应该**：检查返回值并记录 `logger.Error` 或 `Warn`。

#### P1-7 [§4.4] platform_call_manager.go json.Marshal 错误被吞没
- **位置**：`settlement/service/platform_call_manager.go:27,61`
- **现状**：`reqBody, _ := json.Marshal(params.ReqBody)` 丢弃 error。

#### P1-8 [§9.5/TX-6] DefaultCreditRetryConfig 未引用 dto.CreditRetryBaseDelay 常量
- **位置**：`settlement/service/credit_retry_service.go:30-31`
- **现状**：硬编码 `BaseDelay: 5 * time.Second`、`MaxDelay: 5 * time.Minute`，未引用 `dto.CreditRetryBaseDelay`/`dto.CreditRetryMaxDelay`（`dto/constants.go:27,28`）。而 `game_settle_service.go:450-451` 引用了 dto 常量，两处默认值不一致。
- **应该**：引用 dto 常量统一默认值。

### 命名 & 规范

#### P1-9 [§3.3] user_id_convert_service.go 接口名 UserService 过于泛化 + GetUserById 应 GetUserByID
- **位置**：`settlement/service/user_id_convert_service.go:12-14`
- **应该**：接口改名为 `UserIDConverter` 或 `UserFetcher`；方法名改为 `GetUserByID`。

#### P1-10 [§2.5] reward_settler.go 使用 case 1 / case 2 裸数字
- **位置**：`settlement/service/reward_settler.go:47,49`
- **应该**：在 `dto/constants.go` 补充 `RewardTypeStraight=1` / `RewardTypeLeopard=2` 常量。

### 日志规范

#### P1-11 [§5.3] credit_retry_scheduler 和 game_settle_retry_scheduler 重试失败用 Error 应改 Warn
- **位置**：`settlement/scheduler/credit_retry_scheduler.go:55`；`settlement/scheduler/game_settle_retry_scheduler.go:56,63`
- **现状**：调度器单次重试失败用 `logger.Error`，与 `settlement_check_service.go:85` 用 `Warn` 不一致。
- **应该**：改为 `logger.Warn`（调度器重试失败属于"可恢复失败"）。

### 随机数（game 模块）

#### P1-12 [§6.4] robot_player / robot_behavior / robot_scheduler_service 使用 math/rand
- **位置**：
  - `game/application/robot_player.go:6, 260, 269`（pickRandomEmptySeat、randomDelay）
  - `game/application/robot_behavior.go:6, 321, 327`（randomDelay、shouldSkipGrab 用 rand.Float64）
  - `game/application/robot_scheduler_service.go:5, 531`（randomDelay）
- **应该**：封装统一随机源（建议在 `common/utils` 提供 `crypto/rand` 封装），机器人行为虽不直接涉及金额，但影响游戏公平性。

### 重复代码 & DI

#### P1-13 [§15.8] ClearAllTimeouts 与 ClearAllRoomTimeouts 字节级重复
- **位置**：`game/scheduler/timeout_scheduler.go:190-200`
- **应该**：删除 `ClearAllTimeouts`，保留 `ClearAllRoomTimeouts`，清理调用方。

#### P1-14 [§12.2] NewContainer 参数过多（30 个）
- **位置**：`game/bootstrap/container.go:95-125`
- **应该**：使用 options struct 模式（如 `ContainerOptions`）封装参数，按功能分组。

#### P1-15 [§6.1] config_listener 直接 go func() 未经 AsyncTaskRunner
- **位置**：`game/bootstrap/config_listener.go:27`
- **现状**：用 `go func() { ... }()` 启动 nacos 配置监听 goroutine，虽有 panic recover，但未经 AsyncTaskRunner 管理。
- **应该**：通过 `AsyncTaskRunner.Submit` 管理。

#### P1-16 [§3.2] Repository 命名不一致（三种并存）
- **位置**：
  - `game/infrastructure/persistence/mysql/robot_account_repo.go:12`（`RobotAccountRepository`）
  - `game/infrastructure/persistence/mysql/user_repository.go:12`（`GormUserRepository`）
  - 其余 5 个为 `gorm<Domain>Repository`（合规）
- **应该**：统一为 `gormRobotAccountRepository` 和 `gormUserRepository`。

#### P1-17 [§4.3] game/application/ 各 service 裸 return err 不包装
- **位置**：`game/application/game_app_service.go:755, 942, 1200`、`robot_account_service.go:89,98,103,115,120,125,130,175,184,187,208,217,222,227,230`、`robot_player.go:77,82,111,146,155,168,184`、`room_app_service.go:124`、`user_service.go:122`
- **现状**：整个 application 包仅 1 处使用 `fmt.Errorf("xxx: %w", err)`，其余 27 处均为裸返回。
- **应该**：包装为 `fmt.Errorf("xxx failed: %w", err)`。

#### P1-18 [§10.8] gRPC recoveryUnaryInterceptor 返回 gRPC status error 而非 ForwardResponse
- **位置**：`game/server/generic_service.go:797-809`
- **现状**：panic 时返回 `status.Errorf(codes.Internal, "internal server error")`，但本服务协议是 `ForwardResponse`（含 `Code`/`Msg`/`Data`），网关解析的是 `ForwardResponse.Code`。
- **应该**：构造包含 `CodeSystemError` 的 `ForwardResponse` 返回。

### 锁 & 并发（common/gateway）

#### P1-19 [DL-6] common/lock Release 未用 Lua 脚本原子释放
- **位置**：`common/lock/distributed_lock.go:91-104`
- **现状**：`Release` 调用 `l.mutex.UnlockContext(ctx)`，依赖 redsync 内部释放逻辑。但 `common/lock/scripts/release_lock.lua.go` 已定义 `ReleaseLockScript`（token 校验后才 DEL），却未被 `common/lock` 包自身使用（仅 game 模块使用）。
- **应该**：`Release` 改用 `ReleaseLockScript.Run(ctx, redis, []string{l.key}, token).Result()`。

#### P1-20 [DL-5] common/lock 锁值未显式用 UUID token
- **位置**：`common/lock/distributed_lock.go:65-70`
- **现状**：用 `redsyncClient.NewMutex(...)` 创建 mutex，锁值由 redsync 内部生成（基于时间戳+随机数），未显式用 `uuid.NewString()` 作为持有者 token。
- **应该**：自定义锁值为 `uuid.NewString()`，配合 P1-19 的 Lua 脚本释放。

#### P1-21 [§4.3] common/broadcast 13 处裸 return err 未包装
- **位置**：`common/broadcast/kafka_broadcaster.go:30,37,42,57,63,68`；`redis_pubsub_broadcaster.go:30,37,42,57,63,68`；`consumer_factory.go:59`
- **应该**：每处包装上下文（函数名/步骤/roomID/userID）。

### HTTP 接口（gateway）

#### P1-22 [§10.3] Gateway ratelimit 用裸数字 429
- **位置**：`gateway/middleware/ratelimit.go:124, 156, 188`
- **应该**：`http.StatusTooManyRequests`。

#### P1-23 [§10.1] Gateway ratelimit 响应信封与统一格式不一致
- **位置**：`gateway/middleware/ratelimit.go:124-128, 156-160, 188-192`
- **现状**：用 `gin.H{"success":false,"code":429,"msg":...}`，多了 `success` 字段，缺 `data` 字段。
- **应该**：统一为 `gin.H{"code": code, "msg": msg, "data": nil}`。

#### P1-24 [§10.7] Gateway health HealthStatus.Uptime 字段从未赋值
- **位置**：`gateway/health/health.go:35`（字段定义）与 `health.go:38-60`（CheckHealth 未赋值）
- **应该**：在 `HealthChecker` 中记录启动时间 `startTime time.Time`，在 `CheckHealth` 中计算 `status.Uptime = int64(time.Since(h.startTime).Seconds())`。

#### P1-25 [§10.1] Gateway health 响应非统一信封
- **位置**：`gateway/health/health.go:59, 101-104, 108-110, 114-116`
- **现状**：`CheckHealth` 直接返回 `HealthStatus` struct，`CheckReady`/`CheckLive` 用 `gin.H{"status":...}`，均非统一 `{"code","msg","data"}` 信封。
- **应该**：统一用 `gin.H{"code":0,"msg":"success","data": status}` 包装。

#### P1-26 [§11.1] Gateway config 重复声明 RedisConfig 和 LogConfig
- **位置**：`gateway/config/config.go:41-46`（RedisConfig）和 `config.go:61-68`（LogConfig）
- **现状**：重复定义了 `RedisConfig{Addr,Password,DB,PoolSize}` 和 `LogConfig{Level,Filename,MaxSize,...}`，而 `common/config` 已有这两个类型。`gateway/bootstrap/app.go:324-329` 还需要手动字段拷贝转换。
- **应该**：gateway/config 直接嵌入 `commonconfig.RedisConfig` 和 `commonconfig.LogConfig`。

#### P1-27 [§15.8] Gateway service saveUserAndGetInternalID 重复实现
- **位置**：`gateway/service/game.go:126-139` 与 `gateway/service/test.go:67-80`
- **现状**：两个文件各自实现 `saveUserAndGetInternalID`，逻辑几乎字节级相同（仅 deviceID 参数不同）。
- **应该**：抽到公共 helper 函数，通过参数差异化 deviceID。

#### P1-28 [§6.2] Gateway 多个长生命周期组件用 context.Background() 派生
- **位置**：`gateway/broadcast/broadcast.go:48`；`gateway/connection/manager.go:59`；`gateway/middleware/auth.go:39`
- **现状**：三个长生命周期组件都在构造函数中 `context.WithCancel(context.Background())`，导致它们不受 appCtx 控制。当 appCtx 取消时，这些组件不会收到取消信号，graceful shutdown 失败。
- **应该**：构造函数接收 `appCtx context.Context` 参数，用 `context.WithCancel(appCtx)` 派生。

#### P1-29 [§19 L-1] common/limiter FixedWindow 用 Incr+Expire 两步式，存在 race condition
- **位置**：`common/limiter/limiter.go:54-68`
- **现状**：`FixedWindow` 先 `Incr` 再 `Expire`，两步非原子。若 Incr 后客户端崩溃或网络中断，key 永不过期，导致该 key 永远被计数到 limit 后拒绝所有请求。SlidingWindow 已收敛到 `SlidingWindowScript`，但 FixedWindow 仍是两步式。
- **应该**：用 Lua 脚本封装 INCR + EXPIRE 原子操作（仅在 count==1 时 EXPIRE），通过 `cRedis.NewScript` 注册。

#### P1-30 [§16.5 SC-5] common/limiter FixedWindow 用 fmt.Sprintf 拼 Redis key
- **位置**：`common/limiter/limiter.go:55`（FixedWindow）、`line 34`（Allow）
- **应该**：在 `common/rediskeys/keys.go` 添加 `RateLimitFixedWindowKey(prefix, key string, windowSec int64) string` 工厂函数。

#### P1-31 [§16.3 SC-3] common/config/server.go 与 gateway/server/server.go 用 fmt.Sprintf 拼 host:port
- **位置**：`common/config/server.go:14`、`gateway/server/server.go:151`、`stats/bootstrap/app.go:75`
- **现状**：`fmt.Sprintf(":%d", port)` 拼 host:port，违反 SC-3。
- **应该**：`strutil.JoinHostPort("", port)`。

#### P1-32 [§15.8] gateway router generateRequestID 与 message.generateRequestID 重复且不一致
- **位置**：`gateway/router/router.go:236-238` 与 `common/message/request.go:50-52`
- **现状**：两处都实现了 `generateRequestID`，但截断长度不同：router.go 截 12 字符，message/request.go 截 16 字符。同一项目内 RequestID 格式不一致。
- **应该**：抽到 `common/message` 包统一一个 `GenerateRequestID()` 函数。

### HTTP 接口（stats）

#### P1-33 [§10.1] stats/handler 响应信封三套并存
- **位置**：`stats/handler/stats_handler.go:43-48, 63-66` + `stats/bootstrap/app.go:67-69`
- **现状**：错误用 `gin.H{"code": code*100, "message": msg}`；成功用 `gin.H{"code": 0, "data": ...}`（缺 `msg` 字段）；/health 用 `gin.H{"status": "ok"}`。三套形状并存。
- **应该**：统一为 `{"code":0,"msg":"","data":<object|null>}`，业务 code 不应是 HTTP 状态码乘 100。

#### P1-34 [§10.2] stats/handler 缺 respondOK 助手，respondError 签名错误
- **位置**：`stats/handler/stats_handler.go:43-48`
- **现状**：只有 `respondError(c, code, msg)` 三参数（`code` 既是 HTTP 状态码又当业务码用），无 `respondOK`。
- **应该**：按规约提供 `respondOK(c, data)` 与 `respondError(c, httpStatus, code, msg)` 双助手。

#### P1-35 [§10.5] stats/handler 用 strconv.Atoi(c.DefaultQuery(...)) 手动解析分页
- **位置**：`stats/handler/stats_handler.go:133-140`
- **现状**：`limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))` 忽略 error。
- **应该**：使用 `c.ShouldBindQuery` + `dto.PaginationReq`（dto 包已定义该结构体，见 `stats/dto/stats_dto.go:60-63`，但 handler 未使用）。

#### P1-36 [§10.7] stats 仅 /health 内联端点，缺 /ready 与 /live
- **位置**：`stats/bootstrap/app.go:67-69`
- **现状**：只有 `/health` 一个端点，且内联在 bootstrap/app.go 中，返回非统一信封 `{"status":"ok"}`。
- **应该**：抽到独立 `stats/health/` 包，暴露三端点，返回统一信封。

#### P1-37 [§10.8] stats/handler 把 Go err.Error() 字符串直接透传给前端
- **位置**：`stats/handler/stats_handler.go:59, 78, 97, 116, 144, 164`
- **现状**：6 处 `respondError(c, http.StatusInternalServerError, err.Error())`，把内部错误消息（可能含 SQL/DSN 等敏感信息）直接返回给客户端。
- **应该**：用业务 code + 通用消息，详细错误只入日志。

#### P1-38 [§10.3] gamingpanda_client.go 用裸数字 500 比较 HTTP 状态码
- **位置**：`api/platform/gamingpanda_client.go:244`
- **应该**：用 `http.StatusInternalServerError`。

### scripts & 日志

#### P1-39 [§5.1/§12.4] scripts 全部用 log.Fatalf/log.Printf，未走 logger.Fatal
- **位置**：`scripts/check_tables.go:15, 23`、`scripts/clear_data.go:25, 30, 36`、`scripts/init_robot_accounts.go:31, 41, 51, 62, 66, 95, 105`、`scripts/init_rooms.go:18, 22, 55, 59, 69, 89, 94, 99`、`scripts/test_websocket.go:41, 51, 58, 65, 72, 79, 86`
- **应该**：统一 `logger.Fatal`/`logger.Info`。

#### P1-40 [§16.1 SC-1] scripts/test_game_apis.go 用 fmt.Sprintf 反引号模板拼 JSON
- **位置**：`scripts/test_game_apis.go:90, 128, 136, 144`、`scripts/test_websocket.go:48, 62, 76`
- **应该**：用 `json.Marshal`。

#### P1-41 [§16.2 SC-2] scripts/test_game_apis.go 用 fmt.Sprintf 手拼 URL+query
- **位置**：`scripts/test_game_apis.go:69-70, 94-95`
- **应该**：用 `strutil.BuildURLWithQuery` 或 `url.Values.Encode()`。

#### P1-42 [§14.1 gofmt] common/message/push.go, request.go, response.go 4 空格缩进
- **位置**：`common/message/push.go`、`common/message/request.go`、`common/message/response.go` 整文件
- **现状**：全文用 4 空格缩进，非 tab。
- **应该**：运行 `gofmt -w` 修复。

---

## 3. P2 级问题（命名 / 注释 / 测试 / 防御性编程）

### settlement 模块

#### P2-1 [TX-2] applyForRefundLocked 未复用 BillManager 已有的乐观锁方法
- **位置**：`settlement/service/refund_service.go:112-127`
- **现状**：内联 `tx.Create(refundAudit)` + `tx.Model(BillRecord).Updates(...)`，而 `BillManager.CreateRefundAuditAndUpdateBillRefundStatusInTransaction`（`bill_manager.go:394-415`）已实现同样逻辑且带乐观锁。
- **应该**：调用 `s.billMgr.CreateRefundAuditAndUpdateBillRefundStatusInTransaction`。

#### P2-2 [§4.4] virtual_balance_service.go GetBalance 解析错误被吞没
- **位置**：`settlement/service/virtual_balance_service.go:67`
- **现状**：`fmt.Sscanf(val, "%d", &balance)` 返回值未检查，解析失败时 balance 静默为 0。

#### P2-3 [§4.2] refund_service.go 字符串错误未用哨兵
- **位置**：`settlement/service/refund_service.go:79, 83, 139, 251`
- **现状**：`fmt.Errorf("bill status is not success, cannot refund")` 等字符串错误，无法 `errors.Is` 判别。
- **应该**：定义 `var ErrBillNotRefundable = errors.New(...)` 等哨兵。

#### P2-4 [§4.5] redisRobotChecker.IsRobot fail-open
- **位置**：`settlement/service/robot_checker.go:34-37`
- **现状**：Redis 不可用时 `return false`，将机器人误判为真人。虽不会丢钱，但会增加平台调用。
- **应该**：fail-closed 或加显式注释说明选择。

#### P2-5 [§3.4] Exists 方法命名不一致
- **位置**：`settlement/service/bill_manager.go:88, 115`
- **现状**：`ExistsByRoundAndType`（带 By）与 `ExistsRoundSettlement`（不带 By）并存。
- **应该**：统一为 `ExistsByRoundSettlement`。

#### P2-6 [§5.3] 调度器 execute 内 DB 查询失败日志级别应统一 Warn
- **位置**：`settlement/scheduler/credit_retry_scheduler.go:45`、`game_settle_retry_scheduler.go:48`、`game_settle_timeout_scheduler.go:48`、`refund_process_scheduler.go:47`、`settlement_check_scheduler.go:44,48`
- **现状**：DB 查询失败用 `logger.Error`。
- **应该**：调度器单次 tick 的查询失败属于"可恢复失败"，统一用 `Warn`。

#### P2-7 [§11.1] settlement/config/config.go 缺少聚合 Config 结构体
- **位置**：`settlement/config/config.go`
- **现状**：只有 `PlatformConfig` 和 `LockConfig` 类型别名，无聚合 `Config` struct。
- **应该**：若 settlement 作为独立模块，应补充 `Config` struct 聚合所需子配置。

### game 模块

#### P2-8 [§4.3] 应用层 message.NewError 丢弃底层错误链
- **位置**：`game/application/penalty_service.go:66, 136`、`room_app_service.go:85, 92, 111`、`seat_app_service.go:219, 233, 251, 255`、`history_service.go:45, 67, 77, 87, 93, 100, 107, 191`、`game_app_service.go:157, 175, 198, 202, 208, 213, 220, 280, 380, 384, 389, 1622, 1651, 1660, 1701, 1725, 1766, 1787`
- **现状**：先 `logger.Error(...)` 记录错误，然后 `return nil, message.NewError(message.CodeSystemError)` 丢弃底层错误对象。调用方无法通过 `errors.Is`/`errors.As` 判断根因。
- **应该**：在 `GameError` 中增加 `Cause error` 字段，或使用 `fmt.Errorf("xxx: %w", err)` 包装后再判断。

#### P2-9 [§8.3 / TX-1] MySQL Repository 更新方法未检查 RowsAffected
- **位置**：`game/infrastructure/persistence/mysql/round_repository.go:43-76`
- **现状**：`UpdateRoundStatus`、`UpdateRoundDeductInfo`、`UpdateRoundFailed`、`UpdateRoundSender`、`UpdateRoundAmount` 均直接返回 `.Error`，未检查 `RowsAffected`。
- **应该**：对状态更新类操作检查 `RowsAffected==0` 并按业务语义返回幂等 nil 或特定错误。

#### P2-10 [文档一致性] CODING_STANDARD.md §7.3 引用 robot_lock.lua.go 但文件不存在
- **位置**：`CODING_STANDARD.md:351, 1194, 1365`
- **现状**：CODING_STANDARD.md 多处引用 `game/infrastructure/persistence/redis/scripts/robot_lock.lua.go`，但该文件已被删除（迁移至 `common/lock/scripts/release_lock.lua.go`）。
- **应该**：更新 CODING_STANDARD.md 中的引用。

### common 模块

#### P2-11 [§13.1] common/message/errors.go 全文使用西班牙语注释与消息
- **位置**：`common/message/errors.go:5, 15, 41, 53, 93, 103, 111, 123, 130, 141, 143-218, 222-249`
- **现状**：分节注释（如 "Códigos de error generales"）和 codeMessages map 的 value（如 "Éxito"、"Error de parámetro"）全部是西班牙语。
- **应该**：统一改为英文。

#### P2-12 [§13.1] common/message/types.go 中文分节注释
- **位置**：`common/message/types.go:3, 27, 60, 67, 73, 84`
- **现状**：分节注释用中文（"命令类型"、"推送类型"、"房间状态常量"等），与 errors.go 的西班牙语不一致。
- **应该**：统一为英文。

#### P2-13 [§15.10] common/message/broadcast.go Marshal() 与 ToJSON() 命名并存
- **位置**：`common/message/broadcast.go:68`（Marshal）与 `push.go:22`、`request.go:42`、`response.go:40`（ToJSON）
- **应该**：统一为 `ToJSON() ([]byte, error)`。

#### P2-14 [§20 SID-T2] common/idgen/snowflake_test.go 缺时钟回拨场景测试
- **位置**：`common/idgen/snowflake_test.go`（全文）
- **现状**：测试覆盖了 nodeID 边界、并发唯一性、序列号溢出、时间戳单调递增、JSON Marshal，但未覆盖时钟回拨场景。`snowflake.go:93-103` 实现了时钟回拨检测（≤5ms 等待，>5ms 返回 `ErrClockMovedBackwards`），但无测试验证。
- **应该**：新增 `TestSnowflakeGenerator_ClockBackward` 子测试。

### gateway 模块

#### P2-15 [§10.4] gateway/server/server.go setupRoutes 在 server 内命令式注册
- **位置**：`gateway/server/server.go:123, 127-148`
- **现状**：`NewServer` 在构造函数末尾调用 `s.setupRoutes()`，路由注册逻辑耦合在 server 内。
- **应该**：抽出 `RegisterRoutes(engine *gin.Engine)` 公开方法。

#### P2-16 [§7.5] gateway/middleware/auth.go redis.Set 未检查返回值
- **位置**：`gateway/middleware/auth.go:143`
- **现状**：`m.redis.Set(ctx, key, "1", m.lockDuration)` 未检查 `.Err()`。若 Redis Set 失败，IP 锁定记录丢失，后续该 IP 仍可继续尝试。

#### P2-17 [§7.5] gateway/connection/manager.go 多处 Redis 调用未检查返回值
- **位置**：`gateway/connection/manager.go:200`（Publish）、`319`（Del）、`336`（Expire）、`327`（Get 的 `_` 丢弃 error）
- **现状**：例如 `publishKickNotification` 的 Publish 失败，踢人消息丢失，旧连接不会被踢。

#### P2-18 [§6 防御] gateway/connection/manager.go 类型断言无 ok 检查
- **位置**：`gateway/connection/manager.go:136-138`
- **现状**：`result[0].(int64)` 等类型断言无 ok 检查，若 Lua 脚本返回值类型与预期不符会 panic。

#### P2-19 [§10.8] gateway/handler/game.go 错误信息字符串拼接可能泄露内部错误
- **位置**：`gateway/handler/game.go:75, 115, 122`
- **现状**：`"Invalid request parameters: " + err.Error()` 直接将内部错误信息暴露给客户端。
- **应该**：对客户端返回通用错误信息，内部错误仅记日志。

#### P2-20 [§4.5] common/limiter/limiter.go Allow 方法 fail-open
- **位置**：`common/limiter/limiter.go:33-44`
- **现状**：与 P0-10 同源，`RateLimiter.Allow` 在 Redis 出错时 `return true, nil` 放行。
- **应该**：返回 `(false, error)`，调用方决定降级策略。

### stats 模块

#### P2-21 [§2.3] stats/repository 直接定义具体 struct 被 service 直接依赖
- **位置**：`stats/repository/stats_repository.go:14` + `stats/service/stats_service.go:21`
- **现状**：`StatsRepository` 是具体 struct，`StatsService.repo` 字段类型为 `*repository.StatsRepository`（具体类型），无 domain 接口。
- **应该**：在 `stats/domain/` 定义 `StatsRepository` 接口。

#### P2-22 [§3.1] stats/handler/stats_handler.go 文件名冗余前缀
- **位置**：`stats/handler/stats_handler.go`（整个文件名）
- **应该**：改名为 `handler.go`。

#### P2-23 [§3.1] stats/repository/stats_repository.go 文件名冗余前缀
- **位置**：`stats/repository/stats_repository.go`（整个文件名）
- **应该**：改名为 `repository.go`。

#### P2-24 [§4.3] stats/repository 与 stats/service 全部裸 return nil, err
- **位置**：`stats/repository/stats_repository.go:71, 99, 129, 180, 205, 256` + `stats/service/stats_service.go:72, 88, 108, 128, 148, 168`
- **应该**：包装为 `fmt.Errorf("get dashboard stats failed: %w", err)` 等。

#### P2-25 [§16.5 SC-5] stats/service 用裸字符串拼 Redis key
- **位置**：`stats/service/stats_service.go:17, 31, 120` + `stats/service/format.go:11`
- **现状**：`cachePrefix = "stats"` 常量定义在 service 包内，`cacheKey()` 用 `fmt.Sprintf("%s:%s:%s", ...)` 拼 key。
- **应该**：委托 `common/rediskeys`。

#### P2-26 [§15.8] api/platform/mock_client.go 重复定义 CommonResponse.Data 匿名 struct 4 次
- **位置**：`api/platform/mock_client.go:80-97, 131-148, 182-198, 228-244`
- **应该**：在 `types.go` 把 `CommonResponse.Data` 抽成命名 struct。

#### P2-27 [§4.4] scripts 多处错误吞没
- **位置**：`scripts/check_tables.go:18`、`init_robot_accounts.go:43`、`init_rooms.go:63, 102, 136, 145`、`force_end_room.go:118`、`test_websocket.go:23`

#### P2-28 [§8.4] migrations 两个 SQL 文件未覆盖 idx_round_trace_bill_user 复合唯一索引
- **位置**：`migrations/20260625_add_player_history_indexes.sql`、`migrations/20260626_add_bill_history_indexes.sql`
- **现状**：两个文件只新增普通 INDEX，未包含 `(round_trace_id, bill_type, user_id)` 复合唯一索引。
- **应该**：补充复合唯一索引迁移文件 `settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index.sql`。

---

## 4. 已合规项（无需修改）

以下检查项经逐文件核对已确认符合规约：

- **TX-1 乐观锁**：bill_manager.go 中所有状态机 UPDATE 方法均带 `WHERE status = ?` 条件，且 `RowsAffected==0` 时返回 nil。
- **TX-3 Processing 中间态**：executeSingleDeduct、creditSessionPayout、settlePlayer 三处均遵守"先置 Processing → 调 RPC → 成功置终态"流程。
- **TX-5 跨服务调用**：SettleRound 在 Redis 锁内执行 DB 写入，无跨服务调用；SettleGame 的 platform.Settle 调用在锁外。
- **TX-6 指数退避**：`credit_retry_service.go:191-198` 和 `game_settle_service.go:450-453` 均用 `delay = base * 2^retryCount` 封顶 maxDelay。
- **TX-10 ExceptionNo 确定性**：`trace_id_generator.go:68-70` 使用 `EXC_{billID}_{exceptionType}` 确定性生成。
- **三层幂等**：deduct_service、game_settle_service 均有"锁外预检 + 锁内双检 + DB 唯一索引"。
- **CreateBillsInTransaction vs CreateBillsOnly**：`CreateBillsOnly` 不存在，已收敛。
- **§6.3 atomic.Pointer 热配置**：`algorithm/packet_generator.go:19-26` 已用 `config atomic.Pointer[Config]` + `straightGenerator atomic.Pointer[StraightGenerator]`。
- **§17 调度器规约**：5 个 settlement 调度器 + 3 个 game 调度器均实现 `Scheduler` 接口、注册到 `SchedulerRegistry`、`InitialDelay` 用 select、`WithRedisLock` 返回值检查、`StopAll` 并行+30s 预算。`settlement/scheduler/base.go` 已删除。
- **§19 Lua 脚本规约**：5 个 game 业务脚本 + 1 个 settlement 通用脚本 + 1 个 common lock 脚本均通过 `cRedis.NewScript` 注册，TTL 通过 ARGV 传入，无 `math.random`/`KEYS` 命令，业务脚本返回 `{code, ...}` 并用 `parseLuaCode` + `MapLuaError` 映射。
- **§7.1 Redis key 集中**：`game/infrastructure/persistence/redis/keys.go`、`settlement/infrastructure/persistence/redis/keys.go`、`gateway/keys.go` 全部为 `rediskeys.KeyXxx` re-export。
- **§7.4 tryAcquire 三返回值**：`game_event_consumer.go:505-519`、`room_event_consumer.go:165-179` 均为 `(bool, string, error)`，使用 uuid token + Lua 释放，fail-closed。
- **§8.5 Repository 聚合 eager 初始化**：`db_repository.go` 已 eagerly 初始化所有子 repo，字段构造后只读。
- **§5.4 广播失败 Warn 日志**：`game/infrastructure/broadcast/broadcaster.go:30-34, 43-47` 已 Warn 级别带 room_id/event。
- **§12.3/§12.4 Application 生命周期**：`bootstrap/app.go:349-352` 有 `Wait() error`；用 `logger.Fatal` 而非 panic。
- **§10.8 gRPC 拦截器存在**：`generic_service.go:700-703` 已注册 `recoveryUnaryInterceptor` 和 `loggingUnaryInterceptor`（但 recovery 返回类型有问题，见 P1-18）。
- **应用层无直接持有 *gorm.DB 开事务**：application 包无 `gorm.DB`/`tx.Begin`/`db.Begin` 调用，事务均通过 Repository 层封装。
- **§2.1 cmd 入口**：三个 cmd 入口（game/gateway/stats）均只有一行 `bootstrap.Run()`。
- **§11.4 stats 默认端口**：已是 8082。
- **§16.9 UUID 使用**：`common/message/request.go:7` 用 `github.com/google/uuid`；`common/utils/utils.go GenerateUUID` 已删除。
- **§18.11 LockConfig**：`common/config/types.go:360-439` 已定义 `LockConfig` + `SetLockDefaults`。
- **§20 雪花 ID**：`common/idgen/snowflake.go` Epoch 已设为 `1735689600000`；`node_allocator.go` 用 Redis INCR + SET NX + 5 分钟心跳 + Lua 安全释放。

---

## 5. 重构方案（分阶段）

### 阶段一：P0 紧急修复（资金/安全/正确性，1-2 天）

**目标**：消除资金安全风险、安全漏洞、限流/消费者失效等阻断性问题。

#### 5.1.1 修复 refund_service 资金正确性（P0-1, P0-2, P0-4）

**文件**：`settlement/service/refund_service.go`、`settlement/service/deduct_service.go`

**步骤**：
1. 删除 `applyForRefundLocked` 中的内联事务，改为调用 `s.billMgr.CreateRefundAuditAndUpdateBillRefundStatusInTransaction(ctx, refundAudit, bill.ID, dto.RefundStatusSuccess)`。该方法已在 `bill_manager.go:394-415` 实现，带 `WHERE id = ? AND refund_status = ?` 乐观锁。
2. `executeRefund` 失败路径（line 182, 209）：将 `UpdateRefundAuditError(ctx, refund.ID, dto.RefundStatusProcessing, err.Error())` 改为 `UpdateRefundAuditError(ctx, refund.ID, dto.RefundStatusFailed, err.Error())`，确保 RefundProcessScheduler 能查到 Failed 状态并重试。
3. `RejectRefund`（line 254-256）和 `handleFirstRoundDeductFailure`（`deduct_service.go:363-368`）：将内联事务下沉到 `BillManager` 新增方法 `RejectRefundInTransaction(ctx, refundID, billID)`。

**验证**：
- 单元测试覆盖并发退款场景（两个 goroutine 同时申请退款，验证只有一个成功）。
- 集成测试覆盖 executeRefund 失败后 RefundProcessScheduler 能重试。

#### 5.1.2 修复 BillRecord 复合唯一索引（P0-3, P2-28）

**文件**：`settlement/model/bill.go`、新增 `settlement/infrastructure/persistence/mysql/migrations/add_bill_record_compound_unique_index.sql`

**步骤**：
1. 修改 `bill.go:7,10,16` 三字段 GORM 标签：
```go
RoundTraceID string `gorm:"size:64;uniqueIndex:idx_round_trace_bill_user,priority:1" json:"round_trace_id"`
BillType     int    `gorm:"not null;uniqueIndex:idx_round_trace_bill_user,priority:2" json:"bill_type"`
UserID       int64  `gorm:"not null;uniqueIndex:idx_round_trace_bill_user,priority:3" json:"user_id"`
```
2. 新增迁移文件 `add_bill_record_compound_unique_index.sql`：
```sql
ALTER TABLE bill_record
  DROP INDEX idx_bill_record_round_type_user,
  ADD UNIQUE INDEX idx_round_trace_bill_user (round_trace_id, bill_type, user_id);
```

**验证**：
- 启动时 GORM AutoMigrate 生成的 schema 与迁移文件一致。
- 单元测试覆盖同一 round_trace_id 不同 bill_type 的多条记录可共存。

#### 5.1.3 替换 math/rand 为 crypto/rand（P0-6, P0-7, P1-12）

**文件**：`game/algorithm/straight.go`、`game/application/grab_service.go`、`game/application/robot_player.go`、`game/application/robot_behavior.go`、`game/application/robot_scheduler_service.go`

**步骤**：
1. 在 `common/utils` 新增 `crypto/rand` 封装：
```go
// common/utils/rand.go
package utils

import (
    "crypto/rand"
    "math/big"
)

func CryptoRandIntn(n int) (int, error) {
    if n <= 0 { return 0, fmt.Errorf("n must be positive") }
    big, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
    if err != nil { return 0, err }
    return int(big.Int64()), nil
}

func CryptoRandInt63n(n int64) (int64, error) { ... }
func CryptoRandFloat64() (float64, error) { ... }
```
2. 替换所有 `math/rand` 调用为 `utils.CryptoRand*`，并检查 error。
3. `straight.go:48` 用 `crypto/rand` 生成 Perm（参考 `packet_generator.go` 已有实现）。

**验证**：
- 性能压测：crypto/rand 相比 math/rand 在红包生成场景的吞吐下降可接受（< 20%）。
- 单元测试覆盖随机数分布均匀性。

#### 5.1.4 修复 UserLimiter data race（P0-8）

**文件**：`common/limiter/limiter.go`

**步骤**：
1. `AllowGrab/AllowJoin/AllowCreate` 改为值类型传递：
```go
func (ul *UserLimiter) AllowGrab(ctx context.Context, userID string) (bool, error) {
    cfg := *ul.configs["grab"] // 值拷贝
    cfg.Key = fmt.Sprintf("grab:%s", userID)
    return ul.limiter.Allow(ctx, &cfg)
}
```
2. 或更彻底地：`Allow` 方法签名改为接收 `key string` 而非 `*LimitConfig`。

**验证**：`go test -race` 覆盖 100 goroutine 并发调用 AllowGrab。

#### 5.1.5 修复 Kafka consumer panic recovery（P0-9）

**文件**：`common/kafka/retry.go`

**步骤**：
1. 在 `processWithRetry` 函数体开头加：
```go
func processWithRetry(ctx context.Context, handler MessageHandler, msg Message, cfg ConsumerConfig) (err error) {
    defer func() {
        if r := recover(); r != nil {
            err = fmt.Errorf("handler panic: %v\n%s", r, debug.Stack())
            logger.Error("kafka handler panic", "topic", msg.Topic, "error", err)
        }
    }()
    // ... 原有重试逻辑
}
```
2. 修正 `consumer.go:112` 注释使其与实现一致。

**验证**：单元测试注入会 panic 的 handler，验证 processWithRetry 返回 error 而非 panic。

#### 5.1.6 修复限流器 fail-open（P0-10, P2-20）

**文件**：`common/limiter/limiter.go`、`gateway/middleware/ratelimit.go`

**步骤**：
1. `RateLimiter.Allow` 在 Redis 出错时返回 `(false, error)`。
2. `UserLimiter.AllowGrab` 等方法透传 error。
3. `gateway/middleware/ratelimit.go:97-108` 的 `slidingWindowAllow` 返回 `(false, error)`，调用方根据业务决定：抢红包路径 fail-closed（返回 429），查询路径可 fail-open + Warn。

**验证**：集成测试覆盖 Redis 故障时限流器行为。

#### 5.1.7 移除硬编码凭据（P0-5）

**文件**：`scripts/check_tables.go`、`scripts/init_rooms.go`、`scripts/test_game_apis.go`、`scripts/test_websocket.go`

**步骤**：
1. 所有 DSN、merchant secret、IP 改为从环境变量读取：
```go
dsn := os.Getenv("CASHPARTY_DB_DSN")
if dsn == "" { logger.Fatal("CASHPARTY_DB_DSN not set") }
```
2. 提供 `.env.example` 模板（不入库）。

---

### 阶段二：P1 关键修复（架构/可维护性/可观测性，3-5 天）

#### 5.2.1 修复错误处理与可观测性（P1-3 ~ P1-8, P1-17, P1-21, P2-24）

**统一策略**：
1. 所有 `return err` 改为 `return fmt.Errorf("<动词+对象> failed: %w", err)`。
2. 所有 `if exists, _ :=` 改为显式检查 error。
3. `UpdateBillStatus` 返回值检查：
```go
if err := s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusProcessing, dto.BillStatusFailed, err.Error()); err != nil {
    logger.Error("update bill status to failed failed", "bill_id", bill.ID, "error", err)
}
```
4. `json.Marshal` 错误检查（callLog 序列化失败降级 Warn）。

**影响文件**：
- `settlement/service/bill_manager.go`（24 处）
- `settlement/service/exception_manager.go`（4 处）
- `settlement/service/platform_call_manager.go`（4 处）
- `settlement/service/deduct_service.go`、`game_settle_service.go`、`settlement_service.go`、`refund_service.go`、`credit_retry_service.go`（11 处 UpdateBillStatus）
- `game/application/*.go`（27 处）
- `common/broadcast/*.go`（13 处）
- `stats/repository/*.go`、`stats/service/*.go`（12 处）

#### 5.2.2 修复幂等与重试（P1-1, P1-2, P1-8）

**步骤**：
1. `settlement_service.go:274, 283, 310` 三处 `UpdateBillStatus` 失败路径补充：
```go
if err := s.billMgr.UpdateBillStatus(...); err != nil { ... }
if err := s.creditRetrySvc.CreateDebitFailedException(ctx, bill); err != nil {
    logger.Error("create debit failed exception failed", "bill_id", bill.ID, "error", err)
}
```
2. `DistributePenaltyFromPlatform`（line 347-350）改为 `existingBill.Status != dto.BillStatusFailed`。
3. `DefaultCreditRetryConfig` 引用 dto 常量：
```go
return &CreditRetryConfig{
    MaxRetryCount:   dto.MaxRetryCount,
    BaseDelay:       dto.CreditRetryBaseDelay,
    MaxDelay:        dto.CreditRetryMaxDelay,
    RetryMultiplier: 2.0,
}
```

#### 5.2.3 修复锁规约（P1-19, P1-20）

**文件**：`common/lock/distributed_lock.go`

**步骤**：
1. `Obtain` 时生成 `token := uuid.NewString()`，存储到 `Lock` 结构体。
2. `Release` 改用 `ReleaseLockScript.Run(ctx, redis, []string{l.key}, token).Result()`。
3. 删除对 redsync `UnlockContext` 的依赖（保留 redsync 仅用于获取锁的 retry 逻辑，或完全自持 SetNX + Lua）。

**验证**：单元测试覆盖 TTL 过期后误删场景。

#### 5.2.4 修复 HTTP 接口规范（P1-22 ~ P1-38）

**统一策略**：
1. 创建 `common/http/respond.go`：
```go
package http

func RespondOK(c *gin.Context, data interface{}) {
    c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "", "data": data})
}

func RespondError(c *gin.Context, httpStatus, code int, msg string) {
    c.JSON(httpStatus, gin.H{"code": code, "msg": msg, "data": nil})
}
```
2. 所有 handler 调用统一助手。
3. gateway ratelimit 用 `http.StatusTooManyRequests`。
4. gateway health 补充 Uptime 赋值，统一信封。
5. stats 抽 `health/` 包，补三端点。
6. stats handler 用 `ShouldBindQuery` + `dto.PaginationReq`。

#### 5.2.5 修复 context.Background 滥用（P1-28）

**文件**：`gateway/broadcast/broadcast.go`、`gateway/connection/manager.go`、`gateway/middleware/auth.go`

**步骤**：
1. 构造函数增加 `appCtx context.Context` 参数。
2. `context.WithCancel(appCtx)` 替代 `context.WithCancel(context.Background())`。
3. `bootstrap/app.go` 启动时传入 appCtx。

#### 5.2.6 修复 FixedWindow race condition（P1-29, P1-30）

**文件**：`common/limiter/scripts/`（新增 Lua 脚本）、`common/limiter/limiter.go`、`common/rediskeys/keys.go`

**步骤**：
1. 新增 `common/limiter/scripts/fixed_window.lua.go`：
```lua
-- KEYS[1] = rediskeys.RateLimitFixedWindowKey(prefix, key, windowSec)
-- ARGV[1] = window_seconds
-- ARGV[2] = limit
-- 返回值: 1=允许, 0=拒绝
local count = redis.call('INCR', KEYS[1])
if count == 1 then
    redis.call('EXPIRE', KEYS[1], ARGV[1])
end
if count > tonumber(ARGV[2]) then
    return 0
end
return 1
```
2. 在 `common/rediskeys/keys.go` 新增 `RateLimitFixedWindowKey` 工厂函数。
3. `FixedWindow.Allow` 改用脚本调用。

#### 5.2.7 修复字符串拼接规约（P1-30 ~ P1-32, P1-40, P1-41）

**统一策略**：
1. `common/config/server.go:14`、`gateway/server/server.go:151`、`stats/bootstrap/app.go:75` 改用 `strutil.JoinHostPort("", port)`。
2. `scripts/test_game_apis.go` 的 JSON 拼接改用 `json.Marshal`，URL 拼接改用 `strutil.BuildURLWithQuery`。
3. `gateway/router/router.go:236-238` 的 `generateRequestID` 抽到 `common/message`，统一截断长度（建议 12 字符）。

#### 5.2.8 修复 Repository 命名（P1-16）

**文件**：`game/infrastructure/persistence/mysql/robot_account_repo.go`、`user_repository.go`

**步骤**：
1. `type RobotAccountRepository struct` → `type gormRobotAccountRepository struct`
2. `type GormUserRepository struct` → `type gormUserRepository struct`
3. 构造函数 `NewRobotAccountRepository` / `NewUserRepository` 返回 domain 接口（已如此，只需改 struct 名）。
4. 更新 `db_repository.go` 中的字段类型。

#### 5.2.9 修复 gRPC recovery 返回类型（P1-18）

**文件**：`game/server/generic_service.go`

**步骤**：
1. `recoveryUnaryInterceptor` panic 时构造 `ForwardResponse`：
```go
defer func() {
    if r := recover(); r != nil {
        logger.Error("gRPC handler panic", "error", r, "stack", debug.Stack())
        resp = &commonPb.ForwardResponse{
            Code: int32(message.CodeSystemError),
            Msg:  message.GetErrorMsg(message.CodeSystemError),
        }
        err = nil // 返回 ForwardResponse 而非 gRPC status error
    }
}()
```

#### 5.2.10 修复日志级别与重复代码（P1-11, P1-13, P1-27）

1. `credit_retry_scheduler.go:55`、`game_settle_retry_scheduler.go:56,63` 的 `logger.Error` 改 `logger.Warn`。
2. 删除 `ClearAllTimeouts`，保留 `ClearAllRoomTimeouts`，更新调用方。
3. 抽取 `gateway/service` 的 `saveUserAndGetInternalID` 到 `gateway/service/saver.go` 共享 helper。

#### 5.2.11 修复 config_listener AsyncTaskRunner（P1-15）

**文件**：`game/bootstrap/config_listener.go`

**步骤**：
1. `registerAlgorithmConfigListener` 改为通过 `taskRunner.Submit` 提交：
```go
taskRunner.Submit(func(ctx context.Context) {
    if err := nacosClient.ListenConfig(...); err != nil { ... }
})
```

#### 5.2.12 修复配置重复（P1-26）

**文件**：`gateway/config/config.go`

**步骤**：
1. 删除本地 `RedisConfig` 和 `LogConfig` 定义。
2. `Config` struct 改为嵌入 `commonconfig.RedisConfig` 和 `commonconfig.LogConfig`。
3. `gateway/bootstrap/app.go:324-329` 删除手动字段拷贝。

#### 5.2.13 修复 NewContainer 参数过多（P1-14）

**文件**：`game/bootstrap/container.go`

**步骤**：
1. 定义 `ContainerOptions` struct：
```go
type ContainerOptions struct {
    PlatformCfg   *config.PlatformConfig
    TimeoutCfg    *config.TimeoutConfig
    AvatarCfg     *config.AvatarConfig
    // ... 按功能分组
    IDGen         idgen.IDGenerator
}

func NewContainer(opts ContainerOptions) *Container
```
2. 更新 `app.go` 调用方。

---

### 阶段三：P2 渐进收敛（命名/注释/测试，1-2 周）

#### 5.3.1 修复命名规范

- P1-9: `UserService` 接口改名 `UserIDConverter`，`GetUserById` → `GetUserByID`。
- P1-10: `dto/constants.go` 补充 `RewardTypeStraight=1` / `RewardTypeLeopard=2` 常量，`reward_settler.go` switch 引用常量。
- P2-5: `ExistsByRoundAndType` 与 `ExistsRoundSettlement` 统一为 `ExistsByRoundSettlement`。
- P2-22, P2-23: `stats/handler/stats_handler.go` → `handler.go`，`stats/repository/stats_repository.go` → `repository.go`。
- P2-13: `Marshal()` 与 `ToJSON()` 统一为 `ToJSON()`。

#### 5.3.2 修复注释语言

- P2-11: `common/message/errors.go` 西语注释/消息改英文。
- P2-12: `common/message/types.go` 中文分节注释改英文。
- P2-10: 更新 `CODING_STANDARD.md` 中 `robot_lock.lua.go` 引用为 `common/lock/scripts/release_lock.lua.go`。

#### 5.3.3 补充测试与防御性编程

- P2-14: 新增 `TestSnowflakeGenerator_ClockBackward` 子测试，覆盖小幅回拨（≤5ms）和大幅回拨（>5ms）。
- P2-9: `round_repository.go` 5 个 Update 方法检查 `RowsAffected==0`。
- P2-16, P2-17: gateway Redis 调用检查 `.Err()`。
- P2-18: `manager.go:136-138` 类型断言加 ok 检查。
- P2-19: `gateway/handler/game.go` 错误信息不透传 err.Error()，改用业务码+通用消息。
- P2-27: scripts 错误吞没修复。

#### 5.3.4 修复其他 P2

- P2-1: `applyForRefundLocked` 调用 `BillManager.CreateRefundAuditAndUpdateBillRefundStatusInTransaction`。
- P2-2: `virtual_balance_service.go:67` 检查 `fmt.Sscanf` 返回值。
- P2-3: 定义 `ErrBillNotRefundable` 等哨兵。
- P2-4: `redisRobotChecker.IsRobot` fail-closed 或加注释说明。
- P2-7: `settlement/config/config.go` 补充聚合 `Config` struct。
- P2-8: `GameError` 增加 `Cause error` 字段，或用 `fmt.Errorf` 包装。
- P2-15: `gateway/server/server.go` 抽 `RegisterRoutes` 公开方法。
- P2-21: `stats/domain/` 定义 `StatsRepository` 接口。
- P2-25: stats Redis key 委托 `common/rediskeys`。
- P2-26: `api/platform/types.go` 抽 `CommonResponse.Data` 命名 struct。
- P2-28: 补充复合唯一索引迁移文件。

---

## 6. 验证清单

每个阶段完成后必须通过以下验证：

### 6.1 编译与格式
```bash
cd backend
gofmt -l . | wc -l          # 应为 0
go vet ./...                 # 应无 warning
go build ./...               # 应成功
```

### 6.2 单元测试
```bash
go test ./... -race -count=1 # 应全部通过
```

### 6.3 规约检查（Grep 验证）
```bash
# math/rand 残留（应仅 common/utils/rand.go 等封装层）
grep -rn "math/rand" --include="*.go" game/ settlement/ gateway/ stats/

# context.Background 残留（应仅 cmd/ 与测试）
grep -rn "context.Background()" --include="*.go" game/application/ game/scheduler/ gateway/

# 裸 return err 残留（应大幅减少）
grep -rn "return err$" --include="*.go" settlement/service/ game/application/

# if exists, _ 残留（应为 0）
grep -rn "if .* , _ :=" --include="*.go" settlement/service/ game/application/

# 裸数字 HTTP 状态码（应为 0）
grep -rn "c.JSON([0-9]" --include="*.go" gateway/ stats/

# fmt.Sprintf 拼接 host:port（应为 0）
grep -rn 'fmt.Sprintf(":%d"' --include="*.go" .
```

### 6.4 资金安全回归
- 并发退款测试：100 goroutine 同时申请退款，验证只有一个成功。
- BillRecord 复合索引：同一 round_trace_id 不同 bill_type 多条记录可共存。
- executeRefund 失败重试：模拟 RPC 失败，验证 RefundProcessScheduler 能查到 Failed 并重试。

### 6.5 限流器回归
- Redis 故障时限流器返回 error（fail-closed）。
- 100 goroutine 并发调用 AllowGrab 无 data race（`go test -race`）。

---

## 7. 风险评估与回滚预案

### 7.1 高风险变更

| 变更 | 风险 | 回滚预案 |
|------|------|----------|
| BillRecord GORM 标签修改 | AutoMigrate 可能误删旧索引 | 先在 staging 验证，生产手动执行迁移 SQL，不依赖 AutoMigrate |
| math/rand → crypto/rand | 性能下降可能影响吞吐 | 压测对比，若下降 > 30% 保留 math/rand 但加种子熵 |
| 限流器 fail-closed | Redis 故障时所有请求被拒 | 加 circuit breaker，Redis 持续故障 30s 后降级为本地令牌桶 |
| common/lock Release 改用 Lua | 与 redsync 行为不一致 | 保留 redsync 作为 fallback，新逻辑灰度切换 |
| refund_service 事务下沉 | 行为变化可能导致退款流程异常 | 灰度发布，监控退款成功率 |

### 7.2 灰度策略

1. **阶段一**（P0）：先在 staging 环境完整验证 1 周，再分批灰度生产（10% → 50% → 100%）。
2. **阶段二**（P1）：可独立部署，每个修复点单独 PR，code review 后合并。
3. **阶段三**（P2）：低风险，可批量提交。

### 7.3 监控指标

部署后需关注：
- 退款成功率（应 ≥ 99.9%）
- 限流器拒绝率（Redis 故障时不应突然归零）
- Kafka consumer lag（不应持续增长）
- gRPC panic 率（应为 0）
- BillRecord 插入失败率（复合索引冲突应 < 0.01%）

---

## 附录：问题编号索引

| 编号 | 级别 | 模块 | 简述 |
|------|------|------|------|
| P0-1 | P0 | settlement | refund_service bill.refund_status 缺乐观锁 |
| P0-2 | P0 | settlement | executeRefund 失败不置 Failed |
| P0-3 | P0 | settlement | BillRecord 复合唯一索引 GORM 标签残缺 |
| P0-4 | P0 | settlement | service 层直接持有 *gorm.DB 开事务 |
| P0-5 | P0 | scripts | 硬编码生产凭据 |
| P0-6 | P0 | game | algorithm/straight.go math/rand |
| P0-7 | P0 | game | grab_service math/rand |
| P0-8 | P0 | common | UserLimiter 共享指针 data race |
| P0-9 | P0 | common | Kafka processWithRetry 缺 panic recovery |
| P0-10 | P0 | gateway | 限流器 fail-open |
| P1-1 ~ P1-42 | P1 | 各模块 | 见正文 |
| P2-1 ~ P2-28 | P2 | 各模块 | 见正文 |

---

**审查人**：TRAE 自动审查（4 个搜索代理并行 + 人工整合去重）
**审查日期**：2026-07-05
**下次复审**：阶段一修复完成后
