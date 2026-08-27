package robot

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/game/application"
	repository "github.com/cashparty/backend/game/domain/repository"
	"github.com/cashparty/backend/game/model"
	settlementDomain "github.com/cashparty/backend/settlement/domain"
)

// minRoomFee and maxRoomFee define the room fee range a robot can join.
// The range covers all configured room tiers (100 .. 50000).
const (
	minRoomFee = 100
	maxRoomFee = 50000
)

// RobotAccountService manages the lifecycle of robot accounts including
// creation, status transitions, virtual balance operations and lookups.
type RobotAccountService struct {
	repo             repository.RobotAccountRepository
	userSvc          *application.UserService
	virtualBalance   settlementDomain.VirtualBalanceService
	robotPool        repository.RobotPoolRepository
	avatarCfg        *config.AvatarConfig
	robotUserIDGen   idgen.IDGenerator
}

// NewRobotAccountService creates a new RobotAccountService instance.
func NewRobotAccountService(
	repo repository.RobotAccountRepository,
	userSvc *application.UserService,
	virtualBalance settlementDomain.VirtualBalanceService,
	robotPool repository.RobotPoolRepository,
	avatarCfg *config.AvatarConfig,
	robotUserIDGen idgen.IDGenerator,
) *RobotAccountService {
	return &RobotAccountService{
		repo:             repo,
		userSvc:          userSvc,
		virtualBalance:   virtualBalance,
		robotPool:        robotPool,
		avatarCfg:        avatarCfg,
		robotUserIDGen:   robotUserIDGen,
	}
}

// BatchCreateRobotsWithBalance creates the given number of robot accounts with
// a specified initial virtual balance and registers them in Redis.
func (s *RobotAccountService) BatchCreateRobotsWithBalance(ctx context.Context, count int, initialBalance int64) error {
	if count <= 0 {
		return nil
	}

	for i := 1; i <= count; i++ {
		// 机器人外部 user_id 采用 "robot_" + 雪花 ID，天然唯一。
		// 旧版使用固定编号 robot_%05d，该格式在 SaveUser 复用旧记录时会导致
		// 新昵称无法写入（SaveUser 遇到已存在 user_id 直接跳过），雪花 ID 可避免此问题，
		// 并支持任意规模扩容，无需记忆起始编号。
		robotUserIDInt, err := s.robotUserIDGen.GenerateInt64()
		if err != nil {
			logger.Error("failed to generate robot user id", "seq", i, "error", err)
			return err
		}
		robotUserID := fmt.Sprintf("robot_%d", robotUserIDInt)
		nickname := GenerateNickname()

		avatar := ""
		if s.avatarCfg != nil {
			avatar = application.GetRandomAvatar(s.avatarCfg.BaseURL, s.avatarCfg.DefaultCount)
		}

		formattedID, savedAvatar, err := s.userSvc.SaveUser(ctx, robotUserID, nickname, avatar, "", "")
		if err != nil {
			logger.Error("failed to save robot user", "user_id", robotUserID, "error", err)
			return err
		}
		if savedAvatar != "" {
			avatar = savedAvatar
		}

		userID, err := strconv.ParseInt(formattedID, 10, 64)
		if err != nil {
			logger.Error("failed to parse robot user id", "formatted_id", formattedID, "error", err)
			return err
		}

		if err := s.userSvc.SetUserIsRobot(ctx, userID); err != nil {
			logger.Error("failed to mark user as robot", "user_id", userID, "error", err)
			return err
		}

		account := &model.RobotAccount{
			UserID:         userID,
			Status:         model.RobotStatusIdle,
			VirtualBalance: initialBalance,
			MinRoomFee:     minRoomFee,
			MaxRoomFee:     maxRoomFee,
		}
		if err := s.repo.Create(ctx, account); err != nil {
			logger.Error("failed to create robot account", "user_id", userID, "error", err)
			return err
		}

		if err := s.virtualBalance.AddToRobotSet(ctx, userID); err != nil {
			logger.Error("failed to add to robot set", "user_id", userID, "error", err)
			return err
		}

		if err := s.virtualBalance.SetBalance(ctx, userID, initialBalance); err != nil {
			logger.Error("failed to set virtual balance", "user_id", userID, "error", err)
			return err
		}

		if err := s.robotPool.AddToAvailablePool(ctx, userID); err != nil {
			logger.Error("failed to add to available pool", "user_id", userID, "error", err)
			return err
		}

		logger.Info("robot account created",
			"seq", i,
			"user_id", userID,
			"nickname", nickname,
			"avatar", avatar,
			"virtual_balance", initialBalance,
		)
	}

	logger.Info("batch create robots completed", "count", count, "initial_balance", initialBalance)
	return nil
}

// GetAvailableRobot returns an idle robot that can join a room with the given
// room fee and has enough virtual balance to play. Returns nil, nil if no
// robot is available.
func (s *RobotAccountService) GetAvailableRobot(ctx context.Context, roomFee int) (*model.RobotAccount, error) {
	balanceRequired := int64(roomFee/5 + roomFee*9)

	robots, err := s.repo.GetAvailableRobots(ctx, roomFee, roomFee)
	if err != nil {
		return nil, err
	}

	for _, robot := range robots {
		balance, err := s.virtualBalance.GetBalance(ctx, robot.UserID)
		if err != nil {
			logger.Warn("failed to get robot virtual balance", "user_id", robot.UserID, "error", err)
			continue
		}
		if balance >= balanceRequired {
			return robot, nil
		}
	}

	return nil, nil
}

// MarkRobotInGame marks a robot as being in an active game and removes it from
// the available pool.
func (s *RobotAccountService) MarkRobotInGame(ctx context.Context, userID int64) error {
	if err := s.repo.UpdateStatus(ctx, userID, model.RobotStatusInGame); err != nil {
		return err
	}
	return s.robotPool.RemoveFromAvailablePool(ctx, userID)
}

// MarkRobotIdle marks a robot as idle, returns it to the available pool and
// refreshes its last active timestamp.
func (s *RobotAccountService) MarkRobotIdle(ctx context.Context, userID int64) error {
	if err := s.repo.UpdateStatus(ctx, userID, model.RobotStatusIdle); err != nil {
		return err
	}
	if err := s.robotPool.AddToAvailablePool(ctx, userID); err != nil {
		return err
	}
	return s.repo.UpdateLastActiveAt(ctx, userID)
}

// CheckLowBalance reports whether the robot's virtual balance is below the
// threshold multiplied balance required for the given room fee.
func (s *RobotAccountService) CheckLowBalance(ctx context.Context, userID int64, roomFee int, threshold float64) (bool, error) {
	balanceRequired := int64(float64(roomFee/5+roomFee*9) * threshold)

	balance, err := s.virtualBalance.GetBalance(ctx, userID)
	if err != nil {
		return false, err
	}
	return balance < balanceRequired, nil
}

// DisableRobot marks a robot as disabled and removes it from the available
// pool.
func (s *RobotAccountService) DisableRobot(ctx context.Context, userID int64) error {
	if err := s.repo.UpdateStatus(ctx, userID, model.RobotStatusDisabled); err != nil {
		return err
	}
	return s.robotPool.RemoveFromAvailablePool(ctx, userID)
}

// RechargeVirtualBalance credits the robot's virtual balance. If the robot was
// disabled, it is re-enabled and returned to the available pool.
func (s *RobotAccountService) RechargeVirtualBalance(ctx context.Context, userID int64, amount int64) error {
	if err := s.virtualBalance.Credit(ctx, userID, amount); err != nil {
		return err
	}

	account, err := s.repo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	if account.Status == model.RobotStatusDisabled {
		if err := s.repo.UpdateStatus(ctx, userID, model.RobotStatusIdle); err != nil {
			return err
		}
		if err := s.robotPool.AddToAvailablePool(ctx, userID); err != nil {
			return err
		}
	}

	return nil
}

// GetRobotByUserID returns the robot account for the given user id.
func (s *RobotAccountService) GetRobotByUserID(ctx context.Context, userID int64) (*model.RobotAccount, error) {
	return s.repo.GetByUserID(ctx, userID)
}

// GetTotalRobotCount returns the total number of robot accounts.
func (s *RobotAccountService) GetTotalRobotCount(ctx context.Context) (int64, error) {
	return s.repo.Count(ctx)
}
