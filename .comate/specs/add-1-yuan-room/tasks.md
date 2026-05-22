# Add 1-Yuan Room to Init Script

- [x] Task 1: Modify `roomConfigs` slice - add 1元房 config and adjust SortOrder
    - 1.1: Add `{Name: "Sala de 1", RoomFee: 100, MaxPlayers: 5, MaxRounds: 10, SortOrder: 1, Status: 1}` at the beginning of `roomConfigs`
    - 1.2: Increment SortOrder of all existing configs by 1 (1→2, 2→3, ... 8→9)

- [x] Task 2: Modify `roomCountPerConfig` - switch key from ID to RoomFee and add 1元房 count
    - 2.1: Change map key type from `map[int64]int` (ID-based) to RoomFee-based: `{100: 10, 500: 10, 1000: 50, 2000: 40, 3000: 30, 5000: 30, 10000: 20, 20000: 10, 50000: 10}`
    - 2.2: Update `initRooms` function: change `roomCountPerConfig[cfg.ID]` to `roomCountPerConfig[cfg.RoomFee]`
