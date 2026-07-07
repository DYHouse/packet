// Package rediskeys 是项目 Redis key 的单一真相源（Single Source of Truth）。
// 所有跨服务的 Redis key 常量与工厂函数 MUST 集中在此处，禁止散落在各服务的 keys.go 中重复定义。
// 规约参考 CODING_STANDARD.md §6.2 与 §16 SC-5 / SC-10。
//
// 命名约定：
//   - 所有 key MUST 以 KeyPrefix = "cashparty" 开头
//   - 多参数 key 的分隔符 MUST 统一为 ":"，禁止下划线 "_"
//   - Prefix 常量 MUST 带尾随冒号（如 "cashparty:room:hash:"），调用方一律 + "*"
//   - 工厂函数命名规范：XxxKey(args...) string
package rediskeys

import "fmt"

// KeyPrefix 是所有 Redis key 的命名空间前缀。
// 任何新增 key MUST 以 KeyPrefix 开头，禁止裸用 "cashparty" 字面量。
const KeyPrefix = "cashparty"

// ============================================================================
// 房间相关 key
// ============================================================================

const (
	// KeyRoomHashPrefix 房间哈希表前缀（带尾随冒号，用于 SCAN/Keys 匹配）。
	KeyRoomHashPrefix = KeyPrefix + ":room:hash:"
	// KeyRoomHash 房间哈希表 key 模板。
	KeyRoomHash = KeyRoomHashPrefix + "%s"

	// KeyRoomSpectatorsPrefix 房间观众集合前缀。
	KeyRoomSpectatorsPrefix = KeyPrefix + ":room:spectators:"
	// KeyRoomSpectators 房间观众集合 key 模板。
	KeyRoomSpectators = KeyRoomSpectatorsPrefix + "%s"

	// KeyRoomPlayersPrefix 房间玩家集合前缀。
	KeyRoomPlayersPrefix = KeyPrefix + ":room:players:"
	// KeyRoomPlayers 房间玩家集合 key 模板。
	KeyRoomPlayers = KeyRoomPlayersPrefix + "%s"

	// KeyPlayerRoom 玩家当前所在房间映射。
	KeyPlayerRoom = KeyPrefix + ":player:room:%s"
	// KeyRoomNode 房间所属节点。
	KeyRoomNode = KeyPrefix + ":room:node:%s"
	// KeyRoomSeq 房间序号（按日期）。
	KeyRoomSeq = KeyPrefix + ":room:seq:%s"
	// KeyDeadLetterQueue DB 死信队列。
	KeyDeadLetterQueue = KeyPrefix + ":db:dead_letter"
	// KeyRoomSeats 房间座位集合。
	KeyRoomSeats = KeyPrefix + ":room:seats:%s"
	// KeyRoomSeatOwner 房间座位归属（注意：使用 ":" 分隔，禁止下划线 "_owner"）。
	KeyRoomSeatOwner = KeyPrefix + ":room:seat:owner:%s"
	// KeyRoomQueue 房间排队队列。
	KeyRoomQueue = KeyPrefix + ":room:queue:%s"
	// KeySeatTimeout 座位超时集合。
	KeySeatTimeout = KeyPrefix + ":room:seat:timeout"
	// KeyReadyTimeout 准备超时集合。
	KeyReadyTimeout = KeyPrefix + ":room:ready:timeout"
	// KeyDisconnectTimeout 断线超时集合。
	KeyDisconnectTimeout = KeyPrefix + ":room:disconnect:timeout"
	// KeyRoomPlayer 房间内玩家信息。
	KeyRoomPlayer = KeyPrefix + ":room:player:%s:%s"
	// KeyRoomNoToID 房间号到房间ID的映射。
	KeyRoomNoToID = KeyPrefix + ":room:no_to_id:%s"
)

// ============================================================================
// 用户相关 key
// ============================================================================

const (
	// KeyUserByUserID 按 user_id 查询用户。
	KeyUserByUserID = KeyPrefix + ":user:user_id:%s"
	// KeyUserById 按 id 查询用户。
	KeyUserById = KeyPrefix + ":user:id:%s"
)

// ============================================================================
// 轮次/红包相关 key
// ============================================================================

const (
	// KeyRoundPackets 轮次红包列表。
	KeyRoundPackets = KeyPrefix + ":round:packets:%s"
	// KeyRoundAvailablePackets 轮次可用红包列表。
	KeyRoundAvailablePackets = KeyPrefix + ":round:available_packets:%s"
	// KeyRoundGrabbers 轮次抢红包者集合。
	KeyRoundGrabbers = KeyPrefix + ":round:grabbers:%s"
	// KeyRoundReward 轮次奖励信息。
	KeyRoundReward = KeyPrefix + ":round:reward:%s"
	// KeyUserGrabbed 用户抢过红包标记。
	KeyUserGrabbed = KeyPrefix + ":round:grabbed:%s:%s"
	// KeyRoundGrabbed 玩家本轮已抢标记(Lua 脚本使用)
	// 对应 Lua 中的 roundGrabbedPrefix .. roundID .. ':' .. userID
	KeyRoundGrabbed = KeyPrefix + ":round:grabbed:%s:%s"
	// KeyRoundGrabbedPrefix 玩家本轮已抢标记 key 前缀(Lua 脚本循环内动态拼接使用)
	// 对应 Lua 中的 roundGrabbedPrefix .. roundID .. ':' .. userID
	KeyRoundGrabbedPrefix = KeyPrefix + ":round:grabbed:"
	// KeyPacketInfo 红包详情。
	KeyPacketInfo = KeyPrefix + ":packet:info:%s"
	// KeyPacketInfoPrefix 红包信息 key 前缀(Lua 脚本循环内动态拼接使用)
	KeyPacketInfoPrefix = KeyPrefix + ":packet:info:"
	// KeyGrabRecord 抢红包记录。
	KeyGrabRecord = KeyPrefix + ":grab:record:%s"
	// KeyRoundGrabRecord 轮次抢红包记录。
	KeyRoundGrabRecord = KeyPrefix + ":round:grab_record:%s:%s"

	// KeyRoundState 轮次状态。
	KeyRoundState = KeyPrefix + ":round:state:%s"
	// KeyRoundStatePrefix 轮次状态前缀（带尾随冒号）。
	KeyRoundStatePrefix = KeyPrefix + ":round:state:"

	// KeyPacketAvailablePrefix 红包可用标记前缀（Lua 脚本使用）。
	// 对应 Lua 中的 keyPrefix .. ':packet:available:' .. packetID
	KeyPacketAvailablePrefix = KeyPrefix + ":packet:available:"
	// KeyPacketAvailable 红包可用标记 key 模板。
	KeyPacketAvailable = KeyPacketAvailablePrefix + "%s"
	// KeyGlobalPacketID 全局红包ID自增计数器（Lua 脚本使用）。
	// 对应 Lua 中的 keyPrefix .. ':global:packet_id'
	KeyGlobalPacketID = KeyPrefix + ":global:packet_id"
)

// ============================================================================
// 锁相关 key
// ============================================================================

const (
	// KeyGameStartLock 游戏开始锁。
	KeyGameStartLock = KeyPrefix + ":lock:game_start:%s"
	// KeySettleLock 结算锁。
	KeySettleLock = KeyPrefix + ":lock:settle:%s:%s"
	// KeyGameEndLock 游戏结束锁。
	KeyGameEndLock = KeyPrefix + ":lock:game_end:%s"
	// KeySendPacketLock 发红包锁。
	KeySendPacketLock = KeyPrefix + ":lock:send_packet:%s:%s"
	// KeyReplaceTimeoutLock 替补超时锁。
	KeyReplaceTimeoutLock = KeyPrefix + ":lock:replace_timeout:%s:%s"
)

// ============================================================================
// 惩罚 / 超时 / 结算完成状态
// ============================================================================

const (
	// KeyPenaltyCount 惩罚计数。
	KeyPenaltyCount = KeyPrefix + ":penalty:count:%s:%s"
	// KeyPenaltyRecord 惩罚记录。
	KeyPenaltyRecord = KeyPrefix + ":penalty:record:%s:%s"
	// KeyTimeout 超时类型 key。
	KeyTimeout = KeyPrefix + ":timeout:%s"
	// KeySettlementDone 结算完成标记。
	KeySettlementDone = KeyPrefix + ":settle:done:%d"
)

// ============================================================================
// 事件幂等 key
// ============================================================================

const (
	// KeyRoomEventProcessed 房间事件已处理标记。
	KeyRoomEventProcessed = KeyPrefix + ":room:event:processed:%s"
	// KeyGameEventProcessed 游戏事件已处理标记。
	KeyGameEventProcessed = KeyPrefix + ":game:event:processed:%s"
)

// ============================================================================
// 奖励 / 利润 / 会话统计
// ============================================================================

const (
	// KeyRewardCycleStraight 连续豹子奖励周期。
	KeyRewardCycleStraight = KeyPrefix + ":reward:cycle:%s:%s:straight"
	// KeyRewardCycleLeopard 豹子奖励周期。
	KeyRewardCycleLeopard = KeyPrefix + ":reward:cycle:%s:%s:leopard"
	// KeyProfitDaily 每日利润统计。
	KeyProfitDaily = KeyPrefix + ":profit:daily:%s"
	// KeySessionPlayerTotals 会话玩家总额。
	KeySessionPlayerTotals = KeyPrefix + ":session:%s:player:totals"
)

// ============================================================================
// 机器人相关 key
// ============================================================================

const (
	// KeyRobotPoolAvailable 可用机器人账号池。
	KeyRobotPoolAvailable = KeyPrefix + ":robot:pool:available"
	// KeyRobotRoom 房间机器人集合。
	KeyRobotRoom = KeyPrefix + ":robot:room:%s"
	// KeyRobotRoomPrefix 房间机器人集合前缀（带尾随冒号）。
	KeyRobotRoomPrefix = KeyPrefix + ":robot:room:"
	// KeyRobotAssignLock 机器人分配锁。
	KeyRobotAssignLock = KeyPrefix + ":robot:assign:%d"
	// KeyRobotRoomAssignLock 房间分配限流锁。
	KeyRobotRoomAssignLock = KeyPrefix + ":robot:room_assign:%s"
	// KeyRobotRecycleCooldown 机器人回收冷却。
	KeyRobotRecycleCooldown = KeyPrefix + ":robot:recycle_cooldown:%d"
	// KeyRobotSchedulerActive 调度器活跃机器人集合。
	KeyRobotSchedulerActive = KeyPrefix + ":robot:scheduler:active"
	// KeyRobotVirtualBalance 机器人虚拟余额。
	KeyRobotVirtualBalance = KeyPrefix + ":robot:virtual_balance:%d"
	// KeyRobotVirtualBalanceDirty 虚拟余额脏数据集合。
	KeyRobotVirtualBalanceDirty = KeyPrefix + ":robot:virtual_balance:dirty"
	// KeyRobotUserIDs 机器人用户ID集合。
	KeyRobotUserIDs = KeyPrefix + ":robot:user_ids"
)

// ============================================================================
// 结算锁相关 key（settlement 层）
// ============================================================================

const (
	// KeySettleSendPacketLock 发红包结算锁。
	KeySettleSendPacketLock = KeyPrefix + ":settle:lock:send_packet:%d"
	// KeySettleRoundLock 轮次结算锁。
	KeySettleRoundLock = KeyPrefix + ":settle:lock:round:%d"
	// KeySettlePenaltyLock 惩罚结算锁。
	KeySettlePenaltyLock = KeyPrefix + ":settle:lock:penalty:%d"
	// KeyFirstRoundDeductLock 首轮扣款锁。
	KeyFirstRoundDeductLock = KeyPrefix + ":settle:lock:first_round:%d"
	// KeyLaterRoundDeductLock 后续轮扣款锁。
	KeyLaterRoundDeductLock = KeyPrefix + ":settle:lock:later_round:%d"
	// KeySystemPacketDeductLock 系统红包扣款锁。
	KeySystemPacketDeductLock = KeyPrefix + ":settle:lock:system_packet:%d"
	// KeyRefundLock 退款锁。
	KeyRefundLock = KeyPrefix + ":settle:lock:refund:%s"
	// KeyRefundApplyLock 退款申请锁。
	KeyRefundApplyLock = KeyPrefix + ":settle:lock:refund_apply:%d"
	// KeyDeductLock 扣款锁（注意：使用 ":" 分隔，禁止下划线 "_"，P1-8 修复）。
	// 旧值：cashparty:settle:lock:deduct:%d_%d_%d
	// 新值：cashparty:settle:lock:deduct:%d:%d:%d
	KeyDeductLock = KeyPrefix + ":settle:lock:deduct:%d:%d:%d"
	// KeyBillRetryLock 账单重试锁。
	KeyBillRetryLock = KeyPrefix + ":settle:lock:bill_retry:%d"
	// KeyPairBillCheckLock 配对账单检查锁。
	KeyPairBillCheckLock = KeyPrefix + ":settle:lock:pair_bill_check:%s"
	// KeyGameSettleLock 游戏结算锁。
	KeyGameSettleLock = KeyPrefix + ":settle:lock:game:%d"
	// KeyGameSettleRetryLock 游戏结算重试锁（注意：使用 ":" 分隔，禁止下划线 "_"，P1-8 修复）。
	// 旧值：cashparty:settle:lock:game_retry:%d_%d
	// 新值：cashparty:settle:lock:game_retry:%d:%d
	KeyGameSettleRetryLock = KeyPrefix + ":settle:lock:game_retry:%d:%d"
)

// ============================================================================
// 网关相关 key
// ============================================================================

const (
	// KeyGatewayConn 网关连接映射。
	KeyGatewayConn = KeyPrefix + ":gateway:conn:%s"
	// KeyGatewayKick 网关踢人通道。
	KeyGatewayKick = KeyPrefix + ":gateway:kick:%s"
	// KeyRateLimitIP IP 维度限流。
	KeyRateLimitIP = KeyPrefix + ":ratelimit:ip:%s"
	// KeyRateLimitUser 用户维度限流。
	KeyRateLimitUser = KeyPrefix + ":ratelimit:user:%s"
	// KeyRateLimitGlobal 全局限流。
	KeyRateLimitGlobal = KeyPrefix + ":ratelimit:global"
	// KeyRateLimitCmd 命令维度限流。
	KeyRateLimitCmd = KeyPrefix + ":ratelimit:cmd:%s:%s"
	// KeyGatewayLockedIP 网关锁定 IP 集合。
	KeyGatewayLockedIP = KeyPrefix + ":gateway:locked_ip:%s"
	// KeyGatewayAuthFail Auth 失败计数 key
	KeyGatewayAuthFail = KeyPrefix + ":gateway:auth_fail:%s"
	// KeyGatewayNonce 网关签名防重放 nonce key
	KeyGatewayNonce = KeyPrefix + ":gateway:nonce:%s"
)

// ============================================================================
// 雪花 ID 生成器 nodeID 分配相关 key
// ============================================================================

const (
	// KeyIDGenNodeIDSeq nodeID 分配序列号（INCR 递增）。
	// 用途：NodeAllocator 通过 INCR 获取候选 nodeID。
	KeyIDGenNodeIDSeq = KeyPrefix + ":idgen:node_id_seq"

	// KeyIDGenNodeIDAllocPrefix nodeID 分配记录前缀（带尾随冒号）。
	// 完整 key = KeyIDGenNodeIDAllocPrefix + <nodeID>，value = instanceID。
	// 用途：SET NX 抢占 nodeID，TTL 1 小时，实例宕机后自动回收。
	KeyIDGenNodeIDAllocPrefix = KeyPrefix + ":idgen:node_id:alloc:"
)

// ============================================================================
// Scheduler 锁相关 key（settlement/scheduler 与 game/scheduler 层，P1-3 修复：补齐 cashparty: 前缀）
// ============================================================================

const (
	// KeySchedulerCreditRetryLock 信用重试调度器锁。
	KeySchedulerCreditRetryLock = KeyPrefix + ":scheduler:credit_retry:lock"
	// KeySchedulerSettlementCheckLock 结算检查调度器锁。
	KeySchedulerSettlementCheckLock = KeyPrefix + ":scheduler:settlement_check:lock"
	// KeySchedulerRefundProcessLock 退款处理调度器锁。
	KeySchedulerRefundProcessLock = KeyPrefix + ":scheduler:refund_process:lock"
	// KeySchedulerGameSettleRetryLock 游戏结算重试调度器锁。
	KeySchedulerGameSettleRetryLock = KeyPrefix + ":scheduler:game_settle_retry:lock"
	// KeySchedulerGameSettleTimeoutLock 游戏结算超时调度器锁。
	KeySchedulerGameSettleTimeoutLock = KeyPrefix + ":scheduler:game_settle_timeout:lock"
	// KeySchedulerVirtualBalanceSyncLock 虚拟余额同步调度器锁。
	KeySchedulerVirtualBalanceSyncLock = KeyPrefix + ":scheduler:virtual_balance_sync:lock"
)

// ============================================================================
// 房间相关 key 工厂函数
// ============================================================================

// RoomHashKey 房间哈希表 key
func RoomHashKey(roomID string) string {
	return fmt.Sprintf(KeyRoomHash, roomID)
}

// RoomSpectatorsKey 房间观众集合 key
func RoomSpectatorsKey(roomID string) string {
	return fmt.Sprintf(KeyRoomSpectators, roomID)
}

// RoomPlayersKey 房间玩家集合 key
func RoomPlayersKey(roomID string) string {
	return fmt.Sprintf(KeyRoomPlayers, roomID)
}

// PlayerRoomKey 玩家当前所在房间映射 key
func PlayerRoomKey(userID string) string {
	return fmt.Sprintf(KeyPlayerRoom, userID)
}

// RoomNodeKey 房间所属节点 key
func RoomNodeKey(roomID string) string {
	return fmt.Sprintf(KeyRoomNode, roomID)
}

// RoomSeqKey 房间序号 key
func RoomSeqKey(date string) string {
	return fmt.Sprintf(KeyRoomSeq, date)
}

// DeadLetterQueueKey 死信队列 key
func DeadLetterQueueKey() string {
	return KeyDeadLetterQueue
}

// RoomSeatsKey 房间座位集合 key
func RoomSeatsKey(roomID string) string {
	return fmt.Sprintf(KeyRoomSeats, roomID)
}

// RoomSeatOwnerKey 房间座位归属 key
// 注意：key 中使用 ":" 分隔，禁止下划线 "_"，P0-4 Bug 修复
func RoomSeatOwnerKey(roomID string) string {
	return fmt.Sprintf(KeyRoomSeatOwner, roomID)
}

// RoomQueueKey 房间排队队列 key
func RoomQueueKey(roomID string) string {
	return fmt.Sprintf(KeyRoomQueue, roomID)
}

// SeatTimeoutKey 座位超时集合 key
func SeatTimeoutKey() string {
	return KeySeatTimeout
}

// ReadyTimeoutKey 准备超时集合 key
func ReadyTimeoutKey() string {
	return KeyReadyTimeout
}

// DisconnectTimeoutKey 断线超时集合 key
func DisconnectTimeoutKey() string {
	return KeyDisconnectTimeout
}

// RoomPlayerKey 房间内玩家信息 key
func RoomPlayerKey(roomID, userID string) string {
	return fmt.Sprintf(KeyRoomPlayer, roomID, userID)
}

// RoomNoToIDKey 房间号到房间ID映射 key
func RoomNoToIDKey(roomNo string) string {
	return fmt.Sprintf(KeyRoomNoToID, roomNo)
}

// ============================================================================
// 用户相关 key 工厂函数
// ============================================================================

// UserByUserIDKey 按 user_id 查询用户 key
func UserByUserIDKey(userID string) string {
	return fmt.Sprintf(KeyUserByUserID, userID)
}

// UserByIdKey 按 id 查询用户 key
func UserByIdKey(id string) string {
	return fmt.Sprintf(KeyUserById, id)
}

// ============================================================================
// 轮次/红包相关 key 工厂函数
// ============================================================================

// RoundPacketsKey 轮次红包列表 key
func RoundPacketsKey(roundID string) string {
	return fmt.Sprintf(KeyRoundPackets, roundID)
}

// RoundAvailablePacketsKey 轮次可用红包列表 key
func RoundAvailablePacketsKey(roundID string) string {
	return fmt.Sprintf(KeyRoundAvailablePackets, roundID)
}

// RoundGrabbersKey 轮次抢红包者集合 key
func RoundGrabbersKey(roundID string) string {
	return fmt.Sprintf(KeyRoundGrabbers, roundID)
}

// RoundRewardKey 轮次奖励信息 key
func RoundRewardKey(roundID string) string {
	return fmt.Sprintf(KeyRoundReward, roundID)
}

// UserGrabbedKey 用户抢过红包标记 key
func UserGrabbedKey(roundID, userID string) string {
	return fmt.Sprintf(KeyUserGrabbed, roundID, userID)
}

// RoundGrabbedKey 生成玩家本轮已抢标记 key
func RoundGrabbedKey(roundID, userID string) string {
	return fmt.Sprintf(KeyRoundGrabbed, roundID, userID)
}

// PacketInfoKey 红包详情 key
func PacketInfoKey(packetID string) string {
	return fmt.Sprintf(KeyPacketInfo, packetID)
}

// GrabRecordKey 抢红包记录 key
func GrabRecordKey(packetID string) string {
	return fmt.Sprintf(KeyGrabRecord, packetID)
}

// RoundGrabRecordKey 轮次抢红包记录 key
func RoundGrabRecordKey(roundID, userID string) string {
	return fmt.Sprintf(KeyRoundGrabRecord, roundID, userID)
}

// RoundStateKey 轮次状态 key
func RoundStateKey(roundID string) string {
	return fmt.Sprintf(KeyRoundState, roundID)
}

// PacketAvailableKey 红包可用标记 key（供 Go 侧与 Lua 脚本共享）
func PacketAvailableKey(packetID string) string {
	return fmt.Sprintf(KeyPacketAvailable, packetID)
}

// GlobalPacketIDKey 全局红包ID计数器 key（供 Go 侧与 Lua 脚本共享）
func GlobalPacketIDKey() string {
	return KeyGlobalPacketID
}

// ============================================================================
// 锁相关 key 工厂函数
// ============================================================================

// GameStartLockKey 游戏开始锁 key
func GameStartLockKey(roomID string) string {
	return fmt.Sprintf(KeyGameStartLock, roomID)
}

// SettleLockKey 结算锁 key
func SettleLockKey(roomID, roundID string) string {
	return fmt.Sprintf(KeySettleLock, roomID, roundID)
}

// GameEndLockKey 游戏结束锁 key
func GameEndLockKey(roomID string) string {
	return fmt.Sprintf(KeyGameEndLock, roomID)
}

// SendPacketLockKey 发红包锁 key
func SendPacketLockKey(roomID, userID string) string {
	return fmt.Sprintf(KeySendPacketLock, roomID, userID)
}

// ReplaceTimeoutLockKey 替补超时锁 key
func ReplaceTimeoutLockKey(roomID, userID string) string {
	return fmt.Sprintf(KeyReplaceTimeoutLock, roomID, userID)
}

// ============================================================================
// 惩罚 / 超时 / 结算完成状态 工厂函数
// ============================================================================

// PenaltyCountKey 惩罚计数 key
func PenaltyCountKey(roomID, userID string) string {
	return fmt.Sprintf(KeyPenaltyCount, roomID, userID)
}

// PenaltyRecordKey 惩罚记录 key
func PenaltyRecordKey(roomID, userID string) string {
	return fmt.Sprintf(KeyPenaltyRecord, roomID, userID)
}

// TimeoutKey 超时类型 key
func TimeoutKey(timeoutType string) string {
	return fmt.Sprintf(KeyTimeout, timeoutType)
}

// SettlementDoneKey 结算完成标记 key
func SettlementDoneKey(roundID int64) string {
	return fmt.Sprintf(KeySettlementDone, roundID)
}

// ============================================================================
// 事件幂等 key 工厂函数
// ============================================================================

// RoomEventProcessedKey 房间事件已处理标记 key
func RoomEventProcessedKey(eventID string) string {
	return fmt.Sprintf(KeyRoomEventProcessed, eventID)
}

// GameEventProcessedKey 游戏事件已处理标记 key
func GameEventProcessedKey(traceID string) string {
	return fmt.Sprintf(KeyGameEventProcessed, traceID)
}

// ============================================================================
// 奖励 / 利润 / 会话统计 工厂函数
// ============================================================================

// RewardCycleStraightKey 连续豹子奖励周期 key
func RewardCycleStraightKey(roomID, sessionID string) string {
	return fmt.Sprintf(KeyRewardCycleStraight, roomID, sessionID)
}

// RewardCycleLeopardKey 豹子奖励周期 key
func RewardCycleLeopardKey(roomID, sessionID string) string {
	return fmt.Sprintf(KeyRewardCycleLeopard, roomID, sessionID)
}

// ProfitDailyKey 每日利润统计 key
func ProfitDailyKey(date string) string {
	return fmt.Sprintf(KeyProfitDaily, date)
}

// SessionPlayerTotalsKey 会话玩家总额 key
func SessionPlayerTotalsKey(sessionID string) string {
	return fmt.Sprintf(KeySessionPlayerTotals, sessionID)
}

// ============================================================================
// 机器人相关 key 工厂函数
// ============================================================================

// RobotPoolAvailableKey 可用机器人账号池 key
func RobotPoolAvailableKey() string {
	return KeyRobotPoolAvailable
}

// RobotRoomKey 房间机器人集合 key
func RobotRoomKey(roomID string) string {
	return fmt.Sprintf(KeyRobotRoom, roomID)
}

// RobotAssignLockKey 机器人分配锁 key
func RobotAssignLockKey(robotUserID int64) string {
	return fmt.Sprintf(KeyRobotAssignLock, robotUserID)
}

// RobotRoomAssignLockKey 房间分配限流锁 key
func RobotRoomAssignLockKey(roomID string) string {
	return fmt.Sprintf(KeyRobotRoomAssignLock, roomID)
}

// RobotRecycleCooldownKey 机器人回收冷却 key
func RobotRecycleCooldownKey(userID int64) string {
	return fmt.Sprintf(KeyRobotRecycleCooldown, userID)
}

// RobotSchedulerActiveKey 调度器活跃机器人集合 key
func RobotSchedulerActiveKey() string {
	return KeyRobotSchedulerActive
}

// RobotVirtualBalanceKey 机器人虚拟余额 key
func RobotVirtualBalanceKey(userID int64) string {
	return fmt.Sprintf(KeyRobotVirtualBalance, userID)
}

// RobotVirtualBalanceDirtyKey 虚拟余额脏数据集合 key
func RobotVirtualBalanceDirtyKey() string {
	return KeyRobotVirtualBalanceDirty
}

// RobotUserIDsKey 机器人用户ID集合 key
func RobotUserIDsKey() string {
	return KeyRobotUserIDs
}

// ============================================================================
// 结算锁相关 key 工厂函数（settlement 层）
// ============================================================================

// SettleSendPacketLockKey 发红包结算锁 key（settlement 层，与 game 层的 SendPacketLockKey 区分）
func SettleSendPacketLockKey(roundID int64) string {
	return fmt.Sprintf(KeySettleSendPacketLock, roundID)
}

// SettleRoundLockKey 轮次结算锁 key
func SettleRoundLockKey(roundID int64) string {
	return fmt.Sprintf(KeySettleRoundLock, roundID)
}

// PenaltyLockKey 惩罚结算锁 key
func PenaltyLockKey(roundID int64) string {
	return fmt.Sprintf(KeySettlePenaltyLock, roundID)
}

// FirstRoundDeductLockKey 首轮扣款锁 key
func FirstRoundDeductLockKey(sessionID int64) string {
	return fmt.Sprintf(KeyFirstRoundDeductLock, sessionID)
}

// LaterRoundDeductLockKey 后续轮扣款锁 key
func LaterRoundDeductLockKey(roundID int64) string {
	return fmt.Sprintf(KeyLaterRoundDeductLock, roundID)
}

// SystemPacketDeductLockKey 系统红包扣款锁 key
func SystemPacketDeductLockKey(roundID int64) string {
	return fmt.Sprintf(KeySystemPacketDeductLock, roundID)
}

// RefundLockKey 退款锁 key
func RefundLockKey(refundOrderNo string) string {
	return fmt.Sprintf(KeyRefundLock, refundOrderNo)
}

// RefundApplyLockKey 退款申请锁 key
func RefundApplyLockKey(billID int64) string {
	return fmt.Sprintf(KeyRefundApplyLock, billID)
}

// DeductLockKey 扣款锁 key（P1-8 修复：使用 ":" 分隔）
func DeductLockKey(roundID int64, billType int, userID int64) string {
	return fmt.Sprintf(KeyDeductLock, roundID, billType, userID)
}

// BillRetryLockKey 账单重试锁 key
func BillRetryLockKey(billID int64) string {
	return fmt.Sprintf(KeyBillRetryLock, billID)
}

// PairBillCheckLockKey 配对账单检查锁 key
func PairBillCheckLockKey(roundTraceID string) string {
	return fmt.Sprintf(KeyPairBillCheckLock, roundTraceID)
}

// GameSettleLockKey 游戏结算锁 key
func GameSettleLockKey(sessionID int64) string {
	return fmt.Sprintf(KeyGameSettleLock, sessionID)
}

// GameSettleRetryLockKey 游戏结算重试锁 key（P1-8 修复：使用 ":" 分隔）
func GameSettleRetryLockKey(sessionID int64, userID int64) string {
	return fmt.Sprintf(KeyGameSettleRetryLock, sessionID, userID)
}

// ============================================================================
// 网关相关 key 工厂函数
// ============================================================================

// GatewayConnKey 网关连接映射 key
func GatewayConnKey(userID string) string {
	return fmt.Sprintf(KeyGatewayConn, userID)
}

// GatewayKickKey 网关踢人通道 key
func GatewayKickKey(nodeID string) string {
	return fmt.Sprintf(KeyGatewayKick, nodeID)
}

// RateLimitIPKey IP 维度限流 key
func RateLimitIPKey(ip string) string {
	return fmt.Sprintf(KeyRateLimitIP, ip)
}

// RateLimitUserKey 用户维度限流 key
func RateLimitUserKey(userID string) string {
	return fmt.Sprintf(KeyRateLimitUser, userID)
}

// RateLimitGlobalKey 全局限流 key
func RateLimitGlobalKey() string {
	return KeyRateLimitGlobal
}

// RateLimitCmdKey 命令维度限流 key
func RateLimitCmdKey(cmd, userID string) string {
	return fmt.Sprintf(KeyRateLimitCmd, cmd, userID)
}

// GatewayLockedIPKey 网关锁定 IP 集合 key
func GatewayLockedIPKey(ip string) string {
	return fmt.Sprintf(KeyGatewayLockedIP, ip)
}

// GatewayAuthFailKey 生成 Auth 失败计数 key
func GatewayAuthFailKey(ip string) string {
	return fmt.Sprintf(KeyGatewayAuthFail, ip)
}

// GatewayNonceKey 生成网关签名防重放 nonce key
func GatewayNonceKey(nonce string) string {
	return fmt.Sprintf(KeyGatewayNonce, nonce)
}

// ============================================================================
// 雪花 ID 生成器 nodeID 分配相关 key 工厂函数
// ============================================================================

// IDGenNodeIDAllocKey 生成 nodeID 分配记录 key。
// 用途：NodeAllocator 通过 SET NX 抢占 nodeID，TTL 1 小时。
// 完整 key 形如 cashparty:idgen:node_id:alloc:5
func IDGenNodeIDAllocKey(nodeID int64) string {
	return fmt.Sprintf("%s%d", KeyIDGenNodeIDAllocPrefix, nodeID)
}
