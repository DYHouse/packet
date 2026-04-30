package main

import (
	"fmt"
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	dsn := "root:123456@tcp(127.0.0.1:3306)/cashparty?charset=utf8mb4&parseTime=True&loc=Local"
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}

	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	var tables []string
	if err := db.Raw("SHOW TABLES").Scan(&tables).Error; err != nil {
		log.Fatalf("查询表失败: %v", err)
	}

	fmt.Println("数据库中的表:")
	fmt.Println("================")
	for i, table := range tables {
		fmt.Printf("%d. %s\n", i+1, table)
	}
	fmt.Println("================")
	fmt.Printf("共 %d 个表\n", len(tables))

	var roomCount, configCount int64
	db.Table("rooms").Count(&roomCount)
	db.Table("room_configs").Count(&configCount)
	fmt.Printf("\n房间配置数: %d\n", configCount)
	fmt.Printf("房间数: %d\n", roomCount)
}
