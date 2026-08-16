package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/cashparty/backend/common/config"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	gameconfig "github.com/cashparty/backend/game/config"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./config/game.yaml"
	}

	cfg, err := gameconfig.Load(configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	log.Println("开始清空数据库...")
	if err := clearDatabase(cfg.MySQL); err != nil {
		log.Fatalf("清空数据库失败: %v", err)
	}
	log.Println("数据库清空完成！")

	log.Println("开始清空 Redis...")
	if err := clearRedis(cfg.Redis); err != nil {
		log.Fatalf("清空 Redis 失败: %v", err)
	}
	log.Println("Redis 清空完成！")

	log.Println("所有数据已清空，请运行 init_rooms 重新初始化")
}

func clearDatabase(cfg config.MySQLConfig) error {
	// 去掉 DSN 中的数据库名，避免数据库不存在时连接失败（上次 clear 可能已删除数据库）。
	// 标准 DSN 格式: user:pass@tcp(host:port)/dbname?params → user:pass@tcp(host:port)/?params
	dsn := cfg.DSN
	parts := strings.SplitN(dsn, ")/", 2)
	if len(parts) == 2 {
		queryIdx := strings.Index(parts[1], "?")
		if queryIdx >= 0 {
			dsn = parts[0] + ")/" + parts[1][queryIdx:]
		}
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("连接 MySQL 失败: %w", err)
	}

	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	result := db.Exec("DROP DATABASE IF EXISTS cashparty")
	if result.Error != nil {
		return fmt.Errorf("删除数据库失败: %w", result.Error)
	}

	log.Println("数据库 cashparty 已删除")
	return nil
}

func clearRedis(cfg config.RedisConfig) error {
	// 通过封装层创建客户端，复用 TLS 能力
	client, err := cRedis.NewClient(&cfg)
	if err != nil {
		return fmt.Errorf("连接 Redis 失败: %w", err)
	}
	defer client.Close()

	ctx := context.Background()

	// Cluster 模式下 Keys 命令只扫描单个节点，会漏掉其他节点上的 key。
	// 必须用 ScanAll 跨所有 master 节点扫描，确保全局 key（如 robot:pool:available）
	// 也能被清除，否则会导致 init_robot_accounts 后 Redis 池残留旧 user_id。
	keys, err := client.ScanAll(ctx, fmt.Sprintf("%s:*", rediskeys.KeyPrefix), 1000)
	if err != nil {
		return fmt.Errorf("扫描 Redis keys 失败: %w", err)
	}

	if len(keys) == 0 {
		log.Println("Redis 中没有需要清空的数据")
		return nil
	}

	// Cluster 模式下批量 Del 会触发 CROSSSLOT 错误（keys 不在同一 slot），
	// 改为逐个删除以兼容 Cluster 模式。
	var deleted int64
	for _, key := range keys {
		if err := client.Del(ctx, key).Err(); err != nil {
			// 单个 key 删除失败不中断，记录警告继续
			log.Printf("删除 Redis key 失败: key=%s, error=%v", key, err)
			continue
		}
		deleted++
	}

	log.Printf("已删除 %d 个 Redis keys", deleted)
	return nil
}
