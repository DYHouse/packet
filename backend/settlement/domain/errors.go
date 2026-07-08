package domain

import "errors"

// 聚合根未找到错误。Repository 实现层在查不到记录时应包装这些错误返回，
// 供 Application/Service 层通过 errors.Is 判断是否为"不存在"语义。
var (
	// ErrBillNotFound 表示按条件查询 BillRecord 时未找到对应记录。
	ErrBillNotFound = errors.New("bill record not found")
	// ErrRoundSettlementNotFound 表示按条件查询 RoundSettlement 时未找到对应记录。
	ErrRoundSettlementNotFound = errors.New("round settlement not found")
	// ErrRefundAuditNotFound 表示按条件查询 RefundAudit 时未找到对应记录。
	ErrRefundAuditNotFound = errors.New("refund audit not found")
	// ErrExceptionRecordNotFound 表示按条件查询 ExceptionRecord 时未找到对应记录。
	ErrExceptionRecordNotFound = errors.New("exception record not found")
)

// 状态机违规错误。在聚合根状态机校验方法（CanRefund/CanSettle 等）返回 false 时，
// 调用方可选择包装这些错误返回，明确表达"非法状态转换"语义。
var (
	// ErrInvalidBillStatus 表示 BillRecord 的状态转换不合法。
	ErrInvalidBillStatus = errors.New("invalid bill status transition")
	// ErrInvalidRoundStatus 表示 RoundSettlement 的状态转换不合法。
	ErrInvalidRoundStatus = errors.New("invalid round settlement status transition")
	// ErrInvalidRefundStatus 表示 RefundAudit 的状态转换不合法。
	ErrInvalidRefundStatus = errors.New("invalid refund audit status transition")
	// ErrInvalidExceptionStatus 表示 ExceptionRecord 的状态转换不合法。
	ErrInvalidExceptionStatus = errors.New("invalid exception record status transition")
)
