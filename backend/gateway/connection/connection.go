package connection

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DefaultSendQueueSize = 256
	HeartbeatInterval    = 30 * time.Second
	HeartbeatTimeout     = 2 * time.Minute
)

type ConnStatus int

const (
	StatusConnecting ConnStatus = iota
	StatusAuthed
	StatusDisconnected
	// StatusKicking 表示连接已被标记为踢出,等待 writeAndHeartbeatPump drain 完 sendChan 后自行关闭。
	// 此状态下 Send 仍可工作(允许 kicked 推送塞入),Close 仍可被外部调用(兜底)。
	StatusKicking
	StatusClosed
)

type Connection struct {
	ConnID    string
	UserID    string
	IP        string
	DeviceID  string
	Platform  string
	UserAgent string
	Nickname  string
	Avatar    string

	conn          *websocket.Conn
	status        ConnStatus
	lastHeartbeat time.Time
	sendChan      chan []byte
	closeChan     chan struct{}
	closeOnce     sync.Once
	mu            sync.RWMutex
	sendQueueSize int
}

func NewConnection(connID string, wsConn *websocket.Conn, sendQueueSize int) *Connection {
	if sendQueueSize <= 0 {
		sendQueueSize = DefaultSendQueueSize
	}

	return &Connection{
		ConnID:        connID,
		conn:          wsConn,
		status:        StatusConnecting,
		lastHeartbeat: time.Now(),
		sendChan:      make(chan []byte, sendQueueSize),
		closeChan:     make(chan struct{}),
		sendQueueSize: sendQueueSize,
	}
}

func (c *Connection) SetUserInfo(userID, nickname, avatar string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.UserID = userID
	c.Nickname = nickname
	c.Avatar = avatar
}

func (c *Connection) SetStatus(status ConnStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = status
}

func (c *Connection) GetStatus() ConnStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status
}

// MarkKicking 将连接状态置为 StatusKicking,通知 writeAndHeartbeatPump 进入 drain 模式。
// 不关闭 sendChan/closeChan/wsConn,让 writePump 自然 flush 完待发送数据(如 kicked 推送)后自行关闭。
func (c *Connection) MarkKicking() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = StatusKicking
}

// IsKicking 返回连接是否处于 drain 模式。
func (c *Connection) IsKicking() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status == StatusKicking
}

func (c *Connection) UpdateHeartbeat() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastHeartbeat = time.Now()
}

func (c *Connection) GetLastHeartbeat() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastHeartbeat
}

func (c *Connection) IsHeartbeatTimeout() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return time.Since(c.lastHeartbeat) > HeartbeatTimeout
}

func (c *Connection) Send(data []byte) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.status == StatusClosed {
		return false
	}

	select {
	case c.sendChan <- data:
		return true
	default:
		return false
	}
}

func (c *Connection) SendChan() <-chan []byte {
	return c.sendChan
}

func (c *Connection) CloseChan() <-chan struct{} {
	return c.closeChan
}

func (c *Connection) WebSocketConn() *websocket.Conn {
	return c.conn
}

func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.status = StatusClosed
		c.mu.Unlock()

		close(c.closeChan)
		close(c.sendChan)

		if c.conn != nil {
			c.conn.Close()
		}
	})
}

func (c *Connection) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.status == StatusClosed
}
