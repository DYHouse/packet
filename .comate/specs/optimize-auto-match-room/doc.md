# 优化自动匹配房间逻辑 - 优先匹配快满座房间

## 需求场景

当前自动匹配房间逻辑中，SQL 排序为 `ORDER BY room_fee DESC, player_count DESC`，即优先匹配价格最高的房间，其次才考虑人数。需要改为优先匹配快满座的房间（剩余座位少的房间优先），不再使用价格排序。

## 处理逻辑

### 当前逻辑
- 过滤条件：`room_fee <= balance`、`status IN (0, 1)`、`player_count < max_players`
- 排序：`room_fee DESC, player_count DESC`（价格优先，人数次之）
- 结果：用户总是被匹配到能负担的最贵房间，而非最接近满座的房间

### 优化后逻辑
- 过滤条件不变
- 排序改为：`(max_players - player_count) ASC`
  - 剩余座位数升序：剩余座位越少 = 越接近满座 = 优先匹配
  - 不再使用价格排序
- 结果：用户优先被分配到快满座的房间，加快房间满座速度

## 技术方案

### 修改文件

1. **`/Users/aaron.pan/Desktop/RedPacket-master/backend/game/infrastructure/persistence/mysql/room_repository.go`** (修改 SQL 查询)
   - 函数：`MatchRoomByBalance` (第128-146行)
   - 修改内容：将 `ORDER BY room_fee DESC, player_count DESC` 改为 `ORDER BY (max_players - player_count) ASC`

## 实现细节

修改后的 SQL 查询：
```sql
SELECT room_id
FROM rooms
WHERE room_fee <= ?
  AND status IN (0, 1)
  AND player_count < max_players
ORDER BY (max_players - player_count) ASC
LIMIT 1
```

### 排序逻辑说明
- `(max_players - player_count) ASC`：剩余座位数升序
  - 剩余1个座位的房间排在最前（最接近满座）
  - 剩余2个座位的房间次之
  - 依此类推

### 边界条件
- 所有房间均为空（player_count = 0）：`(max_players - 0) = max_players`，排序退化为 `max_players ASC`，即优先匹配人数上限最小的房间（更容易满座），合理
- 所有房间只剩1个座位：所有房间排序值相同，MySQL 将按存储顺序返回，行为可接受
- 无可用房间：返回 `CodeNoIdleRoom` 错误，逻辑不变

## 数据流路径

```
用户发送 auto_match 命令
  → GenericServiceServer.handleAutoMatch()
  → RoomAppService.AutoMatchAndJoin()
  → SettlementService.CheckBalance()  // 获取余额
  → RoomDBRepo.MatchRoomByBalance()   // 【此处修改】SQL 排序优化
  → RoomAppService.JoinRoom()         // 以观众身份加入
```

## 预期结果

- 用户自动匹配时优先进入快满座的房间
- 加快房间满座速度，减少等待时间
- 不再按价格排序匹配
