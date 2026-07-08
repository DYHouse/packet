package mysql

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/settlement/domain/repository"
	"github.com/cashparty/backend/settlement/dto"
	"github.com/cashparty/backend/settlement/model"
	"gorm.io/gorm"
)

// PlatformCallLogRepositoryImpl 是 PlatformCallLogRepository 接口的 MySQL 实现。
// 从原 service/platform_call_manager.go 迁移，行为完全一致。
type PlatformCallLogRepositoryImpl struct {
	db *gorm.DB
}

// NewPlatformCallLogRepository 构造 PlatformCallLogRepositoryImpl 实例。
func NewPlatformCallLogRepository(db *gorm.DB) *PlatformCallLogRepositoryImpl {
	return &PlatformCallLogRepositoryImpl{db: db}
}

// CreateLog 创建平台调用日志。
// 行为与原 service 层实现完全一致。
func (r *PlatformCallLogRepositoryImpl) CreateLog(ctx context.Context, params *dto.CallLogCreateParams) (*model.PlatformCallLog, error) {
	reqBody, _ := json.Marshal(params.ReqBody)

	log := &model.PlatformCallLog{
		CallType:    params.CallType,
		BizOrderNo:  params.BizOrderNo,
		RequestBody: string(reqBody),
		RequestTime: time.Now(),
		Status:      model.CallLogStatusPending,
	}

	if err := r.db.WithContext(ctx).Create(log).Error; err != nil {
		return nil, err
	}

	return log, nil
}

// UpdateLog 更新平台调用日志。
// 行为与原 service 层实现完全一致。
func (r *PlatformCallLogRepositoryImpl) UpdateLog(ctx context.Context, params *dto.CallLogUpdateParams) error {
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

	return r.db.WithContext(ctx).Model(&model.PlatformCallLog{}).
		Where("id = ?", params.ID).
		Updates(updates).Error
}

// GetLogByID 根据 ID 查询平台调用日志。
// 行为与原 service 层实现完全一致。
func (r *PlatformCallLogRepositoryImpl) GetLogByID(ctx context.Context, id int64) (*model.PlatformCallLog, error) {
	var log model.PlatformCallLog
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// GetFailedLogs 查询失败的平台调用日志。
// 行为与原 service 层实现完全一致。
func (r *PlatformCallLogRepositoryImpl) GetFailedLogs(ctx context.Context, limit int) ([]*model.PlatformCallLog, error) {
	var logs []*model.PlatformCallLog
	err := r.db.WithContext(ctx).
		Where("status = ?", model.CallLogStatusFailed).
		Order("id desc").
		Limit(limit).
		Find(&logs).Error
	if err != nil {
		return nil, err
	}
	return logs, nil
}

// 编译时接口实现校验
var _ repository.PlatformCallLogRepository = (*PlatformCallLogRepositoryImpl)(nil)
