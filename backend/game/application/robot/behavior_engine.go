package robot

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/utils"
	"github.com/cashparty/backend/game/application"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/scheduler"
	"github.com/google/uuid"
)

// RobotBehaviorEngine drives delayed robot actions through the timeout
// scheduler and reacts to game lifecycle events (packet created, round
// settled, session ended) by scheduling the corresponding robot behavior.
// It implements the RobotActionScheduler interface used by RobotPlayer.
type RobotBehaviorEngine struct {
	config              *config.RobotConfig
	robotPlayer         *RobotPlayer
	scheduler           *scheduler.TimeoutScheduler
	accountSvc          *RobotAccountService
	grabSvc             *application.GrabService
	robotSchedulerRedis repository.RobotSchedulerRepository
}

// NewRobotBehaviorEngine creates a new RobotBehaviorEngine instance.
func NewRobotBehaviorEngine(
	config *config.RobotConfig,
	robotPlayer *RobotPlayer,
	scheduler *scheduler.TimeoutScheduler,
	accountSvc *RobotAccountService,
	grabSvc *application.GrabService,
	robotSchedulerRedis repository.RobotSchedulerRepository,
) *RobotBehaviorEngine {
	return &RobotBehaviorEngine{
		config:              config,
		robotPlayer:         robotPlayer,
		scheduler:           scheduler,
		accountSvc:          accountSvc,
		grabSvc:             grabSvc,
		robotSchedulerRedis: robotSchedulerRedis,
	}
}

// ScheduleAction schedules a delayed robot action. It satisfies the
// RobotActionScheduler interface used by RobotPlayer.
//
// Data format: "robotUserID:action:uuid"
// Data format with retry: "robotUserID:action:uuid:retryCount"
// Note: grab actions are scheduled directly via OnPacketCreated with an
// extended format that includes the round id.
func (e *RobotBehaviorEngine) ScheduleAction(ctx context.Context, roomID string, robotUserID string, action string, delay time.Duration) {
	data := fmt.Sprintf("%s:%s:%s:0", robotUserID, action, uuid.New().String()[:12])
	e.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeRobot, roomID, data, delay)
}

// scheduleRetry schedules a retry for a failed robot action with an
// incremented retry count. If the retry count exceeds the configured maximum,
// no retry is scheduled.
//
// For grab action, roundID must be non-empty to preserve the round context
// across retries (grab data format: "robotUserID:grab:roundID:uuid:retryCount").
// For other actions, roundID is ignored and the standard format is used
// ("robotUserID:action:uuid:retryCount").
func (e *RobotBehaviorEngine) scheduleRetry(ctx context.Context, roomID string, robotUserID string, action string, retryCount int, roundID string) {
	maxRetry := e.config.Behavior.ActionRetryMax
	if maxRetry <= 0 {
		maxRetry = 2
	}
	if retryCount >= maxRetry {
		logger.Warn("robot action retry exhausted",
			"action", action,
			"room_id", roomID,
			"robot_user_id", robotUserID,
			"retry_count", retryCount,
		)
		// 选座重试耗尽后让机器人离开房间，归还空闲池，避免观众机器人滞留
		// 触发后续轮次 OnPacketCreated 误调度抢红包，并导致 robot pool reserve low
		if action == "seat" {
			if leaveErr := e.handleLeaveAction(ctx, roomID, robotUserID); leaveErr != nil {
				logger.Error("robot leave after seat retry exhausted failed",
					"room_id", roomID,
					"robot_user_id", robotUserID,
					"error", leaveErr,
				)
			} else {
				logger.Info("robot left room after seat retry exhausted, returned to idle pool",
					"room_id", roomID,
					"robot_user_id", robotUserID,
				)
			}
		}
		return
	}
	delay := e.config.Behavior.ActionRetryDelay
	if delay <= 0 {
		delay = 2 * time.Second
	}
	var data string
	if action == "grab" && roundID != "" {
		// grab 格式必须保留 roundID，否则重试时 GrabPacket 拿到错误的 roundID
		data = fmt.Sprintf("%s:grab:%s:%s:%d", robotUserID, roundID, uuid.New().String()[:12], retryCount+1)
	} else {
		data = fmt.Sprintf("%s:%s:%s:%d", robotUserID, action, uuid.New().String()[:12], retryCount+1)
	}
	e.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeRobot, roomID, data, delay)
	logger.Info("robot action retry scheduled",
		"action", action,
		"room_id", roomID,
		"robot_user_id", robotUserID,
		"retry_count", retryCount+1,
		"delay", delay,
	)
}

// HandleRobotTimeout is the timeout callback registered with the scheduler
// for TimeoutTypeRobot. It parses the data field and dispatches to the
// appropriate RobotPlayer method.
//
// Data formats:
//   - "robotUserID:action:uuid:retryCount" for seat, ready, send, leave
//   - "robotUserID:grab:roundID:uuid:retryCount" for grab
func (e *RobotBehaviorEngine) HandleRobotTimeout(ctx context.Context, roomID string, data string) {
	parts := strings.SplitN(data, ":", 5)
	if len(parts) < 3 {
		logger.Error("invalid robot timeout data", "data", data)
		return
	}
	robotUserID := parts[0]
	action := parts[1]

	// Parse retry count from the last segment (default 0 for old format).
	// 仅用于非 grab action：grab 固定 5 段格式，retryCount 固定在 parts[4]。
	retryCount := 0
	parseRetryFromEnd := func() int {
		for i := len(parts) - 1; i >= 2; i-- {
			if n, err := strconv.Atoi(parts[i]); err == nil {
				return n
			}
		}
		return 0
	}

	// roundID 仅 grab 使用；grab 重试时必须保留 roundID，否则 GrabPacket 会拿到错误的 roundID
	var roundID string
	var err error
	switch action {
	case "seat":
		retryCount = parseRetryFromEnd()
		err = e.robotPlayer.SelectSeat(ctx, roomID, robotUserID)
	case "ready":
		retryCount = parseRetryFromEnd()
		err = e.robotPlayer.Ready(ctx, roomID, robotUserID)
	case "grab":
		// grab 固定 5 段格式：robotUserID:grab:roundID:uuid:retryCount
		if len(parts) < 5 {
			logger.Error("invalid grab timeout data, expected 5 parts", "data", data, "got_parts", len(parts))
			return
		}
		roundID = parts[2]
		// 固定从 parts[4] 读 retryCount，不用 parseRetryFromEnd
		// （parseRetryFromEnd 若 roundID 是纯数字可能解析到错误位置）
		retryCount, _ = strconv.Atoi(parts[4])
		err = e.robotPlayer.GrabPacket(ctx, roomID, roundID, robotUserID)
	case "send":
		retryCount = parseRetryFromEnd()
		err = e.robotPlayer.SendPacket(ctx, roomID, robotUserID)
	case "leave":
		retryCount = parseRetryFromEnd()
		err = e.handleLeaveAction(ctx, roomID, robotUserID)
	default:
		logger.Error("unknown robot action", "action", action)
		return
	}

	if err != nil {
		// grab 永久性错误（如观众机器人误触抢红包）重试必然失败，仅记录 warn 不重试
		if action == "grab" && isPermanentGrabError(err) {
			logger.Warn("robot grab skipped, permanent error",
				"action", action,
				"room_id", roomID,
				"robot_user_id", robotUserID,
				"retry_count", retryCount,
				"error", err,
			)
			return
		}
		logger.Error("robot action failed",
			"action", action,
			"room_id", roomID,
			"robot_user_id", robotUserID,
			"retry_count", retryCount,
			"error", err,
		)
		// Schedule retry for transient failures (seat, ready, grab)
		if action == "seat" || action == "ready" || action == "grab" {
			e.scheduleRetry(ctx, roomID, robotUserID, action, retryCount, roundID)
		}
	}
}

// handleLeaveAction makes the robot leave the room and returns it to the
// available pool with idle status. It is idempotent: if the robot is no
// longer in the room robot set, the action is skipped to avoid duplicate
// leave processing from OnGameEnd.
func (e *RobotBehaviorEngine) handleLeaveAction(ctx context.Context, roomID string, robotUserID string) error {
	userID := converter.ParseID(robotUserID)

	// Idempotency check: skip if robot is no longer in the room robot set
	if userID != 0 {
		robotIDs, err := e.robotSchedulerRedis.GetRoomRobots(ctx, roomID)
		if err == nil {
			found := false
			for _, id := range robotIDs {
				if id == userID {
					found = true
					break
				}
			}
			if !found {
				logger.Debug("robot leave skipped, already removed from room",
					"room_id", roomID,
					"user_id", userID,
				)
				return nil
			}
		}
	}

	err := e.robotPlayer.LeaveRoom(ctx, roomID, robotUserID)

	// Always clean up room robot set and active set, even if LeaveRoom failed
	// (e.g. code 14 "not_found" means the robot was already removed from the
	// room by LuaEndGame, but the robot set wasn't cleaned up yet).
	if userID != 0 {
		e.robotSchedulerRedis.RemoveRobotFromRoom(ctx, roomID, userID)
		e.robotSchedulerRedis.RemoveFromActiveSet(ctx, userID)
		if markErr := e.accountSvc.MarkRobotIdle(ctx, userID); markErr != nil {
			logger.Error("failed to mark robot idle after leave",
				"room_id", roomID,
				"user_id", userID,
				"error", markErr,
			)
		}
	}

	// Code 14 "not_found" means the robot is already out of the room
	// (e.g. removed by LuaEndGame). This is expected and not an error
	// since the cleanup above has already been performed.
	if err != nil && message.IsErrorCode(err, message.CodeNotInRoom) {
		logger.Debug("robot already left room, cleanup done",
			"room_id", roomID,
			"user_id", userID,
		)
		return nil
	}
	return err
}

// OnPacketCreated 处理红包创建事件，为房间内机器人调度抢红包任务。
// 仅对玩家机器人调度抢红包任务，观众机器人不参与；每个机器人按
// GrabSkipProb 概率决定是否跳过本次抢红包。
func (e *RobotBehaviorEngine) OnPacketCreated(ctx context.Context, roomID string, roundID string) {
	robotIDs, err := e.robotSchedulerRedis.GetRoomPlayerRobots(ctx, roomID)
	if err != nil {
		logger.Error("failed to get room robots for packet created event",
			"room_id", roomID,
			"error", err,
		)
		return
	}

	for _, robotID := range robotIDs {
		if e.shouldSkipGrab(e.config.Behavior.GrabSkipProb) {
			continue
		}
		delay := utils.RandomDelay(e.config.Behavior.GrabDelayMin, e.config.Behavior.GrabDelayMax)
		robotUserID := converter.FormatID(robotID)
		data := fmt.Sprintf("%s:grab:%s:%s:0", robotUserID, roundID, uuid.New().String()[:12])
		e.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeRobot, roomID, data, delay)
	}
}

// OnRoundSettle 处理回合结算事件，当下一个发红包者（minPlayerID）为房间内
// 机器人时调度 send 任务。使用玩家机器人交集避免观众机器人被误判为下一发包人。
// 若 isGameEnd 为 true，表示游戏已结束，不再调度 send 任务。
func (e *RobotBehaviorEngine) OnRoundSettle(ctx context.Context, roomID string, minPlayerID int64, isGameEnd bool) {
	if isGameEnd {
		return
	}

	robotIDs, err := e.robotSchedulerRedis.GetRoomPlayerRobots(ctx, roomID)
	if err != nil {
		logger.Error("failed to get room robots for round settle event",
			"room_id", roomID,
			"error", err,
		)
		return
	}

	isRobot := false
	for _, id := range robotIDs {
		if id == minPlayerID {
			isRobot = true
			break
		}
	}
	if !isRobot {
		return
	}

	delay := utils.RandomDelay(e.config.Behavior.SendDelayMin, e.config.Behavior.SendDelayMax)
	robotUserID := converter.FormatID(minPlayerID)
	data := fmt.Sprintf("%s:send:%s:0", robotUserID, uuid.New().String()[:12])
	e.scheduler.SetTimeout(ctx, scheduler.TimeoutTypeRobot, roomID, data, delay)
}

// LeaveRoomNow makes the robot leave the room immediately without going
// through the timeout scheduler. It is used by OnGameEnd and
// cleanupEndedRooms where no delay is needed.
func (e *RobotBehaviorEngine) LeaveRoomNow(ctx context.Context, roomID string, robotUserID string) {
	if err := e.handleLeaveAction(ctx, roomID, robotUserID); err != nil {
		logger.Error("robot leave room now failed",
			"room_id", roomID,
			"robot_user_id", robotUserID,
			"error", err,
		)
	}
}

// shouldSkipGrab returns true with probability prob, used to decide whether
// a robot skips grabbing the current packet.
func (e *RobotBehaviorEngine) shouldSkipGrab(prob float64) bool {
	return rand.Float64() < prob
}

// isPermanentGrabError 判断 grab 错误是否为永久性错误（重试必然失败）。
// 永久性错误包括：非玩家操作、不在房间、机器人不允许、非抢红包阶段、无玩家。
// 这类错误重试无意义，应直接放弃以避免日志噪音与调度器空转。
func isPermanentGrabError(err error) bool {
	codes := []int{
		message.CodeNotPlayer,
		message.CodeNotInRoom,
		message.CodeRobotNotAllowed,
		message.CodeNotInGrabbingPhase,
		message.CodeNoPlayers,
	}
	for _, code := range codes {
		if message.IsErrorCode(err, code) {
			return true
		}
	}
	return false
}
