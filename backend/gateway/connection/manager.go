package connection

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/gateway"
	"github.com/redis/go-redis/v9"
)

type ManagerConfig struct {
	MaxConnections       int
	DisconnectTimeout    time.Duration
	ConnRedisTTL         time.Duration
	HeartbeatRenewalTick time.Duration
}

type EventCallback func(conn *Connection, event string, roomID string)

type EventType string

const (
	EventKicked       EventType = "kicked"
	EventDisconnected EventType = "disconnected"
	EventReconnected  EventType = "reconnected"
	EventTimeout      EventType = "timeout"
)

type KickMessage struct {
	UserID string `json:"user_id"`
	ConnID string `json:"conn_id"`
	Reason string `json:"reason"`
}

type Manager struct {
	localConnections sync.Map
	userConnections  sync.Map
	config           *ManagerConfig
	redis            *cRedis.Client
	nodeID           string
	eventCallback    EventCallback
	connectionCount  int64
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

func NewManager(config *ManagerConfig, redis *cRedis.Client, nodeID string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	if config == nil {
		config = &ManagerConfig{
			MaxConnections:       10000,
			DisconnectTimeout:    30 * time.Second,
			ConnRedisTTL:         24 * time.Hour,
			HeartbeatRenewalTick: 30 * time.Second,
		}
	}

	m := &Manager{
		config: config,
		redis:  redis,
		nodeID: nodeID,
		ctx:    ctx,
		cancel: cancel,
	}

	m.wg.Add(1)
	go m.subscribeKickChannel()

	return m
}

func (m *Manager) Register(conn *Connection) error {
	if !m.tryIncrementCount() {
		return fmt.Errorf("connection limit reached")
	}

	needKick, oldConnID, oldNodeID := m.registerInRedis(conn)
	if needKick {
		m.kickExistingConnection(conn.UserID, oldConnID, oldNodeID)
	}

	m.localConnections.Store(conn.ConnID, conn)
	m.userConnections.Store(conn.UserID, conn.ConnID)

	logger.Info("connection registered",
		"conn_id", conn.ConnID,
		"user_id", conn.UserID,
		"current_connections", atomic.LoadInt64(&m.connectionCount))

	return nil
}

func (m *Manager) tryIncrementCount() bool {
	for {
		current := atomic.LoadInt64(&m.connectionCount)
		if current >= int64(m.config.MaxConnections) {
			logger.Warn("connection limit reached",
				"current", current,
				"max", m.config.MaxConnections)
			return false
		}
		if atomic.CompareAndSwapInt64(&m.connectionCount, current, current+1) {
			return true
		}
	}
}

func (m *Manager) registerInRedis(conn *Connection) (needKick bool, oldConnID, oldNodeID string) {
	if m.redis == nil {
		return false, "", ""
	}

	key := gateway.GatewayConnKey(conn.UserID)
	result, err := m.redis.Eval(m.ctx, LuaRegisterConnection,
		[]string{key},
		conn.ConnID, m.nodeID, conn.Platform, conn.DeviceID, time.Now().Unix()).Slice()
	if err != nil {
		logger.Error("failed to register connection in redis", "error", err, "user_id", conn.UserID)
		return false, "", ""
	}

	if len(result) >= 3 {
		needKick = result[0].(int64) == 1
		oldConnID = result[1].(string)
		oldNodeID = result[2].(string)
	}
	return
}

func (m *Manager) kickExistingConnection(userID, oldConnID, oldNodeID string) {
	if oldNodeID == m.nodeID {
		m.kickLocalConnection(oldConnID)
	} else {
		m.publishKickNotification(userID, oldConnID, oldNodeID)
	}
}

func (m *Manager) kickLocalConnection(connID string) {
	conn, ok := m.localConnections.Load(connID)
	if !ok {
		return
	}
	c := conn.(*Connection)

	pushMsg := message.NewPushMessage(message.PushKicked, &message.KickedPush{
		UserID:  c.UserID,
		Reason:  message.ReasonLoginElsewhere,
		Message: message.GetKickMessage(message.ReasonLoginElsewhere),
	})
	data, err := pushMsg.ToJSON()
	if err != nil {
		logger.Error("failed to marshal kick push message",
			"conn_id", connID,
			"user_id", c.UserID,
			"error", err)
	} else {
		c.Send(data)
	}

	c.Close()
	m.localConnections.Delete(connID)
	m.userConnections.Delete(c.UserID)
	atomic.AddInt64(&m.connectionCount, -1)

	m.emitEvent(c, string(EventKicked), "")
}

func (m *Manager) publishKickNotification(userID, oldConnID, oldNodeID string) {
	if m.redis == nil {
		return
	}

	channel := gateway.GatewayKickKey(oldNodeID)
	kickMsg := KickMessage{
		UserID: userID,
		ConnID: oldConnID,
		Reason: message.ReasonLoginElsewhere,
	}
	data, err := json.Marshal(kickMsg)
	if err != nil {
		logger.Error("failed to marshal kick notification",
			"user_id", userID,
			"conn_id", oldConnID,
			"error", err)
		return
	}
	m.redis.Publish(m.ctx, channel, string(data))
}

// subscribeKickChannel runs a reconnect loop. On disconnect it retries with
// exponential backoff (initial 1s, cap 30s) plus jitter, mirroring the
// RedisPubSubConsumer pattern.
func (m *Manager) subscribeKickChannel() {
	defer m.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			logger.Error("subscribe kick channel panic",
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	if m.redis == nil {
		return
	}

	channel := gateway.GatewayKickKey(m.nodeID)
	backoff := time.Second

	for {
		if err := m.ctx.Err(); err != nil {
			return
		}

		sub := m.redis.Subscribe(m.ctx, channel)

		if _, err := sub.Receive(m.ctx); err != nil {
			_ = sub.Close()
			logger.Warn("kick channel subscribe failed, reconnecting",
				"channel", channel,
				"error", err,
				"backoff", backoff)
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(backoff + time.Duration(rand.Intn(100))*time.Millisecond):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}

		disconnected := m.consumeKickMessages(sub)
		_ = sub.Close()
		if !disconnected {
			return
		}
		if m.ctx.Err() != nil {
			return
		}
		logger.Warn("kick channel disconnected, reconnecting",
			"channel", channel,
			"backoff", backoff)
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(backoff + time.Duration(rand.Intn(100))*time.Millisecond):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// consumeKickMessages blocks on the pubsub channel. It returns true when the
// channel closes (should reconnect) and false when ctx is cancelled (exit).
func (m *Manager) consumeKickMessages(sub *redis.PubSub) bool {
	ch := sub.Channel()
	for {
		select {
		case <-m.ctx.Done():
			return false
		case msg, ok := <-ch:
			if !ok {
				return true
			}
			var kickMsg KickMessage
			if err := json.Unmarshal([]byte(msg.Payload), &kickMsg); err != nil {
				logger.Error("failed to unmarshal kick message",
					"error", err,
					"payload", msg.Payload)
				continue
			}
			m.kickLocalConnection(kickMsg.ConnID)
		}
	}
}

func (m *Manager) MarkDisconnected(conn *Connection) {
	conn.SetStatus(StatusDisconnected)

	m.localConnections.Delete(conn.ConnID)
	m.userConnections.Delete(conn.UserID)
	atomic.AddInt64(&m.connectionCount, -1)

	roomID := m.GetPlayerRoom(conn.UserID)
	m.emitEvent(conn, string(EventDisconnected), roomID)
}

func (m *Manager) CleanupOldConnection(userID string) {
	m.deleteConnectionMapping(userID)

	if oldConnIDI, ok := m.userConnections.Load(userID); ok {
		oldConnID := oldConnIDI.(string)
		if oldConn, ok := m.localConnections.Load(oldConnID); ok {
			c := oldConn.(*Connection)
			c.Close()
		}
		m.localConnections.Delete(oldConnID)
		m.userConnections.Delete(userID)
		atomic.AddInt64(&m.connectionCount, -1)
	}
}

func (m *Manager) deleteConnectionMapping(userID string) {
	if m.redis == nil {
		return
	}
	key := gateway.GatewayConnKey(userID)
	m.redis.Del(m.ctx, key)
}

func (m *Manager) GetPlayerRoom(userID string) string {
	if m.redis == nil {
		return ""
	}
	key := gateway.PlayerRoomKey(userID)
	roomID, _ := m.redis.Get(m.ctx, key).Result()
	return roomID
}

func (m *Manager) RenewConnectionTTL(userID string) {
	if m.redis == nil {
		return
	}
	key := gateway.GatewayConnKey(userID)
	m.redis.Expire(m.ctx, key, m.config.ConnRedisTTL)
}

func (m *Manager) Unregister(connID string) {
	conn, ok := m.localConnections.Load(connID)
	if !ok {
		return
	}
	c := conn.(*Connection)

	m.localConnections.Delete(connID)
	m.userConnections.Delete(c.UserID)
	atomic.AddInt64(&m.connectionCount, -1)

	c.Close()
}

func (m *Manager) GetConnection(connID string) (*Connection, bool) {
	conn, ok := m.localConnections.Load(connID)
	if !ok {
		return nil, false
	}
	return conn.(*Connection), true
}

func (m *Manager) GetConnectionByUserID(userID string) (*Connection, bool) {
	connIDI, ok := m.userConnections.Load(userID)
	if !ok {
		return nil, false
	}
	connID := connIDI.(string)
	return m.GetConnection(connID)
}

func (m *Manager) IsUserConnectedLocally(userID string) bool {
	_, ok := m.userConnections.Load(userID)
	return ok
}

func (m *Manager) BroadcastToUser(userID string, msgData []byte) {
	conn, ok := m.GetConnectionByUserID(userID)
	if !ok {
		return
	}
	if !conn.Send(msgData) {
		logger.Warn("failed to send message to user, send queue full",
			"user_id", userID,
			"conn_id", conn.ConnID)
	}
}

func (m *Manager) GetConnectionCount() int64 {
	return atomic.LoadInt64(&m.connectionCount)
}

func (m *Manager) GetConnectionCountInt() int {
	return int(atomic.LoadInt64(&m.connectionCount))
}

func (m *Manager) GetAllConnections() []*Connection {
	connections := make([]*Connection, 0)
	m.localConnections.Range(func(_, value interface{}) bool {
		conn := value.(*Connection)
		connections = append(connections, conn)
		return true
	})
	return connections
}

func (m *Manager) CloseAll() {
	m.localConnections.Range(func(_, value interface{}) bool {
		conn := value.(*Connection)
		conn.Close()
		return true
	})
}

func (m *Manager) Stop() {
	m.cancel()
	m.wg.Wait()
}

func (m *Manager) SetEventCallback(cb EventCallback) {
	m.eventCallback = cb
}

func (m *Manager) emitEvent(conn *Connection, event string, roomID string) {
	if m.eventCallback != nil {
		m.eventCallback(conn, event, roomID)
	}
}

func (m *Manager) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"current_connections": m.GetConnectionCount(),
		"max_connections":     m.config.MaxConnections,
	}
}

func (m *Manager) Range(fn func(conn *Connection) bool) {
	m.localConnections.Range(func(_, value interface{}) bool {
		return fn(value.(*Connection))
	})
}

func (m *Manager) WaitForAllConnectionsClose(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&m.connectionCount) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

const LuaRegisterConnection = `
local userConnKey = KEYS[1]
local newConnID = ARGV[1]
local newNodeID = ARGV[2]
local platform = ARGV[3]
local deviceID = ARGV[4]
local connectedAt = tonumber(ARGV[5])

local oldConnID = ''
local oldNodeID = ''

local oldData = redis.call('HGETALL', userConnKey)
if #oldData > 0 then
    for i = 1, #oldData, 2 do
        if oldData[i] == 'conn_id' then oldConnID = oldData[i+1] end
        if oldData[i] == 'node_id' then oldNodeID = oldData[i+1] end
    end
end

redis.call('HMSET', userConnKey,
    'conn_id', newConnID,
    'node_id', newNodeID,
    'platform', platform,
    'device_id', deviceID,
    'connected_at', connectedAt
)
redis.call('EXPIRE', userConnKey, 86400)

if oldConnID ~= '' and oldConnID ~= newConnID then
    return {1, oldConnID, oldNodeID}
end
return {0, '', ''}
`
