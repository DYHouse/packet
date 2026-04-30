# WebSocket 通信协议文档

## 一、连接说明

- **WebSocket 地址**: `ws://{host}:8080/ws?token={jwt_token}`
- **认证方式**: 连接时通过 URL 参数传递 `token`(JWT 令牌),服务端自动完成认证
- **消息格式**: JSON
- **字符编码**: UTF-8

**连接 URL 示例:**
```
ws://localhost:8080/ws?token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| token | string | 是 | JWT 令牌,由 `/test/token` 或 `/game/start` 接口返回 |

**连接流程:**
1. 客户端通过 HTTP 接口获取 JWT Token
2. 使用 Token 建立 WebSocket 连接: `ws://host:8080/ws?token={token}`
3. 服务端自动验证 Token 并完成认证
4. 认证成功后,客户端可以发送游戏命令

---

## 二、认证流程

### 2.1 获取 Token

**方式一:测试接口(开发测试用)**

```http
POST /test/token
Content-Type: application/json

{
    "user_id": "10001",
    "nickname": "TestPlayer",
    "avatar": ""
}
```

**响应:**
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "user_id": "10001",
        "nickname": "TestPlayer"
    }
}
```

**方式二:游戏启动接口(生产环境)**

```http
POST /game/start?mid={merchant_id}&ts={timestamp}&sign={signature}
Content-Type: application/json

{
    "game_id": 1,
    "game_code": "redpacket",
    "user_id": "test_user_001",
    "username": "TestPlayer",
    "avatar": "",
    "currency": "BRL",
    "lang": "en",
    "client_ip": "127.0.0.1",
    "version": "1.0"
}
```

**响应:**
```json
{
    "code": 0,
    "data": {
        "user_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "game_url": "http://example.com/redpacket?token=...&userId=...",
        "game_config": "{...}"
    }
}
```

### 2.2 JWT Token 结构

Token 采用 HMAC-SHA256 签名的 JWT 格式,包含以下 Claims:

```json
{
    "sid": "a901b8f3-a41c-44f2-9c55-13ddeb43865c",
    "uid": "1776143722951007605",
    "puid": "10001",
    "nick": "TestPlayer",
    "avatar": "",
    "iss": "gateway-service",
    "exp": 1776150923,
    "nbf": 1776143723,
    "iat": 1776143723
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| sid | string | 会话 ID(UUID) |
| uid | string | 内部用户 ID(系统生成) |
| puid | string | 平台用户 ID(原始用户 ID) |
| nick | string | 用户昵称 |
| avatar | string | 用户头像 URL |
| iss | string | 签发者(gateway-service) |
| exp | int64 | 过期时间(Unix 时间戳) |
| nbf | int64 | 生效时间(Unix 时间戳) |
| iat | int64 | 签发时间(Unix 时间戳) |

### 2.3 认证机制说明

1. **URL 参数认证**: WebSocket 连接时通过 URL 参数传递 Token
2. **Token 有效期**: 默认 2 小时,可在配置中调整
3. **连接保持**: Token 过期不影响已建立的连接,断线重连需重新获取 Token
4. **多地登录**: 同一账号在新设备登录时,旧连接会被踢出
5. **安全建议**: 生产环境使用 `wss://` 加密连接

**服务端内部处理流程:**

```
客户端连接: ws://host:8080/ws?token={jwt_token}
    ↓
Gateway 提取 URL 参数中的 token
    ↓
认证中间件验证 token
    ↓
认证成功,连接建立完成
    ↓
客户端可以开始发送游戏命令
```

这样设计的好处:
- 简化客户端实现,无需处理认证逻辑
- 减少一次网络往返,提高连接效率
- 统一认证流程,便于维护

---

## 三、消息基本结构

### 3.1 客户端请求消息格式

```json
{
    "cmd": "命令类型",
    "request_id": "请求ID",
    "data": { /* 请求数据 */ },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| cmd | string | 是 | 命令类型,见命令列表 |
| request_id | string | 否 | 请求ID,服务端响应时会原样返回 |
| data | object | 否 | 请求数据,具体结构见各命令说明 |
| timestamp | int64 | 否 | 客户端时间戳(毫秒) |

### 3.2 服务端响应消息格式

```json
{
    "cmd": "命令类型",
    "request_id": "请求ID(与请求一致)",
    "code": 0,
    "msg": "成功",
    "data": { /* 响应数据 */ },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| cmd | string | 命令类型,与请求一致 |
| request_id | string | 请求ID,与请求一致 |
| code | int | 状态码,0 表示成功,其他见错误码表 |
| msg | string | 状态消息 |
| data | object | 响应数据,具体结构见各命令说明 |
| timestamp | int64 | 服务端时间戳(毫秒) |

### 3.3 服务端推送消息格式

```json
{
    "type": "推送类型",
    "data": { /* 推送数据 */ },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| type | string | 推送类型,见推送列表 |
| data | object | 推送数据,具体结构见各推送说明 |
| timestamp | int64 | 服务端时间戳(毫秒) |

---

## 四、命令列表(客户端 → 服务端)

### 4.2 ping - 心跳

**请求:**
```json
{
    "cmd": "ping"
}
```

**响应:**
```json
{
    "cmd": "pong",
    "code": 0,
    "msg": "成功",
    "data": {
        "server_time": 1234567890
    }
}
```

---

### 4.3 join_room - 加入指定房间

**请求:**
```json
{
    "cmd": "join_room",
    "data": {
        "room_id": "123456789"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 指定房间ID |

**响应:**
```json
{
    "cmd": "join_room",
    "code": 0,
    "msg": "成功",
    "data": {
        "room_id": "123456789",
        "room_no": "R00010001",
        "room_state": { /* RoomState 结构 */ },
        "is_spectator": false
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| room_no | string | 房间编号(显示用) |
| room_state | RoomState | 房间状态,见 [RoomState 结构](#61-roomstate-结构) |
| is_spectator | bool | 是否为观众 |

---

### 4.4 auto_match - 自动匹配房间

**请求:**
```json
{
    "cmd": "auto_match",
    "data": {}
}
```

**响应:**
```json
{
    "cmd": "auto_match",
    "code": 0,
    "msg": "成功",
    "data": {
        "room_id": "123456789",
        "room_no": "R00010001",
        "room_state": { /* RoomState 结构 */ },
        "is_spectator": false
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| room_no | string | 房间编号(显示用) |
| room_state | RoomState | 房间状态 |
| is_spectator | bool | 是否为观众 |

---

### 4.5 leave_room - 离开房间

**请求:**
```json
{
    "cmd": "leave_room",
    "data": {
        "room_id": "123456789",
        "reason": "user_request"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |
| reason | string | 否 | 离开原因 |

**响应:**

成功离开:
```json
{
    "cmd": "leave_room",
    "code": 0,
    "msg": "成功",
    "data": null
}
```

---

### 4.6 room_state - 获取房间状态

**请求:**
```json
{
    "cmd": "room_state",
    "data": {
        "room_id": "123456789"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |

**响应:**
```json
{
    "cmd": "room_state",
    "code": 0,
    "msg": "成功",
    "data": {
        "room_state": { /* RoomState 结构 */ }
    }
}
```

---

### 4.8 select_seat - 选择座位

**请求:**
```json
{
    "cmd": "select_seat",
    "data": {
        "room_id": "123456789",
        "seat_no": 1
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |
| seat_no | int | 是 | 座位号(1-5) |

**响应:**
```json
{
    "cmd": "select_seat",
    "code": 0,
    "msg": "成功",
    "data": {
        "seat_no": 1,
        "room_state": { /* RoomState 结构 */ }
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| seat_no | int | 座位号 |
| room_state | RoomState | 房间状态 |

---

### 4.8 cancel_seat - 取消座位

**请求:**
```json
{
    "cmd": "cancel_seat",
    "data": {
        "room_id": "123456789"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |

**响应:**
```json
{
    "cmd": "cancel_seat",
    "code": 0,
    "msg": "成功",
    "data": {
        "room_state": { /* RoomState 结构 */ }
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_state | RoomState | 房间状态 |

---

### 4.9 player_ready - 玩家准备

**请求:**
```json
{
    "cmd": "player_ready",
    "data": {
        "room_id": "123456789"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |

**响应:**
```json
{
    "cmd": "player_ready",
    "code": 0,
    "msg": "成功",
    "data": {
        "room_state": { /* RoomState 结构 */ }
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_state | RoomState | 房间状态 |

---

### 4.10 send_packet - 发红包

**请求:**
```json
{
    "cmd": "send_packet",
    "data": {
        "room_id": "123456789"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |

**响应:**
```json
{
    "cmd": "send_packet",
    "code": 0,
    "msg": "成功",
    "data": {
        "packet_count": 5
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| packet_count | int | 红包数量 |

---

### 4.11 grab_packet - 抢红包

**请求:**
```json
{
    "cmd": "grab_packet",
    "data": {
        "room_id": "123456789",
        "packet_id": "packet_001"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |
| packet_id | string | 是 | 红包ID |

**响应:**
```json
{
    "cmd": "grab_packet",
    "code": 0,
    "msg": "成功",
    "data": {
        "packet_id": "packet_001",
        "amount": 95,
        "position": 1,
        "is_last": false
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| packet_id | string | 红包ID |
| amount | int64 | 抢到的金额(分) |
| position | int32 | 红包位置 |
| is_last | bool | 是否是最后一个红包 |

---

### 4.12 get_room_list - 获取房间列表

**请求:**
```json
{
    "cmd": "get_room_list",
    "data": {
        "room_type": 1,
        "status": 0,
        "page": 1,
        "page_size": 20
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_type | int | 否 | 房间类型:1/2/5/10 |
| status | int | 否 | 房间状态:0=全部,1=等待,2=游戏中,3=已结束 |
| page | int | 否 | 页码,默认1 |
| page_size | int | 否 | 每页数量,默认20,最大100 |

**响应:**
```json
{
    "cmd": "get_room_list",
    "code": 0,
    "msg": "成功",
    "data": {
        "list": [
            {
                "room_id": "123456789",
                "room_no": "R00010001",
                "room_fee": 100,
                "max_players": 5,
                "max_rounds": 10,
                "max_spectators": 100,
                "current_round": 1,
                "player_count": 3,
                "spectator_count": 2,
                "status": 1
            }
        ],
        "total": 100,
        "page": 1,
        "page_size": 20
    }
}
```

---

### 4.13 get_room_type_list - 获取房间类型列表

**请求:**
```json
{
    "cmd": "get_room_type_list",
    "data": {}
}
```

**响应:**
```json
{
    "cmd": "get_room_type_list",
    "code": 0,
    "msg": "成功",
    "data": {
        "list": [
            {
                "id": 1,
                "name": "1元房",
                "room_fee": 100,
                "max_rounds": 10,
                "total_people": 5
            }
        ]
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| id | int | 房间类型ID |
| name | string | 房间类型名称 |
| room_fee | int64 | 房费(分) |
| max_rounds | int | 最大回合数 |
| total_people | int | 在线人数 |

---

### 4.14 reconnect - 断线重连

**请求:**
```json
{
    "cmd": "reconnect",
    "data": {
        "room_id": "123456789"
    }
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| room_id | string | 是 | 房间ID |

**响应:**
```json
{
    "cmd": "reconnect",
    "code": 0,
    "msg": "成功",
    "data": {
        "room_id": "123456789",
        "room_state": { /* RoomState 结构 */ }
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| room_state | RoomState | 房间状态 |

---

### 4.15 get_user_balance - 获取用户余额

**请求:**
```json
{
    "cmd": "get_user_balance",
    "data": {}
}
```

**响应:**
```json
{
    "cmd": "get_user_balance",
    "code": 0,
    "msg": "成功",
    "data": {
        "balance": 10000
    }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| balance | int64 | 用户余额(分) |

---

## 五、推送列表(服务端 → 客户端)

### 5.1 room_state - 房间状态更新

```json
{
    "type": "room_state",
    "data": { /* RoomState 结构 */ },
    "timestamp": 1234567890
}
```

**触发场景:** 玩家加入/离开、选座/取消选座、准备、游戏开始等

---

### 5.2 player_disconnected - 玩家断线

```json
{
    "type": "player_disconnected",
    "data": {
        "user_id": "123",
        "nickname": "玩家1",
        "seat_no": 1
    },
    "timestamp": 1234567890
}
```

---

### 5.3 player_reconnected - 玩家重连

```json
{
    "type": "player_reconnected",
    "data": {
        "user_id": "123",
        "nickname": "玩家1",
        "seat_no": 1
    },
    "timestamp": 1234567890
}
```

---

### 5.4 game_start - 游戏开始

```json
{
    "type": "game_start",
    "data": {
        "room_id": "123456789",
        "current_round": 1,
        "max_rounds": 10
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| current_round | int32 | 当前回合(开始时为1) |
| max_rounds | int32 | 最大回合数 |

---

### 5.5 game_resumed - 游戏恢复

```json
{
    "type": "game_resumed",
    "data": {
        "room_id": "123456789",
        "current_round": 3,
        "next_sender_id": "456",
        "message": "游戏已恢复,系统发送红包"
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| current_round | int32 | 当前回合 |
| next_sender_id | string | 下一回合发红包者ID |
| message | string | 恢复消息 |

**触发场景:** 断线玩家超时后游戏恢复

---

### 5.6 countdown_start - 倒计时开始

```json
{
    "type": "countdown_start",
    "data": {
        "room_id": "123456789",
        "countdown": 3
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| countdown | int32 | 倒计时秒数 |

**触发场景:** 所有玩家准备后,游戏开始前倒计时

---

### 5.7 round_start - 回合开始

```json
{
    "type": "round_start",
    "data": {
        "room_id": "123456789",
        "round_id": "123456789_1",
        "current_round": 1,
        "sender_id": "0",
        "sender_nickname": "系统",
        "sender_type": "system",
        "total_amount": 475,
        "commission": 25,
        "actual_amount": 450,
        "packet_count": 5,
        "grab_timeout": 10,
        "packets": [
            {
                "packet_id": "packet_001",
                "position": 1
            },
            {
                "packet_id": "packet_002",
                "position": 2
            }
        ]
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| round_id | string | 回合ID |
| current_round | int32 | 当前回合数 |
| sender_id | string | 发红包者ID,"0"表示系统 |
| sender_nickname | string | 发红包者昵称 |
| sender_type | string | 发红包者类型,见下表 |
| total_amount | int64 | 红包总金额(分) |
| commission | int64 | 手续费(分) |
| actual_amount | int64 | 实际金额(分) |
| packet_count | int32 | 红包数量 |
| grab_timeout | int32 | 抢红包超时时间(秒) |
| packets | PacketInfo[] | 红包列表 |

**sender_type 类型说明:**

| 值 | 说明 |
|------|------|
| system | 系统发红包(首回合) |
| player | 玩家发红包 |
| system_forced | 系统强制发红包(玩家超时惩罚后) |
| system_resume | 系统恢复发红包(游戏中断恢复) |

**PacketInfo 结构:**

| 字段 | 类型 | 说明 |
|------|------|------|
| packet_id | string | 红包ID |
| position | int32 | 红包位置 |

---

### 5.8 packet_grabbed - 红包被抢

```json
{
    "type": "packet_grabbed",
    "data": {
        "room_id": "123456789",
        "round_id": "123456789_1",
        "packet_id": "packet_001",
        "position": 1,
        "user_id": "123",
        "nickname": "玩家1",
        "amount": 95,
        "is_last": false
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| round_id | string | 回合ID |
| packet_id | string | 红包ID |
| position | int32 | 红包位置 |
| user_id | string | 抢到红包的玩家ID |
| nickname | string | 玩家昵称 |
| amount | int64 | 抢到的金额(分) |
| is_last | bool | 是否是最后一个红包 |

---

### 5.9 round_end - 回合结束

```json
{
    "type": "round_end",
    "data": {
        "room_id": "123456789",
        "round_id": "123456789_1",
        "current_round": 1,
        "sender_id": "0",
        "total_amount": 475,
        "commission": 25,
        "results": [
            {
                "user_id": "123",
                "nickname": "玩家1",
                "avatar": "https://...",
                "amount": 95,
                "position": 1,
                "packet_id": "packet_001",
                "is_auto_assigned": false
            }
        ],
        "min_amount_player": "456",
        "next_sender_id": "456",
        "is_game_end": false
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| round_id | string | 回合ID |
| current_round | int32 | 当前回合数 |
| sender_id | string | 发红包者ID |
| total_amount | int64 | 红包总金额(分) |
| commission | int64 | 手续费(分) |
| results | RoundResult[] | 本回合结果列表,按红包位置排序 |
| min_amount_player | string | 本回合抢到最小金额的玩家ID,豹子奖励时为"0" |
| next_sender_id | string | 下一回合发红包者ID,"0"表示系统发红包(豹子奖励) |
| is_game_end | bool | 是否游戏结束 |

**RoundResult 结构:**

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | string | 玩家ID |
| nickname | string | 昵称 |
| avatar | string | 头像URL |
| amount | int64 | 抢到的金额(分) |
| position | int32 | 红包位置 |
| packet_id | string | 红包ID |
| is_auto_assigned | bool | 是否自动分配 |

**特殊场景说明:**

当本回合开出豹子奖励(所有红包金额相同)时:
- `min_amount_player` 返回 "0"
- `next_sender_id` 返回 "0",表示下一回合由系统发红包
- 客户端应显示"豹子奖励,系统发红包"提示,不显示"确认发红包"按钮

---

### 5.10 auto_distribute - 自动分配

```json
{
    "type": "auto_distribute",
    "data": {
        "room_id": "123456789",
        "round_id": "123456789_1",
        "distributed_count": 2,
        "results": [
            {
                "user_id": "123",
                "amount": 95,
                "position": 1
            }
        ]
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| round_id | string | 回合ID |
| distributed_count | int32 | 自动分配数量 |
| results | DistributeResult[] | 分配结果列表 |

**DistributeResult 结构:**

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | string | 玩家ID |
| amount | int64 | 分配金额(分) |
| position | int32 | 红包位置 |

**触发场景:** 抢红包超时后,未抢的红包自动分配

---

### 5.11 penalty - 惩罚通知

```json
{
    "type": "penalty",
    "data": {
        "room_id": "123456789",
        "user_id": "123",
        "penalty_type": "send_timeout",
        "penalty_amount": 100,
        "penalty_count": 1,
        "kick_required": false,
        "reason": "send_timeout"
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| user_id | string | 被惩罚的用户ID |
| penalty_type | string | 惩罚类型:send_timeout/leave_during_game/disconnect_timeout |
| penalty_amount | int64 | 惩罚金额(分) |
| penalty_count | int | 累计惩罚次数 |
| kick_required | bool | 是否需要踢出房间(累计两次惩罚) |
| reason | string | 惩罚原因 |

---

### 5.12 reward - 奖励通知

```json
{
    "type": "reward",
    "data": {
        "reward_type": 1,
        "amount": 100
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| reward_type | int | 奖励类型 |
| amount | int64 | 奖励金额(分) |

---

### 5.13 wait_replacement - 等待补位

```json
{
    "type": "wait_replacement",
    "data": {
        "room_id": "123456789",
        "vacant_seat": 1,
        "left_user_id": "123",
        "wait_time": 30
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| vacant_seat | int32 | 空缺座位号 |
| left_user_id | string | 离开的玩家ID |
| wait_time | int32 | 等待补位时间(秒) |

**触发场景:** 游戏进行中有玩家离开,等待观众补位

---

### 5.14 game_interrupted - 游戏中断

```json
{
    "type": "game_interrupted",
    "data": {
        "room_id": "123456789",
        "reason": "replacement_timeout",
        "penalty_share": 50
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| reason | string | 中断原因:normal/first_round_deduct_failed/later_round_deduct_failed/partial_deduct_failed/replacement_timeout/system_error |
| penalty_share | int64 | 惩罚分摊金额(分) |

**触发场景:** 游戏进行中玩家断线超时导致游戏无法继续

---

### 5.15 game_end - 游戏结束

```json
{
    "type": "game_end",
    "data": {
        "room_id": "123456789",
        "total_rounds": 10,
        "final_results": [
            {
                "user_id": "123",
                "nickname": "玩家1",
                "total_profit": 500,
                "rank": 1
            }
        ]
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| total_rounds | int32 | 总回合数 |
| final_results | GameResult[] | 最终结果列表 |

**GameResult 结构:**

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | string | 玩家ID |
| nickname | string | 昵称 |
| total_profit | int64 | 总收益(分) |
| rank | int32 | 排名 |

---

### 5.16 kicked - 被踢出房间

```json
{
    "type": "kicked",
    "data": {
        "room_id": "123456789",
        "user_id": "123",
        "reason": "disconnect_timeout",
        "message": "断线超时,已被移出房间"
    },
    "timestamp": 1234567890
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| user_id | string | 被踢出的用户ID |
| reason | string | 踢出原因:seat_timeout/ready_timeout/disconnect_timeout/system_kick/user_request/player_leave/login_elsewhere |
| message | string | 踢出消息 |

---

### 5.17 error - 错误推送

```json
{
    "type": "error",
    "data": {
        "code": 5000,
        "message": "系统错误"
    },
    "timestamp": 1234567890
}
```

---

## 六、数据结构

### 6.1 RoomState 结构

```json
{
    "room_id": "123456789",
    "room_no": "R00010001",
    "room_type": 1,
    "room_fee": 100,
    "status": 1,
    "current_round": 0,
    "max_rounds": 10,
    "player_count": 3,
    "spectator_count": 2,
    "max_players": 5,
    "max_spectators": 100,
    "players": [ /* PlayerInfo 结构 */ ],
    "spectators": [ /* SpectatorInfo 结构 */ ],
    "seats": [ /* SeatInfo 结构 */ ]
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| room_id | string | 房间ID |
| room_no | string | 房间编号 |
| room_type | int32 | 房间类型:1/2/5/10 |
| room_fee | int64 | 房费(分) |
| status | int32 | 房间状态:1=等待,2=游戏中,4=中断 |
| current_round | int32 | 当前回合数 |
| max_rounds | int32 | 最大回合数 |
| player_count | int32 | 玩家数量 |
| spectator_count | int32 | 观众数量 |
| max_players | int32 | 最大玩家数(固定5) |
| max_spectators | int32 | 最大观众数 |
| players | PlayerInfo[] | 玩家列表 |
| spectators | SpectatorInfo[] | 观众列表 |
| seats | SeatInfo[] | 座位列表 |

### 6.2 PlayerInfo 结构

```json
{
    "user_id": "123",
    "nickname": "玩家1",
    "avatar": "https://...",
    "seat_no": 1,
    "is_online": true
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | string | 玩家ID |
| nickname | string | 昵称 |
| avatar | string | 头像URL |
| seat_no | int32 | 座位号 |
| is_online | bool | 是否在线 |

### 6.3 SpectatorInfo 结构

```json
{
    "user_id": "123",
    "nickname": "观众1",
    "avatar": "https://...",
    "seat_no": 0,
    "seat_selected_at": 0
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| user_id | string | 用户ID |
| nickname | string | 昵称 |
| avatar | string | 头像URL |
| seat_no | int32 | 座位号(观众为0) |
| seat_selected_at | int64 | 选座时间戳(观众为0) |

### 6.4 SeatInfo 结构

```json
{
    "seat_no": 1,
    "occupied": true,
    "user_id": "123",
    "nickname": "玩家1",
    "avatar": "https://...",
    "ready": true
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| seat_no | int32 | 座位号(1-5) |
| occupied | bool | 是否被占用 |
| user_id | string | 占用玩家ID |
| nickname | string | 占用玩家昵称 |
| avatar | string | 占用玩家头像 |
| ready | bool | 是否已准备 |

---

## 七、错误码表

### 7.1 通用错误码 (0-999)

| 错误码 | 说明 |
|--------|------|
| 0 | 成功 |
| 400 | 参数错误 |
| 401 | 未授权 |
| 403 | 禁止访问 |
| 404 | 资源不存在 |
| 500 | 内部服务器错误 |

### 7.2 房间相关错误码 (1000-1999)

| 错误码 | 说明 |
|--------|------|
| 1001 | 无效的房间类型 |
| 1002 | 用户已在房间中 |
| 1003 | 余额不足 |
| 1004 | 房间已满 |
| 1005 | 房间不存在 |
| 1006 | 房间不在等待状态 |
| 1007 | 同IP玩家数量超限 |
| 1008 | 同设备玩家数量超限 |
| 1009 | 用户不存在 |
| 1011 | 用户账号已禁用 |
| 1012 | 用户账号已冻结 |
| 1013 | 系统繁忙 |
| 1014 | 操作过于频繁 |
| 1015 | 无空闲房间 |
| 1016 | 达到每日房间限制 |
| 1017 | 操作进行中 |
| 1018 | 游戏进行中 |
| 1019 | 用户被列入黑名单 |
| 1021 | 离开惩罚已应用 |
| 1022 | 玩家已准备 |
| 1023 | 玩家无法离开房间 |
| 1024 | 只有玩家才能操作 |

### 7.3 连接相关错误码 (2000-2999)

| 错误码 | 说明 |
|--------|------|
| 2001 | 无效的消息格式 |
| 2002 | 缺少命令 |
| 2003 | 未知的命令 |
| 2004 | 连接数超限 |
| 2005 | 认证失败 |
| 2006 | 用户已连接 |
| 2007 | 用户不在房间中 |
| 2008 | 请求频率超限 |

### 7.4 游戏相关错误码 (3000-3999)

| 错误码 | 说明 |
|--------|------|
| 3001 | 红包不存在 |
| 3002 | 红包已被抢过 |
| 3003 | 未轮到你发红包 |
| 3004 | 游戏未开始 |
| 3005 | 游戏已结束 |
| 3006 | 无效的游戏状态 |
| 3007 | 抢红包超时 |
| 3008 | 发红包超时 |
| 3009 | 无效的座位号 |
| 3010 | 座位已被占用 |
| 3011 | 玩家不在房间中 |
| 3013 | 已经是玩家 |
| 3014 | 已经选座 |
| 3015 | 未选座 |
| 3016 | 需要先选座 |
| 3017 | 红包已创建 |
| 3018 | 已抢过红包 |
| 3019 | 没有可抢的红包 |
| 3020 | 玩家未离线 |
| 3021 | 不在抢红包阶段 |
| 3022 | 回合不存在 |
| 3023 | 无效的回合编号 |
| 3024 | 没有玩家 |
| 3025 | 红包已存在 |
| 3026 | 惩罚已应用 |
| 3027 | 玩家已被踢出 |
| 3028 | 补位失败 |
| 3029 | 不是所有玩家都已准备 |
| 3030 | 重连已过期 |
| 3031 | 游戏已恢复 |
| 3032 | 玩家已发送红包 |

### 7.5 系统错误码 (5000-5999)

| 错误码 | 说明 |
|--------|------|
| 5000 | 系统错误 |
| 5001 | Redis操作失败 |
| 5002 | 数据库操作失败 |
| 5003 | 消息队列错误 |
| 5004 | 平台API错误 |
| 5005 | 获取锁失败 |

---

## 八、HTTP 接口

### 8.1 测试接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `/test/token` | POST | 生成测试 Token(开发测试用) |
| `/health` | GET | 健康检查 |
| `/ready` | GET | 就绪检查 |
| `/live` | GET | 存活检查 |

### 8.2 游戏接口

| 接口 | 方法 | 说明 |
|------|------|------|
| `/game/list` | GET | 获取游戏列表 |
| `/game/start` | POST | 启动游戏(需要签名验证) |

### 8.3 签名验证

游戏接口需要通过签名验证,签名算法:

```
signStr = requestBody + {"mid":"merchant_id","ts":"timestamp"}
sign = HMAC-SHA256(signStr, merchant_secret)
```

请求 URL 格式:
```
/game/start?mid={merchant_id}&ts={timestamp}&sign={signature}
```

---

## 九、连接管理机制

### 9.1 多地登录互踢

当同一用户在新的设备或浏览器建立连接时:
1. 新连接认证成功
2. 系统检测到该用户已有连接
3. 旧连接收到 `kicked` 推送,原因为 `login_elsewhere`
4. 旧连接被关闭

### 9.2 断线重连

用户断线后:
1. 连接状态标记为 `disconnected`
2. 如果用户在房间中,触发 `player_disconnected` 推送
3. 用户在超时时间内(默认30秒)可以重连
4. 重连成功后触发 `player_reconnected` 推送
5. 超时未重连,用户被踢出房间

### 9.3 心跳机制

- 客户端应定期发送 `ping` 命令(建议每30秒)
- 服务端响应 `pong` 并返回服务器时间
- 超过2分钟未收到心跳,连接将被认为超时

### 9.4 连接限制

- 单节点最大连接数:10000(可配置)
- 单IP认证失败次数限制:5次/15分钟
- 超过失败次数限制后,IP将被锁定15分钟

---

## 十、限流机制

### 10.1 抢红包限流

为防止恶意刷抢,抢红包操作有频率限制:
- 每个用户抢红包频率限制
- 超过限制返回错误码 2008(请求频率超限)

### 10.2 全局限流

- 单用户命令请求频率限制
- 防止恶意刷接口

---

## 十一、架构说明

### 11.1 Gateway 服务

- 负责 WebSocket 连接管理
- 认证和授权
- 消息路由和转发
- 连接状态管理
- 广播消息分发

### 11.2 Game 服务

- 游戏业务逻辑处理
- 房间管理
- 游戏状态机
- 通过 gRPC 与 Gateway 通信

### 11.3 消息广播

- 使用 Kafka 消息队列
- 支持房间广播和用户广播
- 跨节点消息同步

---

## 十二、最佳实践

### 12.1 客户端实现建议

1. **认证流程**: 
   - 先通过 HTTP 接口获取 Token
   - 使用 Token 建立 WebSocket 连接: `ws://host:8080/ws?token={token}`
2. **断线重连**: 实现自动重连机制,包括:
   - 检测连接断开
   - 重新获取 Token
   - 重新建立连接
   - 发送 reconnect 命令恢复游戏状态
3. **错误处理**: 根据错误码进行相应的UI提示和重试

### 12.2 安全建议

1. 生产环境必须使用 WSS 加密连接
2. Token 应妥善保管,不要泄露
3. 实现客户端签名验证
4. 敏感操作需要二次确认

---

## 十三、特殊游戏机制

### 13.1 豹子奖励机制

当本回合所有玩家抢到的红包金额相同时,触发豹子奖励:

**触发条件:**
- 所有红包金额完全相同(如: 5个红包都是100分)

**奖励内容:**
- 每位玩家获得红包总额10倍的奖励金额
- 奖励金额由平台发放,不影响玩家余额

**下一回合处理:**
- 由于没有"最小金额获得者",下一回合由系统发红包
- `round_end` 推送中 `next_sender_id` 为 "0"
- 客户端应显示"豹子奖励,系统发红包"提示
- 系统发红包金额为房费,从平台账户扣除

**消息流程:**
```
1. round_start (sender_type: "player")
2. packet_grabbed (多个)
3. round_end (min_amount_player: "0", next_sender_id: "0")
4. reward (reward_type: 2, 豹子奖励)
5. round_start (sender_type: "system", 3秒后系统发红包)
```

**客户端处理建议:**
```javascript
// 处理 round_end 推送
if (pushData.next_sender_id === "0") {
    // 显示豹子奖励提示
    showToast("🎉 豹子奖励！下轮由系统发红包");
    // 不显示"确认发红包"按钮
    hideConfirmSendButton();
}
```

---
