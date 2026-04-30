# 房间状态广播优化方案

## 一、背景

当前代码中，广播事件分散在多个文件中，每个事件传递的数据格式不统一，前端需要处理大量不同的事件类型，增量更新容易出现状态不一致。

## 二、解决方案

每次关键操作后，广播完整的 `room_state`，前端直接用这个状态更新 UI。

## 三、数据结构

### 3.1 RoomState

```go
type RoomState struct {
    RoomID         string
    RoomNo         string
    Status         int           // 0-空闲 1-等待 2-游戏中
    CurrentRound   int
    MaxRounds      int
    PlayerCount    int
    SpectatorCount int
    MaxPlayers     int
    MaxSpectators  int
    Players        []*PlayerInfo
    Spectators     []*SpectatorInfo
    Seats          []*SeatInfo
}
```

### 3.2 PlayerInfo（玩家信息）

玩家 = 已抢座并点击准备的用户

```go
type PlayerInfo struct {
    UserID      string
    Nickname    string
    Avatar      string
    SeatNo      int           // 座位号 1-5
    TotalGrab   int64         // 总抢包金额
    TotalSend   int64         // 总发包金额
    TotalProfit int64         // 总盈亏
    IsOnline    bool          // 是否在线
}
```

### 3.3 SpectatorInfo（观众信息）

观众 = 进入房间但未准备的用户（可能已选座）

```go
type SpectatorInfo struct {
    UserID         string
    Nickname       string
    Avatar         string
    SeatNo         int           // 座位号 0-未选座, 1-5 已选座
    SeatSelectedAt int64         // 选座时间戳（用于超时判断）
}
```

### 3.4 SeatInfo（座位状态）

**核心数据结构**，用于前端渲染座位图。

```go
type SeatInfo struct {
    SeatNo    int           // 座位号 1-5
    Occupied  bool          // 是否被占用
    UserID    string        // 占用者ID
    Nickname  string        // 占用者昵称
    Avatar   string        // 占用者头像
    Ready     bool          // 是否已准备
}
```

## 四、Seats 数据来源

Seats 数据直接从 Redis 中已有的 `players` 和 `spectators` 数据构建，无需额外查询：

### 4.1 Redis 数据结构

```
RoomMeta:
  - room_id
  - room_no
  - status
  - player_count
  - spectator_count
  - max_players
  - ...

players hash (key: room:players:{room_id}):
  - user_id -> {seat_no, nickname, avatar, status, ...}

spectators hash (key: room:spectators:{room_id}):
  - user_id -> {seat_no, nickname, avatar, seat_selected_at, ...}
```

### 4.2 构建逻辑

```
遍历 1-5 号座位:
  1. 先检查 players 中是否有该座位号
     - 有 → Occupied=true, Ready=true, 填入玩家信息
  2. 没有 → 检查 spectators 中是否有该座位号
     - 有 → Occupied=true, Ready=false, 填入观众信息
  3. 都没有 → Occupied=false, 空座位
```

### 4.3 示例数据

**场景：3个玩家已准备，1个观众已选座未准备，1个观众未选座**

```json
{
  "room_id": "12345",
  "room_no": "A001",
  "status": 1,
  "current_round": 0,
  "max_rounds": 10,
  "player_count": 3,
  "spectator_count": 2,
  "max_players": 5,
  "max_spectators": 100,
  "players": [
    {
      "user_id": "10001",
      "nickname": "张三",
      "avatar": "avatar1.jpg",
      "seat_no": 1,
      "total_grab": 0,
      "total_send": 0,
      "total_profit": 0,
      "is_online": true
    },
    {
      "user_id": "10002",
      "nickname": "李四",
      "avatar": "avatar2.jpg",
      "seat_no": 2,
      "total_grab": 0,
      "total_send": 0,
      "total_profit": 0,
      "is_online": true
    },
    {
      "user_id": "10003",
      "nickname": "王五",
      "avatar": "avatar3.jpg",
      "seat_no": 3,
      "total_grab": 0,
      "total_send": 0,
      "total_profit": 0,
      "is_online": true
    }
  ],
  "spectators": [
    {
      "user_id": "10004",
      "nickname": "赵六",
      "avatar": "avatar4.jpg",
      "seat_no": 4,
      "seat_selected_at": 1703000000
    },
    {
      "user_id": "10005",
      "nickname": "钱七",
      "avatar": "avatar5.jpg",
      "seat_no": 0,
      "seat_selected_at": 0
    }
  ],
  "seats": [
    {
      "seat_no": 1,
      "occupied": true,
      "user_id": "10001",
      "nickname": "张三",
      "avatar": "avatar1.jpg",
      "ready": true
    },
    {
      "seat_no": 2,
      "occupied": true,
      "user_id": "10002",
      "nickname": "李四",
      "avatar": "avatar2.jpg",
      "ready": true
    },
    {
      "seat_no": 3,
      "occupied": true,
      "user_id": "10003",
      "nickname": "王五",
      "avatar": "avatar3.jpg",
      "ready": true
    },
    {
      "seat_no": 4,
      "occupied": true,
      "user_id": "10004",
      "nickname": "赵六",
      "avatar": "avatar4.jpg",
      "ready": false
    },
    {
      "seat_no": 5,
      "occupied": false,
      "user_id": "",
      "nickname": "",
      "avatar": "",
      "ready": false
    }
  ]
}
```

## 五、广播时机

### 5.1 使用 room_state 事件

| 操作 | 说明 |
|------|------|
| 加入房间 | 新用户加入后广播 |
| 选座 | 选座成功后广播 |
| 取消选座 | 取消成功后广播 |
| 准备 | 准备成功后广播 |
| 离开房间 | 离开后广播 |
| 被踢出 | 踢出后广播 |
| 断线重连 | 断线后广播 |
| 重连 | 重连后广播 |

### 5.2 保持原有事件（游戏流程特殊事件）

| 事件名 | 说明 |
|--------|------|
| `countdown` | 倒计时（每秒广播） |
| `game_start` | 游戏开始（包含红包信息） |
| `round_start` | 回合开始 |
| `round_settled` | 回合结算 |
| `round_cancelled` | 回合取消 |
| `game_ended` | 游戏结束 |
| `packet_sent` | 发红包 |
| `grab_result` | 抢红包结果 |
| `auto_grab` | 自动抢红包 |

## 六、前端处理

### 6.1 綈息处理

```javascript
case 'room_state':
    updateRoomUI(data.data);
    break;

function updateRoomUI(roomState) {
    // 更新房间信息
    document.getElementById('roomNo').textContent = roomState.room_no;
    document.getElementById('playerCount').textContent = `${roomState.player_count}/${roomState.max_players}`;
    document.getElementById('spectatorCount').textContent = `${roomState.spectator_count}/${roomState.max_spectators}`;
    
    // 渲染座位
    renderSeats(roomState.seats);
    
    // 渲染观众列表
    renderSpectators(roomState.spectators);
    
    // 更新按钮状态
    updateButtons(roomState);
}
```

### 6.2 座位渲染

```javascript
function renderSeats(seats) {
    const seatsDiv = document.getElementById('seats');
    let html = '';
    
    seats.forEach(seat => {
        let classes = 'seat';
        if (seat.occupied) classes += ' occupied';
        if (seat.ready) classes += ' ready';
        if (seat.user_id === myUserId) classes += ' me';
        
        html += `
            <div class="${classes}" onclick="selectSeat(${seat.seat_no})">
                <div class="seat-no">座位${seat.seat_no}</div>
                <div class="player-name">${seat.occupied ? seat.nickname : '空闲'}</div>
                ${seat.ready ? '<div style="font-size:10px;color:#4caf50;">已准备</div>' : ''}
            </div>
        `;
    });
    
    seatsDiv.innerHTML = html;
}
```

## 七、代码改动清单

### 7.1 新增文件
无

### 7.2 修改文件

| 文件 | 改动内容 |
|------|----------|
| `room_state.go` | 扩展结构体，新增 `BuildFullRoomState` 函数 |
| `seat_app_service.go` | 修改广播逻辑，使用 `room_state` 事件 |
| `room_app_service.go` | 修改广播逻辑，使用 `room_state` 事件 |
| `test-ws.html` | 简化事件处理逻辑 |

## 八、优点

1. **前端逻辑简化**：只需处理一个 `room_state` 事件
2. **状态一致性**：每次都是完整状态，不会出现增量更新导致的不一致
3. **易于调试**：可以清楚看到每次操作后的完整房间状态
4. **扩展性好**：新增字段只需修改一处

## 九、数据量估算

| 场景 | 数据量 |
|------|--------|
| 5玩家 + 95观众 | 约 2-3 KB |
| 5玩家 + 0观众 | 约 0.5 KB |
| 0玩家 + 5观众 | 约 0.5 KB |

房间操作频率不高（秒级），数据量可接受。
