package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/cashparty/backend/game/application"
	"github.com/cashparty/backend/game/application/robot"
	gameconfig "github.com/cashparty/backend/game/config"
	"github.com/cashparty/backend/game/domain/repository"
	mysqlRepo "github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	settlementRedis "github.com/cashparty/backend/settlement/infrastructure/persistence/redis"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// robotAccountStoreAdapter 将 game 层的 repository.RobotAccountRepository 适配为
// settlement/domain.RobotAccountStore 接口，避免 settlement → game 反向依赖。
type robotAccountStoreAdapter struct {
	repo repository.RobotAccountRepository
}

// GetVirtualBalance 根据 userID 查询机器人账户的虚拟余额。
func (a *robotAccountStoreAdapter) GetVirtualBalance(ctx context.Context, userID int64) (int64, error) {
	account, err := a.repo.GetByUserID(ctx, userID)
	if err != nil {
		return 0, err
	}
	return account.VirtualBalance, nil
}

// UpdateBalance 更新机器人账户的虚拟余额。
func (a *robotAccountStoreAdapter) UpdateBalance(ctx context.Context, userID int64, balance int64) error {
	return a.repo.UpdateBalance(ctx, userID, balance)
}

func main() {
	count := flag.Int("count", 10, "Number of robot accounts to create")
	initialBalance := flag.Int64("initial_balance", 1000000, "Initial virtual balance in cents (default: 1000000 = 10000 yuan)")
	configPath := flag.String("config", "./config/game.yaml", "Path to game config file")
	clearExisting := flag.Bool("clear", false, "Clear existing robot accounts before initialization")
	flag.Parse()

	cfg, err := gameconfig.Load(*configPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	logger.Init(&logger.LogConfig{Level: cfg.Log.Level})

	ctx := context.Background()

	// 1. Connect to MySQL
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect mysql failed: %v", err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	// Tables are migrated by init_rooms.go; run it first if not done.

	// 2. Connect to Redis (use common client so services can reuse it)
	redisClient, err := cRedis.NewClient(&cfg.Redis)
	if err != nil {
		log.Fatalf("connect redis failed: %v", err)
	}
	defer redisClient.Close()

	// 2.5 初始化雪花 ID 生成器（脚本场景使用显式 node_id，规约 SID-7）
	// 脚本为单实例运行，使用 node_id=1 即可；若 cfg.IDGenerator.NodeID > 0 则使用配置值
	scriptNodeID := int64(1)
	if cfg.IDGenerator.Enabled && cfg.IDGenerator.NodeID > 0 {
		scriptNodeID = cfg.IDGenerator.NodeID
	}
	if err := idgen.Init(scriptNodeID); err != nil {
		log.Fatalf("init id generator failed: %v", err)
	}
	idGen, err := idgen.GetGenerator()
	if err != nil {
		log.Fatalf("get id generator failed: %v", err)
	}

	// 3. Create RobotAccountRepository
	robotRepo := mysqlRepo.NewRobotAccountRepository(db)

	// 4. Create VirtualBalanceService（settlement 层实现，通过适配器解耦）
	virtualBalanceSvc := settlementRedis.NewVirtualBalanceRepository(redisClient, &robotAccountStoreAdapter{repo: robotRepo})

	// 5. Create RobotPoolService
	robotPoolSvc := redis.NewRobotPoolService(redisClient)

	// 6. Create UserService (use DBRepository so SaveUser works end-to-end)
	dbRepo := mysqlRepo.NewDBRepository(db)
	userSvc := application.NewUserService(dbRepo, redisClient, &cfg.Avatar, idGen)

	// 7. Create RobotAccountService
	robotAccountSvc := robot.NewRobotAccountService(
		robotRepo,
		userSvc,
		virtualBalanceSvc,
		robotPoolSvc,
		&cfg.Avatar,
	)

	// 8. Clear existing robots if requested
	if *clearExisting {
		log.Println("clearing existing robot accounts...")
		if err := clearRobotAccounts(ctx, db, redisClient); err != nil {
			log.Fatalf("clear robot accounts failed: %v", err)
		}
		log.Println("existing robot accounts cleared")
	}

	log.Printf("creating %d robot accounts (initial_balance=%d)...", *count, *initialBalance)
	start := time.Now()

	// 9. Batch create robots
	if err := robotAccountSvc.BatchCreateRobotsWithBalance(ctx, *count, *initialBalance); err != nil {
		log.Fatalf("batch create robots failed: %v", err)
	}

	elapsed := time.Since(start)

	// 9. Print statistics
	totalCount, err := robotAccountSvc.GetTotalRobotCount(ctx)
	if err != nil {
		log.Printf("get total robot count failed: %v", err)
	}

	availableCount, err := robotPoolSvc.GetAvailableCount(ctx)
	if err != nil {
		log.Printf("get available robot count failed: %v", err)
	}

	robotIDCount, err := getRobotIDSetCount(ctx, redisClient)
	if err != nil {
		log.Printf("get robot id set size failed: %v", err)
	}

	fmt.Println()
	fmt.Println("================================================")
	fmt.Println("Robot Account Initialization Statistics")
	fmt.Println("================================================")
	fmt.Printf("Requested count         : %d\n", *count)
	fmt.Printf("Initial balance         : %d cents (%d yuan)\n", *initialBalance, *initialBalance/100)
	fmt.Printf("Elapsed time            : %s\n", elapsed)
	fmt.Printf("Total robots in DB      : %d\n", totalCount)
	fmt.Printf("Robots in available pool: %d\n", availableCount)
	fmt.Printf("Robots in robot id set  : %d\n", robotIDCount)
	fmt.Println("================================================")
}

// getRobotIDSetCount returns the cardinality of the robot user id set in Redis.
func getRobotIDSetCount(ctx context.Context, client cRedis.RedisClient) (int64, error) {
	return client.SCard(ctx, rediskeys.RobotUserIDsKey()).Result()
}

// clearRobotAccounts removes all robot accounts from database and Redis.
// It deletes records from robot_accounts and users tables, and clears all
// robot-related Redis keys (virtual balances, pool, user id set, user caches).
func clearRobotAccounts(ctx context.Context, db *gorm.DB, redisClient cRedis.RedisClient) error {
	// 1. Delete robot_accounts table
	if err := db.Exec("DELETE FROM robot_accounts").Error; err != nil {
		return fmt.Errorf("delete robot_accounts failed: %w", err)
	}

	// 2. Delete robot users from users table
	if err := db.Exec("DELETE FROM users WHERE is_robot = 1 OR user_id LIKE 'robot_%'").Error; err != nil {
		return fmt.Errorf("delete robot users failed: %w", err)
	}

	// 3. Clear Redis robot-related keys using raw client
	rawClient := redisClient.Raw()
	robotKeys, err := rawClient.Keys(ctx, "cashparty:robot:*").Result()
	if err != nil {
		return fmt.Errorf("get robot keys failed: %w", err)
	}
	if len(robotKeys) > 0 {
		if err := redisClient.Del(ctx, robotKeys...).Err(); err != nil {
			return fmt.Errorf("delete robot keys failed: %w", err)
		}
	}

	// 4. Clear user cache keys for robot users
	userCacheKeys, err := rawClient.Keys(ctx, fmt.Sprintf("%s:user:*", rediskeys.KeyPrefix)).Result()
	if err != nil {
		return fmt.Errorf("get user cache keys failed: %w", err)
	}
	if len(userCacheKeys) > 0 {
		if err := redisClient.Del(ctx, userCacheKeys...).Err(); err != nil {
			return fmt.Errorf("delete user cache keys failed: %w", err)
		}
	}

	return nil
}
