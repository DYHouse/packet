package robot

import (
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/game/domain/room"
)

// SendTimeout is the existing send packet timeout used as an upper bound for
// robot send delay validation.
const SendTimeout = 30 * time.Second

// ValidateRobotConfig validates the robot configuration and auto-corrects
// invalid values in place. Issues are reported via logger.Warn.
//
// Note: ReserveCount / total pool count <= ReserveRatioMax validation requires
// a DB query and is therefore performed in bootstrap at startup. This function
// only validates that ReserveRatioMax > 0.
func ValidateRobotConfig(cfg *config.RobotConfig) {
	if cfg == nil {
		return
	}

	// SubTask 11.2: MaxRobotsPerRoom + MinRealPlayers <= MaxPlayers(5)
	if cfg.Scheduler.MaxRobotsPerRoom+cfg.Scheduler.MinRealPlayers > room.MaxPlayers {
		old := cfg.Scheduler.MaxRobotsPerRoom
		cfg.Scheduler.MaxRobotsPerRoom = room.MaxPlayers - cfg.Scheduler.MinRealPlayers
		if cfg.Scheduler.MaxRobotsPerRoom < 0 {
			cfg.Scheduler.MaxRobotsPerRoom = 0
		}
		logger.Warn("robot config: MaxRobotsPerRoom+MinRealPlayers exceeds MaxPlayers, auto-corrected",
			"old_max_robots_per_room", old,
			"min_real_players", cfg.Scheduler.MinRealPlayers,
			"max_players", room.MaxPlayers,
			"new_max_robots_per_room", cfg.Scheduler.MaxRobotsPerRoom,
		)
	}

	// SubTask 11.3: ReserveRatioMax > 0
	if cfg.Scheduler.ReserveRatioMax <= 0 {
		logger.Warn("robot config: ReserveRatioMax should be > 0, please check configuration",
			"reserve_ratio_max", cfg.Scheduler.ReserveRatioMax)
	}

	// SubTask 11.4: Validate all DelayMin < DelayMax, swap if needed
	swapIfInverted := func(name string, minPtr, maxPtr *time.Duration) {
		if *minPtr > *maxPtr {
			*minPtr, *maxPtr = *maxPtr, *minPtr
			logger.Warn("robot config: DelayMin > DelayMax, swapped values",
				"delay_name", name,
				"new_min", *minPtr,
				"new_max", *maxPtr,
			)
		}
	}
	swapIfInverted("SeatDelay", &cfg.Behavior.SeatDelayMin, &cfg.Behavior.SeatDelayMax)
	swapIfInverted("ReadyDelay", &cfg.Behavior.ReadyDelayMin, &cfg.Behavior.ReadyDelayMax)
	swapIfInverted("GrabDelay", &cfg.Behavior.GrabDelayMin, &cfg.Behavior.GrabDelayMax)
	swapIfInverted("SendDelay", &cfg.Behavior.SendDelayMin, &cfg.Behavior.SendDelayMax)
	swapIfInverted("LeaveAfterGameDelay", &cfg.Behavior.LeaveAfterGameMin, &cfg.Behavior.LeaveAfterGameMax)

	// SubTask 11.5: SendDelayMax < SendTimeout(30s)
	if cfg.Behavior.SendDelayMax >= SendTimeout {
		logger.Warn("robot config: SendDelayMax should be < send timeout to ensure robot sends before timeout",
			"send_delay_max", cfg.Behavior.SendDelayMax,
			"send_timeout", SendTimeout,
		)
	}

	// SubTask 11.6: GrabSkipProb in [0, 1]
	if cfg.Behavior.GrabSkipProb < 0 || cfg.Behavior.GrabSkipProb > 1 {
		old := cfg.Behavior.GrabSkipProb
		if cfg.Behavior.GrabSkipProb < 0 {
			cfg.Behavior.GrabSkipProb = 0
		} else {
			cfg.Behavior.GrabSkipProb = 1
		}
		logger.Warn("robot config: GrabSkipProb out of [0,1], clamped",
			"old_value", old,
			"new_value", cfg.Behavior.GrabSkipProb,
		)
	}

	// SubTask 11.7: InitialBalanceMulti >= 1.0
	if cfg.Account.InitialBalanceMulti < 1.0 {
		old := cfg.Account.InitialBalanceMulti
		cfg.Account.InitialBalanceMulti = 1.0
		logger.Warn("robot config: InitialBalanceMulti < 1.0, set to 1.0",
			"old_value", old,
			"new_value", cfg.Account.InitialBalanceMulti,
		)
	}
}
