package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/cashparty/backend/common/idgen"
	"github.com/cashparty/backend/game/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// manage_room_types 房间金额类型管理脚本，支持动态新增/下架/恢复类型与补充房间供给。
//
// 使用示例：
//
//	新增类型：  go run scripts/manage_room_types.go -action add -name "Sala de 1000" -fee 100000 -count 20
//	下架类型：  go run scripts/manage_room_types.go -action disable -fee 100000
//	恢复类型：  go run scripts/manage_room_types.go -action enable -fee 100000
//	补充房间：  go run scripts/manage_room_types.go -action add-rooms -fee 100000 -count 10
//	查看列表：  go run scripts/manage_room_types.go -action list
//
// 下架语义：room_configs.status 置 0（首页类型消失），rooms.status 置 -1（RoomStatusRetired，
// 房间列表与自动匹配不再返回）。运行时不回写 rooms.status，在局对局不受影响，打完为止。
// 进房不校验房间状态，知道 room_id 的直进属于既有边界，本脚本不处理。

// initIDGenerator 初始化雪花 ID 生成器（脚本场景使用显式 node_id=1）。
// 规约 SID-7：脚本显式初始化；SID-C3：脚本可直接调用 GetGenerator，但 MUST 检查 error。
func initIDGenerator() idgen.IDGenerator {
	if err := idgen.Init(1); err != nil {
		log.Fatalf("init id generator failed: %v", err)
	}
	idGen, err := idgen.GetGenerator()
	if err != nil {
		log.Fatalf("get id generator failed: %v", err)
	}
	return idGen
}

func main() {
	action := flag.String("action", "list", "操作类型：add | disable | enable | add-rooms | list")
	name := flag.String("name", "", "类型名称（action=add 必填，如 \"Sala de 1000\"）")
	fee := flag.Int64("fee", 0, "房间费（单位：分，必填，如 1000 元传 100000，用于唯一定位类型）")
	count := flag.Int("count", 0, "房间数量（action=add / add-rooms 必填）")
	maxPlayers := flag.Int("max_players", 5, "最大玩家数（action=add 可选，默认 5）")
	maxRounds := flag.Int("max_rounds", 10, "最大轮数（action=add 可选，默认 10）")
	flag.Parse()

	// list 无需 fee；其余操作按 fee 唯一定位类型，必须为正
	if *action != "list" && *fee <= 0 {
		log.Fatalf("invalid room fee: %d, fee (cents) is required and must be positive", *fee)
	}

	dsn := "root:123456@tcp(127.0.0.1:3306)/cashparty?charset=utf8mb4&parseTime=True&loc=Local"
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("connect database failed: %v", err)
	}

	switch *action {
	case "add":
		if *name == "" {
			log.Fatalf("name is required for action add")
		}
		if *count <= 0 {
			log.Fatalf("count is required for action add and must be positive")
		}
		if err := addRoomType(db, *name, *fee, *count, *maxPlayers, *maxRounds); err != nil {
			log.Fatalf("add room type failed: %v", err)
		}
	case "disable":
		if err := disableRoomType(db, *fee); err != nil {
			log.Fatalf("disable room type failed: %v", err)
		}
	case "enable":
		if err := enableRoomType(db, *fee); err != nil {
			log.Fatalf("enable room type failed: %v", err)
		}
	case "add-rooms":
		if *count <= 0 {
			log.Fatalf("count is required for action add-rooms and must be positive")
		}
		if err := addRooms(db, *fee, *count); err != nil {
			log.Fatalf("add rooms failed: %v", err)
		}
	case "list":
		if err := listRoomTypes(db); err != nil {
			log.Fatalf("list room types failed: %v", err)
		}
	default:
		log.Fatalf("unknown action: %s", *action)
	}
}

// getConfigByFee 按房费查询类型配置。room_fee 在业务上唯一。
func getConfigByFee(db *gorm.DB, fee int64) (*model.RoomConfig, error) {
	var cfg model.RoomConfig
	if err := db.Where("room_fee = ?", fee).First(&cfg).Error; err != nil {
		return nil, fmt.Errorf("room config with fee %d not found: %w", fee, err)
	}
	return &cfg, nil
}

// addRoomType 新增房间类型并创建指定数量的房间。类型已存在时直接报错返回，避免重复创建。
func addRoomType(db *gorm.DB, name string, fee int64, count, maxPlayers, maxRounds int) error {
	var existing model.RoomConfig
	if err := db.Where("room_fee = ?", fee).First(&existing).Error; err == nil {
		return fmt.Errorf("room config with fee %d already exists: %s (id=%d), use add-rooms to supply rooms instead", fee, existing.Name, existing.ID)
	}

	// sort_order 默认取当前最大值 + 1，保持首页排序在末尾
	var maxSort int
	if err := db.Model(&model.RoomConfig{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSort).Error; err != nil {
		return fmt.Errorf("query max sort_order failed: %w", err)
	}

	cfg := model.RoomConfig{
		Name:       name,
		RoomFee:    fee,
		MaxPlayers: maxPlayers,
		MaxRounds:  maxRounds,
		SortOrder:  maxSort + 1,
		Status:     1,
	}
	if err := db.Create(&cfg).Error; err != nil {
		return fmt.Errorf("create room config failed: %w", err)
	}
	log.Printf("created room config: %s (id=%d, fee=%d, sort_order=%d)", name, cfg.ID, fee, cfg.SortOrder)

	return createRooms(db, &cfg, count)
}

// disableRoomType 下架房间类型：类型置停用，全部房间置 RoomStatusRetired。
// 运行时不回写 rooms.status，下架后不会被局终状态翻回，无需收尾任务；在局对局不受影响。
func disableRoomType(db *gorm.DB, fee int64) error {
	cfg, err := getConfigByFee(db, fee)
	if err != nil {
		return err
	}
	if cfg.Status == 0 {
		log.Printf("room config %s (id=%d) already disabled, skip", cfg.Name, cfg.ID)
	}

	res := db.Model(&model.RoomConfig{}).Where("id = ?", cfg.ID).Update("status", 0)
	if res.Error != nil {
		return fmt.Errorf("disable room config failed: %w", res.Error)
	}

	res = db.Model(&model.Room{}).Where("config_id = ?", cfg.ID).Update("status", model.RoomStatusRetired)
	if res.Error != nil {
		return fmt.Errorf("retire rooms failed: %w", res.Error)
	}
	log.Printf("disabled room config: %s (id=%d), retired %d rooms", cfg.Name, cfg.ID, res.RowsAffected)
	return nil
}

// enableRoomType 恢复已下架的房间类型：类型置启用，下架状态（-1）的房间恢复为空闲。
// 仅恢复 RoomStatusRetired 的房间，不触碰其他状态的记录。
func enableRoomType(db *gorm.DB, fee int64) error {
	cfg, err := getConfigByFee(db, fee)
	if err != nil {
		return err
	}
	if err := db.Model(&model.RoomConfig{}).Where("id = ?", cfg.ID).Update("status", 1).Error; err != nil {
		return fmt.Errorf("enable room config failed: %w", err)
	}

	res := db.Model(&model.Room{}).Where("config_id = ? AND status = ?", cfg.ID, model.RoomStatusRetired).
		Update("status", model.RoomStatusIdle)
	if res.Error != nil {
		return fmt.Errorf("restore rooms failed: %w", res.Error)
	}
	log.Printf("enabled room config: %s (id=%d), restored %d rooms", cfg.Name, cfg.ID, res.RowsAffected)
	return nil
}

// addRooms 为现有类型补充房间供给。
func addRooms(db *gorm.DB, fee int64, count int) error {
	cfg, err := getConfigByFee(db, fee)
	if err != nil {
		return err
	}
	return createRooms(db, cfg, count)
}

// createRooms 为类型批量创建房间，房间编号规则与 init_rooms 一致：R{sort_order:04d}{序号:04d}。
// 房间 Redis 元数据由进房逻辑惰性初始化，此处仅写 DB。
func createRooms(db *gorm.DB, cfg *model.RoomConfig, count int) error {
	idGen := initIDGenerator()

	// 解析该类型现有最大编号，续号创建，避免 room_no 唯一索引冲突
	var maxRoomNo string
	db.Model(&model.Room{}).Where("config_id = ?", cfg.ID).
		Select("room_no").Order("room_no desc").Limit(1).Scan(&maxRoomNo)

	startIndex := 0
	if maxRoomNo != "" {
		fmt.Sscanf(maxRoomNo, "R%d", &startIndex)
		startIndex = startIndex % 10000
	}

	for i := 1; i <= count; i++ {
		roomNo := fmt.Sprintf("R%04d%04d", cfg.SortOrder, startIndex+i)
		roomID, err := idGen.GenerateInt64()
		if err != nil {
			return fmt.Errorf("generate room id failed: %w", err)
		}

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
	log.Printf("created %d rooms for %s", count, cfg.Name)
	return nil
}

// listRoomTypes 列出全部类型及房间供给分布。
func listRoomTypes(db *gorm.DB) error {
	var cfgs []model.RoomConfig
	if err := db.Order("sort_order asc").Find(&cfgs).Error; err != nil {
		return fmt.Errorf("query room configs failed: %w", err)
	}

	log.Printf("%-6s %-16s %-10s %-8s %-8s %-10s %s", "ID", "NAME", "FEE(cent)", "STATUS", "MAXPLR", "ROOMS", "ROOM_STATUS")
	for i := range cfgs {
		type row struct {
			Status int
			Cnt    int
		}
		var rows []row
		db.Model(&model.Room{}).Select("status, COUNT(*) as cnt").Where("config_id = ?", cfgs[i].ID).
			Group("status").Scan(&rows)

		dist := ""
		total := 0
		for _, r := range rows {
			if dist != "" {
				dist += ", "
			}
			dist += fmt.Sprintf("%d:%d", r.Status, r.Cnt)
			total += r.Cnt
		}

		status := "enabled"
		if cfgs[i].Status == 0 {
			status = "disabled"
		}
		log.Printf("%-6d %-16s %-10d %-8s %-8d %-10d %s", cfgs[i].ID, cfgs[i].Name, cfgs[i].RoomFee,
			status, cfgs[i].MaxPlayers, total, dist)
	}
	return nil
}
