package domain

import (
	"context"
	"time"

	"github.com/cashparty/backend/settlement/model"
)

// BillRepository 账单记录仓储接口，负责 BillRecord 的 CRUD 与状态机更新。
// 接口方法签名与原 BillManager 中对应方法完全一致，仅做位置迁移。
type BillRepository interface {
	// CreateBill 创建单条账单记录
	CreateBill(ctx context.Context, bill *model.BillRecord) error
	// UpdateBillStatus 乐观锁更新账单状态（仅允许从 fromStatus 转换）
	UpdateBillStatus(ctx context.Context, billID int64, fromStatus, toStatus int, errMsg string) error
	// UpdateBillSuccess 乐观锁更新账单为成功状态并记录余额
	UpdateBillSuccess(ctx context.Context, billID int64, fromStatus int, balanceBefore, balanceAfter int64) error
	// GetBillByTraceID 根据 round_trace_id 查询账单
	GetBillByTraceID(ctx context.Context, traceID string) (*model.BillRecord, error)
	// GetBillByID 根据主键查询账单
	GetBillByID(ctx context.Context, billID int64) (*model.BillRecord, error)
	// ExistsByRoundAndType 检查指定回合与类型的账单是否已存在（幂等检查）
	ExistsByRoundAndType(ctx context.Context, roundID int64, billType int) (bool, error)
	// GetBillByRoundTypeAndUser 根据回合、类型、用户查询账单
	GetBillByRoundTypeAndUser(ctx context.Context, roundID int64, billType int, userID int64) (*model.BillRecord, error)
	// GetBillByTraceTypeAndUser 基于 round_trace_id 查询账单，用于 RoundID 未设置的场景（如惩罚扣款）
	GetBillByTraceTypeAndUser(ctx context.Context, roundTraceID string, billType int, userID int64) (*model.BillRecord, error)
	// GetBillsByTraceID 根据回合 trace id 查询全部账单
	GetBillsByTraceID(ctx context.Context, traceID string) ([]*model.BillRecord, error)
	// GetBillsByUserID 分页查询用户账单（按创建时间倒序）
	GetBillsByUserID(ctx context.Context, userID int64, limit, offset int) ([]*model.BillRecord, error)
	// GetBillsByRoundID 查询指定回合全部账单（按创建时间正序）
	GetBillsByRoundID(ctx context.Context, roundID int64) ([]*model.BillRecord, error)
	// CreateBillsInTransaction 在单事务内批量创建账单
	CreateBillsInTransaction(ctx context.Context, bills []*model.BillRecord) error
	// CreateBillsPairInTransaction 在单事务内创建两条配对账单
	CreateBillsPairInTransaction(ctx context.Context, bill1 *model.BillRecord, bill2 *model.BillRecord) error
	// GetBillsByBatchID 根据批次 id 查询账单（按创建时间正序）
	GetBillsByBatchID(ctx context.Context, batchID string) ([]*model.BillRecord, error)
	// GetBillByBatchAndUser 根据批次 id 与用户 id 查询账单
	GetBillByBatchAndUser(ctx context.Context, batchID string, userID int64) (*model.BillRecord, error)
	// GetRetryableCredits 查询可重试入账的账单（Processing/Failed 且未超重试上限）
	GetRetryableCredits(ctx context.Context, limit int) ([]*model.BillRecord, error)
	// IncrementRetryCountWithNextRetryTime 原子自增重试次数并设置下次重试时间
	IncrementRetryCountWithNextRetryTime(ctx context.Context, billID int64, nextRetryAt time.Time) error
	// UpdateBillExceptionID 更新账单关联的异常记录 id
	UpdateBillExceptionID(ctx context.Context, billID, exceptionID int64) error
	// GetBillsBySessionTypeAndUser 查询指定会话、类型、用户的账单（用于幂等检查）
	GetBillsBySessionTypeAndUser(ctx context.Context, sessionID int64, billType int, userID int64) ([]*model.BillRecord, error)
	// UpdateGameSettleStatusByUser 乐观锁更新该玩家在该会话全部账单的游戏级结算状态
	UpdateGameSettleStatusByUser(ctx context.Context, sessionID int64, userID int64, fromStatus, toStatus int) error
}
