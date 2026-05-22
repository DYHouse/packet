# 优化自动匹配房间排序逻辑 - 总结

## 变更内容

修改了 `backend/game/infrastructure/persistence/mysql/room_repository.go` 中 `MatchRoomByBalance` 方法的 SQL 排序逻辑：

- **修改前**：`ORDER BY room_fee DESC, player_count DESC`（价格优先，人数次之）
- **修改后**：`ORDER BY (max_players - player_count) ASC`（剩余座位少的房间优先）

## 效果

- 用户自动匹配时优先进入快满座的房间
- 加快房间满座速度，减少玩家等待时间
- 不再按价格排序匹配
