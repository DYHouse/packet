// Package service 是 settlement 模块的 Service 层（领域服务层）。
//
// 本包包含 settlement 模块的所有领域服务，负责具体的业务逻辑执行、事务编排（短事务片段）、
// 平台 RPC 调用与幂等控制。外部调用方应通过 settlement/application 包的 AppService facade
// 访问本包，不直接依赖具体 Service（game/application 与 settlement/scheduler 已完成收敛）。
//
// # 按业务域分组
//
// 本包的服务按 7 个业务域组织：
//
// # 1. 扣款域
//
//   - DeductService：负责首回合扣款（DeductForFirstRound）、后续回合扣款（DeductForLaterRound）、
//     系统红包扣款（DeductForSystemPacket）。含 platform.Debit RPC，事务由 Service 内部对
//     CreateRoundSettlementAndBills 片段编排（§5.4 短事务原则）。
//
// # 2. 结算域
//
//   - RoundSettleService：负责单局结算（SettleRound，写 grab/commission BillRecord + 触发奖励结算）
//     和游戏级结算（SettleGame，聚合 + 上报平台）。SettleRound 为纯 DB 写入，由 AppService 层开事务；
//     SettleGame 含 platform.Settle/Credit RPC，事务由 Service 内部编排。
//   - GameSettleReportingService：负责游戏结果上报，调用 platform.Settle(/settle) 上报每个玩家
//     本局游戏结果。仅上报结果（bet_amount、payout、result），不直接移动资金。
//   - SessionPayoutService：负责会话级入账，调用 platform.Credit 完成玩家收益的实际资金移动。
//   - RewardSettler：负责奖励结算，由 RoundSettleService.SettleRound 在事务内调用。
//
// # 3. 罚款域
//
//   - PenaltySettlementService：负责罚款扣款（DeductPenaltyToPlatform，从用户扣款上交平台）
//     和罚款分配（DistributePenaltyFromPlatform，将平台罚款分配给接收方）。
//     扣款含 platform.Debit RPC，事务由 Service 内部编排；分配为纯 DB 写入，由 AppService 层开事务。
//
// # 4. 退款域
//
//   - RefundApplyService：负责退款申请（ApplyForRefund）。
//     跨表事务（refund_audit + bill_record）由 Service 内部通过 dbRepo.WithTransaction 编排。
//   - RefundExecuteService：负责退款审批（ApproveRefund）、驳回（RejectRefund）与执行（executeRefund）。
//     跨表事务（refund_audit + bill_record）由 Service 内部通过 dbRepo.WithTransaction 编排。
//     审批含 platform.Credit RPC，采用 Processing 中间状态 + BizOrderNo 幂等兜底。
//   - RefundQueryService：负责退款记录查询（GetRefundAuditByOrderNo/GetRefundsByStatus），只读用例，无需事务。
//
// # 5. 重试与对账域
//
//   - CreditRetryService：负责入账重试（RetryCredit），含指数退避、最大重试次数限制、
//     超限异常记录创建。由 CreditRetryScheduler 通过 SchedulerAppService 定时触发。
//   - SettlementCheckService：负责一致性检查（CheckFirstRoundDeductFailure、
//     CheckDeductedButNotSettled），发现问题后自动创建退款申请或异常记录。
//     由 SettlementCheckScheduler 通过 SchedulerAppService 定时触发。
//
// # 6. 查询域
//
//   - BalanceQueryService：负责余额查询（CheckBalance）和账单查询，只读用例，无需事务。
//   - BalanceService：负责开局余额检查（CheckBalanceForReady），只读用例，无需事务。
//
// # 7. 基础设施支持
//
//   - TraceIDGenerator：负责生成各类 TraceID 与 BizOrderNo（RoundTraceID、BizOrderNo、
//     RefundOrderNo、ExceptionNo），用于全链路追踪与幂等控制。
//   - UserIDConvertService：负责内部 userID 与平台 platformUserID 之间的转换。
//   - RobotChecker：定义机器人检查接口（IsRobot），由 game 层实现并注入。
//
// # 事务编排策略
//
// 事务编排遵循 GAME_SERVICE_ARCHITECTURE_REVIEW.md：
//
//   - §13.1 #5：Repository 不开事务，由 AppService 通过 dbRepo.WithTransaction 编排。
//   - §5.4 短事务原则：禁止事务内 RPC，含 RPC 的用例由 Service 对 DB 写入片段开事务。
//
// Service 层不直接开启顶层事务，而是通过注入的 dbRepo.WithTransaction 对需要原子性的
// DB 写入片段开短事务。跨表操作（如 CreateRefundAuditAndUpdateBillRefundStatus、
// UpdateRefundSuccess、RejectRefund）在事务内通过 tx 的 sub-repo 访问器执行。
package service
