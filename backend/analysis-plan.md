# 代码逆向分析计划 (analysis-plan)

> 本文件为执行计划的中间产物。最终交付物为 `architecture-analysis.md`。
> 所有结论均基于真实代码,不参考任何 README/设计文档/注释描述。

## 1. 分析范围

仓库根: `/Users/aaron.pan/Desktop/party/RedPacket-master/backend`

### 1.1 模块清单

| # | 模块 | 路径 | 性质 |
|---|------|------|------|
| M1 | game | `game/` | 核心业务服务(独立 main) |
| M2 | settlement | `settlement/` | 结算/账务库(被 game 装配) |
| M3 | gateway | `gateway/` | WebSocket 网关(独立 main) |
| M4 | common | `common/` | 公共基础设施库 |
| M5 | stats | `stats/` | 统计服务(独立 main) |
| M6 | api/platform | `api/platform/` | 平台 RPC 客户端 |
| M7 | scripts | `scripts/` | 初始化脚本 |
| M8 | proto | `proto/` | gRPC 服务定义 |
| M9 | migrations/sql | `migrations/`, `sql/` | 数据库迁移与初始化 |

### 1.2 入口识别

| 入口类型 | 位置 |
|---|---|
| 服务 main | `cmd/game/main.go`, `cmd/gateway/main.go`, `cmd/stats/main.go` |
| RPC 入口 | `game/server/generic_service.go`(实现 `GenericService.Forward`/`SaveUser`) |
| MQ 消费者 | `game/infrastructure/messaging/{game,room}_event_consumer.go`, `gateway/broadcast/broadcast.go` |
| MQ 生产者 | `game/infrastructure/messaging/{game,room}_event_publisher.go`, `common/broadcast/kafka_broadcaster.go` |
| 定时任务 | `game/scheduler/timeout_scheduler.go`, `settlement/scheduler/*.go`(6 个) |
| Event Handler | `game/application/game_event_handler.go`, `game/application/robot/behavior_engine.go` |
| Repository | `game/infrastructure/persistence/{mysql,redis}/`, `settlement/infrastructure/persistence/mysql/` |
| Cache | Redis (`game/infrastructure/persistence/redis/`) |

## 2. 执行计划

| 阶段 | 任务 | 状态 |
|------|------|------|
| 阶段1 | 项目扫描 | 已完成 |
| 阶段2 | 模块深度分析(game/settlement/gateway/common/stats) | 已完成 |
| 阶段3 | 领域模型梳理(实体/聚合/状态机) | 已完成 |
| 阶段4 | 数据层分析(MySQL 表/Redis key/MQ topic) | 已完成 |
| 阶段5 | 系统架构图 | 进行中 |
| 阶段6 | 关键业务规则提取(资金安全/幂等/对账) | 已完成 |
| 阶段7 | 架构 Review 与潜在问题识别 | 进行中 |
| 阶段8 | 生成 `architecture-analysis.md` | 待执行 |

## 3. 关键审计原则

1. **源码唯一可信**:所有结论可追溯至具体文件路径与函数名。
2. **不猜测**:无法确定的部分明确标记 `需要人工确认`。
3. **完整性优先**:不遗漏已实现的业务逻辑,宁愿冗余。
4. **资金安全专项**:资金流向、账务一致性、状态机、幂等、对账、异常恢复、补偿机制单独成节。
5. **Job 必要性评估**:列出所有 scheduler 的作用与必要性判断。

## 4. 模块依赖关系(初步)

```
gateway ──gRPC──> game (GenericService)
game ──装配──> settlement (SettleAppService / SchedulerAppService)
game ──调用──> api/platform (Debit/Credit/Settle/GetBalance)
game, gateway, stats ──依赖──> common
game, gateway, stats ──依赖──> common/kafka, common/redis, common/idgen
```

## 5. 资金安全审计清单

- [x] 资金流向(扣款/入账/退款/罚款)
- [x] 账务一致性(BillRecord 双边记账)
- [x] 状态机(BillRecord/RoundSettlement/RefundAudit)
- [x] 幂等设计(BizOrderNo 确定性生成 + 多层防线)
- [x] 对账机制(SettlementCheckService)
- [x] 异常恢复(ExceptionRecord 业务 DLQ)
- [x] 补偿机制(6 个 scheduler + Kafka 重试 + 业务重试)
- [x] Job 场景与必要性(6 个 settlement scheduler)
