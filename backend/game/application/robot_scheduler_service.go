package application

import (
	"context"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/converter"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/domain"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
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
	repo                domain.RoomRepository
	dbRepo              domain.DBRepository
	redis               *cRedis.Client
	robotSchedulerRedis *redis.RobotSchedulerRedis
	robotPool           *redis.RobotPoolService
	config              *config.RobotConfig
	ctx                 context.Context
	cancel              context.CancelFunc
}

// NewRobotSchedulerService creates a new RobotSchedulerService instance.
func NewRobotSchedulerService(
	accountSvc *RobotAccountService,
	robotPlayer *RobotPlayer,
	repo domain.RoomRepository,
	dbRepo domain.DBRepository,
	redisClient *cRedis.Client,
	robotSchedulerRedis *redis.RobotSchedulerRedis,
	robotPool *redis.RobotPoolService,
	cfg *config.RobotConfig,
) *RobotSchedulerService {
	return &RobotSchedulerService{
		accountSvc:          accountSvc,
		robotPlayer:         robotPlayer,
		repo:                repo,
		dbRepo:              dbRepo,
		redis:               redisClient,
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

// Start launches the scan loop in a background goroutine. The scan interval
// is taken from the scheduler config.
func (s *RobotSchedulerService) Start() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	go s.scanLoop()
	logger.Info("robot scheduler service started", "scan_interval", s.config.Scheduler.ScanInterval)
}

// Stop signals the scan loop to exit. It is safe to call multiple times.
func (s *RobotSchedulerService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	logger.Info("robot scheduler service stopped")
}

// scanLoop periodically calls scanRooms until the service context is cancelled.
func (s *RobotSchedulerService) scanLoop() {
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
// 80% of the scan interval to avoid overlapping with the next tick.
func (s *RobotSchedulerService) scanRooms() {
	ctx := context.Background()
	// Time budget: 80% of scan interval
	budget := time.Duration(float64(s.config.Scheduler.ScanInterval) * 0.8)
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
	locked, err := s.robotSchedulerRedis.AcquireRoomAssignLock(ctx, room.RoomID, s.config.Scheduler.RoomAssignLockTTL)
	if err != nil || !locked {
		return nil // skip, rate limited
	}

	// 2. Detect and recycle zombie robots (in room set but not seated)
	existingRobots, _ := s.robotSchedulerRedis.GetRoomRobots(ctx, room.RoomID)
	if len(existingRobots) > 0 {
		s.recycleZombieRobots(ctx, room.RoomID, existingRobots)
		// Refresh after recycling
		existingRobots, _ = s.robotSchedulerRedis.GetRoomRobots(ctx, room.RoomID)
	}

	// 3. Calculate needed robots = MaxPlayers - seated count
	maxPlayers := MaxPlayers
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
		locked, err := s.robotSchedulerRedis.AcquireAssignLock(ctx, robot.UserID, room.RoomID, s.config.Scheduler.RobotAssignLockTTL)
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
			s.robotSchedulerRedis.ReleaseAssignLock(ctx, robot.UserID)
			continue
		}

		// Add to room robot set and active set
		s.robotSchedulerRedis.AddRobotToRoom(ctx, room.RoomID, robot.UserID)
		s.robotSchedulerRedis.AddToActiveSet(ctx, robot.UserID)

		// Call RobotPlayer.JoinAndReady
		robotUserID := converter.FormatID(robot.UserID)
		err = s.robotPlayer.JoinAndReady(ctx, room.RoomID, robotUserID)
		if err != nil {
			// Failed to join, return robot to pool
			s.accountSvc.MarkRobotIdle(ctx, robot.UserID)
			s.robotSchedulerRedis.RemoveRobotFromRoom(ctx, room.RoomID, robot.UserID)
			s.robotSchedulerRedis.RemoveFromActiveSet(ctx, robot.UserID)
			s.robotSchedulerRedis.ReleaseAssignLock(ctx, robot.UserID)
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

		// Remove from room robot set and active set
		s.robotSchedulerRedis.RemoveRobotFromRoom(ctx, roomID, robotID)
		s.robotSchedulerRedis.RemoveFromActiveSet(ctx, robotID)

		// Mark robot idle so it returns to the available pool
		if err := s.accountSvc.MarkRobotIdle(ctx, robotID); err != nil {
			logger.Error("failed to mark zombie robot idle",
				"room_id", roomID,
				"user_id", robotID,
				"error", err,
			)
		}

		// Try to leave the room (cancel seat if any, leave room)
		s.robotPlayer.LeaveRoom(ctx, roomID, robotUserID)
	}
}

// getWaitingRooms scans Redis for room hash keys and returns those whose
// status is Waiting. The returned candidates are pre-populated with seated
// count, ready player count and room fee for downstream filtering.
func (s *RobotSchedulerService) getWaitingRooms(ctx context.Context) []roomCandidate {
	const scanPattern = redis.KeyRoomHashPrefix + ":*"
	const scanCount = 200

	roomIDs := s.scanRoomIDs(ctx, scanPattern, scanCount)
	if len(roomIDs) == 0 {
		return nil
	}

	candidates := make([]roomCandidate, 0, len(roomIDs))
	for _, roomID := range roomIDs {
		state, err := s.repo.GetRoomStateData(ctx, roomID)
		if err != nil || state == nil {
			continue
		}
		if state.Status != int(domain.RoomStatusWaiting) {
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
	maxPlayers := MaxPlayers
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
	const scanPattern = redis.KeyRobotRoomPrefix + "*"
	const scanCount = 200

	roomIDs := s.scanRoomIDs(ctx, scanPattern, scanCount)
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

		// Only recycle when the room is idle or interrupted (game ended).
		if state.Status != int(domain.RoomStatusIdle) && state.Status != int(domain.RoomStatusInterrupted) {
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

// randomDelay returns a uniform random duration in [min, max). If max <= min
// min is returned unchanged.
func (s *RobotSchedulerService) randomDelay(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
}

// scanRoomIDs scans Redis for keys matching the given pattern and extracts
// the trailing segment after the last colon as the room ID. This works for
// both room hash keys (cashparty:room:hash:{roomID}) and robot room set keys
// (robot:room:{roomID}).
func (s *RobotSchedulerService) scanRoomIDs(ctx context.Context, pattern string, count int64) []string {
	var cursor uint64
	roomIDs := make([]string, 0)
	for {
		keys, nextCursor, err := s.redis.Scan(ctx, cursor, pattern, count).Result()
		if err != nil {
			logger.Warn("redis scan failed", "pattern", pattern, "error", err)
			return roomIDs
		}
		for _, key := range keys {
			// Extract roomID from key by taking the segment after the last colon.
			idx := strings.LastIndex(key, ":")
			if idx < 0 || idx == len(key)-1 {
				continue
			}
			roomID := key[idx+1:]
			if roomID == "" {
				continue
			}
			roomIDs = append(roomIDs, roomID)
		}
		if nextCursor == 0 {
			break
		}
		cursor = nextCursor
	}
	return roomIDs
}
