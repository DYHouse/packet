package domain

import (
	"time"
)

// ExceptionType 异常类型枚举（domain 层原生定义，model 层通过 type alias 引用）。
type ExceptionType int

// ExceptionStatus 异常处理状态枚举（domain 层原生定义）。
type ExceptionStatus int

// HandleType 异常处理方式枚举（domain 层原生定义）。
type HandleType int

const (
	ExceptionTypeDebitFailed        ExceptionType = 1
	ExceptionTypeCreditRetryExceed  ExceptionType = 2
	ExceptionTypeDeductedNotSettled ExceptionType = 3
)

const (
	ExceptionStatusPending    ExceptionStatus = 0
	ExceptionStatusProcessing ExceptionStatus = 1
	ExceptionStatusResolved   ExceptionStatus = 2
	ExceptionStatusIgnored    ExceptionStatus = 3
)

const (
	HandleTypeManual HandleType = 1
	HandleTypeRefund HandleType = 2
	HandleTypeRetry  HandleType = 3
	HandleTypeIgnore HandleType = 4
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
