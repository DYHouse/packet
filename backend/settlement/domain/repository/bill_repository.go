package repository

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/domain"
)

// BillRepository 账单记录仓储接口，负责 BillRecord 的 CRUD 与状态机更新。
// 接口方法签名与原 BillManager 中对应方法完全一致，仅做位置迁移。
type BillRepository interface {
	// CreateBill 创建单条账单记录
	CreateBill(ctx context.Context, bill *domain.BillRecord) error
	// UpdateBillStatus 乐观锁更新账单状态（仅允许从 fromStatus 转换）
	UpdateBillStatus(ctx context.Context, billID int64, fromStatus, toStatus int, errMsg string) error
	// UpdateBillSuccess 乐观锁更新账单为成功状态并记录余额
	UpdateBillSuccess(ctx context.Context, billID int64, fromStatus int, balanceBefore, balanceAfter int64) error
	// GetBillByID 根据主键查询账单
	GetBillByID(ctx context.Context, billID int64) (*domain.BillRecord, error)
	// ExistsByRoundAndType 检查指定回合与类型的账单是否已存在（幂等检查）
	ExistsByRoundAndType(ctx context.Context, roundID int64, billType int) (bool, error)
	// GetBillByRoundTypeAndUser 根据回合、类型、用户查询账单
	GetBillByRoundTypeAndUser(ctx context.Context, roundID int64, billType int, userID int64) (*domain.BillRecord, error)
	// GetBillByTraceTypeAndUser 基于 round_trace_id 查询账单，用于 RoundID 未设置的场景（如惩罚扣款）
	GetBillByTraceTypeAndUser(ctx context.Context, roundTraceID string, billType int, userID int64) (*domain.BillRecord, error)
	// GetBillsByTraceID 根据回合 trace id 查询全部账单
	GetBillsByTraceID(ctx context.Context, traceID string) ([]*domain.BillRecord, error)
	// GetBillsByRoundID 查询指定回合全部账单（按创建时间正序）
	GetBillsByRoundID(ctx context.Context, roundID int64) ([]*domain.BillRecord, error)
	// CreateBills 批量创建账单。事务边界由 AppService 通过 DBRepository.WithTransaction 编排，
	// 调用方应在事务回调内通过 tx.BillRepo() 获取基于事务连接的子 repo 后调用本方法。
	CreateBills(ctx context.Context, bills []*domain.BillRecord) error
	// CreateBillsPair 创建两条配对账单。事务边界由 AppService 通过 DBRepository.WithTransaction 编排。
	CreateBillsPair(ctx context.Context, bill1 *domain.BillRecord, bill2 *domain.BillRecord) error
	// GetBillsByBatchID 根据批次 id 查询账单（按创建时间正序）
	GetBillsByBatchID(ctx context.Context, batchID string) ([]*domain.BillRecord, error)
	// GetBillByBatchAndUser 根据批次 id 与用户 id 查询账单
	GetBillByBatchAndUser(ctx context.Context, batchID string, userID int64) (*domain.BillRecord, error)
	// GetRetryableCredits 查询可重试入账的账单（Processing/Failed 且未超重试上限）
	GetRetryableCredits(ctx context.Context, limit int) ([]*domain.BillRecord, error)
	// IncrementRetryCountWithNextRetryTime 乐观锁自增重试次数并设置下次重试时间。
	// 通过 currentRetryCount 作为乐观锁条件（WHERE id=? AND retry_count=?），防止并发覆盖。
	// 调用方应检查返回 error：RowsAffected==0 表示并发冲突或记录不存在。
	IncrementRetryCountWithNextRetryTime(ctx context.Context, billID int64, currentRetryCount int, nextRetryAt time.Time) error
	// UpdateBillExceptionID 更新账单关联的异常记录 id
	UpdateBillExceptionID(ctx context.Context, billID, exceptionID int64) error
	// GetBillsBySessionTypeAndUser 查询指定会话、类型、用户的账单（用于幂等检查）
	GetBillsBySessionTypeAndUser(ctx context.Context, sessionID int64, billType int, userID int64) ([]*domain.BillRecord, error)
	// UpdateGameSettleStatusByUser 乐观锁更新该玩家在该会话全部账单的游戏级结算状态
	UpdateGameSettleStatusByUser(ctx context.Context, sessionID int64, userID int64, fromStatus, toStatus int) error
}
