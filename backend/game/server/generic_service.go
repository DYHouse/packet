package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/cashparty/backend/common/currency"
	"github.com/cashparty/backend/common/limiter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/application"
	commonPb "github.com/cashparty/backend/proto/common"
	settlementService "github.com/cashparty/backend/settlement/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

type BroadcastFunc func(roomID string, cmd string, payload interface{}, excludeUserID string)

type GenericServiceServer struct {
	commonPb.UnimplementedGenericServiceServer
	roomAppSvc  *application.RoomAppService
	seatAppSvc  *application.SeatAppService
	gameAppSvc  *application.GameAppService
	grabSvc     *application.GrabService
	userSvc     *application.UserService
	balanceSvc  *settlementService.BalanceService
	historySvc  *application.HistoryService
	redis       *cRedis.Client
	userLimiter *limiter.UserLimiter
	broadcastFn BroadcastFunc
}

func NewGenericServiceServer(
	roomAppSvc *application.RoomAppService,
	seatAppSvc *application.SeatAppService,
	gameAppSvc *application.GameAppService,
	grabSvc *application.GrabService,
	userSvc *application.UserService,
	balanceSvc *settlementService.BalanceService,
	historySvc *application.HistoryService,
	redis *cRedis.Client,
	userLimiter *limiter.UserLimiter,
	broadcastFn BroadcastFunc,
) *GenericServiceServer {
	return &GenericServiceServer{
		roomAppSvc:  roomAppSvc,
		seatAppSvc:  seatAppSvc,
		gameAppSvc:  gameAppSvc,
		grabSvc:     grabSvc,
		userSvc:     userSvc,
		balanceSvc:  balanceSvc,
		historySvc:  historySvc,
		redis:       redis,
		userLimiter: userLimiter,
		broadcastFn: broadcastFn,
	}
}

func (s *GenericServiceServer) Forward(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	logger.Info("[Game<-Gateway] received request",
		"user_id", req.UserId,
		"cmd", req.Cmd,
		"request_id", req.RequestId,
		"data", string(req.Data))

	var resp *commonPb.ForwardResponse
	var err error

	switch req.Cmd {
	case message.CmdJoinRoom:
		resp, err = s.handleJoinRoom(ctx, req)
	case message.CmdAutoMatch:
		resp, err = s.handleAutoMatch(ctx, req)
	case message.CmdLeaveRoom:
		resp, err = s.handleLeaveRoom(ctx, req)
	case message.CmdRoomState:
		resp, err = s.handleRoomState(ctx, req)
	case message.CmdSelectSeat:
		resp, err = s.handleSelectSeat(ctx, req)
	case message.CmdCancelSeat:
		resp, err = s.handleCancelSeat(ctx, req)
	case message.CmdPlayerReady:
		resp, err = s.handlePlayerReady(ctx, req)
	case message.CmdSendPacket:
		resp, err = s.handleSendPacket(ctx, req)
	case message.CmdGrabPacket:
		resp, err = s.handleGrabPacket(ctx, req)
	case message.CmdGetRoomList:
		resp, err = s.handleGetRoomList(ctx, req)
	case message.CmdGetRoomTypeList:
		resp, err = s.handleGetRoomTypeList(ctx, req)
	case message.CmdReconnect:
		resp, err = s.handleReconnect(ctx, req)
	case message.CmdGetUserBalance:
		resp, err = s.handleGetUserBalance(ctx, req)
	case message.CmdEnqueue:
		resp, err = s.handleEnqueue(ctx, req)
	case message.CmdDequeue:
		resp, err = s.handleDequeue(ctx, req)
	case message.CmdGetPlayerHistory:
		resp, err = s.handleGetPlayerHistory(ctx, req)
	case message.CmdGetPlayerSessionDetail:
		resp, err = s.handleGetPlayerSessionDetail(ctx, req)
	case message.CmdGetPlayerStats:
		resp, err = s.handleGetPlayerStats(ctx, req)
	default:
		resp = &commonPb.ForwardResponse{
			Cmd:       req.Cmd,
			RequestId: req.RequestId,
			Code:      int32(message.CodeUnknownCommand),
			Msg:       message.GetErrorMsg(message.CodeUnknownCommand),
			Timestamp: req.Timestamp,
		}
	}

	if err != nil {
		logger.Info("[Game->Gateway] sending error response",
			"cmd", req.Cmd,
			"request_id", req.RequestId,
			"error", err.Error())
		return resp, err
	}

	logger.Info("[Game->Gateway] sending response",
		"cmd", resp.Cmd,
		"request_id", resp.RequestId,
		"code", resp.Code,
		"msg", resp.Msg,
		"data", string(resp.Data))

	return resp, nil
}

func (s *GenericServiceServer) handleJoinRoom(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.roomAppSvc.JoinAndAutoSeat(ctx, &application.JoinRoomRequest{
		UserID: req.UserId,
		RoomID: data.RoomID,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"room_id":      result.RoomID,
		"room_no":      result.RoomNo,
		"room_state":   result.RoomState,
		"is_spectator": result.IsSpectator,
		"seat_no":      result.SeatNo,
		"auto_seated":  result.AutoSeated,
	}), nil
}

func (s *GenericServiceServer) handleAutoMatch(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	result, err := s.roomAppSvc.AutoMatchAndJoin(ctx, &application.AutoMatchRequest{
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"room_id":      result.RoomID,
		"room_no":      result.RoomNo,
		"room_state":   result.RoomState,
		"is_spectator": result.IsSpectator,
		"seat_no":      result.SeatNo,
		"auto_seated":  result.AutoSeated,
	}), nil
}

func (s *GenericServiceServer) handleLeaveRoom(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
		Reason string `json:"reason"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	_, err := s.roomAppSvc.LeaveRoom(ctx, &application.LeaveRoomRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
		Reason: data.Reason,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, nil), nil
}

func (s *GenericServiceServer) handleRoomState(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.roomAppSvc.GetRoomState(ctx, &application.GetRoomStateRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"room_state": result.RoomState,
	}), nil
}

func (s *GenericServiceServer) handleSelectSeat(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
		SeatNo int    `json:"seat_no"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.seatAppSvc.SelectSeat(ctx, &application.SelectSeatRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
		SeatNo: data.SeatNo,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"seat_no":    result.SeatNo,
		"room_state": result.RoomState,
	}), nil
}

func (s *GenericServiceServer) handleCancelSeat(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.seatAppSvc.CancelSeat(ctx, &application.CancelSeatRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"room_state": result.RoomState,
	}), nil
}

func (s *GenericServiceServer) handlePlayerReady(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.seatAppSvc.PlayerReady(ctx, &application.PlayerReadyRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"room_state": result.RoomState,
	}), nil
}

func (s *GenericServiceServer) handleSendPacket(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.gameAppSvc.SendPacket(ctx, &application.SendPacketRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"packet_count": result.PacketCount,
	}), nil
}

func (s *GenericServiceServer) handleGrabPacket(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID   string `json:"room_id"`
		PacketID string `json:"packet_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	if s.userLimiter != nil {
		allowed, err := s.userLimiter.AllowGrab(ctx, req.UserId)
		if err != nil {
			// 限流服务异常时 fail-open（放行），但记录告警以便排查
			logger.Warn("grab rate limiter error, fail-open",
				"user_id", req.UserId,
				"request_id", req.RequestId,
				"error", err)
		}
		if err == nil && !allowed {
			return s.errorResponse(req, message.CodeRateLimitExceeded, message.GetErrorMsg(message.CodeRateLimitExceeded)), nil
		}
	}

	result, err := s.gameAppSvc.GrabPacket(ctx, &application.GrabPacketRequest{
		RoomID:   data.RoomID,
		UserID:   req.UserId,
		PacketID: data.PacketID,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"packet_id": result.PacketID,
		"amount":    currency.NewMoneyFromFen(result.Amount),
		"position":  result.Position,
		"is_last":   result.IsLast,
	}), nil
}

func (s *GenericServiceServer) handleGetRoomList(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomType int `json:"room_type"`
		Status   int `json:"status"`
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	if data.Page <= 0 {
		data.Page = 1
	}
	if data.PageSize <= 0 {
		data.PageSize = 20
	}
	if data.PageSize > 100 {
		data.PageSize = 100
	}

	rooms, total := s.roomAppSvc.GetRoomList(ctx, data.RoomType, data.Status, data.Page, data.PageSize)

	items := make([]map[string]interface{}, 0)
	for _, r := range rooms {
		items = append(items, map[string]interface{}{
			"room_id":         r.RoomID,
			"room_no":         r.RoomNo,
			"room_fee":        r.RoomFee,
			"max_players":     r.MaxPlayers,
			"max_rounds":      r.MaxRounds,
			"max_spectators":  r.MaxSpectators,
			"current_round":   r.CurrentRound,
			"player_count":    r.PlayerCount,
			"spectator_count": r.SpectatorCount,
			"status":          r.Status,
			"seats":           r.Seats,
		})
	}

	return s.successResponse(req, map[string]interface{}{
		"list":      items,
		"total":     total,
		"page":      data.Page,
		"page_size": data.PageSize,
	}), nil
}

func (s *GenericServiceServer) handleGetRoomTypeList(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	items, err := s.roomAppSvc.GetRoomTypeList(ctx)
	if err != nil {
		return s.errorResponse(req, message.CodeSystemError, err.Error()), nil
	}

	list := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		list = append(list, map[string]interface{}{
			"id":           item.ID,
			"name":         item.Name,
			"room_fee":     currency.NewMoneyFromFen(item.RoomFee),
			"max_rounds":   item.MaxRounds,
			"total_people": item.TotalPeople,
		})
	}

	return s.successResponse(req, map[string]interface{}{
		"list": list,
	}), nil
}

func (s *GenericServiceServer) handleReconnect(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	if data.RoomID == "" {
		return s.errorResponse(req, message.CodeInvalidParams, "room_id is required"), nil
	}

	result, err := s.roomAppSvc.HandleReconnect(ctx, &application.ReconnectRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"room_id":    result.RoomID,
		"room_state": result.RoomState,
	}), nil
}

func (s *GenericServiceServer) handleGetUserBalance(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	if s.balanceSvc == nil {
		return s.errorResponse(req, message.CodeSystemError, "balance service not available"), nil
	}

	userID, err := s.parseUserID(req.UserId)
	if err != nil {
		return s.errorResponse(req, message.CodeInvalidParams, "invalid user_id"), nil
	}

	balance, err := s.balanceSvc.CheckUserBalance(ctx, userID)
	if err != nil {
		logger.Error("failed to get user balance", "user_id", userID, "error", err)
		return s.errorResponse(req, message.CodeSystemError, "failed to get balance"), nil
	}

	var pendingCredit int64
	if s.userSvc != nil {
		pendingCredit = s.userSvc.GetPendingCredit(ctx, req.UserId)
	}

	return s.successResponse(req, map[string]interface{}{
		"balance":        currency.NewMoneyFromFen(balance),
		"pending_credit": currency.NewMoneyFromFen(pendingCredit),
	}), nil
}

func (s *GenericServiceServer) handleEnqueue(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.roomAppSvc.Enqueue(ctx, &application.EnqueueRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"queue_position": result.QueuePosition,
		"room_state":    result.RoomState,
	}), nil
}

func (s *GenericServiceServer) handleDequeue(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	var data struct {
		RoomID string `json:"room_id"`
	}
	if len(req.Data) > 0 {
		json.Unmarshal(req.Data, &data)
	}

	result, err := s.roomAppSvc.Dequeue(ctx, &application.DequeueRequest{
		RoomID: data.RoomID,
		UserID: req.UserId,
	})
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, map[string]interface{}{
		"room_state": result.RoomState,
	}), nil
}

func (s *GenericServiceServer) handleGetPlayerHistory(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	userID, err := s.parseUserID(req.UserId)
	if err != nil {
		return s.errorResponse(req, message.CodeInvalidParams, "invalid user_id"), nil
	}

	var historyReq application.PlayerHistoryReq
	if len(req.Data) > 0 {
		if err := json.Unmarshal(req.Data, &historyReq); err != nil {
			return s.errorResponse(req, message.CodeHistoryParamInvalid, message.GetErrorMsg(message.CodeHistoryParamInvalid)), nil
		}
	}

	if historyReq.Page < 1 {
		historyReq.Page = 1
	}
	if historyReq.PageSize < 1 || historyReq.PageSize > 50 {
		return s.errorResponse(req, message.CodeHistoryParamInvalid, message.GetErrorMsg(message.CodeHistoryParamInvalid)), nil
	}

	resp, err := s.historySvc.GetPlayerHistory(ctx, userID, historyReq)
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, resp), nil
}

func (s *GenericServiceServer) handleGetPlayerSessionDetail(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	userID, err := s.parseUserID(req.UserId)
	if err != nil {
		return s.errorResponse(req, message.CodeInvalidParams, "invalid user_id"), nil
	}

	var data struct {
		SessionID string `json:"session_id"`
	}
	if len(req.Data) > 0 {
		if err := json.Unmarshal(req.Data, &data); err != nil {
			return s.errorResponse(req, message.CodeHistoryParamInvalid, message.GetErrorMsg(message.CodeHistoryParamInvalid)), nil
		}
	}

	sessionID, err := strconv.ParseInt(data.SessionID, 10, 64)
	if err != nil || sessionID <= 0 {
		return s.errorResponse(req, message.CodeHistoryParamInvalid, message.GetErrorMsg(message.CodeHistoryParamInvalid)), nil
	}

	resp, err := s.historySvc.GetPlayerSessionDetail(ctx, userID, sessionID)
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, resp), nil
}

func (s *GenericServiceServer) handleGetPlayerStats(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	userID, err := s.parseUserID(req.UserId)
	if err != nil {
		return s.errorResponse(req, message.CodeInvalidParams, "invalid user_id"), nil
	}

	resp, err := s.historySvc.GetPlayerStats(ctx, userID)
	if err != nil {
		return s.handleError(req, err), nil
	}

	return s.successResponse(req, resp), nil
}

func (s *GenericServiceServer) parseUserID(userIDStr string) (int64, error) {
	var userID int64
	_, err := fmt.Sscanf(userIDStr, "%d", &userID)
	return userID, err
}

func (s *GenericServiceServer) SaveUser(ctx context.Context, req *commonPb.SaveUserRequest) (*commonPb.SaveUserResponse, error) {
	logger.Info("[Game<-Gateway] SaveUser request",
		"user_id", req.UserId,
		"nickname", req.Nickname,
		"ip", req.Ip,
		"device_id", req.DeviceId)

	id, avatar, err := s.userSvc.SaveUser(ctx, req.UserId, req.Nickname, req.Avatar, req.Ip, req.DeviceId)
	if err != nil {
		return &commonPb.SaveUserResponse{
			Code: int32(message.CodeSystemError),
			Msg:  "保存用户失败",
		}, nil
	}

	logger.Info("[Game->Gateway] SaveUser success", "user_id", req.UserId, "id", id, "avatar", avatar)
	return &commonPb.SaveUserResponse{
		Code:   0,
		Msg:    "成功",
		Id:     id,
		Avatar: avatar,
	}, nil
}

func (s *GenericServiceServer) handleError(req *commonPb.ForwardRequest, err error) *commonPb.ForwardResponse {
	if gameErr, ok := message.IsGameError(err); ok {
		return s.errorResponse(req, gameErr.Code, gameErr.Msg)
	}
	return s.errorResponse(req, message.CodeSystemError, message.GetErrorMsg(message.CodeSystemError))
}

func (s *GenericServiceServer) errorResponse(req *commonPb.ForwardRequest, code int, msg string) *commonPb.ForwardResponse {
	return &commonPb.ForwardResponse{
		Cmd:       req.Cmd,
		RequestId: req.RequestId,
		Code:      int32(code),
		Msg:       msg,
		Timestamp: time.Now().UnixMilli(),
	}
}

func (s *GenericServiceServer) successResponse(req *commonPb.ForwardRequest, data interface{}) *commonPb.ForwardResponse {
	var dataBytes []byte
	if data != nil {
		dataBytes, _ = json.Marshal(data)
	}

	return &commonPb.ForwardResponse{
		Cmd:       req.Cmd,
		RequestId: req.RequestId,
		Code:      int32(message.CodeSuccess),
		Msg:       message.GetErrorMsg(message.CodeSuccess),
		Data:      dataBytes,
		Timestamp: time.Now().UnixMilli(),
	}
}

type GRPCServer struct {
	server *grpc.Server
	port   int
}

func NewGRPCServer(
	port int,
	roomAppSvc *application.RoomAppService,
	seatAppSvc *application.SeatAppService,
	gameAppSvc *application.GameAppService,
	grabSvc *application.GrabService,
	userSvc *application.UserService,
	balanceSvc *settlementService.BalanceService,
	historySvc *application.HistoryService,
	redis *cRedis.Client,
	userLimiter *limiter.UserLimiter,
	broadcastFn BroadcastFunc,
) *GRPCServer {
	kaParams := keepalive.ServerParameters{
		MaxConnectionIdle: 15 * time.Minute,
		Time:              5 * time.Minute,
		Timeout:           1 * time.Minute,
	}

	server := grpc.NewServer(
		grpc.KeepaliveParams(kaParams),
		grpc.MaxRecvMsgSize(10*1024*1024),
		grpc.MaxSendMsgSize(10*1024*1024),
	)

	genericServiceServer := NewGenericServiceServer(
		roomAppSvc,
		seatAppSvc,
		gameAppSvc,
		grabSvc,
		userSvc,
		balanceSvc,
		historySvc,
		redis,
		userLimiter,
		broadcastFn,
	)
	commonPb.RegisterGenericServiceServer(server, genericServiceServer)

	return &GRPCServer{
		server: server,
		port:   port,
	}
}

func (s *GRPCServer) Start() error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.port, err)
	}

	logger.Info("gRPC server starting", "port", s.port)

	go func() {
		if err := s.server.Serve(lis); err != nil {
			logger.Error("gRPC server error", "error", err)
		}
	}()

	return nil
}

func (s *GRPCServer) Stop() {
	logger.Info("stopping gRPC server")
	s.server.GracefulStop()
}

func (s *GRPCServer) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", s.port)
}
