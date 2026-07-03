# Outbox + Saga 完整重构方案(v3.0)

> v3.0 修正了 v2.0 的核心架构错位:**本地消息表应放在扣款/入账环节(真金白银),而非事件传递层**。事件传递保持原样 + 加重试即可,丢失靠最终对账兜底。

## 目录

- [一、现状诊断:6 个关键发现](#一现状诊断6-个关键发现)
- [二、目标架构:三层防护](#二目标架构三层防护)
- [三、事件传递层:保持原样 + 重试](#三事件传递层保持原样--重试)
- [四、本地消息表:扣款/入账环节](#四本地消息表扣款入账环节)
- [五、幂等性修复](#五幂等性修复)
- [六、事务边界修复](#六事务边界修复)
- [七、Saga 设计:跨 step 编排](#七saga-设计跨-step-编排)
- [八、对账兜底:保留现有机制](#八对账兜底保留现有机制)
- [九、迁移路线](#九迁移路线)
- [十、风险与边界](#十风险与边界)

---

## 一、现状诊断:6 个关键发现

### 发现 1:事件发布是 fire-and-forget,无任何可靠性保证

4 个 `PublishXxx` 调用点全部不在 DB 事务内,业务状态由 Redis Lua 原子变更,事件用 `go func()` 异步发出,失败仅 log:

| 调用点 | 方法 | 业务"事务"机制 | publish 方式 | 失败处理 |
|--------|------|--------------|------------|---------|
| [PublishSessionStart](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L500) | startGameCore | `scripts.TryStartGame.Run` | `go func() { Publish(BgCtx) }()` | 仅 log |
| [PublishRoundSettle](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L1036) | settleRound | `scripts.SettleRound.Run` | 同上 | 仅 log |
| [PublishSessionEnd](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L1201) | endGameWithOptions | `scripts.EndGame.Run` | 同上 | 仅 log |
| [PublishPacketCreated](file:///e:/demo/party/packet/backend/game/application/game_app_service.go#L1481) | publishPacketCreatedEvent | caller Lua | 同上 | 仅 log |

**风险**:Lua 成功 → 红包已抢完 → publish 失败 → 结算永不触发。

**修正认知**:这个风险靠"同步调用 + 重试"即可大幅降低(从 ~1% 降到 ~0.01%),剩余极低概率靠最终对账兜底,**不需要在事件传递层建 outbox**。

### 发现 2:Kafka consumer 处理失败仍 commit 消息,无自动重投

[common/kafka/consumer.go:105-117](file:///e:/demo/party/packet/backend/common/kafka/consumer.go):

```go
if err := c.processMessage(ctx, msg); err != nil {
    logger.Error("kafka process message failed", ...)  // 仅 log
}
c.reader.CommitMessages(ctx, msg)  // 无论成功失败都 commit!
```

**风险**:consumer 处理 `SettleRound` 失败 → 消息被 commit 丢弃 → 永远不会重投。

**修复**:改为失败不 commit,Kafka 自动重投(at-least-once)。需配套毒消息防护。

### 发现 3:扣款/入账的"外部调用 + DB 记录"非原子(真正的资金黑洞)

这是**最核心的资金风险**,也是本地消息表的正确应用场景。

**扣款**([deduct_service.go:237-269](file:///e:/demo/party/packet/backend/settlement/service/deduct_service.go)):

```go
result, err := s.platform.Debit(ctx, debitReq)  // L237 真扣钱(外部 HTTP,不可回滚)
if err != nil { ... return }
// ...
if err := s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter); err != nil {  // L267 记录(DB)
    logger.Error("update bill success failed", ...)  // 钱已扣,但记录失败!
    return err  // 钱扣了没记录 → 资金黑洞
}
```

**入账**([game_settle_service.go:362-401](file:///e:/demo/party/packet/backend/settlement/service/game_settle_service.go)):

```go
result, err := s.platform.Credit(ctx, creditReq)  // L362 真给钱(外部 HTTP,不可回滚)
if err != nil { ... return }
// ...
return s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter)  // L401 记录(DB)
// 钱给了但记录失败 → 平台多付,玩家余额对不上
```

**风险矩阵**:

| 失败点 | 扣款后果 | 入账后果 |
|--------|---------|---------|
| platform 调用前崩溃 | 无损失(Bill 仍 Processing) | 无损失 |
| platform 调用中崩溃 | **无法判断是否扣款**(钱可能已扣) | 同左 |
| platform 成功后、DB 更新前崩溃 | **钱已扣但 Bill 仍 Processing** | **钱已给但 Bill 仍 Processing** |
| DB 更新失败 | 同上 | 同上 |

当前兜底:[CreditRetryScheduler](file:///e:/demo/party/packet/backend/settlement/scheduler/credit_retry_scheduler.go) 每 30s 扫描 Processing 状态的 Bill 重试,但它**重新调 platform.Debit/Credit**(可能重复扣款/入账)。

**本地消息表的作用**:确保 platform 调用结果可靠落库,避免"钱已动但记录没跟上"。

### 发现 4:consumer 内 SettleRound 传外层 ctx,settlement 表写入不在 consumer tx 内

[game_event_consumer.go:358](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go):

```go
if err := c.db.Transaction(func(tx *gorm.DB) error {
    // ... game 表更新 ...
    if err := c.settlementService.SettleRound(ctx, settleReq); err != nil {  // ← 传外层 ctx,非 tx
        return fmt.Errorf("settle round failed: %w", err)
    }
    return nil
}); err != nil { return err }
```

`SettleRound` 内部用 `s.billMgr`(基于 `*gorm.DB`,非 tx)写 `RoundSettlement` / `BillRecord`。consumer tx 回滚时,settlement 表写入不回滚。

### 发现 5:SessionPlayer 统计用 gorm.Expr 自增,非幂等

[game_event_consumer.go:274-298](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go):

```go
updates := map[string]interface{}{
    "grab_count": gorm.Expr("grab_count + 1"),       // 重试会重复 +1
    "total_grab":  gorm.Expr("total_grab + ?", r.Amount), // 重试会重复累加
}
```

### 发现 6:BizOrderNo 含随机数,平台侧无法靠它去重

[trace_id_generator.go:24-28](file:///e:/demo/party/packet/backend/settlement/service/trace_id_generator.go):

```go
func (g *TraceIDGenerator) GenerateBizOrderNo(bizType string, userID int64) string {
    return fmt.Sprintf("%s_%d_%d_%03d", bizType, time.Now().Unix(), userID, rand.Intn(1000))
}
```

重试时生成新 BizOrderNo → platform 看到不同 BizID → 无法去重 → 重复扣款/入账。

---

## 二、目标架构:三层防护

```
┌─ 第一层:事件传递(game → Kafka → consumer)────────────────────────┐
│  保持原样 + 加重试(删 go func,改同步调用 + 2-3 次重试)          │
│  丢失靠最终对账兜底(SettlementCheckScheduler)                    │
│  consumer 修复:失败不 commit,Kafka 自动重投                     │
└────────────────────────────────────────────────────────────────────┘
                    │
                    ▼
┌─ 第二层:扣款/入账(settlement 内部)──────────────────────────────┐
│  本地消息表(platform_call_outbox):                                │
│    保证"platform.Debit/Credit 调用结果"与"DB BillRecord"原子性   │
│    platform 成功但 DB 未更新 → 调度器补更新(不重调 platform)     │
│    platform 未调用 → 调度器重调(靠 BizOrderNo 幂等)             │
└────────────────────────────────────────────────────────────────────┘
                    │
                    ▼
┌─ 第三层:结算记账(creditRound / SettleReward / SettleGame)────────┐
│  纯 DB 操作,本身 ACID,不需要额外机制                            │
│  事务边界修复:SettleRound 接受 tx 参数,与 consumer tx 闭合     │
│  Saga 编排跨 step 一致性                                          │
└────────────────────────────────────────────────────────────────────┘
                    │
                    ▼
┌─ 兜底:对账(SettlementCheckScheduler,保留现有)─────────────────────┐
│  每 5min 扫描异常状态:                                           │
│    - 首回合扣款失败但未退款的 → 自动创建退款                      │
│    - 已扣款但未结算的 → 创建异常记录,人工介入                    │
│    - 卡在 Processing 的 Bill → 依赖本地消息表调度器处理            │
└────────────────────────────────────────────────────────────────────┘
```

### 各层职责对比

| 关注点 | 机制 | 位置 | 复杂度 |
|--------|------|------|--------|
| 事件不丢 | 同步重试 + Kafka at-least-once | 第一层 | ★☆☆ |
| 钱扣了有记录 | 本地消息表(platform_call_outbox) | 第二层 | ★★☆ |
| 结账多步一致 | DB 事务 + Saga | 第三层 | ★★★ |
| 最终兜底 | SettlementCheckScheduler | 兜底层 | ★☆☆(已有) |

---

## 三、事件传递层:保持原样 + 重试

### 3.1 改动原则

**保持现有架构不变**,只做两个改动:
1. 删除 `go func()`,改为同步调用
2. 增加重试(2-3 次,指数退避)

不引入 outbox 表、不引入对账调度器。丢失靠最终对账(SettlementCheckScheduler)兜底。

### 3.2 Publisher 增加重试

[game_event_publisher.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_publisher.go):

```go
func (p *GameEventPublisher) publishWithRetry(ctx context.Context, event *domain.GameEvent) error {
    if event.Timestamp == 0 {
        event.Timestamp = time.Now().Unix()
    }
    if event.TraceID == "" {
        return fmt.Errorf("event TraceID must be set by caller")
    }

    data, err := json.Marshal(event)
    if err != nil {
        return fmt.Errorf("marshal event failed: %w", err)
    }

    key := fmt.Sprintf("%s_%s", event.RoomID, event.SessionID)

    var lastErr error
    for i := 0; i < 3; i++ {
        if i > 0 {
            delay := time.Duration(i*i) * 500 * time.Millisecond  // 0.5s, 2s
            select {
            case <-time.After(delay):
            case <-ctx.Done():
                return ctx.Err()
            }
        }
        if err := p.producer.Send(ctx, kafka.TopicGameEvents, []byte(key), data); err != nil {
            lastErr = err
            logger.Warn("publish retry", "attempt", i+1, "event_type", event.EventType, "error", err)
            continue
        }
        return nil
    }
    return lastErr
}
```

### 3.3 4 个调用点:删除 go func,改同步

[game_app_service.go](file:///e:/demo/party/packet/backend/game/application/game_app_service.go) 4 个调用点统一改造:

```go
// 改造前(所有 4 个调用点的统一模式):
go func() {
    if err := s.eventPublisher.PublishRoundSettle(context.Background(), event); err != nil {
        logger.Error("publish round settle event failed", "error", err)
    }
}()

// 改造后:同步调用 + 重试,失败仅 log(不 return err,Redis 状态已变更不可回滚)
if err := s.eventPublisher.PublishRoundSettle(ctx, event); err != nil {
    logger.Error("publish round settle event failed after retries",
        "round_id", roundID, "error", err)
    // 不 return err:Lua 已提交,业务不可回滚
    // 丢失的事件靠最终对账(SettlementCheckScheduler)兜底
}
```

**关键设计**:
- 同步调用会阻塞当前请求 ~0-4.5s(重试 3 次),但红包结算已是异步路径,可接受
- 若需完全不阻塞,可改为 `go s.eventPublisher.PublishRoundSettle(ctx, event)`(仍同步发,但异步等待)— 这样 goroutine 即使丢失,Kafka 同步发送 + 重试已大幅降低丢失概率
- 失败仅 log,不 return err:Lua 已提交不可回滚

### 3.4 Consumer 修复:失败不 commit

[common/kafka/consumer.go:105-117](file:///e:/demo/party/packet/backend/common/kafka/consumer.go):

```go
// 改造前:
msg, err := c.reader.FetchMessage(ctx)
if err := c.processMessage(ctx, msg); err != nil {
    logger.Error("kafka process message failed", ...)
}
c.reader.CommitMessages(ctx, msg)  // 无论成功失败都 commit

// 改造后:
msg, err := c.reader.FetchMessage(ctx)
if err != nil {
    if ctx.Err() != nil { return nil }
    logger.Error("kafka fetch message failed", ...)
    time.Sleep(time.Second)
    continue
}

if err := c.processMessage(ctx, msg); err != nil {
    logger.Error("kafka process message failed, NOT committing",
        "topic", c.topic, "error", err)
    // 不 commit,Kafka 会重投(at-least-once)
    time.Sleep(5 * time.Second)  // 限制重投频率
    continue
}

if err := c.reader.CommitMessages(ctx, msg); err != nil {
    logger.Error("kafka commit failed", "error", err)
}
```

**毒消息防护**(用 Redis 计数,超阈值 commit 跳过 + 告警):

```go
// 在 processMessage 前
retryKey := fmt.Sprintf("cashparty:kafka:retry:%s_%d", msg.Topic, msg.Offset)
retryCount, _ := c.redis.Incr(ctx, retryKey).Result()
if retryCount == 1 {
    c.redis.Expire(ctx, retryKey, 1*time.Hour)
}
if retryCount > 50 {
    logger.Error("kafka message exhausted retries, committing to skip",
        "topic", msg.Topic, "offset", msg.Offset, "retry_count", retryCount)
    c.redis.Del(ctx, retryKey)
    c.reader.CommitMessages(ctx, msg)
    continue
}
```

**额外修复**:`StartOffset` 改为 `FirstOffset`,保证重启不丢未消费消息(当前是 `LastOffset`,会丢重启前的消息)。

### 3.5 tryAcquire 废弃

[game_event_consumer.go:449-475](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) 的 `tryAcquire` / `releaseAcquire` 被 consumer 侧业务幂等(SettleRound 的 RoundStatusCredited 检查等)替代:

```go
// 改造前:
if !c.tryAcquire(ctx, event.TraceID) {
    return nil  // fail-open 风险
}

// 改造后:删除 tryAcquire/releaseAcquire
// 幂等由 consumer handler 内的业务检查保证:
//   - handleSessionStart: FirstOrCreate
//   - handlePacketCreated: RoundStatus 更新条件
//   - handleRoundSettle: SettleRound 内 RoundStatusCredited 检查
//   - handleSessionEnd: SessionStatusCompleted 检查
```

---

## 四、本地消息表:扣款/入账环节

### 4.1 为什么本地消息表应放在这里

本地消息表的经典用途是**保证"外部调用 + DB 记录"的原子性**。本项目的外部调用是 `platform.Debit` / `platform.Credit`,对应的 DB 记录是 `BillRecord.Status`。

| 场景 | 外部调用 | DB 记录 | 非原子后果 |
|------|---------|---------|-----------|
| 扣款 | `platform.Debit` | `UpdateBillSuccess` | 钱已扣但 Bill 仍 Processing |
| 入账 | `platform.Credit` | `UpdateBillSuccess` | 钱已给但 Bill 仍 Processing |

**结算记账**(`creditRound` / `SettleReward`)是纯 DB 操作,本身 ACID,不需要本地消息表。

### 4.2 数据模型

```sql
CREATE TABLE platform_call_outbox (
    id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
    message_id      VARCHAR(64)  NOT NULL UNIQUE COMMENT '确定性 ID,如 debit_{billID}',
    bill_id         BIGINT       NOT NULL COMMENT '关联 bill_record.id',
    call_type       VARCHAR(16)  NOT NULL COMMENT 'debit / credit / settle',
    biz_order_no    VARCHAR(64)  NOT NULL COMMENT 'platform 幂等键',
    status          TINYINT      NOT NULL DEFAULT 0
                    COMMENT '0=PENDING 1=PLATFORM_SUCCESS 2=DONE 3=PLATFORM_FAILED 4=PLATFORM_NOT_CALLED',
    platform_called_at  DATETIME COMMENT 'platform 调用时间(区分未调用 vs 已调用)',
    platform_response   JSON     COMMENT 'platform 返回结果(成功时存,用于补更新 Bill)',
    retry_count     INT          NOT NULL DEFAULT 0,
    next_retry_at   DATETIME,
    error_message   TEXT,
    created_at      DATETIME     NOT NULL,
    updated_at      DATETIME     NOT NULL,
    INDEX idx_status_retry (status, next_retry_at),
    INDEX idx_bill (bill_id)
) ENGINE=InnoDB COMMENT='平台调用本地消息表';
```

### 4.3 状态机

```
PENDING ──platform 成功──► PLATFORM_SUCCESS ──UpdateBillSuccess──► DONE
   │                                                              ↑
   │──platform 失败──► PLATFORM_FAILED                            │
   │                          │                                    │
   │                          └──调度器重试(靠 BizOrderNo 幂等)─┘
   │
   └──进程崩溃(platform 是否已调用未知)──► PLATFORM_NOT_CALLED
                                                │
                                                └──调度器查 platform 结果(若有查询接口)
                                                    或重调 platform(靠 BizOrderNo 幂等)
```

**关键状态**:
- `PENDING`:outbox 已写入,platform 还没调(正常流程的起点)
- `PLATFORM_SUCCESS`:platform 成功但 BillRecord 未更新(崩溃点)
- `DONE`:完整流程完成
- `PLATFORM_FAILED`:platform 调用失败,待重试
- `PLATFORM_NOT_CALLED`:无法判断 platform 是否已调用(进程崩溃在调用过程中)

### 4.4 扣款流程改造

[deduct_service.go:200-281](file:///e:/demo/party/packet/backend/settlement/service/deduct_service.go) 的 `executeSingleDeduct` 改造:

```go
func (s *DeductService) executeSingleDeduct(ctx context.Context, bill *model.BillRecord, amount int64) error {
    // 机器人虚拟通道(不变,Redis Lua 原子)
    if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, bill.UserID) {
        if err := s.virtualBalance.Deduct(ctx, bill.UserID, amount); err != nil {
            s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
            return fmt.Errorf("robot virtual deduct failed: %w", err)
        }
        balanceAfter, _ := s.virtualBalance.GetBalance(ctx, bill.UserID)
        return s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter)
    }

    // ===== 真人玩家:本地消息表保护 =====

    // 1. DB 事务内:写 outbox(PENDING)
    messageID := fmt.Sprintf("debit_%d", bill.ID)
    bizOrderNo := s.traceIDGen.GenerateBizOrderNo("DEBIT", bill.UserID, bill.RoundID)  // 确定性生成
    err := s.outboxRepo.Create(ctx, &outbox.Message{
        MessageID:   messageID,
        BillID:      bill.ID,
        CallType:    "debit",
        BizOrderNo:  bizOrderNo,
        Status:      outbox.StatusPending,
    })
    if err != nil {
        // outbox 写失败(可能是重复 messageID,幂等)
        if !isDuplicateKeyErr(err) {
            return fmt.Errorf("create outbox failed: %w", err)
        }
    }

    // 2. 更新 Bill 的 BizOrderNo(确保 platform 幂等键一致)
    if err := s.billMgr.UpdateBillBizOrderNo(ctx, bill.ID, bizOrderNo); err != nil {
        return fmt.Errorf("update bill biz order no failed: %w", err)
    }

    // 3. 调用 platform
    result, err := s.callPlatformDebit(ctx, bill, bizOrderNo, amount)
    if err != nil {
        // platform 失败:更新 outbox 为 FAILED
        s.outboxRepo.UpdateStatus(ctx, messageID, outbox.StatusPlatformFailed, err.Error())
        s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
        s.creditRetrySvc.CreateDebitFailedException(ctx, bill)
        return err
    }

    // 4. platform 成功:DB 事务内更新 Bill + outbox
    return s.completeDebit(ctx, bill.ID, messageID, result)
}

func (s *DeductService) callPlatformDebit(ctx context.Context, bill *model.BillRecord, bizOrderNo string, amount int64) (*platform.CommonResponse, error) {
    // 标记 outbox 为"正在调用 platform"(用于区分未调用 vs 已调用)
    s.outboxRepo.MarkPlatformCalling(ctx, fmt.Sprintf("debit_%d", bill.ID))

    platformUserID, err := s.userIDConvert.GetPlatformUserID(ctx, bill.UserID)
    if err != nil {
        return nil, fmt.Errorf("get platform user id failed: %w", err)
    }

    debitReq := &platform.DebitRequest{
        BizID:    bizOrderNo,
        // ... 其它字段 ...
    }

    callLog, _ := s.callMgr.CreateLog(ctx, &CallLogCreateParams{
        CallType:   model.CallTypeDebit,
        BizOrderNo: bizOrderNo,
        ReqBody:    debitReq,
    })

    result, err := s.platform.Debit(ctx, debitReq)
    if err != nil {
        if callLog != nil {
            s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
                ID: callLog.ID, Status: model.CallLogStatusFailed, ErrorMessage: err.Error(),
            })
        }
        return nil, err
    }

    if callLog != nil {
        s.callMgr.UpdateLog(ctx, &CallLogUpdateParams{
            ID: callLog.ID, RespBody: result, Status: model.CallLogStatusSuccess,
        })
    }
    return result, nil
}

func (s *DeductService) completeDebit(ctx context.Context, billID int64, messageID string, result *platform.CommonResponse) error {
    // DB 事务内:更新 Bill + 更新 outbox
    return s.db.Transaction(func(tx *gorm.DB) error {
        balanceAfter, err := platform.ParseAmount(result.Data.Balance.Amount)
        if err != nil {
            balanceAfter = 0
            logger.Error("parse balance amount failed, mark bill as success with balance=0",
                "bill_id", billID, "error", err)
        }

        if err := s.billMgr.UpdateBillSuccessWithTx(tx, billID, 0, balanceAfter); err != nil {
            return err  // 事务回滚,outbox 仍是 PLATFORM_SUCCESS,调度器会补更新
        }

        // 更新 outbox 为 DONE(同事务)
        return s.outboxRepo.UpdateStatusWithTx(tx, messageID, outbox.StatusDone, "")
    })
}
```

### 4.5 入账流程改造

[game_settle_service.go:327-401](file:///e:/demo/party/packet/backend/settlement/service/game_settle_service.go) 的 `executeSessionCredit` 同理改造:

```go
func (s *GameSettleService) executeSessionCredit(ctx context.Context, bill *model.BillRecord) error {
    // 机器人虚拟通道(不变)
    if s.robotChecker != nil && s.robotChecker.IsRobot(ctx, bill.UserID) {
        if err := s.virtualBalance.Credit(ctx, bill.UserID, bill.Amount); err != nil {
            s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
            return fmt.Errorf("robot virtual credit failed: %w", err)
        }
        balanceAfter, _ := s.virtualBalance.GetBalance(ctx, bill.UserID)
        return s.billMgr.UpdateBillSuccess(ctx, bill.ID, 0, balanceAfter)
    }

    // ===== 真人玩家:本地消息表保护 =====
    messageID := fmt.Sprintf("credit_%d", bill.ID)
    bizOrderNo := s.traceIDGen.GenerateBizOrderNo("SESSION_CREDIT", bill.UserID, bill.SessionID)  // 确定性

    // 1. 写 outbox(PENDING)
    if err := s.outboxRepo.Create(ctx, &outbox.Message{
        MessageID:  messageID,
        BillID:     bill.ID,
        CallType:   "credit",
        BizOrderNo: bizOrderNo,
        Status:     outbox.StatusPending,
    }); err != nil && !isDuplicateKeyErr(err) {
        return err
    }

    // 2. 调用 platform.Credit
    result, err := s.callPlatformCredit(ctx, bill, bizOrderNo)
    if err != nil {
        s.outboxRepo.UpdateStatus(ctx, messageID, outbox.StatusPlatformFailed, err.Error())
        s.billMgr.UpdateBillStatus(ctx, bill.ID, dto.BillStatusFailed, err.Error())
        nextRetryAt := time.Now().Add(5 * time.Second)
        s.billMgr.SetNextRetryTime(ctx, bill.ID, nextRetryAt)
        return err
    }

    // 3. platform 成功:DB 事务内更新 Bill + outbox
    return s.completeCredit(ctx, bill.ID, messageID, result)
}
```

### 4.6 Outbox 调度器(补更新崩溃中遗漏的记录)

```go
package outbox

// Scheduler 每 30s 扫描 platform_call_outbox,处理中断的调用
type Scheduler struct {
    repo           *Repository
    billMgr        *BillManager
    platform       platform.Client
    userIDConvert  *UserIDConvertService
    callMgr        *PlatformCallManager
    maxRetry       int
}

func (s *Scheduler) execute(ctx context.Context) error {
    // 1. 处理 PLATFORM_SUCCESS:platform 已成功但 Bill 未更新
    successMessages, _ := s.repo.GetByStatus(ctx, StatusPlatformSuccess, 100)
    for _, msg := range successMessages {
        // 直接用 platform_response 补更新 Bill,不重调 platform
        if err := s.completePendingUpdate(ctx, msg); err != nil {
            logger.Error("complete pending update failed", "message_id", msg.MessageID, "error", err)
        }
    }

    // 2. 处理 PLATFORM_FAILED:重试调 platform(靠 BizOrderNo 幂等)
    failedMessages, _ := s.repo.GetByStatus(ctx, StatusPlatformFailed, 100)
    for _, msg := range failedMessages {
        if msg.RetryCount >= s.maxRetry {
            s.repo.UpdateStatus(ctx, msg.MessageID, StatusDone, "max retry exceeded, manual intervention required")
            continue
        }
        if err := s.retryPlatformCall(ctx, msg); err != nil {
            s.repo.IncrementRetry(ctx, msg.MessageID)
        }
    }

    // 3. 处理 PLATFORM_NOT_CALLED:无法判断是否已调用
    //    策略:重调 platform(靠 BizOrderNo 幂等,platform 侧去重)
    notCalledMessages, _ := s.repo.GetByStatus(ctx, StatusPlatformNotCalled, 100)
    for _, msg := range notCalledMessages {
        if err := s.retryPlatformCall(ctx, msg); err != nil {
            s.repo.IncrementRetry(ctx, msg.MessageID)
        }
    }

    return nil
}

// completePendingUpdate 用已存的 platform_response 补更新 Bill,不重调 platform
func (s *Scheduler) completePendingUpdate(ctx context.Context, msg *Message) error {
    var result platform.CommonResponse
    json.Unmarshal(msg.PlatformResponse, &result)

    balanceAfter, _ := platform.ParseAmount(result.Data.Balance.Amount)

    // DB 事务内更新 Bill + outbox
    return s.db.Transaction(func(tx *gorm.DB) error {
        if err := s.billMgr.UpdateBillSuccessWithTx(tx, msg.BillID, 0, balanceAfter); err != nil {
            return err
        }
        return s.repo.UpdateStatusWithTx(tx, msg.MessageID, StatusDone, "")
    })
}

// retryPlatformCall 重调 platform(靠 BizOrderNo 幂等)
func (s *Scheduler) retryPlatformCall(ctx context.Context, msg *Message) error {
    bill, _ := s.billMgr.GetBillByID(ctx, msg.BillID)
    if bill == nil {
        return fmt.Errorf("bill not found: %d", msg.BillID)
    }

    // 标记为"正在调用"
    s.repo.MarkPlatformCalling(ctx, msg.MessageID)

    var result *platform.CommonResponse
    var err error

    switch msg.CallType {
    case "debit":
        result, err = s.callPlatformDebit(ctx, bill, msg.BizOrderNo)
    case "credit":
        result, err = s.callPlatformCredit(ctx, bill, msg.BizOrderNo)
    }

    if err != nil {
        s.repo.UpdateStatus(ctx, msg.MessageID, StatusPlatformFailed, err.Error())
        return err
    }

    return s.completePendingUpdate(ctx, msg)  // platform 成功,补更新 Bill
}
```

### 4.7 platform 幂等性要求

本地消息表的重试依赖 **platform 侧基于 BizOrderNo 去重**。当前 [GamingPandaClient](file:///e:/demo/party/packet/backend/api/platform) 实现未明确支持。需要:

1. **BizOrderNo 确定性生成**(见 §5.2):重试时生成相同 BizOrderNo
2. **platform 侧去重**:platform 收到相同 BizOrderNo 的请求,若已处理则返回幂等结果(而非报错)
3. **若 platform 不支持去重**:PLATFORM_NOT_CALLED 状态需人工介入,不能自动重调(否则重复扣款)

### 4.8 outbox.Repository

```go
package outbox

import (
    "context"
    "encoding/json"
    "time"

    "gorm.io/gorm"
)

type Status int8

const (
    StatusPending           Status = 0
    StatusPlatformSuccess   Status = 1
    StatusDone              Status = 2
    StatusPlatformFailed    Status = 3
    StatusPlatformNotCalled Status = 4
)

type Message struct {
    ID                int64           `gorm:"primaryKey;autoIncrement"`
    MessageID         string          `gorm:"uniqueIndex;size:64"`
    BillID            int64           `gorm:"index;not null"`
    CallType          string          `gorm:"size:16;not null"`
    BizOrderNo        string          `gorm:"size:64;not null"`
    Status            Status          `gorm:"index;default:0"`
    PlatformCalledAt  *time.Time
    PlatformResponse   json.RawMessage `gorm:"type:json"`
    RetryCount        int             `gorm:"default:0"`
    NextRetryAt       *time.Time
    ErrorMessage      string          `gorm:"type:text"`
    CreatedAt         time.Time       `gorm:"autoCreateTime"`
    UpdatedAt         time.Time       `gorm:"autoUpdateTime"`
}

type Repository struct {
    db *gorm.DB
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (r *Repository) Create(ctx context.Context, msg *Message) error {
    return r.db.WithContext(ctx).Create(msg).Error
}

func (r *Repository) GetByStatus(ctx context.Context, status Status, limit int) ([]*Message, error) {
    var msgs []*Message
    err := r.db.WithContext(ctx).
        Where("status = ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", status, time.Now()).
        Where("retry_count < ?", 10).
        Order("id ASC").Limit(limit).Find(&msgs).Error
    return msgs, err
}

func (r *Repository) UpdateStatus(ctx context.Context, messageID string, status Status, errMsg string) error {
    return r.db.WithContext(ctx).Model(&Message{}).
        Where("message_id = ?", messageID).
        Updates(map[string]any{"status": status, "error_message": errMsg}).Error
}

func (r *Repository) UpdateStatusWithTx(tx *gorm.DB, messageID string, status Status, errMsg string) error {
    return tx.Model(&Message{}).
        Where("message_id = ?", messageID).
        Updates(map[string]any{"status": status, "error_message": errMsg}).Error
}

func (r *Repository) MarkPlatformCalling(ctx context.Context, messageID string) error {
    now := time.Now()
    return r.db.WithContext(ctx).Model(&Message{}).
        Where("message_id = ?", messageID).
        Updates(map[string]any{
            "status":             StatusPlatformNotCalled,
            "platform_called_at": &now,
        }).Error
}

func (r *Repository) MarkPlatformSuccess(ctx context.Context, messageID string, response []byte) error {
    return r.db.WithContext(ctx).Model(&Message{}).
        Where("message_id = ?", messageID).
        Updates(map[string]any{
            "status":             StatusPlatformSuccess,
            "platform_response": response,
        }).Error
}

func (r *Repository) IncrementRetry(ctx context.Context, messageID string) error {
    delay := time.Duration(1<<uint(time.Now().Second())) * time.Second
    if delay > 10*time.Minute {
        delay = 10 * time.Minute
    }
    nextRetry := time.Now().Add(delay)
    return r.db.WithContext(ctx).Model(&Message{}).
        Where("message_id = ?", messageID).
        Updates(map[string]any{
            "retry_count":  gorm.Expr("retry_count + 1"),
            "next_retry_at": &nextRetry,
        }).Error
}
```

### 4.9 改造后的扣款完整流程(时序)

```
1. DB 事务内:
   - billMgr.CreateBill(Processing)  [已有]
   - outboxRepo.Create(PENDING)      [新增]

2. 标记 platform 正在调用:
   - outboxRepo.MarkPlatformCalling  [新增]

3. 调用 platform.Debit(外部 HTTP)

4a. platform 成功:
   - DB 事务内:
     - billMgr.UpdateBillSuccessWithTx  [改造:加 tx]
     - outboxRepo.UpdateStatusWithTx(DONE)  [新增]
   - 若此步崩溃 → outbox 停留在 PLATFORM_SUCCESS → 调度器补更新

4b. platform 失败:
   - outboxRepo.UpdateStatus(FAILED)  [新增]
   - billMgr.UpdateBillStatus(Failed)  [已有]
   - creditRetrySvc.CreateDebitFailedException  [已有]
```

---

## 五、幂等性修复

### 5.1 SessionPlayer 统计改幂等

[game_event_consumer.go:274-298](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go):

```go
// 改造前:
for _, r := range data.Results {
    updates := map[string]interface{}{
        "grab_count": gorm.Expr("grab_count + 1"),
        "total_grab":  gorm.Expr("total_grab + ?", r.Amount),
    }
    tx.Model(&model.SessionPlayer{}).
        Where("session_id = ? AND user_id = ?", sessionIDInt64, parseInt64(r.UserID)).
        Updates(updates)
}

// 改造后:先检查 grab_record 是否已存在(幂等标记)
for _, r := range data.Results {
    userID := parseInt64(r.UserID)
    var count int64
    tx.Model(&model.RoundGrabRecord{}).
        Where("round_id = ? AND user_id = ?", parseInt64(event.RoundID), userID).
        Count(&count)

    if count == 0 {
        // 首次处理,累加统计
        tx.Model(&model.SessionPlayer{}).
            Where("session_id = ? AND user_id = ?", sessionIDInt64, userID).
            Updates(map[string]interface{}{
                "grab_count": gorm.Expr("grab_count + 1"),
                "total_grab":  gorm.Expr("total_grab + ?", r.Amount),
            })
    }
    // count > 0 表示重试,跳过累加(grab_record 已存在,FirstOrCreate 不会重复插)
}
```

同理修复 `send_count` / `total_send`([L286-298](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go))。

### 5.2 BizOrderNo 确定性生成

[trace_id_generator.go:24-28](file:///e:/demo/party/packet/backend/settlement/service/trace_id_generator.go):

```go
// 改造前:
func (g *TraceIDGenerator) GenerateBizOrderNo(bizType string, userID int64) string {
    return fmt.Sprintf("%s_%d_%d_%03d", bizType, time.Now().Unix(), userID, rand.Intn(1000))
}

// 改造后:基于业务确定性参数生成,确保重试生成相同 BizOrderNo
func (g *TraceIDGenerator) GenerateBizOrderNo(bizType string, userID int64, bizKey int64) string {
    return fmt.Sprintf("%s_%d_%d", bizType, bizKey, userID)
}
```

所有调用点需同步修改,传入业务键(`roundID` 或 `sessionID`)。这是本地消息表重试安全的前提(见 §4.7)。

### 5.3 BillManager 增加 tx 版本

为支持 §四 的本地消息表事务,`BillManager` 需增加 `WithTx` 版本方法:

```go
// 现有方法保持不变(用 s.db)
func (m *BillManager) UpdateBillSuccess(ctx context.Context, billID int64, balanceBefore, balanceAfter int64) error { ... }

// 新增 tx 版本(用传入的 tx)
func (m *BillManager) UpdateBillSuccessWithTx(tx *gorm.DB, billID int64, balanceBefore, balanceAfter int64) error {
    return tx.Model(&model.BillRecord{}).
        Where("id = ?", billID).
        Updates(map[string]any{
            "status":         dto.BillStatusSuccess,
            "balance_before": balanceBefore,
            "balance_after":  balanceAfter,
        }).Error
}

func (m *BillManager) UpdateBillStatusWithTx(tx *gorm.DB, billID int64, status int, errMsg string) error { ... }
func (m *BillManager) UpdateBillBizOrderNo(ctx context.Context, billID int64, bizOrderNo string) error { ... }
```

---

## 六、事务边界修复

### 6.1 SettleRound 接受 tx 参数

[settlement_service.go:67](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go):

```go
// 改造前:
func (s *SettlementService) SettleRound(ctx context.Context, req *dto.RoundSettleRequest) error {
    // 内部用 s.billMgr(基于 *gorm.DB,非 tx)写库
}

// 改造后:
func (s *SettlementService) SettleRound(ctx context.Context, tx *gorm.DB, req *dto.RoundSettleRequest) error {
    // 内部用 tx 写库,与 consumer 的 db.Transaction 闭合
}
```

### 6.2 consumer 调用改造

[game_event_consumer.go:358](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go):

```go
// 改造前:
if err := c.settlementService.SettleRound(ctx, settleReq); err != nil {

// 改造后:
if err := c.settlementService.SettleRound(ctx, tx, settleReq); err != nil {
```

[game_event_consumer.go:435](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) SettleGame 同理改造。

这样 settlement 表的写入与 game 表的写入在同一事务内,回滚时一起回滚。

---

## 七、Saga 设计:跨 step 编排

### 7.1 为什么需要 Saga

本地消息表保证了单个 platform 调用的原子性(微观),Saga 保证多 step 业务流程的一致性(宏观):

- 扣款成功 → 结算记账 → 入账,跨多个 step
- step 间无事务保护(平台 Debit 成功 → DB 更新失败 → 钱扣了但 Bill 未标记)
- 补偿逻辑散弹枪(`handleFirstRoundDeductFailure` / `RefundService.ApplyForRefund` / `SettlementCheckService.ensureRefundCreated` 做的事高度重叠)
- 重试策略分散(3 套:credit_retry 指数退避 / executeSessionCredit 固定 5s / Kafka 重投)
- 无全局状态机,无法查询"这个 session 的结算卡在哪一步"

### 7.2 数据模型

```sql
CREATE TABLE saga_instance (
    id              BIGINT       PRIMARY KEY AUTO_INCREMENT,
    saga_id         VARCHAR(64)  NOT NULL UNIQUE,
    saga_type       VARCHAR(32)  NOT NULL,
    business_key    VARCHAR(128) NOT NULL COMMENT 'round_id / session_id',
    status          TINYINT      NOT NULL COMMENT '0=RUNNING 1=COMPLETED 2=COMPENSATING 3=FAILED 4=COMPENSATED',
    current_step    INT          NOT NULL DEFAULT 0,
    payload         JSON,
    result          JSON,
    error_message   TEXT,
    retry_count     INT          NOT NULL DEFAULT 0,
    next_retry_at   DATETIME,
    created_at      DATETIME     NOT NULL,
    updated_at      DATETIME     NOT NULL,
    INDEX idx_status_retry (status, next_retry_at),
    INDEX idx_business (saga_type, business_key)
) ENGINE=InnoDB;

CREATE TABLE saga_step_log (
    id              BIGINT      PRIMARY KEY AUTO_INCREMENT,
    saga_id         VARCHAR(64) NOT NULL,
    step_no         INT         NOT NULL,
    step_name       VARCHAR(64) NOT NULL,
    action          VARCHAR(16) NOT NULL COMMENT 'EXECUTE / COMPENSATE',
    status          TINYINT      NOT NULL COMMENT '0=STARTED 1=SUCCESS 2=FAILED',
    request         JSON,
    response        JSON,
    error_message   TEXT,
    started_at      DATETIME    NOT NULL,
    finished_at     DATETIME,
    UNIQUE KEY uk_saga_step_action (saga_id, step_no, action),
    INDEX idx_saga (saga_id)
) ENGINE=InnoDB;
```

### 7.3 核心接口

```go
package saga

type Action string
const (
    ActionExecute    Action = "EXECUTE"
    ActionCompensate Action = "COMPENSATE"
)

type StepContext struct {
    SagaID     string
    StepNo     int
    Payload    []byte
    StepResult []byte
    TX         *gorm.DB
}

type SagaStep interface {
    Name() string
    Execute(ctx context.Context, sctx *StepContext) (result []byte, err error)
    Compensate(ctx context.Context, sctx *StepContext) error
    Retryable(err error) bool
    CompensateOnFailure() bool
}

type SagaDefinition struct {
    SagaType string
    Steps    []SagaStep
}

type Orchestrator interface {
    Start(ctx context.Context, sagaID string, def *SagaDefinition, payload []byte) (*Instance, error)
    Resume(ctx context.Context, sagaID string) error
    GetStatus(ctx context.Context, sagaID string) (*Instance, error)
}
```

### 7.4 状态机

```
RUNNING ──all steps done──► COMPLETED
   │
   ├──step failed & retryable & retry_count < max──► RUNNING(等调度器重试)
   │
   └──step failed & (not retryable | retry exhausted)──► COMPENSATING
                                                                │
                                    ┌───────────────────────────┘
                                    ▼
                              COMPENSATED ──compensate failed──► FAILED
                                                                  │
                                                          创建 ExceptionRecord
                                                          人工介入
```

### 7.5 PlatformError(解决发现 5)

[api/platform/errors.go](file:///e:/demo/party/packet/backend/api/platform)(新增):

```go
package platform

type ErrorType int
const (
    ErrorTypeTransient ErrorType = iota  // 网络/超时/5xx,可重试
    ErrorTypeBusiness   ErrorType = iota  // 余额不足/重复请求,不可重试
)

type PlatformError struct {
    Code    int
    Msg     string
    Type    ErrorType
}

func (e *PlatformError) Error() string {
    return fmt.Sprintf("platform error: code=%d msg=%s", e.Code, e.Msg)
}
func (e *PlatformError) IsTransient() bool { return e.Type == ErrorTypeTransient }
```

`GamingPandaClient` 实现中,HTTP 超时/5xx 包装为 `ErrorTypeTransient`,业务 code != 0 包装为 `ErrorTypeBusiness`。

### 7.6 RetryPolicy

```go
type RetryPolicy struct {
    MaxRetryCount   int
    BaseDelay       time.Duration
    MaxDelay        time.Duration
    RetryMultiplier float64
}

func DefaultRetryPolicy() *RetryPolicy {
    return &RetryPolicy{
        MaxRetryCount:   5,
        BaseDelay:       5 * time.Second,
        MaxDelay:        5 * time.Minute,
        RetryMultiplier: 2.0,
    }
}

func (p *RetryPolicy) Retryable(err error) bool {
    var pe *platform.PlatformError
    if errors.As(err, &pe) {
        return pe.IsTransient()
    }
    return true  // 非 platform 错误默认可重试
}

func (p *RetryPolicy) NextRetry(retryCount int) time.Time {
    delay := time.Duration(float64(p.BaseDelay) *
        math.Pow(p.RetryMultiplier, float64(retryCount)))
    if delay > p.MaxDelay { delay = p.MaxDelay }
    return time.Now().Add(delay)
}
```

### 7.7 Orchestrator 实现

```go
func (o *OrchestratorImpl) Start(ctx context.Context, sagaID string, def *SagaDefinition, payload []byte) (*Instance, error) {
    // 幂等:若已存在则返回
    if existing, _ := o.repo.GetInstance(ctx, sagaID); existing != nil {
        if existing.Status == StatusRunning {
            return existing, o.Resume(ctx, sagaID)
        }
        return existing, nil
    }

    inst := &Instance{
        SagaID: sagaID, SagaType: def.SagaType, Status: StatusRunning,
        Payload: payload, CreatedAt: time.Now(),
    }
    if err := o.repo.CreateInstance(ctx, inst); err != nil {
        return nil, err
    }
    return inst, o.Resume(ctx, sagaID)
}

func (o *OrchestratorImpl) Resume(ctx context.Context, sagaID string) error {
    inst, _ := o.repo.GetInstance(ctx, sagaID)
    if inst.Status != StatusRunning { return nil }
    def := o.registry.Get(inst.SagaType)

    for stepNo := inst.CurrentStep; stepNo < len(def.Steps); stepNo++ {
        step := def.Steps[stepNo]
        sctx := &StepContext{SagaID: sagaID, StepNo: stepNo, Payload: inst.Payload}

        // 幂等:检查 step 是否已成功
        if log, _ := o.repo.GetStepLog(ctx, sagaID, stepNo, ActionExecute); log != nil && log.Status == StepStatusSuccess {
            sctx.StepResult = log.Response
            continue
        }

        stepLog := &StepLog{
            SagaID: sagaID, StepNo: stepNo, StepName: step.Name(),
            Action: ActionExecute, Status: StepStatusStarted, StartedAt: time.Now(),
        }
        o.repo.CreateStepLog(ctx, stepLog)

        result, err := step.Execute(ctx, sctx)
        if err != nil {
            stepLog.ErrorMessage = err.Error()
            stepLog.Status = StepStatusFailed
            o.repo.UpdateStepLog(ctx, stepLog)

            if !step.Retryable(err) || !o.retryPolicy.CanRetry(inst.RetryCount) {
                return o.compensate(ctx, inst, def, stepNo)
            }

            inst.NextRetryAt = o.retryPolicy.NextRetry(inst.RetryCount)
            inst.RetryCount++
            o.repo.UpdateInstance(ctx, inst)
            return nil
        }

        stepLog.Status = StepStatusSuccess
        stepLog.Response = result
        stepLog.FinishedAt = time.Now()
        o.repo.UpdateStepLog(ctx, stepLog)

        inst.CurrentStep = stepNo + 1
        o.repo.UpdateInstance(ctx, inst)
    }

    inst.Status = StatusCompleted
    return o.repo.UpdateInstance(ctx, inst)
}

func (o *OrchestratorImpl) compensate(ctx context.Context, inst *Instance, def *SagaDefinition, failedStepNo int) error {
    inst.Status = StatusCompensating
    o.repo.UpdateInstance(ctx, inst)

    for stepNo := failedStepNo - 1; stepNo >= 0; stepNo-- {
        step := def.Steps[stepNo]
        if !step.CompensateOnFailure() { continue }

        if log, _ := o.repo.GetStepLog(ctx, inst.SagaID, stepNo, ActionCompensate); log != nil && log.Status == StepStatusSuccess {
            continue
        }

        sctx := &StepContext{SagaID: inst.SagaID, StepNo: stepNo, Payload: inst.Payload}
        stepLog := &StepLog{
            SagaID: inst.SagaID, StepNo: stepNo, StepName: step.Name(),
            Action: ActionCompensate, Status: StepStatusStarted, StartedAt: time.Now(),
        }
        o.repo.CreateStepLog(ctx, stepLog)

        if err := step.Compensate(ctx, sctx); err != nil {
            stepLog.ErrorMessage = err.Error()
            stepLog.Status = StepStatusFailed
            o.repo.UpdateStepLog(ctx, stepLog)

            inst.Status = StatusFailed
            inst.ErrorMessage = fmt.Sprintf("compensate step %d failed: %v", stepNo, err)
            o.repo.UpdateInstance(ctx, inst)
            return err
        }

        stepLog.Status = StepStatusSuccess
        stepLog.FinishedAt = time.Now()
        o.repo.UpdateStepLog(ctx, stepLog)
    }

    inst.Status = StatusCompensated
    return o.repo.UpdateInstance(ctx, inst)
}
```

### 7.8 SagaRetryScheduler(替代现有 5 个 scheduler 的重试部分)

```go
type SagaRetryScheduler struct {
    base         *scheduler.BaseScheduler
    orchestrator *saga.Orchestrator
}

func (s *SagaRetryScheduler) execute(ctx context.Context) error {
    instances, _ := s.orchestrator.GetRetryableInstances(ctx, limit=100)
    for _, inst := range instances {
        if err := s.orchestrator.Resume(ctx, inst.SagaID); err != nil {
            logger.Error("saga resume failed", "saga_id", inst.SagaID, "error", err)
        }
    }
    return nil
}
```

### 7.9 5 个 Saga 定义

#### Saga 1:SETTLE_ROUND(回合结算)

**触发**:consumer `handleRoundSettle` 收到 `GameEventRoundSettle` 后。

| step | step_name | Execute | Compensate |
|------|-----------|---------|------------|
| 0 | UPDATE_ROUND_SETTLE_INFO | `billMgr.UpdateRoundSettlementSettleInfo` | 无 |
| 1 | CREDIT_ROUND | `settlementSvc.creditRound`(创建 grab+commission Bill) | 删除已创建的 Bill |
| 2 | SETTLE_REWARD | `rewardSettler.SettleReward` | 删除 reward Bill |
| 3 | MARK_CREDITED | `billMgr.UpdateRoundSettlementCredited` | 无 |

幂等保障:
- `creditRound` 内部已有 `GetBillByRoundTypeAndUser` 检查([settlement_service.go:147-155](file:///e:/demo/party/packet/backend/settlement/service/settlement_service.go))
- `SettleReward` 内部已有 `GetBillByRoundTypeAndUser(BillTypeSystemReward)` 检查([reward_settler.go:69-72](file:///e:/demo/party/packet/backend/settlement/service/reward_settler.go))

#### Saga 2:SETTLE_GAME(会话结算)

**触发**:consumer `handleSessionEnd` 收到 `GameEventSessionEnd` 后。

| step | step_name | Execute | Compensate |
|------|-----------|---------|------------|
| 0 | AGGREGATE_BILLS | `billMgr.AggregateBetBySession` + `AggregatePayOutBySession` | 无(只读) |
| 1 | UPDATE_GAME_SETTLE_STATUS | `billMgr.UpdateGameSettleStatusBySession(Settling)` | 回滚为 None |
| 2 | CREDIT_SESSION_PAYOUTS | `gameSettleSvc.creditSessionPayouts`(调 platform.Credit,**用 outbox 保护**) | 对已成功 Credit 的调 platform.Debit 反向 |
| 3 | SETTLE_EACH_PLAYER | 遍历玩家调 `gameSettleSvc.settlePlayer`(调 platform.Settle) | 标记 Failed,不反向(Settle 仅上报结果,不移动资金) |
| 4 | MARK_GAME_SETTLED | `billMgr.UpdateGameSettleStatusBySession(Success)` | 无 |

#### Saga 3:DEDUCT_FIRST_ROUND(首回合扣款)

**触发**:game application 在 `initRoundAndDeduct` 时调用。

| step | step_name | Execute | Compensate |
|------|-----------|---------|------------|
| 0 | CREATE_ROUND_SETTLEMENT | `billMgr.CreateRoundSettlementAndBills` | 删除 |
| 1 | DEDUCT_ALL_PLAYERS | `deductSvc.executeBatchDeduct`(调 platform.Debit,**用 outbox 保护**) | 对已成功 Debit 的玩家调 `refundSvc.ApplyForRefund` |
| 2 | UPDATE_DEDUCTED | `billMgr.UpdateRoundSettlementDeductSuccess` | 无 |

**补偿复用**:`DeductAllPlayersStep.Compensate` 替代现有 `handleFirstRoundDeductFailure`([deduct_service.go:283-319](file:///e:/demo/party/packet/backend/settlement/service/deduct_service.go))。

#### Saga 4:DEDUCT_LATER_ROUND(后续回合扣款)

| step | step_name | Execute | Compensate |
|------|-----------|---------|------------|
| 0 | CREATE_SETTLEMENT | `billMgr.CreateRoundSettlementAndBills` | 删除 |
| 1 | DEDUCT_SINGLE_USER | `deductSvc.executeSingleDeduct`(**用 outbox 保护**) | `refundSvc.ApplyForRefund` |
| 2 | UPDATE_DEDUCTED | `UpdateRoundSettlementDeductSuccess` | 无 |

#### Saga 5:REFUND(退款)

**触发**:`RefundProcessScheduler` 扫描到 Pending 的 RefundAudit 时。

| step | step_name | Execute | Compensate |
|------|-----------|---------|------------|
| 0 | APPROVE_REFUND | `refundSvc.ApproveRefund` | 回滚为 Pending |
| 1 | EXECUTE_REFUND | `refundSvc.executeRefund`(调 platform.Credit,**用 outbox 保护**) | 标记 Failed,创建 ExceptionRecord |
| 2 | MARK_REFUNDED | `billMgr.UpdateRefundSuccessInTransaction` | 无 |

### 7.10 Saga 与现有 Scheduler 的关系

| 现有 Scheduler | Saga 化后 |
|---------------|----------|
| `CreditRetryScheduler`(30s) | 被 `SagaRetryScheduler` + `outbox.Scheduler` 替代 |
| `RefundProcessScheduler`(1min) | 改为扫描 RefundAudit Pending → 启动 REFUND Saga |
| `SettlementCheckScheduler`(5min) | **保留**,作为最终对账兜底 |
| `GameSettleRetryScheduler`(30s) | 被 `SagaRetryScheduler` 替代 |
| `GameSettleTimeoutScheduler`(5min) | **保留**,作为" Saga 未启动"的兜底 |

---

## 八、对账兜底:保留现有机制

### 8.1 不新建对账调度器

**决策**:不新建复杂的对账系统。事件丢失的最终兜底由现有 `SettlementCheckScheduler` 承担。

[settlement_check_service.go](file:///e:/demo/party/packet/backend/settlement/service/settlement_check_service.go) 已有的两个检查:

1. **CheckFirstRoundDeductFailure**([L34-47](file:///e:/demo/party/packet/backend/settlement/service/settlement_check_service.go)):
   - 扫描 10 分钟前 failed 的首回合 settlement
   - 对每个调 `ensureRefundCreated`,自动创建退款
   - **弥补 DeductService.handleFirstRoundDeductFailure 可能因 crash 未执行的退款登记**

2. **CheckDeductedButNotSettled**([L53-66](file:///e:/demo/party/packet/backend/settlement/service/settlement_check_service.go)):
   - 扫描已扣款但未结算的 round
   - 创建 `ExceptionTypeDeductedNotSettled` 异常记录(不自动退款,人工介入)

### 8.2 事件丢失的兜底路径

| 事件丢失场景 | 兜底机制 | 触发条件 |
|-------------|---------|---------|
| `RoundSettle` 丢失 → 结算永不触发 | `CheckDeductedButNotSettled` | round 已扣款(Deducted)但 5 分钟后仍未 Credited |
| `SessionEnd` 丢失 → 会话结算永不触发 | `GameSettleTimeoutScheduler` | 所有 round Credited 但 GameSettleStatus==None 且超 1h |
| `SessionStart` / `PacketCreated` 丢失 | 业务异常(room 卡在某个阶段),人工介入 | room 状态超时告警 |

**关键设计**:
- `CheckDeductedButNotSettled` 扫描的是 settlement 表的 `RoundSettlement.Status`(不是事件),与事件传递层完全解耦
- 即使事件完全丢失,只要扣款记录在 settlement 表内,对账就能发现
- 扣款记录由本地消息表(§四)保证可靠落库,不会因事件丢失而丢

### 8.3 对账的局限性(已知,可接受)

| 局限 | 后果 | 应对 |
|------|------|------|
| `SessionStart` 丢失 → MySQL 无 session 行 | 后续 round 无法关联 session | 人工补 session 行(极低概率) |
| `PacketCreated` 丢失 → round 无 grab records | 历史查询缺失 | 人工补(不影响资金) |
| 事件丢失 + 扣款也失败 | 无 settlement 行 | 玩家未扣款,无资金损失 |

**核心保障**:资金安全靠"扣款/入账的本地消息表 + settlement 表对账",不依赖事件传递的 100% 可靠。

---

## 九、迁移路线

### 阶段 1.1:Consumer 修复 + 事件重试(P0,先行)

- [ ] [common/kafka/consumer.go](file:///e:/demo/party/packet/backend/common/kafka/consumer.go) 改为失败不 commit + 毒消息防护
- [ ] `StartOffset` 改为 `FirstOffset`
- [ ] [game_event_publisher.go](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_publisher.go) 增加 `publishWithRetry`
- [ ] [game_app_service.go](file:///e:/demo/party/packet/backend/game/application/game_app_service.go) 4 个调用点删除 `go func()`,改同步 + 重试
- [ ] 删除 `tryAcquire` / `releaseAcquire`

### 阶段 1.2:幂等性修复(P0)

- [ ] [game_event_consumer.go:274-298](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) SessionPlayer 统计改幂等
- [ ] [trace_id_generator.go:24-28](file:///e:/demo/party/packet/backend/settlement/service/trace_id_generator.go) BizOrderNo 改确定性生成
- [ ] 所有 `GenerateBizOrderNo` 调用点同步修改

### 阶段 1.3:本地消息表(P0,核心)

- [ ] 创建 `platform_call_outbox` 表 migration
- [ ] 实现 `outbox.Repository`(Create / GetByStatus / UpdateStatus / MarkPlatformCalling / IncrementRetry)
- [ ] 实现 `outbox.Scheduler`(扫描 PLATFORM_SUCCESS/FAILED/NOT_CALLED)
- [ ] `BillManager` 增加 `WithTx` 版本方法
- [ ] [deduct_service.go:200-281](file:///e:/demo/party/packet/backend/settlement/service/deduct_service.go) `executeSingleDeduct` 改造(写 outbox + 事务更新)
- [ ] [game_settle_service.go:327-401](file:///e:/demo/party/packet/backend/settlement/service/game_settle_service.go) `executeSessionCredit` 同理改造
- [ ] [game/bootstrap/app.go](file:///e:/demo/party/packet/backend/game/bootstrap/app.go) 启动 `outbox.Scheduler`

### 阶段 1.4:事务边界修复(P0)

- [ ] `SettlementService.SettleRound` 增加 `tx *gorm.DB` 参数
- [ ] `SettlementService.SettleGame` 增加 `tx *gorm.DB` 参数
- [ ] [game_event_consumer.go:358,435](file:///e:/demo/party/packet/backend/game/infrastructure/messaging/game_event_consumer.go) 传 tx

### 阶段 2.1:Saga 框架(P1)

- [ ] 创建 `saga_instance` / `saga_step_log` 表
- [ ] 实现 `settlement/domain/saga/` 包(接口 + Orchestrator)
- [ ] 实现 `RetryPolicy`
- [ ] 实现 `SagaRetryScheduler`
- [ ] [api/platform/errors.go](file:///e:/demo/party/packet/backend/api/platform) 新增 `PlatformError` + `IsTransient`
- [ ] `GamingPandaClient` 实现中包装错误分类

### 阶段 2.2:SETTLE_ROUND Saga 试点(P1)

- [ ] 实现 4 个 SagaStep(UpdateSettleInfo / CreditRound / SettleReward / MarkCredited)
- [ ] consumer `handleRoundSettle` 改为调 `orchestrator.Start`
- [ ] 对比测试:新旧实现结果一致性

### 阶段 2.3:其余 Saga 迁移(P2)

- [ ] SETTLE_GAME Saga(5 步)
- [ ] DEDUCT_FIRST_ROUND Saga(3 步)
- [ ] DEDUCT_LATER_ROUND Saga(3 步)
- [ ] REFUND Saga(3 步)

### 阶段 2.4:清理冗余(P2-P3)

- [ ] 移除 `handleFirstRoundDeductFailure`(被 Saga Compensate 替代)
- [ ] 移除 `calculateNextRetryTime`(被 RetryPolicy 替代)
- [ ] 移除 `CreditRetryScheduler` / `GameSettleRetryScheduler`(被 SagaRetryScheduler + outbox.Scheduler 替代)
- [ ] 保留 `SettlementCheckScheduler` / `GameSettleTimeoutScheduler` 作为兜底

---

## 十、风险与边界

### 10.1 风险

| 风险 | 应对 |
|------|------|
| 事件传递层无 outbox,仍有丢失风险 | 同步重试覆盖 99.9%;剩余靠 SettlementCheckScheduler 对账 |
| 本地消息表 PLATFORM_NOT_CALLED 状态无法判断 platform 是否已调用 | 靠 BizOrderNo 确定性 + platform 侧去重,重调安全;若 platform 不支持去重则人工介入 |
| `SettleRound(tx, ...)` 改造影响面大 | BillManager 方法增加 tx 参数是渐进式 |
| Saga 框架引入新复杂度 | 阶段 2.1 单元测试覆盖;阶段 2.2 试点后再推广 |
| `PlatformError` 引入需改 platform client | 仅新增错误包装,不改接口签名 |

### 10.2 不适用本地消息表/Saga 的场景

- **Redis Lua 虚拟余额扣减**:已原子,不改
- **结算记账**(`creditRound` / `SettleReward`):纯 DB 操作,本身 ACID
- **只读查询**(`CheckBalance` / `GetBillsByUserID`)
- **机器人虚拟通道**:不走 platform,无外部依赖

### 10.3 性能影响

| 操作 | 延迟增量 | 可接受 |
|------|---------|--------|
| 事件同步重试(每个事件) | 0-4.5s(仅失败时) | ✅ 结算异步路径 |
| outbox MySQL INSERT(每次扣款/入账) | ~1ms | ✅ |
| outbox.Scheduler 扫描(30s 间隔) | 后台,无影响 | ✅ |
| Saga step_log INSERT(每个 step) | ~1ms × 3-5 步 | ✅ |

### 10.4 与现有约束兼容

| 约束 | 兼容性 |
|------|--------|
| `cashparty:` Redis key 前缀 | outbox 用 MySQL;saga 用 MySQL;毒消息计数用 `cashparty:kafka:retry:` 前缀 |
| Settlement 不 import game | outbox 在 settlement 包内;saga 在 settlement 包内 |
| Lua 原子化 | 不改 Lua,事件传递层仍"Lua 后发 Kafka" |
| 事务失败返回 error 触发回滚 | SagaStep.Execute 失败上抛 error 触发补偿,符合现有模式 |

---

**文档版本**:v3.0(修正 v2.0 的架构错位:本地消息表从事件传递层移到扣款/入账层)
**前置文档**:[CODE_ARCHITECTURE_REFACTOR_PLAN.md](./CODE_ARCHITECTURE_REFACTOR_PLAN.md)(架构总览)
**后续文档**:阶段 3(物理拆分 settlement)— 待决策后制定
