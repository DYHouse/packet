package application

import (
	"context"
	"encoding/json"
	"math/rand"
	"time"

	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
)

type GrabService struct {
	redis       *cRedis.Client
	grabTimeout int64
	sendTimeout int64
}

func NewGrabService(redis *cRedis.Client, grabTimeout, sendTimeout time.Duration) *GrabService {
	return &GrabService{
		redis:       redis,
		grabTimeout: int64(grabTimeout.Seconds()),
		sendTimeout: int64(sendTimeout.Seconds()),
	}
}

func (s *GrabService) GrabPacket(ctx context.Context, roomID, roundID, userID, packetID string) (*domain.GrabResult, error) {
	keys := []string{
		redis.RoundAvailablePacketsKey(roundID),
		redis.UserGrabbedKey(roundID, userID),
		redis.RoundGrabbersKey(roundID),
		redis.RoundStateKey(roundID),
		redis.RoomPlayersKey(roomID),
	}

	args := []interface{}{
		userID,
		time.Now().Unix(),
		s.grabTimeout,
		roomID,
		"cashparty",
		packetID,
	}

	res, err := s.redis.Eval(ctx, redis.LuaGrabPacket, keys, args...).Slice()
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

	result := &domain.GrabResult{
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
	packetIDs, err := s.redis.LRange(ctx, redis.RoundAvailablePacketsKey(roundID), 0, -1).Result()
	if err != nil {
		logger.Error("get available packet ids failed", "error", err, "room_id", roomID, "round_id", roundID)
		return "", err
	}
	if len(packetIDs) == 0 {
		return "", message.NewError(message.CodeNoPacket)
	}
	return packetIDs[rand.Intn(len(packetIDs))], nil
}

// RobotGrabPacket atomically picks a random available packet and grabs it
// for the robot in a single Lua call, avoiding the race condition between
// GetAvailablePacketID and GrabPacket.
func (s *GrabService) RobotGrabPacket(ctx context.Context, roomID, roundID, userID string) (*domain.GrabResult, error) {
	keys := []string{
		redis.RoundAvailablePacketsKey(roundID),
		redis.UserGrabbedKey(roundID, userID),
		redis.RoundGrabbersKey(roundID),
		redis.RoundStateKey(roundID),
		redis.RoomPlayersKey(roomID),
	}

	args := []interface{}{
		userID,
		time.Now().Unix(),
		s.grabTimeout,
		roomID,
		"cashparty",
	}

	res, err := s.redis.Eval(ctx, redis.LuaRobotGrabPacket, keys, args...).Slice()
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

	result := &domain.GrabResult{
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

func (s *GrabService) AutoDistribute(ctx context.Context, roomID, roundID string) (int, []domain.DistributeResult, error) {
	keys := []string{
		redis.RoundAvailablePacketsKey(roundID),
		redis.RoundGrabbersKey(roundID),
		redis.RoundStateKey(roundID),
		redis.RoomPlayersKey(roomID),
		redis.RoomHashKey(roomID),
	}

	args := []interface{}{
		time.Now().Unix(),
		"cashparty",
		roundID,
	}

	res, err := s.redis.Eval(ctx, redis.LuaAutoDistributePackets, keys, args...).Slice()
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

	var results []domain.DistributeResult
	if arr, ok := resultsRaw.([]interface{}); ok {
		for _, item := range arr {
			if tuple, ok := item.([]interface{}); ok && len(tuple) >= 3 {
				results = append(results, domain.DistributeResult{
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

func (s *GrabService) InitRoundPackets(ctx context.Context, roomID, roundID, senderID, senderType string, totalAmount, commission, actualAmount int64, packetAmounts []int64, roundNo int, scenario domain.SendScenario, rewardType int, rewardAmount int64) (string, []string, error) {
	amountsJSON, err := json.Marshal(packetAmounts)
	if err != nil {
		return "", nil, err
	}

	keys := []string{
		redis.RoomHashKey(roomID),
		redis.RoomPlayersKey(roomID),
		redis.RoundStateKey(roundID),
		redis.RoundAvailablePacketsKey(roundID),
		redis.RoundGrabbersKey(roundID),
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
		"cashparty",
		string(amountsJSON),
		roundID,
		roomID,
		int(scenario),
		rewardType,
		rewardAmount,
	}

	res, err := s.redis.Eval(ctx, redis.LuaSendPacket, keys, args...).Slice()
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
