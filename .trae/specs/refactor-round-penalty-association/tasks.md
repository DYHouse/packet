# Tasks

- [x] Task 1: 删除死代码罚款类型常量
  - [ ] SubTask 1.1: 修改 `game/domain/round/penalty.go`，删除 `PenaltyTypeLeaveDuringGame` 和 `PenaltyTypeDisconnectTimeout` 常量定义
  - [ ] SubTask 1.2: 删除 `String()` 方法中 `PenaltyTypeLeaveDuringGame` 和 `PenaltyTypeDisconnectTimeout` 的 case 分支
  - [ ] SubTask 1.3: 删除 `ParsePenaltyType()` 函数中 `leave_during_game` 和 `disconnect_timeout` 的 case 分支
  - [ ] SubTask 1.4: 运行 `go build ./...` 验证无编译错误

- [x] Task 2: 新增 PenaltyRecord GORM 模型与仓储（game 域）
  - [ ] SubTask 2.1: 创建 `game/model/penalty_record.go`，定义 PenaltyRecord GORM 模型（含 roundID/roundNo/penaltyType/deductStatus 等字段和索引 tag）
  - [ ] SubTask 2.2: 在 `game/domain/repository/db_repository.go` 的 `RoundDBRepository` 接口新增 `GetRoundBySessionAndRoundNo` 方法
  - [ ] SubTask 2.3: 在 `game/domain/repository/db_repository.go` 新增 `PenaltyRecordRepository` 接口（Create 方法）
  - [ ] SubTask 2.4: 在 `game/domain/repository/db_repository.go` 的 `Transaction` 和 `DBRepository` 接口新增 `PenaltyRecordRepo()` 访问器
  - [ ] SubTask 2.5: 在 `game/infrastructure/persistence/mysql/round_repository.go` 实现 `GetRoundBySessionAndRoundNo`
  - [ ] SubTask 2.6: 创建 `game/infrastructure/persistence/mysql/penalty_record_repository.go`，实现 PenaltyRecordRepository
  - [ ] SubTask 2.7: 在 `game/infrastructure/persistence/mysql/db_repository_impl.go` 的 DBRepositoryImpl 和 GormTransactionImpl 中 eager 初始化 PenaltyRecordRepo
  - [ ] SubTask 2.8: 在 AutoMigrate 注册列表中添加 PenaltyRecord 模型

- [x] Task 3: 新增 PenaltyDistribution GORM 模型与仓储（settlement 域）
  - [ ] SubTask 3.1: 创建 `settlement/model/penalty_distribution.go`，定义 PenaltyDistribution 和 PenaltyDistributionRecipient GORM 模型
  - [ ] SubTask 3.2: 在 `settlement/domain/repository/transaction.go` 新增 `PenaltyDistributionRepository` 接口（Create / CreateRecipients 方法）
  - [ ] SubTask 3.3: 在 `settlement/domain/repository/transaction.go` 的 `Transaction` 和 `DBRepository` 接口新增 `PenaltyDistributionRepo()` 访问器
  - [ ] SubTask 3.4: 创建 `settlement/infrastructure/persistence/mysql/penalty_distribution_repository.go`，实现 PenaltyDistributionRepository
  - [ ] SubTask 3.5: 在 settlement 的 DBRepositoryImpl 和 GormTransactionImpl 中 eager 初始化 PenaltyDistributionRepo
  - [ ] SubTask 3.6: 在 AutoMigrate 注册列表中添加 PenaltyDistribution 和 PenaltyDistributionRecipient 模型

- [x] Task 4: BillRecord 新增关联字段
  - [ ] SubTask 4.1: 在 `settlement/model/bill.go` 的 BillRecord GORM 模型新增 `PenaltyType`/`PenaltyRecordID`/`SpecialRewardID`/`PenaltyDistributionID` 字段（带 index tag）
  - [ ] SubTask 4.2: 在 `settlement/domain/bill.go` 的 domain BillRecord 新增对应字段
  - [ ] SubTask 4.3: 在 model ↔ domain 转换函数中同步新增字段映射
  - [ ] SubTask 4.4: 运行 `go build ./...` 验证无编译错误

- [x] Task 5: DTO 增强 — PenaltyDistributeRequest 新增字段
  - [ ] SubTask 5.1: 在 `settlement/dto/request.go` 的 PenaltyDistributeRequest 新增 `RoundNo int` 和 `TriggerPhase string` 字段
  - [ ] SubTask 5.2: 运行 `go build ./...` 验证无编译错误

- [x] Task 6: TraceIDGenerator 增加 roundNo 参数
  - [ ] SubTask 6.1: 修改 `settlement/service/trace_id_generator.go` 的 `GeneratePenaltyDistTraceID` 方法签名，增加 `roundNo int` 参数
  - [ ] SubTask 6.2: 修改输出格式为 `PENALTY_DIST_{roomID}_{sessionID}_{roundNo}`
  - [ ] SubTask 6.3: 更新 `settlement/service/penalty_settlement_service.go` 中 `DistributePenaltyFromPlatform` 对 `GeneratePenaltyDistTraceID` 的调用，传入 `req.RoundNo`
  - [ ] SubTask 6.4: 运行 `go build ./...` 和 `go vet ./...` 验证

- [x] Task 7: 实现 ensureNextRound 辅助方法
  - [ ] SubTask 7.1: 在 `game/application/game_lifecycle_service.go` 新增 `ensureNextRound` 方法
  - [ ] SubTask 7.2: 方法逻辑：先 GetRoundBySessionAndRoundNo 查询，存在则返回 roundID；不存在则生成 roundID + 创建 Pending round
  - [ ] SubTask 7.3: 查询/创建失败时返回 error，调用方降级为 roundID=0
  - [ ] SubTask 7.4: 确认 GameLifecycleService 有 dbRepo 和 idGen 字段访问权限（如无则通过依赖注入添加）

- [x] Task 8: 修改 OnSendTimeout 使用 ensureNextRound
  - [ ] SubTask 8.1: 修改 `game/application/game_lifecycle_timeout.go` 的 OnSendTimeout，计算 nextRoundNo = CurrentRound + 1
  - [ ] SubTask 8.2: 调用 ensureNextRound 获取 roundID，失败时降级为 0 并记录日志
  - [ ] SubTask 8.3: ApplyPenalty 调用传入 nextRoundNo 和 roundID（替换原来的 CurrentRound 和 roundID=0）
  - [ ] SubTask 8.4: 运行 `go build ./...` 验证

- [x] Task 9: 修改 OnReplaceTimeout 使用 ensureNextRound
  - [ ] SubTask 9.1: 修改 `game/application/game_lifecycle_timeout.go` 的 OnReplaceTimeout，计算 nextRoundNo = CurrentRound + 1
  - [ ] SubTask 9.2: 调用 ensureNextRound 获取 roundID，失败时降级为 0 并记录日志
  - [ ] SubTask 9.3: PenaltyDistributeRequest 新增 RoundNo=nextRoundNo 和 TriggerPhase="inter_round"
  - [ ] SubTask 9.4: 运行 `go build ./...` 验证

- [x] Task 10: 修改 createRoundRecord 复用预创建 round
  - [ ] SubTask 10.1: 修改 `game/application/packet_round_init.go` 的 createRoundRecord
  - [ ] SubTask 10.2: 先调用 GetRoundBySessionAndRoundNo 查询是否已有 round
  - [ ] SubTask 10.3: 已存在则直接返回（复用，保留原 roundID 和 created_at）
  - [ ] SubTask 10.4: 不存在则走原逻辑创建新 round
  - [ ] SubTask 10.5: 运行 `go build ./...` 验证

- [x] Task 11: ApplyPenalty 增加 PenaltyRecord DB 持久化
  - [ ] SubTask 11.1: 修改 `game/application/penalty_service.go` 的 ApplyPenalty 方法
  - [ ] SubTask 11.2: 在 Lua 成功后、DeductPenaltyToPlatform 调用后，同步创建 PenaltyRecord 到 DB
  - [ ] SubTask 11.3: PenaltyRecord 包含 roundID/roundNo/userID/penaltyType/amount/count/kickRequired，deduct_status 初始为 Processing(0)
  - [ ] SubTask 11.4: 持久化失败时记录 Warn 日志（含 roomID/userID/roundID），不阻塞主流程
  - [ ] SubTask 11.5: 确保 PenaltyService 有 PenaltyRecordRepo 访问权限（通过依赖注入添加 dbRepo 字段）
  - [ ] SubTask 11.6: 运行 `go build ./...` 验证

- [x] Task 12: DistributePenaltyFromPlatform 增加 PenaltyDistribution 持久化
  - [ ] SubTask 12.1: 修改 `settlement/service/penalty_settlement_service.go` 的 DistributePenaltyFromPlatform 方法
  - [ ] SubTask 12.2: 在创建 bills 后（同一事务内），通过 tx.PenaltyDistributionRepo() 创建 PenaltyDistribution 记录
  - [ ] SubTask 12.3: 遍历 recipients 创建 PenaltyDistributionRecipient 明细，关联 billID
  - [ ] SubTask 12.4: 同时回填 platformBill 和 shareBills 的 PenaltyDistributionID 字段（通过 UpdateBillPenaltyDistID 或在 CreateBills 前设置）
  - [ ] SubTask 12.5: 持久化失败时记录 Warn 日志，不阻塞主流程（bills 已创建）
  - [ ] SubTask 12.6: 运行 `go build ./...` 验证

- [x] Task 13: rounds 表 idx_session_roundno 升级为唯一索引
  - [ ] SubTask 13.1: 修改 `game/model/round.go` 的 Round struct，将 SessionID 和 RoundNo 的 `index:idx_session_roundno` 改为 `uniqueIndex:idx_session_roundno`
  - [ ] SubTask 13.2: 编写 SQL 检查脚本（不执行），检查现有数据是否有 (session_id, round_no) 重复
  - [ ] SubTask 13.3: 运行 `go build ./...` 验证

- [x] Task 14: DeductPenaltyToPlatform 回填 BillRecord 关联字段
  - [ ] SubTask 14.1: 在 `settlement/service/penalty_settlement_service.go` 的 DeductPenaltyToPlatform 中，创建 playerBill 和 platformBill 时填充 PenaltyType 字段
  - [ ] SubTask 14.2: 确保 domain BillRecord 到 model BillRecord 的转换同步新增字段
  - [ ] SubTask 14.3: 运行 `go build ./...` 验证

- [x] Task 15: 更新 bootstrap 依赖注入
  - [x] SubTask 15.1: 更新 game/bootstrap/container.go 的 InitAppServices()，构造 PenaltyService 时注入 c.DBRepo，构造 GameLifecycleService 时在 c.RoomRepo 后插入 c.DBRepo
  - [x] SubTask 15.2: settlement bootstrap 无需修改（settlementDbRepo 已通过 NewDBRepository eager 初始化 penaltyDistributionRepo）
  - [x] SubTask 15.3: GormTransactionImpl 的 PenaltyRecordRepo() 和 PenaltyDistributionRepo() 访问器均返回 tx 内创建的实例，符合预期
  - [x] SubTask 15.4: 运行 `go build ./...` 验证通过（scripts 包预先存在的 main 重复问题除外）

- [x] Task 16: 编译与基础验证
  - [x] SubTask 16.1: 运行 `cd backend && go build ./...`（除 scripts 包预先存在的 main 重复问题外通过）
  - [x] SubTask 16.2: 运行 `cd backend && go vet $(go list ./... | grep -v /scripts)` 通过
  - [x] SubTask 16.3: 运行 `cd backend && go test ./game/... ./settlement/...` 所有现有测试通过
  - [x] SubTask 16.4: 修复 game/integration/mocks_test.go 中 mockTransaction 和 mockDBRepository 缺失 PenaltyDistributionRepo() 方法导致的编译错误

# Task Dependencies

- Task 2, Task 3 可并行（分别属于 game/settlement 域，互不依赖）
- Task 4 依赖 Task 3（BillRecord 关联字段需要 PenaltyDistribution 模型存在）
- Task 5 独立（仅 DTO 修改）
- Task 6 依赖 Task 5（TraceID 调用方需要 RoundNo 字段）
- Task 7 依赖 Task 2（ensureNextRound 需要 GetRoundBySessionAndRoundNo）
- Task 8, Task 9 依赖 Task 7（使用 ensureNextRound）
- Task 10 依赖 Task 2（createRoundRecord 复用需要 GetRoundBySessionAndRoundNo）
- Task 11 依赖 Task 2（ApplyPenalty 持久化需要 PenaltyRecordRepo）
- Task 12 依赖 Task 3, Task 4, Task 5（DistributePenaltyFromPlatform 持久化需要 PenaltyDistributionRepo + BillRecord 关联字段 + RoundNo）
- Task 13 独立（仅 GORM tag 修改）
- Task 14 依赖 Task 4（BillRecord 关联字段需要先存在）
- Task 15 依赖 Task 2, Task 3, Task 11（依赖注入需要仓储实现和 PenaltyService 改动完成）
- Task 16 依赖所有前序 Task
