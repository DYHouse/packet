# 迁移 VirtualBalanceSyncScheduler 到 settlement 模块 Spec

## Why

`VirtualBalanceSyncScheduler` 当前位于 `game/scheduler/`，但其全部职责属于 settlement 域：
- 调用 `settlementDomain.VirtualBalanceService.SyncToDB(ctx)`（接口定义在 settlement/domain）
- 实现类 `VirtualBalanceRepository` 在 settlement/infrastructure/persistence/redis
- "虚拟余额"是 settlement 模块的领域概念（机器人虚拟通道）

此外，settlement 已有 5 个 scheduler 通过 `SchedulerAppService` 统一管理（`initSettlementSchedulers()`），唯独 `VirtualBalanceSyncScheduler` 被 game 侧独立注册（`initGameSchedulers()`），违反"统一 Application 层入口"规约。

配置项也错位：当前使用 `RobotCfg.Account.SyncInterval`（30s 默认），但 SyncToDB 操作的是 settlement 的虚拟余额表，配置应归 `SettlementSchedulerConfig.VirtualBalanceSync`。

## What Changes

### 迁移文件位置

- **新增** `settlement/scheduler/virtual_balance_sync.go`（从 game/scheduler 迁移）
- **删除** `game/scheduler/virtual_balance_sync.go`

### 通过 SchedulerAppService 统一管理

- `SchedulerAppService` 新增 `virtualBalanceSvc domain.VirtualBalanceService` 字段
- `NewSchedulerAppService` 构造函数新增 `virtualBalanceSvc` 参数
- 新增 `RunVirtualBalanceSync(ctx) error` facade 方法，调用 `s.virtualBalanceSvc.SyncToDB(ctx)`

### 配置项迁移

- `SettlementSchedulerConfig` 新增 `VirtualBalanceSync SettlementSchedulerSubConfig` 字段
- `SetSettlementSchedulerDefaults` 新增 `VirtualBalanceSync` 默认值（Interval=30s, InitialDelay=0, LockTTL=35）
- bootstrap 注入改为从 `c.SettlementSchedulerCfg.VirtualBalanceSync` 读取

### 容器装配迁移

- `container.go` 移除 `initGameSchedulers()` 中 `VirtualBalanceSyncScheduler` 注册
- `initSettlementSchedulers()` 新增 `VirtualBalanceSyncScheduler` 注册
- `SchedulerAppService` 构造调用新增 `c.settlementVirtualBalance` 参数

### 不改变的内容（核心红线）

- `SyncToDB` 方法行为不变（仍调用 `VirtualBalanceRepository.SyncToDB`）
- Scheduler 的 Name / Interval / LockKey / LockTTL 默认值与原实现一致
  - Name: `virtual_balance_sync`（不变）
  - Interval: 30s（默认值不变）
  - InitialDelay: 0（不变，无初始延迟）
  - LockKey: `rediskeys.KeySchedulerVirtualBalanceSyncLock`（不变）
  - LockTTL: `int(interval.Seconds()) + 5`（与原实现一致）
- 业务逻辑零变更

## Impact

- Affected code:
  - `game/scheduler/virtual_balance_sync.go`（**删除**）
  - `settlement/scheduler/virtual_balance_sync.go`（**新增**）
  - `settlement/application/scheduler_app_service.go`（增加字段 + 构造参数 + facade 方法）
  - `common/config/settlement_scheduler.go`（增加 `VirtualBalanceSync` 子配置 + 默认值）
  - `game/bootstrap/container.go`（迁移注册位置 + SchedulerAppService 构造调用）

## ADDED Requirements

### Requirement: VirtualBalanceSyncScheduler 迁移到 settlement 模块

系统 SHALL 将 `VirtualBalanceSyncScheduler` 从 `game/scheduler/` 迁移到 `settlement/scheduler/`，并通过 `SchedulerAppService` 统一管理。

#### Scenario: Scheduler 文件位置
- **WHEN** 查找 `VirtualBalanceSyncScheduler` 定义
- **THEN** 在 `settlement/scheduler/virtual_balance_sync.go` 中找到，不再在 `game/scheduler/` 中

#### Scenario: Scheduler 注册位置
- **WHEN** Container 启动时注册 schedulers
- **THEN** `VirtualBalanceSyncScheduler` 在 `initSettlementSchedulers()` 中注册，不再在 `initGameSchedulers()` 中

#### Scenario: Scheduler 通过 SchedulerAppService 调用
- **WHEN** VirtualBalanceSyncScheduler.execute() 被调用
- **THEN** 调用 `SchedulerAppService.RunVirtualBalanceSync(ctx)`，由 AppService 转发到 `virtualBalanceSvc.SyncToDB(ctx)`

### Requirement: 配置项归位 SettlementSchedulerConfig

系统 SHALL 在 `SettlementSchedulerConfig` 中新增 `VirtualBalanceSync` 子配置。

#### Scenario: 默认值
- **WHEN** 未配置 `VirtualBalanceSync`
- **THEN** 使用默认值：Interval=30s, InitialDelay=0, LockTTL=35（与原 RobotCfg.Account.SyncInterval=30s 等价）

#### Scenario: bootstrap 读取配置
- **WHEN** 构造 `VirtualBalanceSyncScheduler`
- **THEN** 从 `c.SettlementSchedulerCfg.VirtualBalanceSync` 读取配置，不再从 `c.RobotCfg.Account.SyncInterval` 读取

## MODIFIED Requirements

### Requirement: SchedulerAppService

`SchedulerAppService` SHALL 新增 `virtualBalanceSvc` 字段和 `RunVirtualBalanceSync` 方法。

#### Scenario: 构造函数
- **WHEN** 调用 `NewSchedulerAppService`
- **THEN** 接收 `virtualBalanceSvc domain.VirtualBalanceService` 参数并赋值给字段

#### Scenario: RunVirtualBalanceSync facade
- **WHEN** 调用 `RunVirtualBalanceSync(ctx)`
- **THEN** 转发到 `s.virtualBalanceSvc.SyncToDB(ctx)`，行为与原 scheduler 直接调用一致

## REMOVED Requirements

### Requirement: game/scheduler/VirtualBalanceSyncScheduler

**Reason**: 职责属于 settlement 域，应迁移到 settlement/scheduler/
**Migration**: 文件移到 `settlement/scheduler/virtual_balance_sync.go`，通过 SchedulerAppService 统一管理
