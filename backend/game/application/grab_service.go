package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/common/utils"
	"github.com/cashparty/backend/game/domain"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/domain/round"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
)

type GrabService struct {
	redis       cRedis.RedisClient
	packetCache repository.PacketCacheRepository
	idGen       idgen.IDGenerator
	grabTimeout int64
	sendTimeout int64
	redisTTL    config.RedisTTLConfig
}

func NewGrabService(redis cRedis.RedisClient, packetCache repository.PacketCacheRepository, idGen idgen.IDGenerator, grabTimeout, sendTimeout time.Duration, redisTTL config.RedisTTLConfig) *GrabService {
	return &GrabService{
		redis:       redis,
		packetCache: packetCache,
		idGen:       idGen,
		grabTimeout: int64(grabTimeout.Seconds()),
		sendTimeout: int64(sendTimeout.Seconds()),
		redisTTL:    redisTTL,
	}
}

func (s *GrabService) GrabPacket(ctx context.Context, roomID, roundID, userID, packetID string) (*round.GrabResult, error) {
	keys := []string{
		rediskeys.RoundAvailablePacketsKey(roomID, roundID),
		rediskeys.UserGrabbedKey(roomID, roundID, userID),
		rediskeys.RoundGrabbersKey(roomID, roundID),
		rediskeys.RoundStateKey(roomID, roundID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.PacketInfoKey(roomID, packetID),
		rediskeys.PacketAvailableKey(roomID, packetID),
	}

	args := []interface{}{
		userID,
		time.Now().Unix(),
		s.grabTimeout,
		roomID,
		packetID,
		int64(s.redisTTL.PacketDataTTL.Seconds()),
	}

	res, err := scripts.GrabPacket.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		logger.Error("grab packet lua failed", "error", err, "room_id", roomID, "round_id", roundID, "user_id", userID)
		return nil, err
	}

	code := converter.ParseInt(res[0])
	if code != 0 {
		luaErr := domain.MapLuaError(code)
		logger.Warn("grab packet failed", "lua_code", code, "room_id", roomID, "user_id", userID)
		return nil, luaErr
	}

	result := &round.GrabResult{
		PacketID: converter.ParseString(res[1]),
		Amount:   converter.ParseInt64(res[2]),
		Position: converter.ParseInt(res[3]),
		IsLast:   converter.ParseInt(res[5]) == 1,
	}

	logger.Info("packet grabbed",
		"room_id", roomID,
		"round_id", roundID,
		"user_id", userID,
		"amount", result.Amount,
		"position", result.Position,
		"is_last", result.IsLast,
	)

	return result, nil
}

// GetAvailablePacketID 查询可用红包ID供机器人使用
func (s *GrabService) GetAvailablePacketID(ctx context.Context, roomID, roundID string) (string, error) {
	packetIDs, err := s.packetCache.GetAvailablePacketIDs(ctx, roomID, roundID)
	if err != nil {
		logger.Error("get available packet ids failed", "error", err, "room_id", roomID, "round_id", roundID)
		return "", err
	}
	if len(packetIDs) == 0 {
		return "", message.NewError(message.CodeNoPacket)
	}
	return packetIDs[utils.RandomInt64(int64(len(packetIDs)))], nil
}

// RobotGrabPacket atomically picks a random available packet and grabs it
// for the robot in a single Lua call, avoiding the race condition between
// GetAvailablePacketID and GrabPacket.
func (s *GrabService) RobotGrabPacket(ctx context.Context, roomID, roundID, userID string) (*round.GrabResult, error) {
	keys := []string{
		rediskeys.RoundAvailablePacketsKey(roomID, roundID),
		rediskeys.UserGrabbedKey(roomID, roundID, userID),
		rediskeys.RoundGrabbersKey(roomID, roundID),
		rediskeys.RoundStateKey(roomID, roundID),
		rediskeys.RoomPlayersKey(roomID),
	}

	args := []interface{}{
		userID,
		time.Now().Unix(),
		s.grabTimeout,
		roomID,
		rediskeys.PacketInfoPrefix(roomID),
		rediskeys.PacketAvailablePrefix(roomID),
		// 预生成随机起始偏移，Lua 侧用 % packetCount 取模，避免在 Lua 内调用 math.random
		// （Redis Lua 禁用 math.random，会导致主从复制不一致）
		// 1000 取 packetCount 上限 100 的 10 倍冗余，模偏差 < 1% 对机器人选包场景可接受
		utils.RandomInt64(1000),
		int64(s.redisTTL.PacketDataTTL.Seconds()),
	}

	res, err := scripts.RobotGrabPacket.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		logger.Error("robot grab packet lua failed", "error", err, "room_id", roomID, "round_id", roundID, "user_id", userID)
		return nil, err
	}

	code := converter.ParseInt(res[0])
	if code != 0 {
		luaErr := domain.MapLuaError(code)
		logger.Warn("robot grab packet failed", "lua_code", code, "room_id", roomID, "user_id", userID)
		return nil, luaErr
	}

	result := &round.GrabResult{
		PacketID: converter.ParseString(res[1]),
		Amount:   converter.ParseInt64(res[2]),
		Position: converter.ParseInt(res[3]),
		IsLast:   converter.ParseInt(res[5]) == 1,
	}

	logger.Info("robot packet grabbed",
		"room_id", roomID,
		"round_id", roundID,
		"user_id", userID,
		"amount", result.Amount,
		"position", result.Position,
		"is_last", result.IsLast,
	)

	return result, nil
}

func (s *GrabService) AutoDistribute(ctx context.Context, roomID, roundID string) (int, []round.DistributeResult, error) {
	keys := []string{
		rediskeys.RoundAvailablePacketsKey(roomID, roundID),
		rediskeys.RoundGrabbersKey(roomID, roundID),
		rediskeys.RoundStateKey(roomID, roundID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomHashKey(roomID),
	}

	args := []interface{}{
		time.Now().Unix(),
		rediskeys.PacketInfoPrefix(roomID),
		rediskeys.PacketAvailablePrefix(roomID),
		rediskeys.RoundGrabbedPrefix(roomID),
		roundID,
		int64(s.redisTTL.PacketDataTTL.Seconds()),
	}

	res, err := scripts.AutoDistributePackets.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		logger.Error("auto distribute lua failed", "error", err, "room_id", roomID, "round_id", roundID)
		return 0, nil, err
	}

	code := converter.ParseInt(res[0])
	if code != 0 {
		luaErr := domain.MapLuaError(code)
		logger.Warn("auto distribute failed", "lua_code", code, "room_id", roomID, "round_id", roundID)
		return 0, nil, luaErr
	}

	distributedCount := converter.ParseInt(res[1])
	resultsRaw := res[2]

	var results []round.DistributeResult
	if arr, ok := resultsRaw.([]interface{}); ok {
		for _, item := range arr {
			if tuple, ok := item.([]interface{}); ok && len(tuple) >= 3 {
				results = append(results, round.DistributeResult{
					UserID:   converter.ParseString(tuple[0]),
					Amount:   converter.ParseInt64(tuple[1]),
					Position: int32(converter.ParseInt(tuple[2])),
				})
			}
		}
	}

	logger.Info("packets auto distributed",
		"room_id", roomID,
		"round_id", roundID,
		"distributed_count", distributedCount,
	)

	return distributedCount, results, nil
}

func (s *GrabService) InitRoundPackets(ctx context.Context, roomID, roundID, senderID, senderType string, totalAmount, commission, actualAmount int64, packetAmounts []int64, roundNo int, scenario round.SendScenario, rewardType int, rewardAmount int64) (string, []string, error) {
	amountsJSON, err := json.Marshal(packetAmounts)
	if err != nil {
		return "", nil, err
	}

	// 用雪花 ID 生成器预生成 packetID（全局唯一，避免跨房间冲突）。
	// 规约 SID-2：业务代码依赖 IDGenerator 接口；时钟回拨返回 error，调用方需处理。
	// 注意：packetID MUST 以字符串形式传给 Lua，因为 Lua 5.1 的 number 是 double，
	// 雪花 ID 超过 2^53 会精度丢失，导致 Redis key 不匹配。
	packetIDStrs := make([]string, len(packetAmounts))
	for i := range packetAmounts {
		pid, genErr := s.idGen.GenerateString()
		if genErr != nil {
			logger.Error("generate packet id failed", "error", genErr, "room_id", roomID, "round_id", roundID)
			return "", nil, fmt.Errorf("generate packet id failed: %w", genErr)
		}
		packetIDStrs[i] = pid
	}
	packetIDsJSON, err := json.Marshal(packetIDStrs)
	if err != nil {
		return "", nil, err
	}

	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoundStateKey(roomID, roundID),
		rediskeys.RoundAvailablePacketsKey(roomID, roundID),
		rediskeys.RoundGrabbersKey(roomID, roundID),
	}

	args := []interface{}{
		senderID,
		senderType,
		totalAmount,
		commission,
		actualAmount,
		roundNo,
		time.Now().Unix(),
		s.grabTimeout,
		rediskeys.PacketInfoPrefix(roomID),
		rediskeys.PacketAvailablePrefix(roomID),
		string(packetIDsJSON),
		string(amountsJSON),
		roundID,
		roomID,
		int(scenario),
		rewardType,
		rewardAmount,
		int64(s.redisTTL.PacketDataTTL.Seconds()),
		int64(s.redisTTL.RoundStateTTL.Seconds()),
	}

	res, err := scripts.SendPacket.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil {
		logger.Error("init round packets lua failed", "error", err, "room_id", roomID, "round_id", roundID)
		return "", nil, err
	}

	code := converter.ParseInt(res[0])
	if code != 0 {
		luaErr := domain.MapLuaError(code)
		logger.Warn("init round packets failed", "lua_code", code, "room_id", roomID, "round_id", roundID)
		return "", nil, luaErr
	}

	roundIDResult := converter.ParseString(res[1])

	// packetIDs 直接用 Go 侧预生成的雪花 ID（Lua 返回值仅作审计/回显，不再解析）。
	packetIDs := packetIDStrs

	logger.Info("round packets initialized",
		"room_id", roomID,
		"round_id", roundIDResult,
		"sender_id", senderID,
		"sender_type", senderType,
		"packet_count", len(packetAmounts),
		"packet_ids", packetIDs,
	)

	return roundIDResult, packetIDs, nil
}
