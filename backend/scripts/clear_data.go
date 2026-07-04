package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cashparty/backend/common/config"
	gameconfig "github.com/cashparty/backend/game/config"
	"github.com/redis/go-redis/v9"
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
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{})
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
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	ctx := context.Background()

	keys, err := rdb.Keys(ctx, "cashparty:*").Result()
	if err != nil {
		return fmt.Errorf("获取 Redis keys 失败: %w", err)
	}

	if len(keys) == 0 {
		log.Println("Redis 中没有需要清空的数据")
		return nil
	}

	deleted, err := rdb.Del(ctx, keys...).Result()
	if err != nil {
		return fmt.Errorf("删除 Redis keys 失败: %w", err)
	}

	log.Printf("已删除 %d 个 Redis keys", deleted)
	return nil
}
