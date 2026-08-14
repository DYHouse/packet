package robot

import (
	"context"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/common/rediskeys"
	repository "github.com/cashparty/backend/game/domain/repository"
	roomDom "github.com/cashparty/backend/game/domain/room"
)

// roomCandidate represents a room that may need robot assignment. It is the
// internal data structure used by the scheduler scan loop to prioritize and
// dispatch robots.
type roomCandidate struct {
	RoomID           string
	ReadyPlayerCount int
	WaitingSince     time.Time
	SeatedCount      int
	RoomFee          int
}

// RobotSchedulerService periodically scans rooms in Waiting status and
// assigns robots to them. It also recycles robots when games end and
// monitors the available robot pool reserve.
type RobotSchedulerService struct {
	accountSvc          *RobotAccountService
	robotPlayer         *RobotPlayer
	behaviorEngine      *RobotBehaviorEngine
	repo                repository.RoomRepository
	dbRepo              repository.DBRepository
	robotSchedulerRedis repository.RobotSchedulerRepository
	robotPool           repository.RobotPoolRepository
	config              *config.RobotConfig
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
}

// NewRobotSchedulerService creates a new RobotSchedulerService instance.
func NewRobotSchedulerService(
	accountSvc *RobotAccountService,
	robotPlayer *RobotPlayer,
	repo repository.RoomRepository,
	dbRepo repository.DBRepository,
	robotSchedulerRedis repository.RobotSchedulerRepository,
	robotPool repository.RobotPoolRepository,
	cfg *config.RobotConfig,
) *RobotSchedulerService {
	return &RobotSchedulerService{
		accountSvc:          accountSvc,
		robotPlayer:         robotPlayer,
		repo:                repo,
		dbRepo:              dbRepo,
		robotSchedulerRedis: robotSchedulerRedis,
		robotPool:           robotPool,
		config:              cfg,
	}
}

// SetBehaviorEngine injects the behavior engine after construction to
// break the circular dependency.
func (s *RobotSchedulerService) SetBehaviorEngine(engine *RobotBehaviorEngine) {
	s.behaviorEngine = engine
}

// Name returns the scheduler name.
func (s *RobotSchedulerService) Name() string {
	return "robot_scheduler"
}

// Start launches the scan loop in a background goroutine. The scan interval
// is taken from the scheduler config. The provided ctx is used as the parent
// of the service's internal context so that cancellation propagates from the
// application lifecycle.
func (s *RobotSchedulerService) Start(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.scanLoop()
	logger.Info("robot scheduler service started", "scan_interval", s.config.Scheduler.ScanInterval)
	return nil
}

// Stop signals the scan loop to exit and waits for it to drain (with a 10s
// fallback timeout). It is safe to call multiple times.
func (s *RobotSchedulerService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		logger.Warn("robot scheduler stop timeout")
	}
	logger.Info("robot scheduler service stopped")
}

// scanLoop periodically calls scanRooms until the service context is cancelled.
func (s *RobotSchedulerService) scanLoop() {
	defer s.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("scan loop panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()
	if s.config.Scheduler.ScanInterval <= 0 {
		logger.Warn("robot scheduler scan interval is zero, scan loop disabled")
		return
	}
	ticker := time.NewTicker(s.config.Scheduler.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.scanRooms()
		}
	}
}

// scanRooms is the main inspection routine. It runs within a time budget of
// 80% of the scan interval to avoid overlapping with the next tick. The
// per-scan context is derived from the service context so the scan is bound
// both by the per-scan budget and the application lifecycle.
func (s *RobotSchedulerService) scanRooms() {
	// Time budget: 80% of scan interval
	budget := time.Duration(float64(s.config.Scheduler.ScanInterval) * 0.8)
	ctx, cancel := context.WithTimeout(s.ctx, budget)
	defer cancel()
	deadline := time.Now().Add(budget)

	// 1. Get all Waiting status rooms from Redis
	rooms := s.getWaitingRooms(ctx)

	// 2. Filter rooms that need robot assignment
	candidates := s.filterRoomsNeedingRobots(ctx, rooms)

	// 3. Sort by priority (ready players desc, waiting time asc)
	s.sortRoomsByPriority(candidates)

	// 4. Assign robots to each room
	for _, room := range candidates {
		if time.Now().After(deadline) {
			break
		}
		s.assignRobotsToRoom(ctx, room)
	}

	// 5. Check pool reserve count
	s.checkPoolReserve(ctx)

	// 6. Cleanup residual robots in ended rooms
	s.cleanupEndedRooms(ctx)
}

// sortRoomsByPriority sorts room candidates by ready player count descending
// then by waiting time ascending. Sort is stable to keep insertion order for
// equal priority rooms.
func (s *RobotSchedulerService) sortRoomsByPriority(rooms []roomCandidate) {
	sort.SliceStable(rooms, func(i, j int) bool {
		if rooms[i].ReadyPlayerCount != rooms[j].ReadyPlayerCount {
			return rooms[i].ReadyPlayerCount > rooms[j].ReadyPlayerCount
		}
		return rooms[i].WaitingSince.Before(rooms[j].WaitingSince)
	})
}

// assignRobotsToRoom assigns up to MaxRobotsPerRoom robots to the given room.
// It is rate limited per room via the room assign lock to avoid concurrent
// assignments from multiple scheduler instances. Before assigning new robots,
// it detects and recycles zombie robots that are in the room robot set but
// not actually seated.
func (s *RobotSchedulerService) assignRobotsToRoom(ctx context.Context, room roomCandidate) error {
	// 1. Check room assign rate limit lock
	locked, lockToken, err := s.robotSchedulerRedis.AcquireRoomAssignLock(ctx, room.RoomID, s.config.Scheduler.RoomAssignLockTTL)
	if err != nil {
		logger.Warn("acquire room assign lock failed", "room_id", room.RoomID, "error", err)
		return nil // skip, rate limited
	}
	if !locked {
		return nil // skip, rate limited
	}
	defer func() {
		if releaseErr := s.robotSchedulerRedis.ReleaseRoomAssignLock(ctx, room.RoomID, lockToken); releaseErr != nil {
			logger.Warn("release room assign lock failed", "room_id", room.RoomID, "error", releaseErr)
		}
	}()

	// 2. Detect and recycle zombie robots (in room set but not seated)
	existingRobots, _ := s.robotSchedulerRedis.GetRoomRobots(ctx, room.RoomID)
	if len(existingRobots) > 0 {
		s.recycleZombieRobots(ctx, room.RoomID, existingRobots)
		// Refresh after recycling
		existingRobots, _ = s.robotSchedulerRedis.GetRoomRobots(ctx, room.RoomID)
	}

	// 3. Calculate needed robots = MaxPlayers - seated count
	maxPlayers := roomDom.MaxPlayers
	needed := maxPlayers - room.SeatedCount
	maxAllowed := s.config.Scheduler.MaxRobotsPerRoom - len(existingRobots)
	if needed > maxAllowed {
		needed = maxAllowed
	}
	if needed <= 0 {
		return nil
	}

	// 4. Assign robots
	for i := 0; i < needed; i++ {
		// Get available robot
		robot, err := s.accountSvc.GetAvailableRobot(ctx, room.RoomFee)
		if err != nil || robot == nil {
			break // no more robots available
		}

		// Acquire assign lock
		locked, lockToken, err := s.robotSchedulerRedis.AcquireAssignLock(ctx, robot.UserID, room.RoomID, s.config.Scheduler.RobotAssignLockTTL)
		if err != nil || !locked {
			continue
		}

		// Mark robot as in game
		if err := s.accountSvc.MarkRobotInGame(ctx, robot.UserID); err != nil {
			logger.Warn("failed to mark robot in game",
				"room_id", room.RoomID,
				"user_id", robot.UserID,
				"error", err,
			)
			s.robotSchedulerRedis.ReleaseAssignLock(ctx, robot.UserID, lockToken)
			continue
		}

		// Add to room robot set and active set
		s.robotSchedulerRedis.AddRobotToRoom(ctx, room.RoomID, robot.UserID)
		s.robotSchedulerRedis.AddToActiveSet(ctx, robot.UserID)

		// Call RobotPlayer.JoinAndReady
		robotUserID := converter.FormatID(robot.UserID)
		err = s.robotPlayer.JoinAndReady(ctx, room.RoomID, robotUserID)
		if err != nil {
			// 失败分支需区分错误码：
			// - CodeUserAlreadyInRoom: userRoomKey 残留指向其他房间，需先清旧房间再重试一次，
			//   避免简单"加回可用池"导致下个 tick 再次被选中、再次失败、死循环。
			// - 其他错误: 保持原有行为（加回可用池 + 释放锁 + 跳过）
			if message.IsErrorCode(err, message.CodeUserAlreadyInRoom) {
				if s.retryJoinAfterCleaningStaleRoom(ctx, room.RoomID, robot.UserID, robotUserID) {
					logger.Info("robot assigned to room after stale cleanup retry",
						"room_id", room.RoomID,
						"user_id", robot.UserID,
						"room_fee", room.RoomFee,
					)
					continue
				}
				logger.Warn("robot retry join after stale room cleanup still failed",
					"room_id", room.RoomID,
					"user_id", robot.UserID,
				)
			}

			// Failed to join, return robot to pool
			s.accountSvc.MarkRobotIdle(ctx, robot.UserID)
			s.robotSchedulerRedis.RemoveRobotFromRoom(ctx, room.RoomID, robot.UserID)
			s.robotSchedulerRedis.RemoveFromActiveSet(ctx, robot.UserID)
			s.robotSchedulerRedis.ReleaseAssignLock(ctx, robot.UserID, lockToken)
			logger.Warn("robot join and ready failed",
				"room_id", room.RoomID,
				"user_id", robot.UserID,
				"error", err,
			)
			continue
		}

		logger.Info("robot assigned to room",
			"room_id", room.RoomID,
			"user_id", robot.UserID,
			"room_fee", room.RoomFee,
		)
	}
	return nil
}

// retryJoinAfterCleaningStaleRoom 处理 CodeUserAlreadyInRoom 错误：
// 读取 userRoomKey 找到旧房间，调用 LeaveRoom 清理后重试 JoinAndReady。
// 返回 true 表示重试成功，false 表示重试失败或无法清理。
func (s *RobotSchedulerService) retryJoinAfterCleaningStaleRoom(ctx context.Context, roomID string, robotUserID int64, robotUserIDStr string) bool {
	staleRoomID, err := s.robotSchedulerRedis.GetUserRoom(ctx, robotUserID)
	if err != nil {
		logger.Warn("get stale user room failed during retry",
			"room_id", roomID,
			"user_id", robotUserID,
			"error", err,
		)
		return false
	}
	if staleRoomID == "" || staleRoomID == roomID {
		// userRoomKey 已不存在或指向当前房间：并非跨房间残留，重试无意义
		return false
	}

	logger.Warn("robot has stale userRoomKey, cleaning stale room before retry",
		"room_id", roomID,
		"user_id", robotUserID,
		"stale_room_id", staleRoomID,
	)

	// 清理旧房间（LeaveRoom 内部会调 CancelSeat + luaLeaveRoom，DEL userRoomKey + HDEL spectators）
	if leaveErr := s.robotPlayer.LeaveRoom(ctx, staleRoomID, robotUserIDStr); leaveErr != nil {
		if !message.IsErrorCode(leaveErr, message.CodeNotInRoom) {
			logger.Error("clean stale room leave failed",
				"room_id", roomID,
				"stale_room_id", staleRoomID,
				"user_id", robotUserID,
				"error", leaveErr,
			)
			return false
		}
		// CodeNotInRoom 视为已离开，继续重试
	}
	// 旧房间的调度器侧状态也清理
	s.robotSchedulerRedis.RemoveRobotFromRoom(ctx, staleRoomID, robotUserID)
	s.robotSchedulerRedis.RemoveFromActiveSet(ctx, robotUserID)

	// 重试 JoinAndReady
	if retryErr := s.robotPlayer.JoinAndReady(ctx, roomID, robotUserIDStr); retryErr != nil {
		logger.Warn("robot join retry failed after stale cleanup",
			"room_id", roomID,
			"user_id", robotUserID,
			"stale_room_id", staleRoomID,
			"error", retryErr,
		)
		return false
	}
	return true
}

// recycleZombieRobots detects robots that are in the room robot set but not
// actually seated in the room. These are zombie robots that failed to complete
// the seat/ready flow and should be recycled back to the available pool.
func (s *RobotSchedulerService) recycleZombieRobots(ctx context.Context, roomID string, robotIDs []int64) {
	state, err := s.repo.GetRoomStateData(ctx, roomID)
	if err != nil || state == nil {
		return
	}

	// Build set of seated user IDs for quick lookup
	seatedUserIDs := make(map[string]bool, len(state.Players))
	for _, player := range state.Players {
		if player.SeatNo > 0 {
			seatedUserIDs[player.UserID] = true
		}
	}

	for _, robotID := range robotIDs {
		robotUserID := converter.FormatID(robotID)
		if seatedUserIDs[robotUserID] {
			continue // robot is seated, not a zombie
		}

		logger.Warn("zombie robot detected, recycling",
			"room_id", roomID,
			"user_id", robotID,
		)

		// 注意清理顺序：必须先 LeaveRoom（luaLeaveRoom 会 DEL userRoomKey + HDEL spectators），
		// 成功后再清 robot:room 集合与 MySQL Idle。
		// 反序会导致 handleLeaveAction 的幂等检查（基于 robot:room 集合）误判为"已 leave"，
		// 从而跳过真正的 userRoomKey/spectators 清理，产生跨房间残留僵尸。
		// LeaveRoom 内部会先调 CancelSeat，对未选座的观众是 no-op，安全。
		if err := s.robotPlayer.LeaveRoom(ctx, roomID, robotUserID); err != nil {
			// CodeNotInRoom 视为已离开（如 LuaEndGame 已将玩家转 spectator 后又被清掉），
			// 可继续清理调度器侧状态。
			if !message.IsErrorCode(err, message.CodeNotInRoom) {
				logger.Error("zombie robot leave room failed, skip cleanup to allow next scan retry",
					"room_id", roomID,
					"user_id", robotID,
					"error", err,
				)
				continue
			}
			logger.Debug("zombie robot already not in room, proceeding with scheduler cleanup",
				"room_id", roomID,
				"user_id", robotID,
			)
		}

		// LeaveRoom 成功后再清理调度器侧状态
		s.robotSchedulerRedis.RemoveRobotFromRoom(ctx, roomID, robotID)
		s.robotSchedulerRedis.RemoveFromActiveSet(ctx, robotID)
		if err := s.accountSvc.MarkRobotIdle(ctx, robotID); err != nil {
			logger.Error("failed to mark zombie robot idle",
				"room_id", roomID,
				"user_id", robotID,
				"error", err,
			)
		}
	}
}

// getWaitingRooms scans Redis for room hash keys and returns those whose
// status is Waiting. The returned candidates are pre-populated with seated
// count, ready player count and room fee for downstream filtering.
func (s *RobotSchedulerService) getWaitingRooms(ctx context.Context) []roomCandidate {
	// KeyRoomHashPrefix 已是完整 SCAN 匹配模式（cashparty:*:room:hash），无需再 + "*"。
	const scanPattern = rediskeys.KeyRoomHashPrefix
	const scanCount = 200

	roomIDs, err := s.robotSchedulerRedis.ScanRoomIDs(ctx, scanPattern, scanCount)
	if err != nil {
		logger.Warn("redis scan failed", "pattern", scanPattern, "error", err)
	}
	if len(roomIDs) == 0 {
		return nil
	}

	candidates := make([]roomCandidate, 0, len(roomIDs))
	for _, roomID := range roomIDs {
		state, err := s.repo.GetRoomStateData(ctx, roomID)
		if err != nil || state == nil {
			continue
		}
		if state.Status != int(roomDom.RoomStatusWaiting) {
			continue
		}

		seatedCount := 0
		readyRealPlayers := 0
		for _, player := range state.Players {
			if player.SeatNo > 0 {
				seatedCount++
				if !player.IsRobot {
					readyRealPlayers++
				}
			}
		}

		waitingSince := time.Now()
		if state.Meta != nil && state.Meta.StartedAt != nil {
			waitingSince = time.Unix(*state.Meta.StartedAt, 0)
		}

		candidates = append(candidates, roomCandidate{
			RoomID:           roomID,
			ReadyPlayerCount: readyRealPlayers,
			WaitingSince:     waitingSince,
			SeatedCount:      seatedCount,
			RoomFee:          int(state.RoomFee),
		})
	}
	return candidates
}

// filterRoomsNeedingRobots keeps rooms that have at least MinRealPlayers real
// players ready and still have empty seats to fill.
func (s *RobotSchedulerService) filterRoomsNeedingRobots(ctx context.Context, rooms []roomCandidate) []roomCandidate {
	if len(rooms) == 0 {
		return nil
	}
	minRealPlayers := s.config.Scheduler.MinRealPlayers
	maxPlayers := roomDom.MaxPlayers
	result := make([]roomCandidate, 0, len(rooms))
	for _, room := range rooms {
		if room.ReadyPlayerCount < minRealPlayers {
			continue
		}
		if room.SeatedCount >= maxPlayers {
			continue
		}
		result = append(result, room)
	}
	return result
}

// checkPoolReserve logs a warning when the available robot pool drops below
// the configured ReserveCount.
func (s *RobotSchedulerService) checkPoolReserve(ctx context.Context) {
	if s.robotPool == nil {
		return
	}
	availableCount, err := s.robotPool.GetAvailableCount(ctx)
	if err != nil {
		logger.Warn("failed to get robot pool available count", "error", err)
		return
	}
	if s.config.Scheduler.ReserveCount > 0 && availableCount < int64(s.config.Scheduler.ReserveCount) {
		logger.Warn("robot pool reserve low",
			"available", availableCount,
			"reserve", s.config.Scheduler.ReserveCount,
		)
	}
}

// cleanupEndedRooms is a best-effort fallback that recycles robots still
// associated with rooms that are no longer in Waiting or Playing state. It
// guards against missed OnGameEnd events.
func (s *RobotSchedulerService) cleanupEndedRooms(ctx context.Context) {
	// KeyRobotRoomPrefix 已是完整 SCAN 匹配模式（cashparty:*:robot:room），无需再 + "*"。
	const scanPattern = rediskeys.KeyRobotRoomPrefix
	const scanCount = 200

	roomIDs, err := s.robotSchedulerRedis.ScanRoomIDs(ctx, scanPattern, scanCount)
	if err != nil {
		logger.Warn("redis scan failed", "pattern", scanPattern, "error", err)
	}
	if len(roomIDs) == 0 {
		return
	}

	for _, roomID := range roomIDs {
		robotIDs, err := s.robotSchedulerRedis.GetRoomRobots(ctx, roomID)
		if err != nil || len(robotIDs) == 0 {
			continue
		}

		state, err := s.repo.GetRoomStateData(ctx, roomID)
		if err != nil || state == nil {
			continue
		}

		// 仅回收 Idle 状态房间的残留机器人。
		// 不回收 Interrupted 状态：该状态是等待替补的过渡态（30s 窗口），
		// EndGame 尚未执行，players 仍未转为 spectators，
		// 此时调用 LeaveRoom 会因玩家仍在 playersKey 中而被 Lua 脚本拒绝（错误码 16）。
		// Interrupted 状态会在 OnReplaceTimeout 触发 EndGame 后转为 Idle，届时下一轮扫描即可回收。
		if state.Status != int(roomDom.RoomStatusIdle) {
			continue
		}

		logger.Info("cleanup residual robots in ended room",
			"room_id", roomID,
			"robot_count", len(robotIDs),
			"status", state.Status,
		)
		s.recycleRoomRobots(ctx, roomID, robotIDs)
	}
}

// recycleRoomRobots makes every robot leave the room immediately.
// The cleanup (MarkRobotIdle, RemoveRobotFromRoom, etc.) is performed
// by the behavior engine's handleLeaveAction handler.
func (s *RobotSchedulerService) recycleRoomRobots(ctx context.Context, roomID string, robotIDs []int64) {
	for _, robotID := range robotIDs {
		robotUserID := converter.FormatID(robotID)
		if s.behaviorEngine != nil {
			s.behaviorEngine.LeaveRoomNow(ctx, roomID, robotUserID)
		} else {
			s.robotPlayer.ScheduleLeave(ctx, roomID, robotUserID, 0)
		}
	}
}

// OnGameEnd is called when a game ends. It makes every robot leave the
// room immediately. The cleanup is performed by the behavior engine.
func (s *RobotSchedulerService) OnGameEnd(ctx context.Context, roomID string) {
	robotIDs, err := s.robotSchedulerRedis.GetRoomRobots(ctx, roomID)
	if err != nil {
		logger.Warn("failed to get room robots on game end",
			"room_id", roomID,
			"error", err,
		)
		return
	}
	if len(robotIDs) == 0 {
		return
	}

	logger.Info("robots leaving on game end",
		"room_id", roomID,
		"robot_count", len(robotIDs),
	)
	for _, robotID := range robotIDs {
		robotUserID := converter.FormatID(robotID)
		if s.behaviorEngine != nil {
			s.behaviorEngine.LeaveRoomNow(ctx, roomID, robotUserID)
		} else {
			s.robotPlayer.ScheduleLeave(ctx, roomID, robotUserID, 0)
		}
	}
}

// ValidateReserveRatio checks that ReserveCount is not more than 50% of the
// total robot pool size. It is intended to be called once at startup. A
// warning is logged when the ratio exceeds ReserveRatioMax.
func (s *RobotSchedulerService) ValidateReserveRatio(ctx context.Context) {
	if s.config == nil || s.config.Scheduler.ReserveCount <= 0 {
		return
	}
	total, err := s.accountSvc.GetTotalRobotCount(ctx)
	if err != nil {
		logger.Warn("failed to get total robot count for reserve validation", "error", err)
		return
	}
	if total <= 0 {
		return
	}
	ratio := float64(s.config.Scheduler.ReserveCount) / float64(total)
	ratioMax := s.config.Scheduler.ReserveRatioMax
	if ratioMax <= 0 {
		ratioMax = 0.5
	}
	if ratio > ratioMax {
		logger.Warn("robot config: ReserveCount exceeds allowed ratio of total pool",
			"reserve_count", s.config.Scheduler.ReserveCount,
			"total_robots", total,
			"ratio", ratio,
			"ratio_max", ratioMax,
		)
	}
}
