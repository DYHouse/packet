# 修复同用户重复登录未踢掉旧连接 Spec

## Why

当前同一用户第二次登录时，如果该用户仍在房间中（`PlayerRoomKey` 存在），会走 `handleReconnect` 路径。该路径先调用 `CleanupOldConnection` 删除 Redis 中的 `GatewayConnKey`，导致后续 `Register` 里的 Lua 脚本 `HGETALL` 返回空，`needKick` 始终为 false。踢人基础设施（`kickExistingConnection` / `kickLocalConnection` / `publishKickNotification`）虽然存在但从未被触发。

后果：
- 同节点：旧连接被 `CleanupOldConnection` 直接 `Close()`，但不发送 `kicked` 推送，客户端不知道自己被踢，可能自动重连导致两个连接反复互踢（thrashing）
- 跨节点：旧连接根本不会被关闭，两个连接并存，用户可在两台设备同时操作同一账号

用户需求很简单：**同一用户重复登录，必须踢掉上一个连接（让旧端无法继续操作）；如果玩家在房间中，新连接需要恢复房间状态**。踢人和恢复状态是两个独立的事，不应互相干扰。

## What Changes

### 核心修改：统一登录路径，踢人与恢复状态解耦

将 `handleConnection` 中的两条分支（在房间 / 不在房间）合并为一条：

**当前（有问题）**：
```
handleConnection:
  auth 验证
  roomID = GetPlayerRoom(userID)
  if roomID != "":
    handleReconnect:                    # 重连路径
      CleanupOldConnection(userID)      # 先删 GatewayConnKey,破坏踢人检测
      Register(conn)                    # Lua 检测不到旧连接,needKick=false
      发送 reconnect 命令恢复状态
  else:
    Register(conn)                      # 正常路径,踢人正常工作
```

**修复后**：
```
handleConnection:
  auth 验证
  Register(conn)                        # 统一入口,Lua 脚本检测旧连接并踢掉
  roomID = GetPlayerRoom(userID)
  if roomID != "":
    发送 reconnect 命令恢复状态          # 只恢复状态,不做任何清理
```

### 具体改动

1. **`gateway/server/server.go` — `handleConnection`**：移除 if/else 分支，统一先 `Register`，再判断 `roomID` 决定是否恢复状态
2. **`gateway/server/server.go` — `handleReconnect`**：移除 `CleanupOldConnection` 和 `Register` 调用，只保留发送 `reconnect` 命令的逻辑（方法改名为 `restoreRoomState` 更贴切）
3. **`gateway/connection/manager.go` — `CleanupOldConnection`**：删除该方法（不再需要，`Register` 的 Lua 脚本已完整覆盖踢人）
4. **`gateway/connection/manager.go` — `deleteConnectionMapping`**：删除该方法（仅被 `CleanupOldConnection` 调用）

### 不在范围

- **Token 失效机制**：每次 `StartGame` 生成新 token，旧 token 依然有效。Token 层面无法使旧 session 失效。此问题影响范围大，需独立项目推进，本次不修改
- **`KickOldConnection` 死代码清理**：`common/config/gateway.go` 中的 `KickOldConnection` 字段从未被引用，且 gateway 使用的 `gateway/config/config.go` 中的 `GatewayConfig` 不包含此字段。清理属可选项，本次不修改
- **Platform/DeviceID 未设置**：`handleWebSocket` 未从 query 参数提取 `device_id` 和 `platform` 设置到 `conn`。不影响踢人逻辑（基于 userID），本次不修改

## Impact

- **Affected code**：
  - `gateway/server/server.go`：修改 `handleConnection` 和 `handleReconnect`（改名 `restoreRoomState`）
  - `gateway/connection/manager.go`：删除 `CleanupOldConnection` 和 `deleteConnectionMapping`
- **不受影响**：
  - `gateway/connection/scripts/register_connection.lua.go`：Lua 脚本逻辑正确，无需修改
  - `gateway/connection/manager.go` 的 `Register` / `kickExistingConnection` / `kickLocalConnection` / `publishKickNotification` / `subscribeKickChannel`：踢人基础设施完整，无需修改
  - `gateway/middleware/auth.go`：不检查已有会话，本次不修改
  - `gateway/service/token.go`：Token 无失效机制，本次不修改
  - `PlayerRoomKey` 相关逻辑：房间状态保留，不受影响

## ADDED Requirements

### Requirement: 统一登录踢人

系统 SHALL 在用户登录时，无论用户是否在房间中，都通过 `Register` 方法的 Lua 脚本原子检测旧连接并触发踢人，禁止在 `Register` 之前删除 `GatewayConnKey`。

#### Scenario: 用户不在房间时重复登录

- **WHEN** 用户 A 在设备 1 登录（不在房间），随后在设备 2 登录同一账号
- **THEN** 设备 2 的 `Register` 调用 Lua 脚本检测到设备 1 的旧连接
- **AND** 设备 1 收到 `kicked` 推送（reason: `login_elsewhere`）后连接关闭
- **AND** 设备 2 注册成功

#### Scenario: 用户在房间时重复登录（同节点）

- **WHEN** 用户 A 在设备 1 登录并在房间中，随后在设备 2 登录同一账号（同一 gateway 节点）
- **THEN** 设备 2 的 `Register` 调用 Lua 脚本检测到设备 1 的旧连接
- **AND** 设备 1 收到 `kicked` 推送后连接关闭
- **AND** 设备 2 注册成功
- **AND** 设备 2 收到 `reconnect` 命令恢复房间状态

#### Scenario: 用户在房间时重复登录（跨节点）

- **WHEN** 用户 A 在设备 1 登录并在房间中（节点 A），随后在设备 2 登录同一账号（节点 B）
- **THEN** 设备 2 的 `Register` 在节点 B 调用 Lua 脚本检测到旧连接（node_id=节点A）
- **AND** 节点 B 通过 Redis pubsub 发布踢人通知到节点 A
- **AND** 节点 A 收到通知后关闭设备 1 的连接并发送 `kicked` 推送
- **AND** 设备 2 注册成功并恢复房间状态

#### Scenario: 旧连接已断线时重复登录

- **WHEN** 用户 A 的旧连接已断线（`GatewayConnKey` 未过期但连接已关闭），用户再次登录
- **THEN** `Register` 的 Lua 脚本检测到残留的 `GatewayConnKey`
- **AND** `kickExistingConnection` 尝试踢旧连接（本地找不到则 no-op，跨节点通知找不到连接也 no-op）
- **AND** 新连接注册成功
- **AND** 如果用户在房间中则恢复房间状态

### Requirement: 恢复房间状态与踢人解耦

系统 SHALL 在 `Register` 成功后单独判断是否需要恢复房间状态，恢复状态逻辑 SHALL NOT 执行任何连接清理或 Redis key 删除操作。

#### Scenario: 登录后恢复房间状态

- **WHEN** 用户登录且 `Register` 成功，且 `GetPlayerRoom` 返回非空 roomID
- **THEN** 系统发送 `reconnect` 命令到 game-service 恢复房间状态
- **AND** 不删除 `GatewayConnKey` 或 `PlayerRoomKey`
- **AND** 不调用 `CleanupOldConnection` 或类似清理方法

#### Scenario: 登录后无需恢复房间状态

- **WHEN** 用户登录且 `Register` 成功，且 `GetPlayerRoom` 返回空
- **THEN** 系统进入正常等待状态，不发送 `reconnect` 命令

## REMOVED Requirements

### Requirement: CleanupOldConnection 方法

**Reason**：该方法先删除 `GatewayConnKey` 破坏了 `Register` 的踢人检测，且自身只处理本地连接不覆盖跨节点场景，不发送 `kicked` 推送。`Register` 的 Lua 脚本已完整覆盖同节点和跨节点踢人，`CleanupOldConnection` 属冗余且有 bug。
**Migration**：删除 `CleanupOldConnection` 和 `deleteConnectionMapping` 方法，所有调用点（仅 `handleReconnect`）改为依赖 `Register` 的踢人机制。
