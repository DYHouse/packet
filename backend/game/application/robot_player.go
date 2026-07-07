package application

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/utils"
	"github.com/cashparty/backend/game/domain"
)

// ErrNoEmptySeat is returned when no empty seat is available in the room.
var ErrNoEmptySeat = errors.New("no empty seat available")

// RobotActionScheduler schedules delayed robot actions.
// It is implemented by RobotBehaviorEngine (created later) to break the
// circular dependency between RobotPlayer and RobotBehaviorEngine.
type RobotActionScheduler interface {
	ScheduleAction(ctx context.Context, roomID string, robotUserID string, action string, delay time.Duration)
}

// RobotPlayer drives a single robot through the room lifecycle: join, seat,
// ready, grab, send and leave. Delayed actions are delegated to a
// RobotActionScheduler (the behavior engine) which is injected after
// construction via SetBehaviorEngine.
type RobotPlayer struct {
	seatAppService *SeatAppService
	gameAppService *GameAppService
	roomAppService *RoomAppService
	accountSvc     *RobotAccountService
	grabSvc        *GrabService
	repo           domain.RoomRepository
	behaviorEngine RobotActionScheduler
	config         *config.RobotConfig
}

// NewRobotPlayer creates a new RobotPlayer instance. The behavior engine
// must be set separately via SetBehaviorEngine to break the circular
// dependency between RobotPlayer and RobotBehaviorEngine.
func NewRobotPlayer(
	seatAppService *SeatAppService,
	gameAppService *GameAppService,
	roomAppService *RoomAppService,
	accountSvc *RobotAccountService,
	grabSvc *GrabService,
	repo domain.RoomRepository,
	cfg *config.RobotConfig,
) *RobotPlayer {
	return &RobotPlayer{
		seatAppService: seatAppService,
		gameAppService: gameAppService,
		roomAppService: roomAppService,
		accountSvc:     accountSvc,
		grabSvc:        grabSvc,
		repo:           repo,
		config:         cfg,
	}
}

// SetBehaviorEngine injects the behavior engine (scheduler) after both
// RobotPlayer and the engine have been constructed.
func (p *RobotPlayer) SetBehaviorEngine(engine RobotActionScheduler) {
	p.behaviorEngine = engine
}

// JoinAndReady makes the robot join the room as a spectator and schedules a
// delayed seat selection. The ready action is chained after the seat is
// selected via SelectSeat.
func (p *RobotPlayer) JoinAndReady(ctx context.Context, roomID string, robotUserID string) error {
	_, err := p.roomAppService.JoinRoom(ctx, &JoinRoomRequest{
		RoomID: roomID,
		UserID: robotUserID,
	})
	if err != nil {
		return err
	}

	roomState, err := p.repo.GetRoomStateData(ctx, roomID)
	if err != nil {
		return err
	}
	if !p.hasEmptySeat(roomState) {
		return ErrNoEmptySeat
	}

	delay := utils.RandomDelay(p.config.Behavior.SeatDelayMin, p.config.Behavior.SeatDelayMax)
	p.behaviorEngine.ScheduleAction(ctx, roomID, robotUserID, "seat", delay)
	return nil
}

// SelectSeat picks a random empty seat and schedules a delayed ready action.
// If the chosen seat is taken (race condition), it retries with a different
// seat up to config.Behavior.ActionRetryMax times.
func (p *RobotPlayer) SelectSeat(ctx context.Context, roomID string, robotUserID string) error {
	maxRetries := p.config.Behavior.ActionRetryMax
	if maxRetries <= 0 {
		maxRetries = 2
	}
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			logger.Info("robot select seat retry",
				"room_id", roomID,
				"robot_user_id", robotUserID,
				"attempt", attempt,
			)
		}
		roomState, err := p.repo.GetRoomStateData(ctx, roomID)
		if err != nil {
			return err
		}
		seatNo := p.pickRandomEmptySeat(roomState)
		if seatNo == 0 {
			return ErrNoEmptySeat
		}

		_, err = p.seatAppService.SelectSeat(ctx, &SelectSeatRequest{
			RoomID: roomID,
			UserID: robotUserID,
			SeatNo: seatNo,
		})
		if err == nil {
			delay := utils.RandomDelay(p.config.Behavior.ReadyDelayMin, p.config.Behavior.ReadyDelayMax)
			p.behaviorEngine.ScheduleAction(ctx, roomID, robotUserID, "ready", delay)
			return nil
		}
		// Seat taken or other transient error; retry with a different seat
		logger.Warn("robot select seat failed, will retry",
			"room_id", roomID,
			"robot_user_id", robotUserID,
			"seat_no", seatNo,
			"attempt", attempt,
			"error", err,
		)
	}
	return ErrNoEmptySeat
}

// Ready marks the robot as ready for the game to start.
func (p *RobotPlayer) Ready(ctx context.Context, roomID string, robotUserID string) error {
	_, err := p.seatAppService.PlayerReady(ctx, &PlayerReadyRequest{
		RoomID: roomID,
		UserID: robotUserID,
	})
	return err
}

// GrabPacket atomically picks a random available packet and grabs it for the
// robot using a single Lua call, avoiding the race condition of the two-step
// approach (GetAvailablePacketID + GrabPacket).
func (p *RobotPlayer) GrabPacket(ctx context.Context, roomID string, roundID string, robotUserID string) error {
	result, err := p.grabSvc.RobotGrabPacket(ctx, roomID, roundID, robotUserID)
	if err != nil {
		return err
	}
	p.gameAppService.OnRobotGrabbed(ctx, roomID, roundID, robotUserID, result)
	return nil
}

// SendPacket triggers a packet send for the robot when it is the sender's
// turn.
func (p *RobotPlayer) SendPacket(ctx context.Context, roomID string, robotUserID string) error {
	_, err := p.gameAppService.SendPacket(ctx, &SendPacketRequest{
		RoomID: roomID,
		UserID: robotUserID,
	})
	return err
}

// LeaveRoom cancels any held seat and removes the robot from the room.
func (p *RobotPlayer) LeaveRoom(ctx context.Context, roomID string, robotUserID string) error {
	_, _ = p.seatAppService.CancelSeat(ctx, &CancelSeatRequest{
		RoomID: roomID,
		UserID: robotUserID,
	})
	_, err := p.roomAppService.LeaveRoom(ctx, &LeaveRoomRequest{
		RoomID: roomID,
		UserID: robotUserID,
	})
	if err != nil {
		logger.Warn("robot leave room failed", "room_id", roomID, "user_id", robotUserID, "error", err)
	}
	return err
}

// ScheduleLeave schedules a delayed leave action for the robot through the
// behavior engine. The actual cleanup (mark idle, remove from room robot set,
// release locks) is performed by the behavior engine's handleLeaveAction
// handler when the leave action fires.
func (p *RobotPlayer) ScheduleLeave(ctx context.Context, roomID string, robotUserID string, delay time.Duration) {
	if p.behaviorEngine == nil {
		logger.Warn("behavior engine not set, cannot schedule leave",
			"room_id", roomID,
			"user_id", robotUserID,
		)
		return
	}
	p.behaviorEngine.ScheduleAction(ctx, roomID, robotUserID, "leave", delay)
}

// hasEmptySeat reports whether any seat in the room is still free. A seat is
// considered empty when no player occupies it and SeatOwners does not map it
// to a non-empty user id.
func (p *RobotPlayer) hasEmptySeat(state *domain.RoomStateData) bool {
	if state == nil {
		return false
	}
	maxPlayers := state.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = domain.MaxPlayers
	}
	playerSeats := make(map[int]bool, len(state.Players))
	for _, player := range state.Players {
		if player.SeatNo > 0 {
			playerSeats[player.SeatNo] = true
		}
	}
	for i := 1; i <= maxPlayers; i++ {
		if playerSeats[i] {
			continue
		}
		owner, ok := state.SeatOwners[i]
		if !ok || owner == "" || owner == "0" {
			return true
		}
	}
	return false
}

// pickRandomEmptySeat returns a random empty seat number (1-indexed) or 0 if
// no seat is available.
func (p *RobotPlayer) pickRandomEmptySeat(state *domain.RoomStateData) int {
	if state == nil {
		return 0
	}
	maxPlayers := state.MaxPlayers
	if maxPlayers <= 0 {
		maxPlayers = domain.MaxPlayers
	}
	playerSeats := make(map[int]bool, len(state.Players))
	for _, player := range state.Players {
		if player.SeatNo > 0 {
			playerSeats[player.SeatNo] = true
		}
	}
	emptySeats := make([]int, 0, maxPlayers)
	for i := 1; i <= maxPlayers; i++ {
		if playerSeats[i] {
			continue
		}
		owner, ok := state.SeatOwners[i]
		if !ok || owner == "" || owner == "0" {
			emptySeats = append(emptySeats, i)
		}
	}
	if len(emptySeats) == 0 {
		return 0
	}
	return emptySeats[rand.Intn(len(emptySeats))]
}
