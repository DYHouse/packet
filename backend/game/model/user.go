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

func NewUser(userID, nickname, avatar, ip, deviceID string) *User {
	return &User{
		ID:       idgen.GenerateInt64(),
		UserID:   userID,
		Nickname: nickname,
		Avatar:   avatar,
		IP:       ip,
		DeviceID: deviceID,
	}
}
