package domain

import (
	"time"

	"github.com/cashparty/backend/settlement/model"
)

// ExceptionType 异常类型枚举（从 model 层重导出，确保 domain 为单一真理源）。
// 枚举定义保留在 model/ 层，因为 model.ExceptionRecord GORM 实体引用该类型；
// domain 层通过 type alias 重导出，使新代码统一引用 domain.ExceptionType。
type ExceptionType = model.ExceptionType

// ExceptionStatus 异常处理状态枚举（从 model 层重导出）。
type ExceptionStatus = model.ExceptionStatus

// HandleType 异常处理方式枚举（从 model 层重导出）。
type HandleType = model.HandleType

const (
	ExceptionTypeDebitFailed        ExceptionType = model.ExceptionTypeDebitFailed
	ExceptionTypeCreditRetryExceed  ExceptionType = model.ExceptionTypeCreditRetryExceed
	ExceptionTypeDeductedNotSettled ExceptionType = model.ExceptionTypeDeductedNotSettled
)

const (
	ExceptionStatusPending    ExceptionStatus = model.ExceptionStatusPending
	ExceptionStatusProcessing ExceptionStatus = model.ExceptionStatusProcessing
	ExceptionStatusResolved   ExceptionStatus = model.ExceptionStatusResolved
	ExceptionStatusIgnored    ExceptionStatus = model.ExceptionStatusIgnored
)

const (
	HandleTypeManual HandleType = model.HandleTypeManual
	HandleTypeRefund HandleType = model.HandleTypeRefund
	HandleTypeRetry  HandleType = model.HandleTypeRetry
	HandleTypeIgnore HandleType = model.HandleTypeIgnore
)

// ExceptionRecord 异常记录聚合根，表示结算流程中产生的异常事件及其处理记录。
// 纯领域类型，无 GORM tag 与 TableName 方法；持久化由 model.ExceptionRecord 承载，
// Repository 实现层负责 domain ↔ model 转换。
type ExceptionRecord struct {
	ID              int64
	ExceptionNo     string
	ExceptionType   ExceptionType
	BillID          int64
	RoundTraceID    string
	RoundID         int64
	BillType        int
	UserID          int64
	Amount          int64
	Status          ExceptionStatus
	ExceptionDetail string
	HandleType      HandleType
	HandleRemark    string
	HandledAt       *time.Time
	HandledBy       int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CanHandle 校验当前异常记录是否可执行人工处理操作。
// 仅 Pending 状态的异常记录允许处理；其他状态拒绝。
func (e *ExceptionRecord) CanHandle() bool {
	return e.Status == ExceptionStatusPending
}

// IsTerminalStatus 校验当前异常记录是否处于终态（不再发生状态流转）。
// Resolved 与 Ignored 为终态。
func (e *ExceptionRecord) IsTerminalStatus() bool {
	return e.Status == ExceptionStatusResolved || e.Status == ExceptionStatusIgnored
}
