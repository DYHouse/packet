package model

import "time"

// RobotAccount 状态常量
const (
	RobotStatusInactive = 0 // 未激活
	RobotStatusIdle     = 1 // 空闲
	RobotStatusInGame   = 2 // 游戏中
	RobotStatusDisabled = 3 // 停用
)

// RobotAccount 机器人账户表
type RobotAccount struct {
	ID                 int64     `gorm:"primaryKey" json:"id"`
	UserID             int64     `gorm:"uniqueIndex" json:"user_id"`           // 关联 users.id
	Status             int       `gorm:"default:0;index" json:"status"`        // 0=未激活 1=空闲 2=游戏中 3=停用
	VirtualBalance     int64     `gorm:"default:0" json:"virtual_balance"`     // 虚拟余额(分)
	TotalVirtualDebit  int64     `gorm:"default:0" json:"total_virtual_debit"` // 虚拟扣款累计(分)
	TotalVirtualCredit int64     `gorm:"default:0" json:"total_virtual_credit"` // 虚拟入账累计(分)
	MinRoomFee         int       `gorm:"default:0" json:"min_room_fee"`        // 可参与的最低房间费用
	MaxRoomFee         int       `gorm:"default:0" json:"max_room_fee"`        // 可参与的最高房间费用
	TotalGames         int       `gorm:"default:0" json:"total_games"`         // 总参与局数
	TotalProfit        int64     `gorm:"default:0" json:"total_profit"`        // 总盈亏(分)
	LastActiveAt       *time.Time `json:"last_active_at"`
	CreatedAt          time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt          time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName 指定表名
func (RobotAccount) TableName() string {
	return "robot_accounts"
}
