// Package rediskeys 是项目 Redis key 的单一真相源（Single Source of Truth）。
// 所有跨服务的 Redis key 常量与工厂函数 MUST 集中在此处，禁止散落在各服务的 keys.go 中重复定义。
// 规约参考 CODING_STANDARD.md §6.2 与 §16 SC-5 / SC-10。
//
// 命名约定：
//   - 所有 key MUST 以 KeyPrefix = "cashparty" 开头
//   - 多参数 key 的分隔符 MUST 统一为 ":"，禁止下划线 "_"
//   - Prefix 常量 MUST 带尾随冒号（如 "cashparty:room:hash:"），调用方一律 + "*"
//   - 工厂函数命名规范：XxxKey(args...) string
//
// Hash Tag 规约（Cluster 兼容）：
//   - 含变量 ID 的 key MUST 在 cashparty: 前缀后以 {ID} 形式嵌入 hash tag
//   - 房间级数据用 {roomID} tag，格式：cashparty:{roomID}:category:sub
//   - 用户级数据用 {userID} tag，格式：cashparty:{userID}:category:sub
//   - 全局 key（无变量 ID）不需 hash tag，格式：cashparty:category:sub
package rediskeys

import "fmt"

// KeyPrefix 是所有 Redis key 的命名空间前缀。
// 任何新增 key MUST 以 KeyPrefix 开头，禁止裸用 "cashparty" 字面量。
const KeyPrefix = "cashparty"

// ============================================================================
// 房间相关 key（{roomID} hash tag，Cluster 兼容）
// ============================================================================

const (
	// KeyRoomHash 房间哈希表 key 模板（{roomID} hash tag）。
	KeyRoomHash = KeyPrefix + ":{%s}:room:hash"
	// KeyRoomHashPrefix 房间哈希表 SCAN 匹配模式（通配符匹配所有 roomID）。
	KeyRoomHashPrefix = KeyPrefix + ":*:room:hash"

	// KeyRoomSpectators 房间观众集合 key 模板（{roomID} hash tag）。
	KeyRoomSpectators = KeyPrefix + ":{%s}:room:spectators"

	// KeyRoomPlayers 房间玩家集合 key 模板（{roomID} hash tag）。
	KeyRoomPlayers = KeyPrefix + ":{%s}:room:players"

	// KeyPlayerRoomInRoom 房间内玩家映射（Lua 内部房间级清理用，{roomID} hash tag）。
	// 替代旧的全局 KeyPlayerRoom，用于 Lua 脚本中房间级原子清理。
	KeyPlayerRoomInRoom = KeyPrefix + ":{%s}:player:room:%s"

	// KeyRoomNode 房间所属节点（{roomID} hash tag）。
	KeyRoomNode = KeyPrefix + ":{%s}:room:node"
	// KeyRoomSeq 房间序号（按日期）。
	KeyRoomSeq = KeyPrefix + ":room:seq:%s"
	// KeyDeadLetterQueue DB 死信队列。
	KeyDeadLetterQueue = KeyPrefix + ":db:dead_letter"
	// KeyRoomSeats 房间座位集合（{roomID} hash tag）。
	KeyRoomSeats = KeyPrefix + ":{%s}:room:seats"
	// KeyRoomSeatOwner 房间座位归属（{roomID} hash tag，注意：使用 ":" 分隔，禁止下划线 "_owner"）。
	KeyRoomSeatOwner = KeyPrefix + ":{%s}:room:seat:owner"
	// KeyRoomQueue 房间排队队列（{roomID} hash tag）。
	KeyRoomQueue = KeyPrefix + ":{%s}:room:queue"
	// KeySeatTimeout 座位超时集合。
	KeySeatTimeout = KeyPrefix + ":room:seat:timeout"
	// KeyReadyTimeout 准备超时集合。
	KeyReadyTimeout = KeyPrefix + ":room:ready:timeout"
	// KeyDisconnectTimeout 断线超时集合。
	KeyDisconnectTimeout = KeyPrefix + ":room:disconnect:timeout"
	// KeyRoomPlayer 房间内玩家信息（{roomID} hash tag）。
	KeyRoomPlayer = KeyPrefix + ":{%s}:room:player:%s"
	// KeyRoomNoToID 房间号到房间ID的映射（{roomNo} hash tag）。
	KeyRoomNoToID = KeyPrefix + ":{%s}:room:no_to_id"
	// KeyRoomEventProcessed 房间事件已处理标记（{roomID} hash tag）。
	KeyRoomEventProcessed = KeyPrefix + ":{%s}:room:event:processed:%s"
)

// ============================================================================
// 用户相关 key（{userID} hash tag，Cluster 兼容）
// ============================================================================

const (
	// KeyCurrentRoom 用户当前所在房间映射（跨房间反查，{userID} hash tag）。
	// 替代旧的全局 KeyPlayerRoom，用于网关反查与跨房间防重入检查。
	KeyCurrentRoom = KeyPrefix + ":{%s}:current_room"

	// KeyUserByUserID 按 user_id 查询用户（{userID} hash tag）。
	KeyUserByUserID = KeyPrefix + ":{%s}:user:user_id"
	// KeyUserById 按 id 查询用户（{userID} hash tag）。
	KeyUserById = KeyPrefix + ":{%s}:user:id"
)

// ============================================================================
// 轮次/红包相关 key（{roomID} hash tag，Cluster 兼容）
// ============================================================================

const (
	// KeyRoundPackets 轮次红包列表（{roomID} hash tag）。
	KeyRoundPackets = KeyPrefix + ":{%s}:round:packets:%s"
	// KeyRoundAvailablePackets 轮次可用红包列表（{roomID} hash tag）。
	KeyRoundAvailablePackets = KeyPrefix + ":{%s}:round:available_packets:%s"
	// KeyRoundGrabbers 轮次抢红包者集合（{roomID} hash tag）。
	KeyRoundGrabbers = KeyPrefix + ":{%s}:round:grabbers:%s"
	// KeyRoundReward 轮次奖励信息（{roomID} hash tag）。
	KeyRoundReward = KeyPrefix + ":{%s}:round:reward:%s"
	// KeyUserGrabbed 用户抢过红包标记（{roomID} hash tag）。
	KeyUserGrabbed = KeyPrefix + ":{%s}:round:grabbed:%s:%s"
	// KeyRoundGrabbed 玩家本轮已抢标记(Lua 脚本使用，{roomID} hash tag)。
	// 对应 Lua 中的 roundGrabbedPrefix .. roundID .. ':' .. userID
	KeyRoundGrabbed = KeyPrefix + ":{%s}:round:grabbed:%s:%s"
	// KeyGrabRecord 抢红包记录（{roomID} hash tag）。
	KeyGrabRecord = KeyPrefix + ":{%s}:grab:record:%s"
	// KeyRoundGrabRecord 轮次抢红包记录（{roomID} hash tag）。
	KeyRoundGrabRecord = KeyPrefix + ":{%s}:round:grab_record:%s:%s"

	// KeyRoundState 轮次状态（{roomID} hash tag）。
	KeyRoundState = KeyPrefix + ":{%s}:round:state:%s"
	// KeyRoundStatePrefix 轮次状态 key 前缀（带尾随冒号，Lua 脚本动态拼接用）。
	// 完整 key = fmt.Sprintf(KeyRoundStatePrefix, roomID) + roundID。
	KeyRoundStatePrefix = KeyPrefix + ":{%s}:round:state:"

	// KeyPacketInfo 红包详情（{roomID} hash tag）。
	KeyPacketInfo = KeyPrefix + ":{%s}:packet:info:%s"
	// KeyPacketInfoPrefix 红包详情 key 前缀（带尾随冒号，Lua 脚本动态拼接用）。
	// 完整 key = fmt.Sprintf(KeyPacketInfoPrefix, roomID) + packetID。
	KeyPacketInfoPrefix = KeyPrefix + ":{%s}:packet:info:"

	// KeyPacketAvailable 红包可用标记 key 模板（{roomID} hash tag）。
	KeyPacketAvailable = KeyPrefix + ":{%s}:packet:available:%s"
	// KeyPacketAvailablePrefix 红包可用标记 key 前缀（带尾随冒号，Lua 脚本动态拼接用）。
	// 完整 key = fmt.Sprintf(KeyPacketAvailablePrefix, roomID) + packetID。
	KeyPacketAvailablePrefix = KeyPrefix + ":{%s}:packet:available:"

	// KeyRoundGrabbedPrefix 玩家本轮已抢标记 key 前缀（带尾随冒号，Lua 脚本动态拼接用）。
	// 完整 key = fmt.Sprintf(KeyRoundGrabbedPrefix, roomID) + roundID + ":" + userID。
	KeyRoundGrabbedPrefix = KeyPrefix + ":{%s}:round:grabbed:"

	// KeyRoomPacketIDSeq 房间内红包ID自增序列（{roomID} hash tag，替代全局计数器）。
	// Deprecated: packetID 已改为雪花 ID 生成（idgen），不再使用 Redis INCR 序列。
	// 保留常量仅用于兼容历史 Redis 残留 key 的清理脚本，新代码禁止使用。
	KeyRoomPacketIDSeq = KeyPrefix + ":{%s}:packet_id_seq"

	// KeyGlobalPacketID 全局红包ID自增计数器（已废弃）。
	// Deprecated: 先后被 KeyRoomPacketIDSeq 和雪花 ID 生成器替代。
	KeyGlobalPacketID = KeyPrefix + ":global:packet_id"

	// KeyPlayerRoom 玩家当前所在房间映射（已废弃）。
	// Deprecated: 使用 KeyCurrentRoom（跨房间反查）或 KeyPlayerRoomInRoom（房间内清理）替代。
	KeyPlayerRoom = KeyPrefix + ":player:room:%s"
)

// ============================================================================
// 锁相关 key（{roomID} hash tag，Cluster 兼容）
// ============================================================================

const (
	// KeyGameStartLock 游戏开始锁（{roomID} hash tag）。
	KeyGameStartLock = KeyPrefix + ":{%s}:lock:game_start"
	// KeySettleLock 结算锁（{roomID} hash tag）。
	KeySettleLock = KeyPrefix + ":{%s}:lock:settle:%s"
	// KeyGameEndLock 游戏结束锁（{roomID} hash tag）。
	KeyGameEndLock = KeyPrefix + ":{%s}:lock:game_end"
	// KeySendPacketLock 发红包锁（{roomID} hash tag）。
	KeySendPacketLock = KeyPrefix + ":{%s}:lock:send_packet:%s"
	// KeyReplaceTimeoutLock 替补超时锁（{roomID} hash tag）。
	KeyReplaceTimeoutLock = KeyPrefix + ":{%s}:lock:replace_timeout:%s"
)

// ============================================================================
// 惩罚 / 超时 / 结算完成状态（{roomID} hash tag，Cluster 兼容）
// ============================================================================

const (
	// KeyPenaltyCount 惩罚计数（{roomID} hash tag）。
	KeyPenaltyCount = KeyPrefix + ":{%s}:penalty:count:%s"
	// KeyPenaltyRecord 惩罚记录（{roomID} hash tag）。
	KeyPenaltyRecord = KeyPrefix + ":{%s}:penalty:record:%s"
	// KeyTimeout 超时类型 key。
	KeyTimeout = KeyPrefix + ":timeout:%s"
	// KeySettlementDone 结算完成标记（{roomID} hash tag）。
	KeySettlementDone = KeyPrefix + ":{%s}:settle:done:%d"
)

// ============================================================================
// 事件幂等 key（{roomID} hash tag，Cluster 兼容）
// ============================================================================

// KeyGameEventProcessed 游戏事件已处理标记。
const KeyGameEventProcessed = KeyPrefix + ":game:event:processed:%s"

// ============================================================================
// 奖励 / 利润 / 会话统计（{roomID} hash tag，Cluster 兼容）
// ============================================================================

const (
	// KeyRewardCycleStraight 连续豹子奖励周期（{roomID} hash tag）。
	KeyRewardCycleStraight = KeyPrefix + ":{%s}:reward:cycle:%s:straight"
	// KeyRewardCycleLeopard 豹子奖励周期（{roomID} hash tag）。
	KeyRewardCycleLeopard = KeyPrefix + ":{%s}:reward:cycle:%s:leopard"
	// KeyProfitDaily 每日利润统计。
	KeyProfitDaily = KeyPrefix + ":profit:daily:%s"
	// KeySessionPlayerTotals 会话玩家总额（{roomID} hash tag）。
	KeySessionPlayerTotals = KeyPrefix + ":{%s}:session:%s:player:totals"
)

// ============================================================================
// 机器人相关 key
// ============================================================================

const (
	// KeyRobotPoolAvailable 可用机器人账号池（全局，单 key 操作）。
	KeyRobotPoolAvailable = KeyPrefix + ":robot:pool:available"
	// KeyRobotRoom 房间机器人集合（{roomID} hash tag）。
	KeyRobotRoom = KeyPrefix + ":{%s}:robot:room"
	// KeyRobotRoomPrefix 房间机器人集合 SCAN 匹配模式。
	KeyRobotRoomPrefix = KeyPrefix + ":*:robot:room"
	// KeyRobotAssignLock 机器人分配锁。
	KeyRobotAssignLock = KeyPrefix + ":robot:assign:%d"
	// KeyRobotRoomAssignLock 房间分配限流锁（{roomID} hash tag）。
	KeyRobotRoomAssignLock = KeyPrefix + ":{%s}:robot:room_assign"
	// KeyRobotRecycleCooldown 机器人回收冷却。
	KeyRobotRecycleCooldown = KeyPrefix + ":robot:recycle_cooldown:%d"
	// KeyRobotSchedulerActive 调度器活跃机器人集合（全局）。
	KeyRobotSchedulerActive = KeyPrefix + ":robot:scheduler:active"
	// KeyRobotVirtualBalance 机器人虚拟余额（{userID} hash tag）。
	KeyRobotVirtualBalance = KeyPrefix + ":{%d}:robot:virtual_balance"
	// KeyRobotVirtualBalanceDirty 虚拟余额脏数据集合（已废弃，全局 SET）。
	// Deprecated: 使用 KeyRobotDirty 替代，per-user 标记避免 Cluster 跨 slot。
	KeyRobotVirtualBalanceDirty = KeyPrefix + ":robot:virtual_balance:dirty"
	// KeyRobotDirty 机器人虚拟余额脏标记（per-user，{userID} hash tag）。
	// 替代全局 KeyRobotVirtualBalanceDirty SET，与余额 key 同 slot。
	KeyRobotDirty = KeyPrefix + ":{%d}:robot:dirty"
	// KeyRobotUserIDs 机器人用户ID集合（全局，单 key 操作）。
	KeyRobotUserIDs = KeyPrefix + ":robot:user_ids"
)

// ============================================================================
// 结算锁相关 key（settlement 层，全局单 key 操作）
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
	KeyDeductLock = KeyPrefix + ":settle:lock:deduct:%d:%d:%d"
	// KeyBillRetryLock 账单重试锁。
	KeyBillRetryLock = KeyPrefix + ":settle:lock:bill_retry:%d"
	// KeyPairBillCheckLock 配对账单检查锁。
	KeyPairBillCheckLock = KeyPrefix + ":settle:lock:pair_bill_check:%s"
	// KeyGameSettleLock 游戏结算锁。
	KeyGameSettleLock = KeyPrefix + ":settle:lock:game:%d"
	// KeyGameSettleRetryLock 游戏结算重试锁（注意：使用 ":" 分隔，禁止下划线 "_"，P1-8 修复）。
	KeyGameSettleRetryLock = KeyPrefix + ":settle:lock:game_retry:%d:%d"
	// KeyPenaltyDeductLock 罚款扣款锁（按 userID + roundTraceID 粒度，防止重复扣款）。
	KeyPenaltyDeductLock = KeyPrefix + ":settle:lock:penalty_deduct:%d:%s"
	// KeySubstituteFeeDeductLock 替补费扣款锁（按 userID + traceID 粒度，防止重复扣款）。
	KeySubstituteFeeDeductLock = KeyPrefix + ":settle:lock:substitute_fee:%d:%s"
)

// ============================================================================
// 网关相关 key
// ============================================================================

const (
	// KeyGatewayConn 网关连接映射（{userID} hash tag）。
	KeyGatewayConn = KeyPrefix + ":{%s}:gateway:conn"
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
// 房间相关 key 工厂函数（{roomID} hash tag）
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

// CurrentRoomKey 用户当前所在房间映射 key（跨房间反查）。
// 替代旧的 PlayerRoomKey，用于网关反查与跨房间防重入检查。
func CurrentRoomKey(userID string) string {
	return fmt.Sprintf(KeyCurrentRoom, userID)
}

// PlayerRoomInRoomKey 房间内玩家映射 key（Lua 内部房间级清理用）。
// 替代旧的 PlayerRoomKey，用于 Lua 脚本中房间级原子清理（{roomID} hash tag）。
func PlayerRoomInRoomKey(roomID, userID string) string {
	return fmt.Sprintf(KeyPlayerRoomInRoom, roomID, userID)
}

// PlayerRoomKey 玩家当前所在房间映射 key
//
// Deprecated: 使用 CurrentRoomKey（跨房间反查）或 PlayerRoomInRoomKey（房间内清理）替代。
// Cluster 模式下原 key 是 userID 维度，与 roomID 维度 Lua key 跨 slot。
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
// 用户相关 key 工厂函数（{userID} hash tag）
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
// 轮次/红包相关 key 工厂函数（{roomID} hash tag）
// ============================================================================

// RoundStatePrefix 轮次状态 key 前缀（带 {roomID} hash tag，Lua 脚本动态拼接用）。
// 返回值形如 cashparty:{room123}:round:state:，调用方拼接 roundID。
func RoundStatePrefix(roomID string) string {
	return fmt.Sprintf(KeyRoundStatePrefix, roomID)
}

// PacketInfoPrefix 红包详情 key 前缀（带 {roomID} hash tag，Lua 脚本动态拼接用）。
// 返回值形如 cashparty:{room123}:packet:info:，调用方拼接 packetID。
func PacketInfoPrefix(roomID string) string {
	return fmt.Sprintf(KeyPacketInfoPrefix, roomID)
}

// PacketAvailablePrefix 红包可用标记 key 前缀（带 {roomID} hash tag，Lua 脚本动态拼接用）。
// 返回值形如 cashparty:{room123}:packet:available:，调用方拼接 packetID。
func PacketAvailablePrefix(roomID string) string {
	return fmt.Sprintf(KeyPacketAvailablePrefix, roomID)
}

// RoundGrabbedPrefix 玩家本轮已抢标记 key 前缀（带 {roomID} hash tag，Lua 脚本动态拼接用）。
// 返回值形如 cashparty:{room123}:round:grabbed:，调用方拼接 roundID:userID。
func RoundGrabbedPrefix(roomID string) string {
	return fmt.Sprintf(KeyRoundGrabbedPrefix, roomID)
}

// RoundPacketsKey 轮次红包列表 key
func RoundPacketsKey(roomID, roundID string) string {
	return fmt.Sprintf(KeyRoundPackets, roomID, roundID)
}

// RoundAvailablePacketsKey 轮次可用红包列表 key
func RoundAvailablePacketsKey(roomID, roundID string) string {
	return fmt.Sprintf(KeyRoundAvailablePackets, roomID, roundID)
}

// RoundGrabbersKey 轮次抢红包者集合 key
func RoundGrabbersKey(roomID, roundID string) string {
	return fmt.Sprintf(KeyRoundGrabbers, roomID, roundID)
}

// RoundRewardKey 轮次奖励信息 key
func RoundRewardKey(roomID, roundID string) string {
	return fmt.Sprintf(KeyRoundReward, roomID, roundID)
}

// UserGrabbedKey 用户抢过红包标记 key
func UserGrabbedKey(roomID, roundID, userID string) string {
	return fmt.Sprintf(KeyUserGrabbed, roomID, roundID, userID)
}

// RoundGrabbedKey 生成玩家本轮已抢标记 key
func RoundGrabbedKey(roomID, roundID, userID string) string {
	return fmt.Sprintf(KeyRoundGrabbed, roomID, roundID, userID)
}

// PacketInfoKey 红包详情 key
func PacketInfoKey(roomID, packetID string) string {
	return fmt.Sprintf(KeyPacketInfo, roomID, packetID)
}

// GrabRecordKey 抢红包记录 key
func GrabRecordKey(roomID, packetID string) string {
	return fmt.Sprintf(KeyGrabRecord, roomID, packetID)
}

// RoundGrabRecordKey 轮次抢红包记录 key
func RoundGrabRecordKey(roomID, roundID, userID string) string {
	return fmt.Sprintf(KeyRoundGrabRecord, roomID, roundID, userID)
}

// RoundStateKey 轮次状态 key
func RoundStateKey(roomID, roundID string) string {
	return fmt.Sprintf(KeyRoundState, roomID, roundID)
}

// PacketAvailableKey 红包可用标记 key（供 Go 侧与 Lua 脚本共享）
func PacketAvailableKey(roomID, packetID string) string {
	return fmt.Sprintf(KeyPacketAvailable, roomID, packetID)
}

// RoomPacketIDSeqKey 房间内红包ID序列 key（替代全局计数器，按房间分片）。
func RoomPacketIDSeqKey(roomID string) string {
	return fmt.Sprintf(KeyRoomPacketIDSeq, roomID)
}

// GlobalPacketIDKey 全局红包ID计数器 key
//
// Deprecated: 使用 RoomPacketIDKey 替代，按房间分片避免 Cluster 跨 slot。
func GlobalPacketIDKey() string {
	return KeyGlobalPacketID
}

// ============================================================================
// 锁相关 key 工厂函数（{roomID} hash tag）
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
func SettlementDoneKey(roomID string, roundID int64) string {
	return fmt.Sprintf(KeySettlementDone, roomID, roundID)
}

// ============================================================================
// 事件幂等 key 工厂函数
// ============================================================================

// RoomEventProcessedKey 房间事件已处理标记 key
func RoomEventProcessedKey(roomID, eventID string) string {
	return fmt.Sprintf(KeyRoomEventProcessed, roomID, eventID)
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
func SessionPlayerTotalsKey(roomID, sessionID string) string {
	return fmt.Sprintf(KeySessionPlayerTotals, roomID, sessionID)
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

// RobotVirtualBalanceKey 机器人虚拟余额 key（{userID} hash tag）
func RobotVirtualBalanceKey(userID int64) string {
	return fmt.Sprintf(KeyRobotVirtualBalance, userID)
}

// RobotDirtyKey 机器人虚拟余额脏标记 key（per-user，{userID} hash tag）。
// 替代全局 RobotVirtualBalanceDirtyKey，与余额 key 同 slot。
func RobotDirtyKey(userID int64) string {
	return fmt.Sprintf(KeyRobotDirty, userID)
}

// RobotVirtualBalanceDirtyKey 虚拟余额脏数据集合 key
//
// Deprecated: 使用 RobotDirtyKey 替代，per-user 标记避免 Cluster 跨 slot。
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

// PenaltyDeductLockKey 罚款扣款锁 key（按 userID + roundTraceID 粒度，防止重复扣款）
func PenaltyDeductLockKey(userID int64, roundTraceID string) string {
	return fmt.Sprintf(KeyPenaltyDeductLock, userID, roundTraceID)
}

// SubstituteFeeDeductLockKey 替补费扣款锁 key（按 userID + traceID 粒度，防止重复扣款）
func SubstituteFeeDeductLockKey(userID int64, traceID string) string {
	return fmt.Sprintf(KeySubstituteFeeDeductLock, userID, traceID)
}

// ============================================================================
// 网关相关 key 工厂函数
// ============================================================================

// GatewayConnKey 网关连接映射 key（{userID} hash tag）
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
