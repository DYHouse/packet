# Tasks

- [x] Task 1: 删除 model 层字段定义
  - [x] SubTask 1.1: 删除 `backend/game/model/session.go` 中 `SessionPlayer.TotalProfit` 字段

- [x] Task 2: 删除 repository 层接口与实现
  - [x] SubTask 2.1: 删除 `backend/game/domain/repository/db_repository.go` 中 `SessionDBRepository.UpdateSessionPlayerProfit` 接口方法
  - [x] SubTask 2.2: 删除 `backend/game/domain/repository/db_repository.go` 中 dead code `PlayerStatsUpdate` 结构体
  - [x] SubTask 2.3: 删除 `backend/game/infrastructure/persistence/mysql/session_repository.go` 中 `UpdateSessionPlayerProfit` 方法实现

- [x] Task 3: 删除 application 层调用点
  - [x] SubTask 3.1: 删除 `backend/game/application/game_event_handler.go` 中 `handleSessionEnd` 事务内更新 `total_profit` 的 for 循环（保留 `UpdateSessionEnded` 调用与外层 `SettleGame` 调用）

- [x] Task 4: 编译验证
  - [x] SubTask 4.1: 在 `backend/` 目录执行 `go build ./game/...` 及排除 scripts/integration 的全量构建，均编译通过

# Task Dependencies
- Task 2、Task 3 依赖 Task 1 完成（删除字段后才能清理引用）
- Task 2 与 Task 3 可并行
- Task 4 依赖 Task 1-3 全部完成
