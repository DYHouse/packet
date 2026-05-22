# 优化自动匹配房间排序逻辑

- [x] Task 1: 修改 MatchRoomByBalance SQL 排序
    - 1.1: 将 `room_repository.go` 中 `ORDER BY room_fee DESC, player_count DESC` 改为 `ORDER BY (max_players - player_count) ASC`
