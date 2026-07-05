package model

import (
	"time"

	"github.com/cashparty/backend/common/idgen"
)

type User struct {
	ID        int64     `json:"id" gorm:"primaryKey"`
	UserID    string    `json:"user_id" gorm:"uniqueIndex;size:64"`
	Nickname  string    `json:"nickname" gorm:"size:100"`
	Avatar    string    `json:"avatar" gorm:"size:512"`
	IP        string    `json:"ip" gorm:"size:64"`
	DeviceID  string    `json:"device_id" gorm:"size:128"`
	IsRobot   bool      `json:"is_robot" gorm:"default:false;index"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (User) TableName() string {
	return "users"
}

func (u *User) GetUserID() string {
	return u.UserID
}

// NewUser 创建新用户。idGen 必须非 nil。
// 返回 (*User, error)，调用方 MUST 检查 error（规约 SID-3）。
func NewUser(idGen idgen.IDGenerator, userID, nickname, avatar, ip, deviceID string) (*User, error) {
	id, err := idGen.GenerateInt64()
	if err != nil {
		return nil, err
	}
	return &User{
		ID:       id,
		UserID:   userID,
		Nickname: nickname,
		Avatar:   avatar,
		IP:       ip,
		DeviceID: deviceID,
	}, nil
}
