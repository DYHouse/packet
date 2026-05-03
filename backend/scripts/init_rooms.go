package main

import (
	"fmt"
	"log"

	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/game/model"
	settlementModel "github.com/cashparty/backend/settlement/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var roomConfigs = []model.RoomConfig{
	{Name: "Sala de 5", RoomFee: 500, MaxPlayers: 5, MaxRounds: 10, SortOrder: 1, Status: 1},
	{Name: "Sala de 10", RoomFee: 1000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 2, Status: 1},
	{Name: "Sala de 20", RoomFee: 2000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 3, Status: 1},
	{Name: "Sala de 30", RoomFee: 3000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 4, Status: 1},
	{Name: "Sala de 50", RoomFee: 5000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 5, Status: 1},
	{Name: "Sala de 100", RoomFee: 10000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 6, Status: 1},
	{Name: "Sala de 200", RoomFee: 20000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 7, Status: 1},
	{Name: "Sala de 500", RoomFee: 50000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 8, Status: 1},
}

var roomCountPerConfig = map[int64]int{
	1: 10,
	2: 50,
	3: 40,
	4: 30,
	5: 30,
	6: 20,
	7: 10,
	8: 10,
}

func main() {
	dsn := "root:123456@tcp(127.0.0.1:3306)/?charset=utf8mb4&parseTime=True&loc=Local"
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect mysql failed: %v", err)
	}

	if err := db.Exec("CREATE DATABASE IF NOT EXISTS cashparty CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error; err != nil {
		log.Fatalf("create database failed: %v", err)
	}
	log.Println("database created")

	sqlDB, _ := db.DB()
	sqlDB.Close()

	dsn = "root:123456@tcp(127.0.0.1:3306)/cashparty?charset=utf8mb4&parseTime=True&loc=Local"
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect database failed: %v", err)
	}

	if err := db.AutoMigrate(
		&model.RoomConfig{},
		&model.Room{},
		&model.GameSession{},
		&model.SessionPlayer{},
		&model.Round{},
		&model.RoundGrabRecord{},
		&model.Packet{},
		&model.SpecialReward{},
		&model.User{},
		&settlementModel.BillRecord{},
		&settlementModel.RoundSettlement{},
		&settlementModel.RefundAudit{},
		&settlementModel.ExceptionRecord{},
		&settlementModel.PlatformCallLog{},
	); err != nil {
		log.Fatalf("migrate tables failed: %v", err)
	}
	log.Println("tables migrated")

	if err := initRoomConfigs(db); err != nil {
		log.Fatalf("init room configs failed: %v", err)
	}

	if err := initRooms(db); err != nil {
		log.Fatalf("init rooms failed: %v", err)
	}

	var totalRooms int64
	db.Model(&model.Room{}).Count(&totalRooms)
	log.Printf("initialization completed! total rooms: %d", totalRooms)
}

func initRoomConfigs(db *gorm.DB) error {
	for i := range roomConfigs {
		var existing model.RoomConfig
		result := db.Where("room_fee = ?", roomConfigs[i].RoomFee).First(&existing)
		if result.Error == nil {
			roomConfigs[i].ID = existing.ID
			if err := db.Model(&existing).Updates(map[string]interface{}{
				"name":        roomConfigs[i].Name,
				"max_players": roomConfigs[i].MaxPlayers,
				"max_rounds":  roomConfigs[i].MaxRounds,
				"sort_order":  roomConfigs[i].SortOrder,
				"status":      roomConfigs[i].Status,
			}).Error; err != nil {
				return fmt.Errorf("update room config %s failed: %w", roomConfigs[i].Name, err)
			}
			log.Printf("updated room config: %s (ID: %d)", roomConfigs[i].Name, roomConfigs[i].ID)
		} else {
			if err := db.Create(&roomConfigs[i]).Error; err != nil {
				return fmt.Errorf("create room config %s failed: %w", roomConfigs[i].Name, err)
			}
			log.Printf("created room config: %s (ID: %d)", roomConfigs[i].Name, roomConfigs[i].ID)
		}
	}
	return nil
}

func initRooms(db *gorm.DB) error {
	for _, cfg := range roomConfigs {
		var existingCount int64
		db.Model(&model.Room{}).Where("config_id = ?", cfg.ID).Count(&existingCount)

		needCreate := roomCountPerConfig[cfg.ID] - int(existingCount)
		if needCreate <= 0 {
			log.Printf("rooms for %s already exist: %d", cfg.Name, existingCount)
			continue
		}

		var maxRoomNo string
		db.Model(&model.Room{}).Where("config_id = ?", cfg.ID).
			Select("room_no").Order("room_no desc").Limit(1).Scan(&maxRoomNo)

		startIndex := 0
		if maxRoomNo != "" {
			fmt.Sscanf(maxRoomNo, "R%d", &startIndex)
			startIndex = startIndex%10000 + 1
		}

		for i := 0; i < needCreate; i++ {
			roomIndex := startIndex + i
			roomNo := fmt.Sprintf("R%04d%04d", cfg.SortOrder, roomIndex)
			roomID := idgen.GenerateInt64()

			room := model.Room{
				RoomID:        roomID,
				RoomNo:        roomNo,
				ConfigID:      cfg.ID,
				ConfigName:    cfg.Name,
				RoomFee:       cfg.RoomFee,
				MaxPlayers:    cfg.MaxPlayers,
				MaxRounds:     cfg.MaxRounds,
				MaxSpectators: 10,
				Status:        model.RoomStatusIdle,
			}

			if err := db.Create(&room).Error; err != nil {
				return fmt.Errorf("create room %s failed: %w", roomNo, err)
			}
		}

		log.Printf("created %d rooms for %s", needCreate, cfg.Name)
	}
	return nil
}
