# Checklist

## 死代码清理
- [x] `game/domain/round/penalty.go` 中 `PenaltyTypeLeaveDuringGame` 和 `PenaltyTypeDisconnectTimeout` 常量已删除
- [x] `String()` 方法中对应 case 分支已删除
- [x] `ParsePenaltyType()` 函数中对应 case 分支已删除
- [x] `PenaltyTypeSendTimeout` 的 iota 值仍为 1（未因删除后续常量而改变）

## 数据模型
- [x] `game/model/penalty_record.go` 已创建，PenaltyRecord GORM 模型包含 roundID/roundNo/userID/penaltyType/amount/count/kickRequired/deductStatus/billID 字段
- [x] PenaltyRecord 索引 tag 包含 idx_penalty_session_round(session_id, round_id) / idx_penalty_user(user_id, created_at) / idx_penalty_room(room_id, created_at)
- [x] `settlement/model/penalty_distribution.go` 已创建，包含 PenaltyDistribution 和 PenaltyDistributionRecipient 两个 GORM 模型
- [x] PenaltyDistribution 包含 roundID/roundNo/triggerType/totalAmount/shareAmount/recipientCount/platformBillID 字段
- [x] PenaltyDistributionRecipient 包含 distributionID/userID/amount/billID 字段
- [x] `settlement/model/bill.go` 的 BillRecord 新增 PenaltyType/PenaltyRecordID/SpecialRewardID/PenaltyDistributionID 字段（带 index tag）
- [x] `settlement/domain/bill.go` 的 domain BillRecord 新增对应字段
- [x] model ↔ domain 转换函数同步新增字段映射
- [x] `game/model/round.go` 的 idx_session_roundno 从 `index:` 改为 `uniqueIndex:`

## 仓储接口与实现
- [x] `game/domain/repository/db_repository.go` 的 RoundDBRepository 接口新增 `GetRoundBySessionAndRoundNo` 方法
- [x] `game/domain/repository/db_repository.go` 新增 PenaltyRecordRepository 接口（含 Create 方法）
- [x] `game/domain/repository/db_repository.go` 的 Transaction 和 DBRepository 接口新增 PenaltyRecordRepo() 访问器
- [x] `game/infrastructure/persistence/mysql/round_repository.go` 实现 GetRoundBySessionAndRoundNo
- [x] `game/infrastructure/persistence/mysql/penalty_record_repository.go` 已创建并实现 PenaltyRecordRepository
- [x] `settlement/domain/repository/transaction.go` 新增 PenaltyDistributionRepository 接口（含 Create / CreateRecipients 方法）
- [x] `settlement/domain/repository/transaction.go` 的 Transaction 和 DBRepository 接口新增 PenaltyDistributionRepo() 访问器
- [x] `settlement/infrastructure/persistence/mysql/penalty_distribution_repository.go` 已创建并实现 PenaltyDistributionRepository
- [x] game 的 DBRepositoryImpl 和 GormTransactionImpl 中 PenaltyRecordRepo eager 初始化
- [x] settlement 的 DBRepositoryImpl 和 GormTransactionImpl 中 PenaltyDistributionRepo eager 初始化
- [x] AutoMigrate 注册列表包含 PenaltyRecord / PenaltyDistribution / PenaltyDistributionRecipient

## DTO 与 TraceID
- [x] `settlement/dto/request.go` 的 PenaltyDistributeRequest 新增 RoundNo int 和 TriggerPhase string 字段
- [x] `settlement/service/trace_id_generator.go` 的 GeneratePenaltyDistTraceID 签名增加 roundNo int 参数
- [x] 输出格式改为 `PENALTY_DIST_{roomID}_{sessionID}_{roundNo}`
- [x] `settlement/service/penalty_settlement_service.go` 中 DistributePenaltyFromPlatform 调用 GeneratePenaltyDistTraceID 传入 req.RoundNo

## 预创建 Round 核心逻辑
- [x] `game/application/game_lifecycle_service.go` 新增 ensureNextRound 方法
- [x] ensureNextRound 先查询 GetRoundBySessionAndRoundNo，存在则返回 roundID
- [x] ensureNextRound 不存在则生成 roundID + 创建 Pending round
- [x] ensureNextRound 失败时返回 error，调用方降级为 roundID=0
- [x] OnSendTimeout 使用 ensureNextRound，nextRoundNo = CurrentRound + 1
- [x] OnSendTimeout 降级时记录 Error 日志
- [x] OnReplaceTimeout 使用 ensureNextRound，nextRoundNo = CurrentRound + 1
- [x] OnReplaceTimeout 的 PenaltyDistributeRequest 设置 RoundNo=nextRoundNo, TriggerPhase="inter_round"
- [x] `game/application/packet_round_init.go` 的 createRoundRecord 先查询已有 round，存在则复用
- [x] createRoundRecord 复用时保留原 roundID 和 created_at（不更新）

## 持久化逻辑
- [x] `game/application/penalty_service.go` 的 ApplyPenalty 在 Lua 成功后创建 PenaltyRecord 到 DB
- [x] PenaltyRecord 的 deduct_status 初始为 Processing(0)
- [x] ApplyPenalty 持久化失败时记录 Warn 日志，不阻塞主流程
- [x] `settlement/service/penalty_settlement_service.go` 的 DistributePenaltyFromPlatform 在创建 bills 后同事务创建 PenaltyDistribution
- [x] DistributePenaltyFromPlatform 创建 PenaltyDistributionRecipient 明细，关联 billID
- [x] DistributePenaltyFromPlatform 持久化失败时记录 Warn 日志，不阻塞主流程
- [x] DeductPenaltyToPlatform 创建 playerBill/platformBill 时填充 PenaltyType 字段

## 依赖注入
- [x] game/bootstrap/app.go 构造 PenaltyService 时注入 dbRepo（或 PenaltyRecordRepo）
- [x] settlement/bootstrap 构造 PenaltySettlementService 时 dbRepo 包含 PenaltyDistributionRepo
- [x] GormTransactionImpl 的所有子 repo 访问器返回正确实例

## 编译与测试
- [x] `go build ./...` 通过
- [x] `go vet ./...` 通过
- [x] `go test ./game/... ./settlement/...` 现有测试通过

## CODING_STANDARD 合规
- [x] 代码注释为中文（§17.1）
- [x] godoc 注释以类型/函数名开头 + 中文描述
- [x] 日志/错误消息为英文（§5.4）
- [x] GORM struct tag 定义索引（§13）
- [x] 错误包装用 %w（§16 SC-6）
- [x] 仓储聚合对象 eager 初始化，构造后只读字段
- [x] 短事务原则：DistributePenaltyFromPlatform 事务内仅 DB 写入，无 RPC
- [x] Repository 不开事务，由 AppService 编排（§13.1 #5）
