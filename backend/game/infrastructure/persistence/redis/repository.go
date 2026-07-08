package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/domain"
	repository "github.com/cashparty/backend/game/domain/repository"
	roomDom "github.com/cashparty/backend/game/domain/room"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis/scripts"
	goredis "github.com/redis/go-redis/v9"
)

type RoomRepository struct {
	client   redis.RedisClient
	redisTTL config.RedisTTLConfig
}

func NewRoomRepository(client redis.RedisClient, redisTTL config.RedisTTLConfig) *RoomRepository {
	return &RoomRepository{client: client, redisTTL: redisTTL}
}

func parseLuaCode(val interface{}) int {
	switch v := val.(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return -1
}

func parseInt(val interface{}) int {
	switch v := val.(type) {
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func (r *RoomRepository) GetRoomMeta(ctx context.Context, roomID string) (*roomDom.RoomMeta, error) {
	key := rediskeys.RoomHashKey(roomID)
	data, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	meta := r.parseRoomMeta(roomID, data)

	playersKey := rediskeys.RoomPlayersKey(roomID)
	spectatorsKey := rediskeys.RoomSpectatorsKey(roomID)
	meta.PlayerCount = int(r.client.Raw().HLen(ctx, playersKey).Val())
	meta.SpectatorCount = int(r.client.Raw().HLen(ctx, spectatorsKey).Val())

	return meta, nil
}

// GetNextSenderID 读取房间哈希中的 next_sender_id 字段。
// key 不存在或字段不存在时返回空字符串与 nil error。
func (r *RoomRepository) GetNextSenderID(ctx context.Context, roomID string) (string, error) {
	key := rediskeys.RoomHashKey(roomID)
	return r.client.HGet(ctx, key, "next_sender_id").Result()
}

func (r *RoomRepository) parseRoomMeta(roomID string, data map[string]string) *roomDom.RoomMeta {
	meta := &roomDom.RoomMeta{
		RoomID: roomID,
	}

	if v, ok := data["room_no"]; ok {
		meta.RoomNo = v
	}
	if v, ok := data["config_id"]; ok {
		meta.ConfigID, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := data["config_name"]; ok {
		meta.ConfigName = v
	}
	if v, ok := data["room_fee"]; ok {
		meta.RoomFee, _ = strconv.ParseInt(v, 10, 64)
	}
	if v, ok := data["max_players"]; ok {
		meta.MaxPlayers, _ = strconv.Atoi(v)
	}
	if v, ok := data["max_rounds"]; ok {
		meta.MaxRounds, _ = strconv.Atoi(v)
	}
	if v, ok := data["max_spectators"]; ok {
		meta.MaxSpectators, _ = strconv.Atoi(v)
		if meta.MaxSpectators == 0 {
			meta.MaxSpectators = 10
		}
	}
	if v, ok := data["status"]; ok {
		status, _ := strconv.Atoi(v)
		meta.Status = roomDom.RoomStatus(status)
	}
	if v, ok := data["current_round"]; ok {
		meta.CurrentRound, _ = strconv.Atoi(v)
	}
	if v, ok := data["current_round_id"]; ok {
		meta.CurrentRoundID = v
	}
	if v, ok := data["current_session_id"]; ok {
		meta.CurrentSessionID = v
	}
	if v, ok := data["started_at"]; ok {
		if ts, err := strconv.ParseInt(v, 10, 64); err == nil && ts > 0 {
			meta.StartedAt = &ts
		}
	}

	return meta
}

func (r *RoomRepository) GetPlayers(ctx context.Context, roomID string) (map[string]*roomDom.Player, error) {
	key := rediskeys.RoomPlayersKey(roomID)
	data, err := r.client.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	players := make(map[string]*roomDom.Player)
	for userID, playerData := range data {
		var player roomDom.Player
		if err := json.Unmarshal([]byte(playerData), &player); err != nil {
			continue
		}
		players[userID] = &player
	}
	return players, nil
}

func (r *RoomRepository) GetPlayer(ctx context.Context, roomID, userID string) (*roomDom.Player, error) {
	key := rediskeys.RoomPlayersKey(roomID)
	data, err := r.client.HGet(ctx, key, userID).Result()
	if err != nil {
		return nil, err
	}

	var player roomDom.Player
	if err := json.Unmarshal([]byte(data), &player); err != nil {
		return nil, err
	}
	return &player, nil
}

func (r *RoomRepository) GetSpectator(ctx context.Context, roomID, userID string) (*roomDom.Spectator, error) {
	key := rediskeys.RoomSpectatorsKey(roomID)
	data, err := r.client.HGet(ctx, key, userID).Result()
	if err != nil {
		return nil, err
	}

	var spectator roomDom.Spectator
	if err := json.Unmarshal([]byte(data), &spectator); err != nil {
		return nil, err
	}
	return &spectator, nil
}

func (r *RoomRepository) SelectSeat(ctx context.Context, roomID, userID string, seatNo int, isRobot bool) error {
	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomSeatsKey(roomID),
		rediskeys.RoomSeatOwnerKey(roomID),
	}
	args := []interface{}{
		userID,
		seatNo,
		fmt.Sprintf("%d", time.Now().Unix()),
		isRobot,
		int64(r.redisTTL.RoomDataTTL.Seconds()),
	}

	result, err := scripts.SelectSeat.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		return domain.MapLuaError(code)
	}
	return nil
}

func (r *RoomRepository) CancelSeat(ctx context.Context, roomID, userID string) error {
	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomSeatsKey(roomID),
		rediskeys.RoomSeatOwnerKey(roomID),
	}
	args := []interface{}{
		userID,
	}

	result, err := scripts.CancelSeat.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		return domain.MapLuaError(code)
	}
	return nil
}

func (r *RoomRepository) JoinAsSpectator(ctx context.Context, roomID string, spectator *roomDom.Spectator) (*repository.JoinResult, error) {
	spectatorData, err := json.Marshal(spectator)
	if err != nil {
		return nil, err
	}

	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.PlayerRoomKey(spectator.UserID),
	}
	args := []interface{}{
		spectator.UserID,
		string(spectatorData),
		fmt.Sprintf("%d", time.Now().Unix()),
		roomID,
		int64(r.redisTTL.RoomDataTTL.Seconds()),
		int64(r.redisTTL.UserRoomTTL.Seconds()),
	}

	result, err := scripts.JoinAsSpectator.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return nil, err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		return nil, domain.MapLuaError(code)
	}

	resultRoomID := result[1].(string)
	roomNo := result[2].(string)
	configID := result[3].(int64)

	return &repository.JoinResult{
		RoomID:   resultRoomID,
		RoomNo:   roomNo,
		ConfigID: configID,
	}, nil
}

func (r *RoomRepository) LeaveRoom(ctx context.Context, roomID, userID string) error {
	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomSeatsKey(roomID),
		rediskeys.RoomSeatOwnerKey(roomID),
		rediskeys.PlayerRoomKey(userID),
	}
	args := []interface{}{
		userID,
		fmt.Sprintf("%d", time.Now().Unix()),
	}

	result, err := scripts.LeaveRoom.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		logger.Error("LuaLeaveRoom execution failed", "room_id", roomID, "user_id", userID, "error", err)
		return err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		logger.Error("LuaLeaveRoom returned error code", "room_id", roomID, "user_id", userID, "code", code)
		return domain.MapLuaError(code)
	}

	if len(result) >= 3 {
		logger.Info("LeaveRoom success", "room_id", roomID, "user_id", userID, "type", result[1], "seat_no", result[2])
	}

	return nil
}

func (r *RoomRepository) KickPlayerAndInterrupt(ctx context.Context, roomID, userID, reason string) (*repository.KickPlayerResult, error) {
	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomSeatsKey(roomID),
		rediskeys.RoomSeatOwnerKey(roomID),
		rediskeys.PlayerRoomKey(userID),
	}

	args := []interface{}{
		userID,
		reason,
		time.Now().Unix(),
		rediskeys.KeyRoundStatePrefix,
	}

	result, err := scripts.KickPlayer.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return nil, err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		return nil, domain.MapLuaError(code)
	}

	return &repository.KickPlayerResult{
		SeatNo:     parseInt(result[2]),
		RoomStatus: parseInt(result[3]),
	}, nil
}

func (r *RoomRepository) AutoSeatAndReady(ctx context.Context, roomID, userID string, isRobot bool) (*repository.AutoSeatResult, error) {
	keys := []string{
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomSeatsKey(roomID),
		rediskeys.RoomSeatOwnerKey(roomID),
	}
	args := []interface{}{
		userID,
		fmt.Sprintf("%d", time.Now().Unix()),
		isRobot,
		int64(r.redisTTL.RoomDataTTL.Seconds()),
	}

	result, err := scripts.AutoSeatAndReady.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return nil, err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		return nil, domain.MapLuaError(code)
	}

	autoResult := &repository.AutoSeatResult{
		SeatNo:               parseInt(result[1]),
		PlayerCount:          parseInt(result[2]),
		MaxPlayers:           parseInt(result[3]),
		ShouldStartCountdown: parseInt(result[4]),
		CountdownEndTime:     parseLuaInt64(result[5]),
		CurrentRound:         parseInt(result[6]),
	}

	if len(result) >= 8 {
		playerDataStr, ok := result[7].(string)
		if ok && playerDataStr != "" {
			var player roomDom.Player
			if err := json.Unmarshal([]byte(playerDataStr), &player); err == nil {
				autoResult.Player = &player
			}
		}
	}

	return autoResult, nil
}

func (r *RoomRepository) Enqueue(ctx context.Context, roomID, userID string) (int, error) {
	keys := []string{
		rediskeys.RoomQueueKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomHashKey(roomID),
	}
	args := []interface{}{
		userID,
		fmt.Sprintf("%d", time.Now().UnixMilli()),
		int64(r.redisTTL.QueueTTL.Seconds()),
	}

	result, err := scripts.Enqueue.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return 0, err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		return 0, domain.MapLuaError(code)
	}

	return parseInt(result[1]), nil
}

func (r *RoomRepository) Dequeue(ctx context.Context, roomID, userID string) error {
	keys := []string{
		rediskeys.RoomQueueKey(roomID),
		rediskeys.RoomHashKey(roomID),
	}
	args := []interface{}{
		userID,
	}

	result, err := scripts.Dequeue.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		return domain.MapLuaError(code)
	}

	return nil
}

func (r *RoomRepository) AutoSubstitute(ctx context.Context, roomID string, seatNo int) (*repository.SubstituteResult, error) {
	keys := []string{
		rediskeys.RoomQueueKey(roomID),
		rediskeys.RoomHashKey(roomID),
		rediskeys.RoomPlayersKey(roomID),
		rediskeys.RoomSpectatorsKey(roomID),
		rediskeys.RoomSeatsKey(roomID),
		rediskeys.RoomSeatOwnerKey(roomID),
	}
	args := []interface{}{
		seatNo,
		fmt.Sprintf("%d", time.Now().Unix()),
		int64(r.redisTTL.RoomDataTTL.Seconds()),
	}

	result, err := scripts.AutoSubstitute.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return nil, err
	}

	code := parseLuaCode(result[0])
	if code != domain.LuaErrSuccess {
		if code == domain.LuaErrQueueEmpty || code == domain.LuaErrSubstituteFail || code == domain.LuaErrNoEmptySeat {
			return nil, nil
		}
		return nil, domain.MapLuaError(code)
	}

	subResult := &repository.SubstituteResult{
		SubstituteUserID:     parseLuaString(result[1]),
		PlayerCount:          parseInt(result[2]),
		MaxPlayers:           parseInt(result[3]),
		ShouldStartCountdown: parseInt(result[4]),
		CountdownEndTime:     parseLuaInt64(result[5]),
		CurrentRound:         parseInt(result[6]),
		SeatNo:               seatNo,
	}

	if len(result) >= 8 {
		playerDataStr, ok := result[7].(string)
		if ok && playerDataStr != "" {
			var player roomDom.Player
			if err := json.Unmarshal([]byte(playerDataStr), &player); err == nil {
				subResult.Player = &player
			}
		}
	}

	return subResult, nil
}

func (r *RoomRepository) GetQueueList(ctx context.Context, roomID string) ([]*roomDom.QueueInfo, error) {
	queueKey := rediskeys.RoomQueueKey(roomID)
	spectatorsKey := rediskeys.RoomSpectatorsKey(roomID)

	members, err := r.client.Raw().ZRangeWithScores(ctx, queueKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}

	queueList := make([]*roomDom.QueueInfo, 0, len(members))
	for idx, m := range members {
		userID, ok := m.Member.(string)
		if !ok || userID == "" {
			continue
		}

		spectatorData, err := r.client.HGet(ctx, spectatorsKey, userID).Result()
		if err != nil || spectatorData == "" {
			continue
		}

		var spectator roomDom.Spectator
		if err := json.Unmarshal([]byte(spectatorData), &spectator); err != nil {
			continue
		}

		queueList = append(queueList, &roomDom.QueueInfo{
			UserID:        spectator.UserID,
			Nickname:      spectator.Nickname,
			Avatar:        spectator.Avatar,
			QueuePosition: idx + 1,
			QueuedAt:      int64(m.Score),
		})
	}

	return queueList, nil
}

func (r *RoomRepository) RemoveFromQueue(ctx context.Context, roomID, userID string) error {
	queueKey := rediskeys.RoomQueueKey(roomID)
	if err := r.client.ZRem(ctx, queueKey, userID).Err(); err != nil {
		return err
	}
	return nil
}

func parseLuaString(val interface{}) string {
	if v, ok := val.(string); ok {
		return v
	}
	return ""
}

func parseLuaInt64(val interface{}) int64 {
	switch v := val.(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
}

func (r *RoomRepository) UpdateRoomSessionID(ctx context.Context, roomID string, sessionID string) error {
	key := rediskeys.RoomHashKey(roomID)
	return r.client.HSet(ctx, key, "current_session_id", sessionID).Err()
}

func (r *RoomRepository) InitRoom(ctx context.Context, room *roomDom.RoomMeta) error {
	key := rediskeys.RoomHashKey(room.RoomID)

	exists, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return err
	}

	if exists == 0 {
		fields := []interface{}{
			"room_no", room.RoomNo,
			"config_id", room.ConfigID,
			"config_name", room.ConfigName,
			"room_fee", room.RoomFee,
			"max_players", room.MaxPlayers,
			"max_rounds", room.MaxRounds,
			"max_spectators", room.MaxSpectators,
			"status", int(room.Status),
			"current_round", room.CurrentRound,
			"current_session_id", room.CurrentSessionID,
		}
		if err := r.client.HSet(ctx, key, fields...).Err(); err != nil {
			return err
		}
		if err := r.client.Expire(ctx, key, 24*time.Hour).Err(); err != nil {
			return err
		}
	}

	return nil
}

func (r *RoomRepository) GetRoomStateData(ctx context.Context, roomID string) (*repository.RoomStateData, error) {
	roomHashKey := rediskeys.RoomHashKey(roomID)
	playersKey := rediskeys.RoomPlayersKey(roomID)
	spectatorsKey := rediskeys.RoomSpectatorsKey(roomID)
	seatOwnerKey := rediskeys.RoomSeatOwnerKey(roomID)

	pipe := r.client.Pipeline()
	metaCmd := pipe.HGetAll(ctx, roomHashKey)
	playersCmd := pipe.HGetAll(ctx, playersKey)
	spectatorsCmd := pipe.HGetAll(ctx, spectatorsKey)
	seatOwnersCmd := pipe.HGetAll(ctx, seatOwnerKey)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return nil, err
	}

	metaData, err := metaCmd.Result()
	if err != nil {
		return nil, err
	}
	if len(metaData) == 0 {
		return nil, message.NewError(message.CodeRoomNotFound)
	}

	playersData, err := playersCmd.Result()
	if err != nil {
		return nil, err
	}

	spectatorsData, err := spectatorsCmd.Result()
	if err != nil {
		return nil, err
	}

	seatOwnersData, err := seatOwnersCmd.Result()
	if err != nil {
		return nil, err
	}

	meta := r.parseRoomMeta(roomID, metaData)

	players := make(map[string]*roomDom.Player)
	for userID, playerData := range playersData {
		var player roomDom.Player
		if err := json.Unmarshal([]byte(playerData), &player); err != nil {
			continue
		}
		players[userID] = &player
	}

	spectators := make(map[string]*roomDom.Spectator)
	for userID, spectatorData := range spectatorsData {
		var spectator roomDom.Spectator
		if err := json.Unmarshal([]byte(spectatorData), &spectator); err != nil {
			continue
		}
		spectators[userID] = &spectator
	}

	seatOwners := make(map[int]string)
	for seatNoStr, userID := range seatOwnersData {
		seatNo, _ := strconv.Atoi(seatNoStr)
		seatOwners[seatNo] = userID
	}

	queueList, _ := r.GetQueueList(ctx, roomID)

	return &repository.RoomStateData{
		RoomID:         meta.RoomID,
		RoomNo:         meta.RoomNo,
		RoomType:       int(meta.ConfigID),
		RoomFee:        meta.RoomFee,
		Status:         int(meta.Status),
		CurrentRound:   meta.CurrentRound,
		MaxRounds:      meta.MaxRounds,
		PlayerCount:    len(players),
		SpectatorCount: len(spectators),
		MaxPlayers:     meta.MaxPlayers,
		MaxSpectators:  meta.MaxSpectators,
		Players:        players,
		Spectators:     spectators,
		SeatOwners:     seatOwners,
		Queue:          queueList,
		Meta:           meta,
	}, nil
}

func (r *RoomRepository) GetRoomSeatsBatch(ctx context.Context, roomIDs []string) (map[string]*repository.RoomStateData, error) {
	if len(roomIDs) == 0 {
		return make(map[string]*repository.RoomStateData), nil
	}

	pipe := r.client.Pipeline()
	type roomCmds struct {
		meta       *goredis.MapStringStringCmd
		players    *goredis.MapStringStringCmd
		spectators *goredis.MapStringStringCmd
		seatOwners *goredis.MapStringStringCmd
	}
	cmdsMap := make(map[string]roomCmds, len(roomIDs))

	for _, roomID := range roomIDs {
		cmdsMap[roomID] = roomCmds{
			meta:       pipe.HGetAll(ctx, rediskeys.RoomHashKey(roomID)),
			players:    pipe.HGetAll(ctx, rediskeys.RoomPlayersKey(roomID)),
			spectators: pipe.HGetAll(ctx, rediskeys.RoomSpectatorsKey(roomID)),
			seatOwners: pipe.HGetAll(ctx, rediskeys.RoomSeatOwnerKey(roomID)),
		}
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*repository.RoomStateData, len(roomIDs))
	for roomID, cmds := range cmdsMap {
		metaData, err := cmds.meta.Result()
		if err != nil {
			continue
		}

		playersData, err := cmds.players.Result()
		if err != nil {
			continue
		}

		spectatorsData, err := cmds.spectators.Result()
		if err != nil {
			continue
		}

		seatOwnersData, err := cmds.seatOwners.Result()
		if err != nil {
			continue
		}

		players := make(map[string]*roomDom.Player)
		for userID, playerData := range playersData {
			var player roomDom.Player
			if err := json.Unmarshal([]byte(playerData), &player); err != nil {
				continue
			}
			players[userID] = &player
		}

		spectators := make(map[string]*roomDom.Spectator)
		for userID, spectatorData := range spectatorsData {
			var spectator roomDom.Spectator
			if err := json.Unmarshal([]byte(spectatorData), &spectator); err != nil {
				continue
			}
			spectators[userID] = &spectator
		}

		seatOwners := make(map[int]string)
		for seatNoStr, userID := range seatOwnersData {
			seatNo, _ := strconv.Atoi(seatNoStr)
			seatOwners[seatNo] = userID
		}

		currentRound, _ := strconv.Atoi(metaData["current_round"])
		maxRounds, _ := strconv.Atoi(metaData["max_rounds"])
		status, _ := strconv.Atoi(metaData["status"])

		result[roomID] = &repository.RoomStateData{
			RoomID:         roomID,
			CurrentRound:   currentRound,
			MaxRounds:      maxRounds,
			Status:         status,
			PlayerCount:    len(players),
			SpectatorCount: len(spectators),
			Players:        players,
			Spectators:     spectators,
			SeatOwners:     seatOwners,
		}
	}

	return result, nil
}
