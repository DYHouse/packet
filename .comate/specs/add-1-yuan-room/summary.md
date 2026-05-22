# Add 1-Yuan Room - Summary

## Changes Made
Modified `backend/scripts/init_rooms.go` with 3 changes:

1. **Added "Sala de 1" room config** (line 15): `{Name: "Sala de 1", RoomFee: 100, MaxPlayers: 5, MaxRounds: 10, SortOrder: 1, Status: 1}` at the beginning of `roomConfigs` slice, and incremented all existing SortOrder values by 1 (2-9).

2. **Switched `roomCountPerConfig` key from ID to RoomFee** (lines 26-36): Since database auto-increment IDs are unpredictable, using RoomFee (100, 500, 1000, ...) as the map key is more robust. Added `100: 10` for the 1-yuan room.

3. **Updated `initRooms` reference** (line 123): Changed `roomCountPerConfig[cfg.ID]` to `roomCountPerConfig[cfg.RoomFee]` to match the new map key.

## Result
- Running the init script will create 10 rooms for the "Sala de 1" (1-yuan) config
- Existing configs' SortOrder will be updated to 2-9 via the upsert logic in `initRoomConfigs`
- The RoomFee-based key makes the room count mapping independent of database ID assignment
