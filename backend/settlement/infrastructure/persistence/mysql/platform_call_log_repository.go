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

// 查询与清理由运维直接通过数据库执行，Repository 仅保留写入所需的两个方法。

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
// 行为与原 service 层实现完全一致：JSON 序列化失败时降级为空 body 并 Warn。
func (r *PlatformCallLogRepositoryImpl) CreateLog(ctx context.Context, params *dto.CallLogCreateParams) (*domain.PlatformCallLog, error) {
	reqBody, err := json.Marshal(params.ReqBody)
	if err != nil {
		logger.Warn("marshal request body failed, using empty body as fallback",
			"biz_order_no", params.BizOrderNo, "call_type", params.CallType, "error", err)
		reqBody = nil
	}

	now := time.Now()
	log := &model.PlatformCallLog{
		TraceID:        params.TraceID,
		CallType:       params.CallType,
		BizOrderNo:     params.BizOrderNo,
		UserID:         params.UserID,
		PlatformUserID: params.PlatformUserID,
		SessionID:      params.SessionID,
		RoundID:        params.RoundID,
		Amount:         params.Amount,
		Currency:       params.Currency,
		RequestBody:    string(reqBody),
		RequestTime:    now,
		Status:         domain.CallLogStatusPending,
		NodeID:         params.NodeID,
	}

	if err := r.db.WithContext(ctx).Create(log).Error; err != nil {
		return nil, err
	}

	return platformCallLogModelToDomain(log), nil
}

// UpdateLog 更新平台调用日志。
// duration_ms 由 params.RequestTime 与当前时间差值计算（RequestTime 由 service 层从 CreateLog 返回值传入）。
func (r *PlatformCallLogRepositoryImpl) UpdateLog(ctx context.Context, params *dto.CallLogUpdateParams) error {
	now := time.Now()
	updates := map[string]interface{}{
		"response_time": &now,
		"status":        params.Status,
		"error_message": params.ErrorMessage,
	}

	// duration_ms：若 service 层传入有效 RequestTime 则计算耗时，便于排查慢调用
	if !params.RequestTime.IsZero() {
		updates["duration_ms"] = int(now.Sub(params.RequestTime).Milliseconds())
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

// platformCallLogModelToDomain 将 model 层平台调用日志转换为 domain 层聚合根。
func platformCallLogModelToDomain(m *model.PlatformCallLog) *domain.PlatformCallLog {
	if m == nil {
		return nil
	}
	return &domain.PlatformCallLog{
		ID:             m.ID,
		TraceID:        m.TraceID,
		BizOrderNo:     m.BizOrderNo,
		CallType:       m.CallType,
		UserID:         m.UserID,
		PlatformUserID: m.PlatformUserID,
		SessionID:      m.SessionID,
		RoundID:        m.RoundID,
		Amount:         m.Amount,
		Currency:       m.Currency,
		Status:         m.Status,
		ErrorMessage:   m.ErrorMessage,
		RequestBody:    m.RequestBody,
		ResponseBody:   m.ResponseBody,
		RequestTime:    m.RequestTime,
		ResponseTime:   m.ResponseTime,
		DurationMs:     m.DurationMs,
		NodeID:         m.NodeID,
		CreatedAt:      m.CreatedAt,
		UpdatedAt:      m.UpdatedAt,
	}
}

// 编译时接口实现校验
var _ repository.PlatformCallLogRepository = (*PlatformCallLogRepositoryImpl)(nil)
