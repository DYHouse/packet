package application

import (
	"context"
	"encoding/json"
	"fmt"
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

func mapLuaGrabCodeToGoCode(luaCode int) int {
	switch luaCode {
	case 21:
		return message.CodeAlreadyGrabbed
	case 22:
		return message.CodePacketAlreadyGrabbed
	case 23:
		return message.CodePacketNotFound
	case 40:
		return message.CodeNotInGrabbingPhase
	case 41:
		return message.CodeGrabTimeout
	case 60:
		return message.CodeNotPlayer
	default:
		return message.CodeSystemError
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
		goCode := mapLuaGrabCodeToGoCode(code)
		errMsg := ""
		if len(res) > 4 {
			errMsg = converter.ParseString(res[4])
		}
		logger.Warn("grab packet failed", "lua_code", code, "go_code", goCode, "msg", errMsg, "room_id", roomID, "user_id", userID)
		return nil, message.NewErrorWithMsg(goCode, errMsg)
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
		return 0, nil, fmt.Errorf("auto distribute failed: code=%d", code)
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
		errMsg := ""
		if len(res) > 2 {
			errMsg = converter.ParseString(res[2])
		}
		return "", nil, fmt.Errorf("init packets failed: code=%d, msg=%s", code, errMsg)
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
