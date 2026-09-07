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
	"github.com/cashparty/backend/game/model"
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
	clearExisting := flag.Bool("clear", false, "Clear existing robot accounts before initialization (deletes users table rows for robots)")
	retireOld := flag.Bool("retire-old", false, "Retire old fixed-sequence robots (robot_%05d) by marking disabled, removing from pool. Preserves users table for history.")
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
	// 脚本不涉及房间列表查询，minRoomFee 传 0（不过滤）
	dbRepo := mysqlRepo.NewDBRepository(db, 0)
	userCacheRepo := redis.NewUserCacheRepository(redisClient)
	userSvc := application.NewUserService(dbRepo, userCacheRepo, &cfg.Avatar, idGen, nil)

	// 7. Create RobotAccountService
	robotAccountSvc := robot.NewRobotAccountService(
		robotRepo,
		userSvc,
		virtualBalanceSvc,
		robotPoolSvc,
		&cfg.Avatar,
		idGen,
	)

	// 8. Clear existing robots if requested
	if *clearExisting {
		log.Println("clearing existing robot accounts...")
		if err := clearRobotAccounts(ctx, db, redisClient); err != nil {
			log.Fatalf("clear robot accounts failed: %v", err)
		}
		log.Println("existing robot accounts cleared")
	}

	// 8.5 Retire old fixed-sequence robots if requested
	if *retireOld {
		log.Println("retiring old fixed-sequence robots (robot_NNNNN)...")
		stats, err := retireOldFixedSequenceRobots(ctx, db, redisClient, robotRepo, robotPoolSvc)
		if err != nil {
			log.Fatalf("retire old robots failed: %v", err)
		}
		log.Printf("old robots retired: disabled=%d in robot_accounts, removed from available pool, Redis robot:* keys cleared",
			stats.DisabledCount)
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

	// 3. Clear Redis robot-related keys.
	// Cluster 模式下 Keys 命令只扫描单节点，必须用 ScanAll 跨所有 master 节点扫描，
	// 否则全局 key（如 cashparty:robot:pool:available）会残留旧 user_id，
	// 导致调度器取到旧 ID 后查询 users 表报 "El usuario no existe"。
	robotKeys, err := redisClient.ScanAll(ctx, "cashparty:robot:*", 1000)
	if err != nil {
		return fmt.Errorf("scan robot keys failed: %w", err)
	}
	for _, key := range robotKeys {
		if err := redisClient.Del(ctx, key).Err(); err != nil {
			log.Printf("删除 robot key 失败: key=%s, error=%v", key, err)
		}
	}

	// 4. Clear user cache keys for robot users
	userCacheKeys, err := redisClient.ScanAll(ctx, fmt.Sprintf("%s:user:*", rediskeys.KeyPrefix), 1000)
	if err != nil {
		return fmt.Errorf("scan user cache keys failed: %w", err)
	}
	for _, key := range userCacheKeys {
		if err := redisClient.Del(ctx, key).Err(); err != nil {
			log.Printf("删除 user cache key 失败: key=%s, error=%v", key, err)
		}
	}

	return nil
}

// retireStats 统计旧号机器人的淘汰结果。
type retireStats struct {
	DisabledCount int64
}

// retireOldFixedSequenceRobots 淘汰旧版固定编号机器人（robot_00001 格式）。
// 操作：
//  1. robot_accounts 中对旧号记录批量 UPDATE status = disabled
//  2. 清空 Redis 机器人调度相关 key（可用池、虚拟余额、房间关联等）
//  3. 从可用池逐个移除
// 注意：users 表记录保持不变，用于历史对局查询时回显昵称。
func retireOldFixedSequenceRobots(
	ctx context.Context,
	db *gorm.DB,
	redisClient cRedis.RedisClient,
	robotRepo repository.RobotAccountRepository,
	robotPoolSvc repository.RobotPoolRepository,
) (retireStats, error) {
	var stats retireStats

	// 1. 批量 UPDATE robot_accounts 中用户 ID 匹配旧号格式的行。
	// 旧号特征：外部 user_id（users 表）形如 robot_%05d，
	// 对应 robot_accounts.user_id 为内部雪花 int64，需通过 users 表定位。
	// 用 JOIN 更新以覆盖仅 robot_accounts 存在、但 users 中缺失的边缘情况。
	res := db.WithContext(ctx).Exec(`
UPDATE robot_accounts ra
JOIN users u ON ra.user_id = u.id
SET ra.status = ?
WHERE u.is_robot = 1 AND u.user_id REGEXP '^robot_[0-9]{5}$'
`, model.RobotStatusDisabled)
	if res.Error != nil {
		return stats, fmt.Errorf("batch update old robot_accounts status failed: %w", res.Error)
	}
	stats.DisabledCount = res.RowsAffected
	log.Printf("marked %d old robot accounts as disabled", stats.DisabledCount)

	// 2. 清空 Redis 机器人调度 key（与 clearRobotAccounts 相同的 ScanAll 逻辑）。
	// 清空后会由 retire 之后新一轮 -count 建号流程重新写入新号。
	robotKeys, err := redisClient.ScanAll(ctx, "cashparty:robot:*", 1000)
	if err != nil {
		return stats, fmt.Errorf("scan robot redis keys failed: %w", err)
	}
	for _, key := range robotKeys {
		if err := redisClient.Del(ctx, key).Err(); err != nil {
			log.Printf("删除 robot key 失败: key=%s, error=%v", key, err)
		}
	}

	// 3. 清空机器人 user cache，避免旧昵称/旧头像残留。
	userCacheKeys, err := redisClient.ScanAll(ctx, fmt.Sprintf("%s:user:*", rediskeys.KeyPrefix), 1000)
	if err != nil {
		return stats, fmt.Errorf("scan user cache keys failed: %w", err)
	}
	for _, key := range userCacheKeys {
		if err := redisClient.Del(ctx, key).Err(); err != nil {
			log.Printf("删除 user cache key 失败: key=%s, error=%v", key, err)
		}
	}
	_ = robotRepo
	_ = robotPoolSvc

	return stats, nil
}
