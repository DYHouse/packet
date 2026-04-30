package nacos

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type GRPCConnManager struct {
	client       *Client
	serviceName  string
	conns        []*grpcConn
	currentIndex uint64
	mu           sync.RWMutex
}

type grpcConn struct {
	addr string
	conn *grpc.ClientConn
}

type GRPCConnManagerConfig struct {
	ServiceName    string
	MaxRecvMsgSize int
	MaxSendMsgSize int
}

func NewGRPCConnManager(client *Client, cfg *GRPCConnManagerConfig) (*GRPCConnManager, error) {
	manager := &GRPCConnManager{
		client:      client,
		serviceName: cfg.ServiceName,
		conns:       make([]*grpcConn, 0),
	}

	instances, err := client.DiscoverService(cfg.ServiceName)
	if err != nil {
		return nil, fmt.Errorf("failed to discover service %s: %w", cfg.ServiceName, err)
	}

	for _, instance := range instances {
		addr := fmt.Sprintf("%s:%d", instance.Ip, instance.Port)
		conn, err := manager.createConn(addr, cfg)
		if err != nil {
			logger.Error("failed to create grpc connection", "addr", addr, "error", err)
			continue
		}
		manager.conns = append(manager.conns, &grpcConn{addr: addr, conn: conn})
	}

	if len(manager.conns) == 0 {
		return nil, fmt.Errorf("no available connections for service %s", cfg.ServiceName)
	}

	go manager.watchService(cfg)

	return manager, nil
}

func (m *GRPCConnManager) createConn(addr string, cfg *GRPCConnManagerConfig) (*grpc.ClientConn, error) {
	kaParams := keepalive.ClientParameters{
		Time:                5 * time.Minute,
		Timeout:             1 * time.Minute,
		PermitWithoutStream: true,
	}

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kaParams),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(cfg.MaxRecvMsgSize),
			grpc.MaxCallSendMsgSize(cfg.MaxSendMsgSize),
		),
	}

	conn, err := grpc.Dial(addr, opts...)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

func (m *GRPCConnManager) watchService(cfg *GRPCConnManagerConfig) {
	err := m.client.SubscribeService(m.serviceName, func(services []model.Instance) {
		m.updateConnections(services, cfg)
	})
	if err != nil {
		logger.Error("failed to subscribe service", "service", m.serviceName, "error", err)
	}
}

func (m *GRPCConnManager) updateConnections(instances []model.Instance, cfg *GRPCConnManagerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	newAddrSet := make(map[string]bool)
	for _, instance := range instances {
		if instance.Healthy {
			addr := fmt.Sprintf("%s:%d", instance.Ip, instance.Port)
			newAddrSet[addr] = true
		}
	}

	for _, gc := range m.conns {
		if !newAddrSet[gc.addr] {
			gc.conn.Close()
			logger.Info("closed grpc connection", "addr", gc.addr)
		}
	}

	newConns := make([]*grpcConn, 0)
	existingConnMap := make(map[string]*grpcConn)
	for _, gc := range m.conns {
		existingConnMap[gc.addr] = gc
	}

	for addr := range newAddrSet {
		if gc, exists := existingConnMap[addr]; exists {
			newConns = append(newConns, gc)
		} else {
			conn, err := m.createConn(addr, cfg)
			if err != nil {
				logger.Error("failed to create new grpc connection", "addr", addr, "error", err)
				continue
			}
			newConns = append(newConns, &grpcConn{addr: addr, conn: conn})
			logger.Info("created new grpc connection", "addr", addr)
		}
	}

	m.conns = newConns
	logger.Info("updated grpc connections", "service", m.serviceName, "count", len(newConns))
}

func (m *GRPCConnManager) GetConn() *grpc.ClientConn {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.conns) == 0 {
		return nil
	}

	index := atomic.AddUint64(&m.currentIndex, 1) - 1
	return m.conns[index%uint64(len(m.conns))].conn
}

func (m *GRPCConnManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, gc := range m.conns {
		gc.conn.Close()
	}
	m.conns = nil
}
