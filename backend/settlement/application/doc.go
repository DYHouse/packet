// Package application 是 settlement 模块的 Application 层（用例编排层）。
//
// 本包定义 settlement 模块对外的统一入口，外部调用方（game 模块、scheduler、
// HTTP/gRPC handler）应通过本包的 AppService facade 调用 settlement 用例，
// 不直接依赖 settlement/service 下的具体 Service，以实现高内聚低耦合。
//
// # 入口 facade
//
// 本包提供 3 个 AppService：
//
//   - SettleAppService：结算用例入口，编排单局结算（SettleRound）、游戏级结算（SettleGame）、
//     扣款（DeductForFirstRound/LaterRound/SystemPacket）、罚款（DeductPenaltyToPlatform/
//     DistributePenaltyFromPlatform）、余额查询（CheckBalance/CheckBalanceForReady）。
//   - RefundAppService：退款用例入口，转发退款申请（ApplyForRefund）、审批（ApproveRefund）、
//     驳回（RejectRefund）、查询（GetRefundAuditByOrderNo/GetRefundsByStatus）。
//   - SchedulerAppService：调度器用例入口，为 5 个 scheduler 提供统一方法
//     （RetryCreditBills/RetryGameSettle/SettleGameByTimeout/ProcessPendingRefunds/RunSettlementCheck）。
//
// # 事务编排策略
//
// 事务编排遵循 GAME_SERVICE_ARCHITECTURE_REVIEW.md：
//
//   - §13.1 #5：Repository 不开事务，由 AppService 通过 dbRepo.WithTransaction 编排。
//   - §5.4 短事务原则：禁止事务内 RPC，含 RPC 的用例由 Service 对 DB 写入片段开事务。
//
// 具体策略：
//
//   - 纯 DB 写入用例（SettleRound、DistributePenaltyFromPlatform、DeductForSystemPacket）：
//     在 AppService 层通过 dbRepo.WithTransaction 开启事务，向下传递 tx。
//   - 含 RPC 的用例（SettleGame、DeductPenaltyToPlatform、DeductForFirstRound、DeductForLaterRound）：
//     事务由 Service 内部对 DB 写入片段编排，AppService 仅纯转发。
//   - 只读用例（CheckBalance、CheckBalanceForReady、退款查询）：
//     无需事务，AppService 纯转发。
//   - 退款用例（ApplyForRefund/ApproveRefund/RejectRefund）：
//     跨表事务（refund_audit + bill_record）由 RefundService 内部通过 dbRepo.WithTransaction 编排。
//   - 调度器用例（SchedulerAppService 各方法）：
//     逻辑与原 scheduler.execute() 完全等价，事务边界与错误处理保持一致。
package application
