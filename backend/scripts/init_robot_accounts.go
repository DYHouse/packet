package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/game/application"
	mysqlRepo "github.com/cashparty/backend/game/infrastructure/persistence/mysql"
	"github.com/cashparty/backend/game/infrastructure/persistence/redis"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	count := flag.Int("count", 10, "Number of robot accounts to create")
	initialBalance := flag.Int64("initial_balance", 1000000, "Initial virtual balance in cents (default: 1000000 = 10000 yuan)")
	configPath := flag.String("config", "./config/game.yaml", "Path to game config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config failed: %v", err)
	}

	logger.Init(&logger.LogConfig{Level: cfg.Log.Level})

	idgen.InitFromEnv()

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

	// 3. Create RobotAccountRepository
	robotRepo := mysqlRepo.NewRobotAccountRepository(db)

	// 4. Create VirtualBalanceService
	virtualBalanceSvc := redis.NewVirtualBalanceService(redisClient, robotRepo)

	// 5. Create RobotPoolService
	robotPoolSvc := redis.NewRobotPoolService(redisClient)

	// 6. Create UserService (use DBRepository so SaveUser works end-to-end)
	dbRepo := mysqlRepo.NewDBRepository(db)
	userSvc := application.NewUserService(dbRepo, redisClient, &cfg.Avatar)

	// 7. Create RobotAccountService
	robotAccountSvc := application.NewRobotAccountService(
		robotRepo,
		userSvc,
		virtualBalanceSvc,
		robotPoolSvc,
		&cfg.Avatar,
	)

	log.Printf("creating %d robot accounts (initial_balance=%d)...", *count, *initialBalance)
	start := time.Now()

	// 8. Batch create robots
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
func getRobotIDSetCount(ctx context.Context, client *cRedis.Client) (int64, error) {
	return client.SCard(ctx, redis.RobotUserIDsKey()).Result()
}
