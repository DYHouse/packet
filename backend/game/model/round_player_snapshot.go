package model

import "time"

// RoundPlayerSnapshot 会话轮次玩家快照，记录每轮所有座位 + 旁观者的完整状态。
// 一轮一玩家一行，是该轮的"事实表"，覆盖座位流转、角色变更、替补关系等所有玩家状态。
type RoundPlayerSnapshot struct {
	ID          int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	SessionID   int64      `json:"session_id" gorm:"index:idx_round_session,priority:1;index:idx_session_user_round,priority:1;not null"`
	RoundID     int64      `json:"round_id" gorm:"index:idx_round_session,priority:2;not null"`
	RoundNo     int        `json:"round_no" gorm:"index:idx_round_session,priority:3;not null"`
	UserID      int64      `json:"user_id" gorm:"index:idx_session_user_round,priority:2;not null"`
	Role        string     `json:"role" gorm:"size:20;default:'player';not null"`            // player / spectator
	SeatNo      *int       `json:"seat_no,omitempty"`                                         // spectator 为 null
	IsSender    bool       `json:"is_sender" gorm:"default:0"`                                // 本轮是否发红包者
	JoinedAt    time.Time  `json:"joined_at" gorm:"type:datetime(3)"`                         // 加入房间时间
	ActiveStart time.Time  `json:"active_start" gorm:"type:datetime(3)"`                      // 本轮成为活跃玩家/旁观者的时刻
	ActiveEnd   *time.Time `json:"active_end,omitempty" gorm:"type:datetime(3);index"`         // 本轮离开时刻（NULL 表示仍活跃）
	LeftReason  string     `json:"left_reason" gorm:"size:30"`                                // kicked / substituted / user_request / timeout / disconnect
	ReplacedBy  int64      `json:"replaced_by,omitempty"`                                     // 替补者 user_id
	Source      string     `json:"source" gorm:"size:20;default:'initial'"`                   // initial / substitute / rejoin
	CreatedAt   time.Time  `json:"created_at" gorm:"type:datetime(3);autoCreateTime"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"type:datetime(3);autoUpdateTime"`
}

// TableName 指定表名
func (RoundPlayerSnapshot) TableName() string { return "round_player_snapshot" }
