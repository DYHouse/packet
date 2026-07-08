package application

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
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
	grabTimeout int64
	sendTimeout int64
	redisTTL    config.RedisTTLConfig
}

func NewGrabService(redis cRedis.RedisClient, packetCache repository.PacketCacheRepository, grabTimeout, sendTimeout time.Duration, redisTTL config.RedisTTLConfig) *GrabService {
	return &GrabService{
		redis:       redis,
		packetCache: packetCache,
		grabTimeout: int64(grabTimeout.Seconds()),
		sendTimeout: int64(sendTimeout.Seconds()),
		redisTTL:    redisTTL,
	}
}

func (s *GrabService) GrabPacket(ctx context.Context, roomID, roundID, userID, packetID string) (*round.GrabResult, error) {
	keys := []string{
		rediskeys.RoundAvailablePacketsKey(roundID),
		rediskeys.UserGrabbedKey(roundID, userID),
		rediskeys.RoundGrabbersKey(roundID),
		rediskeys.RoundStateKey(roundID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.PacketInfoKey(packetID),
		rediskeys.PacketAvailableKey(packetID),
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
	packetIDs, err := s.packetCache.GetAvailablePacketIDs(ctx, roundID)
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
		rediskeys.RoundAvailablePacketsKey(roundID),
		rediskeys.UserGrabbedKey(roundID, userID),
		rediskeys.RoundGrabbersKey(roundID),
		rediskeys.RoundStateKey(roundID),
		rediskeys.RoomPlayersKey(roomID),
	}

	args := []interface{}{
		userID,
		time.Now().Unix(),
		s.grabTimeout,
		roomID,
		rediskeys.KeyPacketInfoPrefix,
		rediskeys.KeyPacketAvailablePrefix,
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
		rediskeys.RoundAvailablePacketsKey(roundID),
		rediskeys.RoundGrabbersKey(roundID),
		rediskeys.RoundStateKey(roundID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomHashKey(roomID),
	}

	args := []interface{}{
		time.Now().Unix(),
		rediskeys.KeyPacketInfoPrefix,
		rediskeys.KeyPacketAvailablePrefix,
		rediskeys.KeyRoundGrabbedPrefix,
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

	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoundStateKey(roundID),
		rediskeys.RoundAvailablePacketsKey(roundID),
		rediskeys.RoundGrabbersKey(roundID),
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
		rediskeys.KeyPacketInfoPrefix,
		rediskeys.KeyPacketAvailablePrefix,
		rediskeys.KeyGlobalPacketID,
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

	var packetIDs []string
	if len(res) > 2 {
		packetIDsJSON := converter.ParseString(res[2])
		var packetIDInts []int64
		if err := json.Unmarshal([]byte(packetIDsJSON), &packetIDInts); err == nil {
			packetIDs = converter.FormatIDs(packetIDInts)
		}
	}

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
