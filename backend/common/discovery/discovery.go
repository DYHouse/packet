package discovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/nacos"
	commonPb "github.com/cashparty/backend/proto/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
)

type ServiceClient interface {
	Forward(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error)
	SaveUser(ctx context.Context, req *commonPb.SaveUserRequest) (*commonPb.SaveUserResponse, error)
	Close() error
}

type nacosResolverBuilder struct {
	nacosClient *nacos.Client
}

func (b *nacosResolverBuilder) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	serviceName := target.URL.Host
	if serviceName == "" {
		serviceName = strings.TrimPrefix(target.URL.Path, "/")
	}
	r := &nacosResolver{
		nacosClient: b.nacosClient,
		serviceName: serviceName,
		cc:          cc,
	}
	r.start()
	return r, nil
}

func (b *nacosResolverBuilder) Scheme() string {
	return "nacos"
}

type nacosResolver struct {
	nacosClient *nacos.Client
	serviceName string
	cc          resolver.ClientConn
	cancel      context.CancelFunc
}

func (r *nacosResolver) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.updateAddresses()
	go r.watch(ctx)
}

func (r *nacosResolver) watch(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.updateAddresses()
		}
	}
}

func (r *nacosResolver) updateAddresses() {
	instances, err := r.nacosClient.DiscoverService(r.serviceName)
	if err != nil {
		logger.Error("failed to discover service", "service", r.serviceName, "error", err)
		return
	}

	var addrs []resolver.Address
	for _, ins := range instances {
		addrs = append(addrs, resolver.Address{
			Addr:       fmt.Sprintf("%s:%d", ins.Ip, ins.Port),
			ServerName: r.serviceName,
		})
	}

	if len(addrs) > 0 {
		r.cc.UpdateState(resolver.State{Addresses: addrs})
		logger.Info("service addresses updated", "service", r.serviceName, "count", len(addrs))
	}
}

func (r *nacosResolver) ResolveNow(resolver.ResolveNowOptions) {}

func (r *nacosResolver) Close() {
	if r.cancel != nil {
		r.cancel()
	}
}

type ServiceDiscovery struct {
	nacosClient *nacos.Client
	connections sync.Map
	clients     sync.Map
	builder     *nacosResolverBuilder
}

func NewServiceDiscovery(nacosClient *nacos.Client) *ServiceDiscovery {
	return &ServiceDiscovery{
		nacosClient: nacosClient,
		builder:     &nacosResolverBuilder{nacosClient: nacosClient},
	}
}

func (d *ServiceDiscovery) GetClient(serviceName string) (ServiceClient, error) {
	if client, ok := d.clients.Load(serviceName); ok {
		return client.(ServiceClient), nil
	}

	conn, err := d.getConnection(serviceName)
	if err != nil {
		return nil, err
	}

	client := &genericServiceClient{conn: conn}
	d.clients.Store(serviceName, client)

	return client, nil
}

func (d *ServiceDiscovery) getConnection(serviceName string) (*grpc.ClientConn, error) {
	if conn, ok := d.connections.Load(serviceName); ok {
		return conn.(*grpc.ClientConn), nil
	}

	conn, err := grpc.Dial(
		fmt.Sprintf("nacos:///%s", serviceName),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithResolvers(d.builder),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to service %s: %w", serviceName, err)
	}

	d.connections.Store(serviceName, conn)
	logger.Info("service connection established", "service", serviceName)

	return conn, nil
}

func (d *ServiceDiscovery) Close() {
	d.connections.Range(func(_, value interface{}) bool {
		if conn, ok := value.(*grpc.ClientConn); ok {
			conn.Close()
		}
		return true
	})
}

type genericServiceClient struct {
	conn *grpc.ClientConn
}

func (c *genericServiceClient) Forward(ctx context.Context, req *commonPb.ForwardRequest) (*commonPb.ForwardResponse, error) {
	return commonPb.NewGenericServiceClient(c.conn).Forward(ctx, req)
}

func (c *genericServiceClient) SaveUser(ctx context.Context, req *commonPb.SaveUserRequest) (*commonPb.SaveUserResponse, error) {
	return commonPb.NewGenericServiceClient(c.conn).SaveUser(ctx, req)
}

func (c *genericServiceClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

type UserSaverAdapter struct {
	discovery *ServiceDiscovery
}

func NewUserSaverAdapter(discovery *ServiceDiscovery) *UserSaverAdapter {
	return &UserSaverAdapter{discovery: discovery}
}

func (a *UserSaverAdapter) SaveUser(ctx context.Context, thirdPartyUserID, nickname, avatar, ip, deviceID string) (string, string, error) {
	client, err := a.discovery.GetClient("game-service")
	if err != nil {
		return "", "", err
	}
	resp, err := client.SaveUser(ctx, &commonPb.SaveUserRequest{
		UserId:   thirdPartyUserID,
		Nickname: nickname,
		Avatar:   avatar,
		Ip:       ip,
		DeviceId: deviceID,
	})
	if err != nil {
		return "", "", err
	}
	return resp.Id, resp.Avatar, nil
}
