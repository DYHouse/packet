# Add 1-Yuan Room Config to Init Script

## Requirement Scenario
在初始化脚本 `backend/scripts/init_rooms.go` 中增加 "1元房" 配置，并初始化 10 个该配置的房间。

## Technical Approach
- RoomFee 单位为 **分 (fen)**，1元 = 100分
- 在 `roomConfigs` 切片最前面添加 `{Name: "Sala de 1", RoomFee: 100, MaxPlayers: 5, MaxRounds: 10, SortOrder: 1, Status: 1}`
- 将现有所有配置的 SortOrder 递增 1（原 1→2, 2→3, ... 8→9），保持排序逻辑一致
- 将 `roomCountPerConfig` 的 key 从 `cfg.ID` 改为 `cfg.RoomFee`，因为 ID 是数据库自增的、不可预知，而 RoomFee 是唯一且稳定的
- `initRooms` 函数中对应改为 `roomCountPerConfig[cfg.RoomFee]`
- 新增 `roomCountPerConfig` 条目: `100: 10`（RoomFee=100，10个房间）

## Affected Files
- `backend/scripts/init_rooms.go`（修改）
  - `roomConfigs` 变量：增加 1 元房配置，调整 SortOrder
  - `roomCountPerConfig` 变量：改用 RoomFee 作 key，增加 100: 10
  - `initRooms` 函数：将 `roomCountPerConfig[cfg.ID]` 改为 `roomCountPerConfig[cfg.RoomFee]`

## Implementation Details

### 1. roomConfigs 修改
```go
var roomConfigs = []model.RoomConfig{
	{Name: "Sala de 1",   RoomFee: 100,   MaxPlayers: 5, MaxRounds: 10, SortOrder: 1, Status: 1},
	{Name: "Sala de 5",   RoomFee: 500,   MaxPlayers: 5, MaxRounds: 10, SortOrder: 2, Status: 1},
	{Name: "Sala de 10",  RoomFee: 1000,  MaxPlayers: 5, MaxRounds: 10, SortOrder: 3, Status: 1},
	{Name: "Sala de 20",  RoomFee: 2000,  MaxPlayers: 5, MaxRounds: 10, SortOrder: 4, Status: 1},
	{Name: "Sala de 30",  RoomFee: 3000,  MaxPlayers: 5, MaxRounds: 10, SortOrder: 5, Status: 1},
	{Name: "Sala de 50",  RoomFee: 5000,  MaxPlayers: 5, MaxRounds: 10, SortOrder: 6, Status: 1},
	{Name: "Sala de 100", RoomFee: 10000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 7, Status: 1},
	{Name: "Sala de 200", RoomFee: 20000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 8, Status: 1},
	{Name: "Sala de 500", RoomFee: 50000, MaxPlayers: 5, MaxRounds: 10, SortOrder: 9, Status: 1},
}
```

### 2. roomCountPerConfig 修改
```go
var roomCountPerConfig = map[int64]int{
	100:   10,
	500:   10,
	1000:  50,
	2000:  40,
	3000:  30,
	5000:  30,
	10000: 20,
	20000: 10,
	50000: 10,
}
```

### 3. initRooms 函数修改
```go
needCreate := roomCountPerConfig[cfg.RoomFee] - int(existingCount)
```

## Boundary Conditions
- 已有数据库运行时，`initRoomConfigs` 会通过 `room_fee` 查找现有记录，不会重复创建
- 已有房间不受影响，`initRooms` 仅补充缺少的房间数
- SortOrder 变更会通过 `initRoomConfigs` 的 Updates 逻辑同步到数据库

## Expected Outcome
- 执行脚本后，数据库中新增 RoomFee=100 的房间配置和 10 个对应房间
- 现有配置的 SortOrder 更新为 2-9，保持排序正确
