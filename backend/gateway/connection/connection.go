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
