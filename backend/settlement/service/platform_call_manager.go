package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

type PlatformCallManager struct {
	db *gorm.DB
}

func NewPlatformCallManager(db *gorm.DB) *PlatformCallManager {
	return &PlatformCallManager{db: db}
}

type CallLogCreateParams struct {
	CallType   string
	BizOrderNo string
	ReqBody    interface{}
}

func (m *PlatformCallManager) CreateLog(ctx context.Context, params *CallLogCreateParams) (*model.PlatformCallLog, error) {
	reqBody, _ := json.Marshal(params.ReqBody)

	log := &model.PlatformCallLog{
		CallType:    params.CallType,
		BizOrderNo:  params.BizOrderNo,
		RequestBody: string(reqBody),
		RequestTime: time.Now(),
		Status:      model.CallLogStatusPending,
	}

	if err := m.db.WithContext(ctx).Create(log).Error; err != nil {
		return nil, err
	}

	return log, nil
}

type CallLogUpdateParams struct {
	ID           int64
	RespBody     interface{}
	Status       int
	ErrorMessage string
}

func (m *PlatformCallManager) UpdateLog(ctx context.Context, params *CallLogUpdateParams) error {
	now := time.Now()
	updates := map[string]interface{}{
		"response_time": &now,
		"status":        params.Status,
		"error_message": params.ErrorMessage,
		// retry_count 语义：表示重试次数。首次调用（当前状态为 pending）保持 retry_count=0；
		// 仅当当前状态为 failed（即本次为重试调用）时才自增 1。CASE 表达式中的 status
		// 引用的是更新前的当前列值，故与本次 status 赋值互不影响。
		"retry_count": gorm.Expr("CASE WHEN status = ? THEN retry_count + 1 ELSE retry_count END", model.CallLogStatusFailed),
	}

	if params.RespBody != nil {
		respBody, _ := json.Marshal(params.RespBody)
		updates["response_body"] = string(respBody)
	}

	return m.db.WithContext(ctx).Model(&model.PlatformCallLog{}).
		Where("id = ?", params.ID).
		Updates(updates).Error
}

func (m *PlatformCallManager) GetLogByID(ctx context.Context, id int64) (*model.PlatformCallLog, error) {
	var log model.PlatformCallLog
	err := m.db.WithContext(ctx).Where("id = ?", id).First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}

func (m *PlatformCallManager) GetFailedLogs(ctx context.Context, limit int) ([]*model.PlatformCallLog, error) {
	var logs []*model.PlatformCallLog
	err := m.db.WithContext(ctx).
		Where("status = ?", model.CallLogStatusFailed).
		Order("id desc").
		Limit(limit).
		Find(&logs).Error
	if err != nil {
		return nil, err
	}
	return logs, nil
}
