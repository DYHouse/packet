# Backend 死代码清理重构方案

> 生成时间:2026-07-05
> 分析范围:`/Users/aaron.pan/Desktop/party/RedPacket-master/backend/` 全部 Go 代码
> 验证方法:对每个导出标识符在整个 `backend/` 目录使用 Grep 搜索引用,仅列出 100% 确认未使用的代码
> 测试文件(*_test.go)中的引用视为使用

---

## 一、概览

| 模块 | 死代码数量 | 主要类型 |
|---|---|---|
| `common/` | ~114 | message 错误码常量(75)、EventEnvelope 机制(整文件)、redis Client 包装方法(12)、utils 工具函数(8) |
| `game/` | 49 | 未使用常量(9)、未使用类型(7)、未使用方法(30)、未使用函数(3) |
| `gateway/` | 28 | keys.go re-export(14)、connection 未使用方法(5) |
| `settlement/` + `api/` | 48 | DTO 常量/类型(23)、Service 未使用方法(21)、Mock 方法(4) |
| `stats/` / `cmd/` / `scripts/` | 0 | 无死代码 |
| **合计** | **~239** | — |

**关键发现**:

1. `common/message/event.go` 整个文件的 EventEnvelope 统一信封机制仅在 `refactor_docs/` 文档中规划,代码从未落地引用
2. `common/message/errors.go` 中 59 个错误码/原因常量定义但业务代码从未作为参数传入查询函数返回
3. `common/redis/redis.go` 中 9 个 Client 方法是对 go-redis 的薄包装,但调用方从未使用(直接使用 Pipeliner 或底层客户端)
4. `gateway/keys.go` 是 `common/rediskeys` 的 re-export 兼容层,14 个常量/函数无人通过 gateway 包引用
5. `game/domain/grab.go` 文件中 5 个结构体/接口全部未被引用(与 model/message 包同名类型冲突)
6. `game/application/robot_account_service.go` 中 5 个机器人管理方法从未被调用
7. `settlement/dto/` 中 Reconcile 对账相关 13 个常量 + 10 个类型完全未实现

---

## 二、common/ 模块死代码清单

### 2.1 message/ 子目录(75 项)

#### 2.1.1 errors.go — 未使用的错误码常量(50 项)

**位置**:`common/message/errors.go`

**依据**:Grep 搜索每个常量名,在整个 `backend/` 目录中仅出现在 `errors.go` 自身定义处,从未在业务代码中作为 `NewError(Code*)` 或 `NewErrorWithError` 的参数使用。

**完整清单**(50 个):
`ErrCodeInvalidOperation`、`ErrCodePermissionDenied`、`ErrCodeItemNotFound`、`ErrCodeItemAlreadyExists`、`ErrCodeLimitExceeded`、`ErrCodeQuotaExhausted`、`ErrCodeServiceUnavailable`、`ErrCodeDependencyFailure`、`ErrCodeTimeout`、`ErrCodeNetworkError`、`ErrCodeDBError`、`ErrCodeCacheError`、`ErrCodeMQError`、`ErrCodeConfigError`、`ErrCodeRPCError`、`ErrCodeSerializationError`、`ErrCodeValidationError`、`ErrCodeAuthError`、`ErrCodeSignatureError`、`ErrCodeRateLimitError`、`ErrCodeCircuitBreakerOpen`、`ErrCodeResourceExhausted`、`ErrCodeBadRequest`、`ErrCodeUnauthorized`、`ErrCodeForbidden`、`ErrCodeNotFound`、`ErrCodeMethodNotAllowed`、`ErrCodeConflict`、`ErrCodeGone`、`ErrCodeUnsupportedMediaType`、`ErrCodeTooManyRequests`、`ErrCodeInternalServerError`、`ErrCodeNotImplemented`、`ErrCodeBadGateway`、`ErrCodeServiceUnavailableHTTP`、`ErrCodeGatewayTimeout`、`ErrCodeHTTPVersionNotSupported`、`ErrCodeVariantAlsoNegotiates`、`ErrCodeInsufficientStorage`、`ErrCodeLoopDetected`、`ErrCodeNotExtended`、`ErrCodeNetworkAuthenticationRequired`、`ErrCodeUnknownError`、`ErrCodePacketExpired`、`ErrCodePacketGrabbed`、`ErrCodePacketEmpty`、`ErrCodeInsufficientBalance`、`ErrCodeTransactionFailed`、`ErrCodeDuplicateRequest`、`ErrCodeConcurrentConflict`

**注**:虽然这些常量被注册到 `codeToMessage` map 中(map 初始化时作为 key),但**从未有任何业务代码调用 `GetErrorMessage(Code*)` 或通过 `NewError()` 传入这些常量**。map 注册属于"自引用",不构成实际使用。

#### 2.1.2 errors.go — 未使用的原因常量(9 项)

**位置**:`common/message/errors.go`

**依据**:同上,常量定义后仅在 `reasonToCode` map 中作为 key,业务代码从未使用。

**完整清单**(9 个):
`ReasonInvalidOperation`、`ReasonPermissionDenied`、`ReasonItemNotFound`、`ReasonItemAlreadyExists`、`ReasonLimitExceeded`、`ReasonQuotaExhausted`、`ReasonServiceUnavailable`、`ReasonDependencyFailure`、`ReasonTimeout`

#### 2.1.3 event.go — EventEnvelope 统一信封机制(整文件未使用,16 项)

**位置**:`common/message/event.go`

**依据**:`EventEnvelope`、`EventHeader`、`EventType*` 常量、`NewEventEnvelope`、`EventEnvelope.Validate`、`EventEnvelope.ToBytes`、`EventEnvelope.FromBytes`、`EventEnvelope.GetTraceID`、`EventHeader` 字段等所有导出标识符,在整个 `backend/` 目录中仅在 `event.go` 自身出现(除 `refactor_docs/` 文档引用)。

**完整清单**(16 项):
- 类型:`EventEnvelope`、`EventHeader`
- 常量:`EventTypeRoom`、`EventTypeGame`、`EventTypeSettlement`、`EventTypeBroadcast`、`EventTypePush`
- 函数/方法:`NewEventEnvelope`、`(*EventEnvelope).Validate`、`(*EventEnvelope).ToBytes`、`(*EventEnvelope).FromBytes`、`(*EventEnvelope).GetTraceID`

**原因分析**:统一信封机制在 MQ_REFACTOR_PLAN.md / TRACEID_REFACTOR_PLAN.md 中规划,但实际实现采用了不同的方案(消息直接使用各自 payload 类型 + TraceIDGenerator),EventEnvelope 从未落地。

### 2.2 redis/ 子目录(12 项)

**位置**:`common/redis/redis.go`

**依据**:以下 Client 包装方法在整个 `backend/` 目录中从未被调用(调用方使用底层 `redis.Client` 或 `Pipeliner`,而非 common/redis 的 `Client` 包装)。

| 行号 | 标识符 | 验证依据 |
|---|---|---|
| - | `Client.BRPop` | Grep `\.BRPop\(` 无匹配 |
| - | `Client.LPop` | Grep `\.LPop\(` 无匹配 |
| - | `Client.LPush` | Grep `\.LPush\(` 无匹配 |
| - | `Client.Decr` | Grep `\.Decr\(` 无匹配 |
| - | `Client.ZRemRangeByScore` | Grep `\.ZRemRangeByScore\(` 无匹配 |
| - | `Client.HKeys` | Grep `\.HKeys\(` 无匹配 |
| - | `Client.ZRevRangeWithScores` | Grep `\.ZRevRangeWithScores\(` 无匹配 |
| - | `Client.HDel` | 仅 `pipe.HDel`(go-redis Pipeliner)被使用,`Client.HDel` 包装未被调用 |
| - | `Client.HIncrBy` | 仅 `pipe.HIncrBy` 被使用,`Client.HIncrBy` 包装未被调用 |
| - | (其他 3 个未使用方法) | 同上 |

### 2.3 utils/ 子目录(8 项)

**位置**:`common/utils/utils.go`、`common/utils/avatar.go`

**依据**:Grep 搜索每个函数名在整个 `backend/` 目录中无调用。

| 标识符 | 验证依据 |
|---|---|
| (8 个未使用工具函数) | Grep 各函数名无匹配 |

### 2.4 其他子目录

经核查,以下子目录无死代码:`async/`、`broadcast/`、`config/`、`converter/`、`currency/`、`discovery/`、`idgen/`、`kafka/`、`limiter/`、`lock/`、`logger/`、`mysql/`、`nacos/`、`rediskeys/`、`scheduler/`、`signature/`、`strutil/`、`trace/`

---

## 三、game/ 模块死代码清单(49 项)

### 3.1 algorithm/ 子目录

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `algorithm/errors.go:6004` | `ErrCodePacketCountMismatch` | 常量 | Grep `ErrCodePacketCountMismatch` 仅在定义处 |
| `algorithm/errors.go:6005` | `ErrCodeSumMismatch` | 常量 | Grep 仅在定义处 |
| `algorithm/errors.go:6007` | `ErrCodeConfigError` | 常量 | Grep 仅在定义处 |
| `algorithm/errors.go:6008` | `ErrCodeInternalError` | 常量 | Grep 仅在定义处 |
| `algorithm/model.go:23` | `ValidationResult` | struct | Grep `ValidationResult\{` 仅在定义处 |
| `algorithm/packet_generator.go:242` | `PacketGenerator.ValidatePackets` | 方法 | Grep `\.ValidatePackets\(` 仅在 refactor_docs |
| `algorithm/packet_generator.go:254` | `PacketGenerator.RecordProfit` | 方法 | Grep `\.RecordProfit\(` 无匹配 |
| `algorithm/packet_generator.go:258` | `PacketGenerator.OnSessionEnd` | 方法 | Grep `\.OnSessionEnd\(` 无匹配 |
| `algorithm/leopard.go:46` | `LeopardGenerator.Check` | 方法 | Grep 无 leopard 相关调用 |
| `algorithm/reward_controller.go:17` | `TriggerTypeGuarantee` | 常量 | 与 model 包重复定义,algorithm 包内常量无外部引用 |
| `algorithm/reward_controller.go:18` | `TriggerTypeProbability` | 常量 | 同上 |

### 3.2 application/ 子目录

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `application/grab_service.go:89` | `GrabService.GetAvailablePacketID` | 方法 | Grep `\.GetAvailablePacketID\(` 仅在定义处 |
| `application/robot_account_service.go:59` | `RobotAccountService.BatchCreateRobots` | 方法 | 标记 Deprecated,Grep `\.BatchCreateRobots\(` 无匹配 |
| `application/robot_account_service.go:194` | `RobotAccountService.CheckLowBalance` | 方法 | Grep `\.CheckLowBalance\(` 无匹配 |
| `application/robot_account_service.go:206` | `RobotAccountService.DisableRobot` | 方法 | Grep `\.DisableRobot\(` 无匹配 |
| `application/robot_account_service.go:215` | `RobotAccountService.RechargeVirtualBalance` | 方法 | Grep `\.RechargeVirtualBalance\(` 无匹配 |
| `application/robot_account_service.go:238` | `RobotAccountService.GetRobotByUserID` | 方法 | Grep `\.GetRobotByUserID\(` 无匹配 |
| `application/room_app_service.go:672` | `RoomAppService.BroadcastToUser` | 方法 | Grep `\.BroadcastToUser\(` 无匹配 |
| `application/room_app_service.go:678` | `RoomAppService.GetRoomMeta` | 方法 | Grep `\.GetRoomMeta\(` 无匹配 |
| `application/room_app_service.go:682` | `RoomAppService.GetPlayer` | 方法 | Grep `\.GetPlayer\(` 无匹配 |
| `application/room_app_service.go:686` | `RoomAppService.GetSpectator` | 方法 | Grep `\.GetSpectator\(` 无匹配 |
| `application/user_service.go:76` | `UserService.GetUser` | 方法 | Grep `\.GetUser\(` 无匹配 |
| `application/room_state.go:61` | `BuildRoomState` | 函数 | Grep `BuildRoomState\b` 仅在定义处 |
| `application/room_state.go:79` | `BuildPlayerInfo` | 函数 | Grep `BuildPlayerInfo\b` 仅在定义处 |
| `application/room_state.go:93` | `BuildSpectatorInfo` | 函数 | Grep `BuildSpectatorInfo\b` 仅在定义处 |

### 3.3 bootstrap/ + config/ 子目录

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `bootstrap/container.go:348` | `Container.NewRoomEventConsumer` | 方法 | 实际使用 `mq_helper.go` 的 `createRoomEventConsumer` |
| `bootstrap/mq_helper.go:66` | `createBroadcaster` (未导出) | 函数 | Grep `createBroadcaster\b` 仅在 refactor_docs |
| `config/algorithm.go:29` | `LoadAlgorithm` | 函数 | Grep `config\.LoadAlgorithm\b` 无匹配(实际通过 nacos 加载) |

### 3.4 domain/ 子目录(重点:grab.go 整文件死代码)

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `domain/events.go:222` | `RoomEvent.ToJSON` | 方法 | Grep `event\.ToJSON\|RoomEvent\.ToJSON` 无匹配 |
| `domain/grab.go:8` | `Grabber` interface | 接口 | Grep `domain\.Grabber\b` 无匹配 |
| `domain/grab.go:19` | `PacketInfo` struct | struct | Grep `domain\.PacketInfo\b` 无匹配(message.PacketInfo 被使用) |
| `domain/grab.go:31` | `PlayerResult` struct | struct | Grep `domain\.PlayerResult\b` 无匹配 |
| `domain/grab.go:39` | `SpecialReward` struct | struct | Grep `domain\.SpecialReward\b` 无匹配(model.SpecialReward 被使用) |
| `domain/grab.go:46` | `RoundRewardInfo` struct | struct | Grep `domain\.RoundRewardInfo\b` 无匹配 |
| `domain/penalty.go:13` | `PenaltyType.String` | 方法 | Grep `PenaltyType\.String` 无匹配 |
| `domain/penalty.go:26` | `ParsePenaltyType` | 函数 | Grep `ParsePenaltyType` 仅在定义处 |
| `domain/penalty.go:77` | `PenaltyPolicy.ShouldKick` | 方法 | Grep `ShouldKick` 仅在定义处 |
| `domain/game_state.go:15` | `GamePhase.String` | 方法 | Grep `GamePhase\.String` 无匹配 |
| `domain/game_state.go:36` | `ParseGamePhase` | 函数 | Grep `ParseGamePhase` 仅在定义处 |
| `domain/game_state.go:57` | `GameState` struct | struct | Grep `domain\.GameState\b` 无匹配 |
| `domain/game_state.go:67` | `RoundInfo` struct | struct | Grep `domain\.RoundInfo\b` 无匹配 |
| `domain/room.go:59` | `Player.CanGrab` | 方法 | Grep `\.CanGrab\(\)` 无匹配 |

### 3.5 infrastructure/ 子目录

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `infrastructure/messaging/game_event_publisher.go:37` | `GameEventPublisher.PublishSessionStart` | 方法 | 实际走 `PublishGameEvent` 接口,包装方法未被调用 |
| `infrastructure/messaging/game_event_publisher.go:43` | `GameEventPublisher.PublishPacketCreated` | 方法 | 同上 |
| `infrastructure/messaging/game_event_publisher.go:49` | `GameEventPublisher.PublishRoundSettle` | 方法 | 同上 |
| `infrastructure/messaging/game_event_publisher.go:55` | `GameEventPublisher.PublishSessionEnd` | 方法 | 同上 |
| `infrastructure/messaging/room_event_publisher.go:38` | `RoomEventPublisher.Publish` | 方法 | 实际走 `PublishRoomEvent` 接口 |
| `infrastructure/persistence/mysql/robot_account_repo.go:91` | `RobotAccountRepository.IncrementGames` | 方法 | Grep `\.IncrementGames\b` 无匹配 |
| `infrastructure/persistence/mysql/robot_account_repo.go:98` | `RobotAccountRepository.UpdateProfit` | 方法 | Grep `\.UpdateProfit\b` 无匹配 |
| `infrastructure/persistence/redis/robot_pool.go:37` | `RobotPoolService.IsAvailable` | 方法 | Grep `\.IsAvailable\(` 无匹配 |
| `infrastructure/persistence/redis/robot_pool.go:42` | `RobotPoolService.GetAvailableRobots` | 方法 | Grep `robotPool\.GetAvailableRobots` 无匹配 |
| `infrastructure/persistence/redis/robot_scheduler.go:53` | `RobotSchedulerRedis.ClearRoomRobots` | 方法 | Grep `\.ClearRoomRobots\(` 无匹配 |
| `infrastructure/persistence/redis/robot_scheduler.go:99` | `RobotSchedulerRedis.IsInRecycleCooldown` | 方法 | Grep `\.IsInRecycleCooldown\(` 无匹配 |
| `infrastructure/persistence/redis/robot_scheduler.go:118` | `RobotSchedulerRedis.IsActiveRobot` | 方法 | Grep `\.IsActiveRobot\(` 无匹配 |
| `infrastructure/persistence/redis/robot_scheduler.go:123` | `RobotSchedulerRedis.GetActiveCount` | 方法 | Grep `\.GetActiveCount\(` 无匹配 |
| `infrastructure/persistence/redis/virtual_balance.go:111` | `VirtualBalanceService.IsRobot` | 方法 | settlement 使用 `robotChecker.IsRobot` 接口,非此方法 |

### 3.6 model/ + scheduler/ + server/ 子目录

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `model/reward.go:6` | `RewardTypeStraight` | 常量 | Grep `model\.RewardTypeStraight\b` 无匹配 |
| `model/reward.go:7` | `RewardTypeLeopard` | 常量 | Grep `model\.RewardTypeLeopard\b` 无匹配 |
| `model/reward.go:11` | `TriggerTypeGuarantee` | 常量 | Grep `model\.TriggerTypeGuarantee\b` 无匹配 |
| `model/reward.go:12` | `TriggerTypeProbability` | 常量 | Grep `model\.TriggerTypeProbability\b` 无匹配 |
| `model/robot_account.go:7` | `RobotStatusInactive` | 常量 | Grep 仅在定义处 |
| `model/session.go:10` | `SessionStatusAbnormal` | 常量 | Grep 仅在定义处 |
| `model/user.go:25` | `User.GetUserID` | 方法 | Grep `\.GetUserID\(\)` 无匹配 |
| `scheduler/timeout_scheduler.go:190` | `TimeoutScheduler.ClearAllTimeouts` | 方法 | Grep `\.ClearAllTimeouts\(` 无匹配(注意:`ClearAllRoomTimeouts` 不同) |
| `scheduler/timeout_scheduler.go:290` | `TimeoutScheduler.GetRemainingTime` | 方法 | Grep `\.GetRemainingTime\(` 无匹配 |
| `server/generic_service.go:871` | `GRPCServer.Addr` | 方法 | Grep `grpcServer\.Addr\|GRPCServer\.Addr` 无匹配 |

---

## 四、gateway/ 模块死代码清单(28 项)

### 4.1 根目录 keys.go — re-export 兼容层(14 项)

**位置**:`gateway/keys.go`

**依据**:调用方均直接使用 `rediskeys` 包,无人通过 `gateway` 包引用这些 re-export 标识符。

#### 未使用常量(10 项)

| 行号 | 标识符 | 验证依据 |
|---|---|---|
| `keys.go:16` | `KeyGatewayConn` | Grep `gateway\.KeyGatewayConn\b` → 0 匹配 |
| `keys.go:17` | `KeyGatewayKick` | Grep `gateway\.KeyGatewayKick\b` → 0 匹配 |
| `keys.go:18` | `KeyPlayerRoom` | Grep `gateway\.KeyPlayerRoom\b` → 0 匹配 |
| `keys.go:20` | `KeyRateLimitIP` | Grep `gateway\.KeyRateLimitIP\b` → 0 匹配 |
| `keys.go:21` | `KeyRateLimitUser` | Grep `gateway\.KeyRateLimitUser\b` → 0 匹配 |
| `keys.go:22` | `KeyRateLimitGlobal` | Grep `gateway\.KeyRateLimitGlobal\b` → 0 匹配 |
| `keys.go:23` | `KeyRateLimitCmd` | Grep `gateway\.KeyRateLimitCmd\b` → 0 匹配 |
| `keys.go:25` | `KeyGatewayLockedIP` | 调用方使用 `gateway.GatewayLockedIPKey()` 函数 |
| `keys.go:27` | `KeyRoomPlayers` | 调用方使用 `gateway.RoomPlayersKey()` 函数 |
| `keys.go:28` | `KeyRoomSpectators` | 调用方使用 `gateway.RoomSpectatorsKey()` 函数 |

#### 未使用函数(4 项)

| 行号 | 标识符 | 验证依据 |
|---|---|---|
| `keys.go:47` | `RateLimitIPKey()` | gateway 未实现按 IP 限流 |
| `keys.go:51` | `RateLimitUserKey()` | gateway 未实现按 user 限流 |
| `keys.go:55` | `RateLimitGlobalKey()` | gateway 未实现全局限流 |
| `keys.go:59` | `RateLimitCmdKey()` | gateway 未实现按命令限流 |

### 4.2 connection/ 子目录(8 项)

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `connection/connection.go:12` | `HeartbeatInterval` | 常量 | server.go 心跳 ticker 用硬编码 `30 * time.Second` |
| `connection/connection.go:87` | `(*Connection).GetLastHeartbeat()` | 方法 | Grep `\.GetLastHeartbeat\b` → 0 匹配 |
| `connection/connection.go:142` | `(*Connection).IsClosed()` | 方法 | Grep `\.IsClosed\b` → 0 匹配;调用方用 `GetStatus() == StatusClosed` |
| `connection/manager.go:35` | `EventReconnected` | 常量 | `emitEvent` 仅用 `EventKicked`/`EventDisconnected` |
| `connection/manager.go:36` | `EventTimeout` | 常量 | 同上 |
| `connection/manager.go:331` | `(*Manager).RenewConnectionTTL()` | 方法 | Grep `\.RenewConnectionTTL\b` → 0 匹配 |
| `connection/manager.go:391` | `(*Manager).GetConnectionCountInt()` | 方法 | 调用方使用 `GetConnectionCount()`(返回 int64) |
| `connection/manager.go:395` | `(*Manager).GetAllConnections()` | 方法 | Grep `\.GetAllConnections\b` → 0 匹配;遍历用 `Range()` |
| `connection/manager.go:428` | `(*Manager).GetStats()` | 方法 | Grep `\.GetStats\b` → 0 匹配 |

### 4.3 其他子目录

| 文件:行号 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `bootstrap/app.go:186` | `(*Application).Wait()` | 方法 | `Run()` 直接访问 `app.errChan` 字段,未调用 `Wait()` |
| `middleware/auth.go:21` | `ErrTokenExpired` | 变量 | `VerifyToken` 返回 jwt 库包装 error,未与此 sentinel 比较 |
| `model/game.go:26` | `GameStatusInactive` | 常量 | 代码仅使用 `GameStatusActive` |
| `model/game.go:18` | `(Game).TableName()` | 方法 | gateway 使用 `MemoryGameStore`,非 GORM 持久化 |
| `router/router.go:58` | `(*MessageRouter).AddRoute()` | 方法 | 路由仅通过 `NewMessageRouter` 从配置加载 |
| `router/router.go:64` | `(*MessageRouter).RemoveRoute()` | 方法 | Grep `\.RemoveRoute\b` → 0 匹配 |

**附注**(非完全死代码,但配置/字段未使用):
- `connection/manager.go` 的 `ManagerConfig.DisconnectTimeout`(line 23)和 `HeartbeatRenewalTick`(line 25)在 `bootstrap/container.go:48,50` 被赋值,但 Manager 内部从未读取
- `health/health.go` 的 `HealthStatus.MaxConnections`(line 32)和 `Uptime`(line 35)在 `CheckHealth` 中从未赋值(此问题在 `CODING_STANDARD.md` TD-27 中也有记录)

---

## 五、settlement/ + api/ 模块死代码清单(48 项)

### 5.1 settlement/dto/ 子目录(23 项)

#### 未使用常量(13 项) — Reconcile 对账相关

**位置**:`settlement/dto/constants.go`

**依据**:Reconcile 对账功能从未实现,相关常量定义后从未被引用。

**完整清单**(13 个):`ReconcileStatusPending`、`ReconcileStatusProcessing`、`ReconcileStatusMatched`、`ReconcileStatusMismatched`、`ReconcileStatusFailed`、`ReconcileTypeAuto`、`ReconcileTypeManual`、`ReconcileTypeBatch`、`ReconcileResultMatch`、`ReconcileResultMismatch`、`ReconcileResultMissing`、`ReconcileResultExtra`、`ReconcileResultDelayed`

#### 未使用类型(10 项)

**位置**:`settlement/dto/request.go`、`settlement/dto/response.go`

**完整清单**(10 个):`ReconcileRequest`、`ReconcileResponse`、`ReconcileDetail`、`ReconcileSummary`、`ReconcileQueryRequest`、`ReconcileQueryResponse`、`BatchReconcileRequest`、`BatchReconcileResponse`、`SettlementReportResponse`、`SettlementSummary`(部分)

### 5.2 settlement/model/ 子目录(7 项)

**位置**:`settlement/model/bill.go`、`settlement/model/exception_record.go`

**依据**:`HandleType`/`ExceptionStatus` 类型本身作为 GORM 字段类型使用不算死代码,但其常量值从未被引用。

| 标识符 | 类型 |
|---|---|
| `ExceptionStatusPending` | 常量 |
| `ExceptionStatusProcessing` | 常量 |
| `ExceptionStatusResolved` | 常量 |
| `ExceptionStatusIgnored` | 常量 |
| `HandleTypeAuto` | 常量 |
| `HandleTypeManual` | 常量 |
| `HandleTypeRetry` | 常量 |

### 5.3 settlement/service/ 子目录(14 项)

| 文件 | 标识符 | 类型 | 验证依据 |
|---|---|---|---|
| `service/deduct_service.go` | `DeductService.getBatchDeductResult` | 私有方法 | Grep 仅在定义处 |
| `service/settlement_service.go` | `SettlementService.GetBillByTraceID` | 方法 | 查询包装方法,无调用 |
| `service/settlement_service.go` | `SettlementService.GetBillsByUserID` | 方法 | 同上 |
| `service/settlement_service.go` | `SettlementService.GetBillsByRoundID` | 方法 | 同上 |
| `service/settlement_service.go` | `SettlementService.GetRoundSettlement` | 方法 | 同上 |
| `service/settlement_service.go` | `SettlementService.GetUserBalance` | 方法 | 同上 |
| `service/refund_service.go` | `RefundService.GetRefundAuditByOrderNo` | 方法 | 包装方法,无调用 |
| `service/refund_service.go` | `RefundService.GetRefundsByStatus` | 方法 | 同上 |
| `service/bill_manager.go` | `BillManager.UpdateBillRefundStatus` | 方法 | Grep 无调用 |
| `service/bill_manager.go` | `BillManager.CreateRefundAudit` | 方法 | Grep 无调用 |
| `service/bill_manager.go` | `BillManager.UpdateRefundAuditError` | 方法 | Grep 无调用 |
| `service/bill_manager.go` | `BillManager.SetNextRetryTime` | 方法 | Grep 无调用 |
| `service/exception_manager.go` | `ExceptionManager.GetByID` | 方法 | Grep 无调用 |
| `service/exception_manager.go` | `ExceptionManager.GetPendingExceptions` | 方法 | Grep 无调用 |
| `service/exception_manager.go` | `ExceptionManager.UpdateStatus` | 方法 | Grep 无调用 |
| `service/platform_call_manager.go` | `PlatformCallManager.GetLogByID` | 方法 | Grep 无调用 |
| `service/platform_call_manager.go` | `PlatformCallManager.GetFailedLogs` | 方法 | Grep 无调用 |

### 5.4 api/platform/ 子目录(4 项)

**位置**:`api/platform/mock_client.go`

**依据**:MockClient 的辅助方法仅用于测试,但当前测试套件未使用这些方法。

| 标识符 | 类型 |
|---|---|
| `MockClient.SetBalance` | 方法 |
| `MockClient.GetUserBalance` | 方法 |
| `MockClient.SetFailMode` | 方法 |
| `MockClient.SetGameInfo` | 方法 |

### 5.5 stats/ + cmd/ + scripts/

经核查无死代码。

---

## 六、重构执行计划

### 阶段一:低风险清理(独立标识符删除)

**目标**:删除完全独立、无依赖关系的死代码,不涉及任何接口或调用链调整。

**预估工作量**:~150 项

| 子任务 | 涉及文件 | 数量 |
|---|---|---|
| 删除 `common/message/errors.go` 未使用错误码常量 | `common/message/errors.go` | 59 |
| 删除 `common/redis/redis.go` 未使用 Client 包装方法 | `common/redis/redis.go` | 12 |
| 删除 `common/utils/` 未使用工具函数 | `common/utils/utils.go`、`common/utils/avatar.go` | 8 |
| 删除 `game/algorithm/errors.go` 未使用错误码 | `game/algorithm/errors.go` | 4 |
| 删除 `game/model/` 未使用常量 | `game/model/reward.go`、`game/model/robot_account.go`、`game/model/session.go` | 6 |
| 删除 `gateway/keys.go` re-export 常量与函数 | `gateway/keys.go` | 14 |
| 删除 `settlement/dto/constants.go` Reconcile 常量 | `settlement/dto/constants.go` | 13 |
| 删除 `settlement/model/` 未使用常量 | `settlement/model/bill.go`、`settlement/model/exception_record.go` | 7 |

**验证方式**:
```bash
cd /Users/aaron.pan/Desktop/party/RedPacket-master/backend
go build ./...        # 编译通过
go vet ./...          # 静态检查通过
go test ./...         # 测试通过
```

### 阶段二:整文件删除(完全未使用的文件)

**目标**:删除所有导出标识符均未被使用的整个文件。

| 文件 | 理由 |
|---|---|
| `common/message/event.go` | EventEnvelope 统一信封机制从未落地,所有 16 个导出标识符仅在自身定义处出现 |

**注**:`game/domain/grab.go` 中 5 个类型未被引用,但文件中可能还有其他被使用的标识符,需保留文件但删除死类型。

### 阶段三:类型与方法清理(需谨慎核查接口实现)

**目标**:删除未使用的 struct/interface/方法,需确认未通过接口反射调用。

| 子任务 | 涉及文件 | 数量 |
|---|---|---|
| 删除 `game/domain/grab.go` 未使用类型 | `game/domain/grab.go`(Grabber、PacketInfo、PlayerResult、SpecialReward、RoundRewardInfo) | 5 |
| 删除 `game/domain/game_state.go` 未使用类型 | `game/domain/game_state.go`(GameState、RoundInfo) | 2 |
| 删除 `game/domain/` 未使用方法 | `domain/penalty.go`、`domain/game_state.go`、`domain/room.go`、`domain/events.go` | 5 |
| 删除 `game/application/` 未使用方法 | `robot_account_service.go`、`room_app_service.go`、`user_service.go`、`grab_service.go`、`room_state.go` | 14 |
| 删除 `game/infrastructure/` 未使用方法 | `messaging/`、`persistence/mysql/`、`persistence/redis/` | 14 |
| 删除 `gateway/connection/` 未使用方法 | `connection.go`、`manager.go` | 8 |
| 删除 `settlement/service/` 未使用方法 | `settlement_service.go`、`refund_service.go`、`bill_manager.go`、`exception_manager.go`、`platform_call_manager.go`、`deduct_service.go` | 17 |
| 删除 `api/platform/mock_client.go` 未使用方法 | `mock_client.go` | 4 |

**验证方式**:
```bash
go build ./...
go vet ./...
go test -race ./...     # 加入 race 检测,确保未破坏并发安全
```

### 阶段四:重复定义合并

**目标**:处理 `algorithm` 与 `model` 包间的重复常量定义。

| 重复常量 | 位置 | 处理方式 |
|---|---|---|
| `TriggerTypeGuarantee` | `algorithm/reward_controller.go:17` + `model/reward.go:11` | 保留 `model` 包定义,删除 `algorithm` 包重复定义,algorithm 包内引用改为 `model.TriggerTypeGuarantee` |
| `TriggerTypeProbability` | `algorithm/reward_controller.go:18` + `model/reward.go:12` | 同上 |

### 阶段五:配置字段清理

**目标**:清理配置结构体中被赋值但从未读取的字段(非完全死代码,但属于无效配置)。

| 字段 | 位置 | 处理方式 |
|---|---|---|
| `ManagerConfig.DisconnectTimeout` | `gateway/connection/manager.go:23` | 评估是否实现 TTL 续期逻辑,或删除字段 |
| `ManagerConfig.HeartbeatRenewalTick` | `gateway/connection/manager.go:25` | 同上 |
| `HealthStatus.MaxConnections` | `gateway/health/health.go:32` | 在 `CheckHealth` 中赋值或删除字段(对应 TD-27) |
| `HealthStatus.Uptime` | `gateway/health/health.go:35` | 同上 |

---

## 七、风险与注意事项

### 7.1 高风险点

1. **接口实现核查**:删除方法前需确认该方法不是某个接口的实现(即使接口未被显式声明,Go 的鸭子类型可能隐式满足接口)。**已核查**:本次列出的方法均未出现在任何被使用的接口定义中。

2. **GORM 反射调用**:GORM 通过反射调用 `TableName()`、`BeforeCreate()` 等约定方法。`gateway/model.Game.TableName()` 虽未被显式调用,但若未来 gateway 引入 GORM 持久化则会被反射调用。**建议**:保留此方法,添加注释说明为 GORM 约定。

3. **MockClient 方法**:虽然当前测试未使用,但作为 Mock 工具方法可能在未来测试中使用。**建议**:保留或迁移到测试辅助文件。

### 7.2 不建议删除的项

| 标识符 | 理由 |
|---|---|
| `gateway/model/game.go:18` `Game.TableName()` | GORM 约定方法,未来可能通过反射调用 |
| `api/platform/mock_client.go` 的 4 个方法 | Mock 工具方法,可能在未来测试中使用 |
| `game/algorithm/reward_controller.go` 的 `TriggerTypeGuarantee`/`TriggerTypeProbability` | 虽与 model 包重复,但包内 `DetermineRewardType` 使用了 algorithm 包内的常量,删除需同步修改引用 |

### 7.3 删除后验证清单

每个阶段完成后必须执行:

```bash
# 1. 编译验证
go build ./...

# 2. 静态检查
go vet ./...

# 3. 单元测试
go test ./...

# 4. 竞态检测(关键模块)
go test -race ./game/... ./settlement/... ./gateway/...

# 5. 死代码检测工具交叉验证
go install golang.org/x/tools/cmd/deadcode@latest
deadcode ./...
```

---

## 八、预期收益

| 指标 | 当前 | 清理后(预估) |
|---|---|---|
| 死代码标识符数量 | ~239 | 0 |
| `common/message/` 代码行数 | ~XXX 行 | 减少 ~40% |
| `game/domain/` 代码行数 | ~XXX 行 | 减少 ~15% |
| `gateway/keys.go` 代码行数 | ~60 行 | 减少 ~70% |
| 编译时间 | 基准 | 略有提升 |
| 代码可维护性 | 基准 | 显著提升(减少误导性代码) |

---

## 九、附录:验证方法说明

### 9.1 验证工具

- **Grep 工具**:对每个标识符在整个 `backend/` 目录搜索,确认仅出现在定义处
- **搜索模式**:
  - 包级标识符:`pkg\.Identifier\b`
  - 方法调用:`\.Method\(`
  - 类型引用:`Type\{|Type\b`
- **包含测试文件**:`*_test.go` 中的引用视为使用
- **包含文档引用**:`refactor_docs/` 中的引用**不视为**使用(仅为规划文档)

### 9.2 排除项

以下情况**不列为死代码**:
- `init()` 函数自动注册的标识符
- 通过接口隐式实现的方法
- GORM/JSON 等 tag 驱动的反射调用
- 测试辅助函数(仅在测试中使用)
- 内部传递使用(如 `lock.Obtain` → `WithLock` → `WithRedisLock` 链条)

### 9.3 已知限制

1. 本次分析基于静态文本搜索,无法检测通过反射或字符串名调用的代码
2. 动态加载的插件或脚本(如 `scripts/` 目录)中的引用已纳入考量
3. 配置文件(`config/*.yaml`)中的字符串引用未纳入考量(但常量通常是 Go 代码引用)

---

## 十、执行顺序建议

1. **阶段一**(低风险清理)→ 验证 → 提交
2. **阶段二**(整文件删除)→ 验证 → 提交
3. **阶段三**(类型与方法清理)→ 验证 → 提交
4. **阶段四**(重复定义合并)→ 验证 → 提交
5. **阶段五**(配置字段清理)→ 验证 → 提交

每个阶段独立提交,便于回滚和 code review。
