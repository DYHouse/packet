package mysql

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/settlement/domain"
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
func NewPlatformCallLogRepository(db *gorm.DB) repository.PlatformCallLogRepository {
	return &PlatformCallLogRepositoryImpl{db: db}
}

// CreateLog 创建平台调用日志。
// 行为与原 service 层实现完全一致。
func (r *PlatformCallLogRepositoryImpl) CreateLog(ctx context.Context, params *dto.CallLogCreateParams) (*domain.PlatformCallLog, error) {
	reqBody, err := json.Marshal(params.ReqBody)
	if err != nil {
		logger.Warn("marshal request body failed, using empty body as fallback",
			"biz_order_no", params.BizOrderNo, "call_type", params.CallType, "error", err)
		reqBody = nil
	}

	log := &model.PlatformCallLog{
		CallType:    params.CallType,
		BizOrderNo:  params.BizOrderNo,
		RequestBody: string(reqBody),
		RequestTime: time.Now(),
		Status:      domain.CallLogStatusPending,
	}

	if err := r.db.WithContext(ctx).Create(log).Error; err != nil {
		return nil, err
	}

	return platformCallLogModelToDomain(log), nil
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
		"retry_count": gorm.Expr("CASE WHEN status = ? THEN retry_count + 1 ELSE retry_count END", domain.CallLogStatusFailed),
	}

	if params.RespBody != nil {
		respBody, err := json.Marshal(params.RespBody)
		if err != nil {
			logger.Warn("marshal response body failed, using empty body as fallback",
				"log_id", params.ID, "error", err)
			respBody = nil
		}
		updates["response_body"] = string(respBody)
	}

	return r.db.WithContext(ctx).Model(&model.PlatformCallLog{}).
		Where("id = ?", params.ID).
		Updates(updates).Error
}

// GetLogByID 根据 ID 查询平台调用日志。
// 行为与原 service 层实现完全一致。
func (r *PlatformCallLogRepositoryImpl) GetLogByID(ctx context.Context, id int64) (*domain.PlatformCallLog, error) {
	var log model.PlatformCallLog
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&log).Error
	if err != nil {
		return nil, err
	}
	return platformCallLogModelToDomain(&log), nil
}

// GetFailedLogs 查询失败的平台调用日志。
// 行为与原 service 层实现完全一致。
func (r *PlatformCallLogRepositoryImpl) GetFailedLogs(ctx context.Context, limit int) ([]*domain.PlatformCallLog, error) {
	var logs []*model.PlatformCallLog
	err := r.db.WithContext(ctx).
		Where("status = ?", domain.CallLogStatusFailed).
		Order("id desc").
		Limit(limit).
		Find(&logs).Error
	if err != nil {
		return nil, err
	}
	return platformCallLogModelSliceToDomain(logs), nil
}

// platformCallLogModelToDomain 将 model 层平台调用日志转换为 domain 层聚合根。
func platformCallLogModelToDomain(m *model.PlatformCallLog) *domain.PlatformCallLog {
	if m == nil {
		return nil
	}
	return &domain.PlatformCallLog{
		ID:           m.ID,
		CallType:     m.CallType,
		BizOrderNo:   m.BizOrderNo,
		RequestBody:  m.RequestBody,
		ResponseBody: m.ResponseBody,
		Status:       m.Status,
		ErrorMessage: m.ErrorMessage,
		RetryCount:   m.RetryCount,
		RequestTime:  m.RequestTime,
		ResponseTime: m.ResponseTime,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}

// platformCallLogModelSliceToDomain 批量转换 model 切片为 domain 切片。
func platformCallLogModelSliceToDomain(ms []*model.PlatformCallLog) []*domain.PlatformCallLog {
	if ms == nil {
		return nil
	}
	result := make([]*domain.PlatformCallLog, 0, len(ms))
	for _, m := range ms {
		result = append(result, platformCallLogModelToDomain(m))
	}
	return result
}

// 编译时接口实现校验
var _ repository.PlatformCallLogRepository = (*PlatformCallLogRepositoryImpl)(nil)
