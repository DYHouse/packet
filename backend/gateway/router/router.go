package router

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cashparty/backend/common/logger"
	"github.com/cashparty/backend/common/message"
	"github.com/cashparty/backend/gateway/connection"
	"github.com/cashparty/backend/gateway/discovery"
	commonPb "github.com/cashparty/backend/proto/common"
	"github.com/google/uuid"
)

type RouteConfig struct {
	CmdPrefix string `yaml:"cmd_prefix" json:"cmd_prefix"`
	Service   string `yaml:"service" json:"service"`
}

type RouterConfig struct {
	Routes []RouteConfig `yaml:"routes" json:"routes"`
}

type MessageRouter struct {
	serviceDiscovery *discovery.ServiceDiscovery
	routes           map[string]string
	routesMu         sync.RWMutex
}

func NewMessageRouter(serviceDiscovery *discovery.ServiceDiscovery, config *RouterConfig) *MessageRouter {
	router := &MessageRouter{
		serviceDiscovery: serviceDiscovery,
		routes:           make(map[string]string),
	}

	if config != nil {
		router.LoadRoutes(config)
	}

	return router
}

func (r *MessageRouter) LoadRoutes(config *RouterConfig) {
	r.routesMu.Lock()
	defer r.routesMu.Unlock()

	for _, route := range config.Routes {
		r.routes[route.CmdPrefix] = route.Service
		logger.Info("route loaded", "cmd", route.CmdPrefix, "service", route.Service)
	}
}

func (r *MessageRouter) AddRoute(cmd, service string) {
	r.routesMu.Lock()
	defer r.routesMu.Unlock()
	r.routes[cmd] = service
}

func (r *MessageRouter) RemoveRoute(cmd string) {
	r.routesMu.Lock()
	defer r.routesMu.Unlock()
	delete(r.routes, cmd)
}

func (r *MessageRouter) Route(conn *connection.Connection, rawMessage []byte) {
	var req message.Request
	if err := json.Unmarshal(rawMessage, &req); err != nil {
		if conn != nil {
			logger.Warn("failed to parse message", "conn_id", conn.ConnID, "error", err)
			r.sendError(conn, "", "", message.CodeInvalidMessage)
		}
		return
	}

	if req.Cmd == "" {
		if conn != nil {
			logger.Warn("missing command", "conn_id", conn.ConnID)
			r.sendError(conn, "", req.RequestID, message.CodeMissingCommand)
		}
		return
	}

	if req.RequestID == "" {
		req.RequestID = generateRequestID()
	}

	if conn != nil {
		logger.Debug("routing message",
			"conn_id", conn.ConnID,
			"user_id", conn.UserID,
			"cmd", req.Cmd,
			"request_id", req.RequestID)
	} else {
		logger.Debug("routing server-initiated message",
			"cmd", req.Cmd,
			"request_id", req.RequestID)
	}

	serviceName := r.getServiceName(req.Cmd)
	if serviceName == "" {
		if conn != nil {
			logger.Warn("unknown command", "conn_id", conn.ConnID, "cmd", req.Cmd)
			r.sendError(conn, req.Cmd, req.RequestID, message.CodeUnknownCommand)
		}
		return
	}

	if serviceName == "gateway" {
		if conn != nil {
			resp := r.handleLocalCommand(conn, &req)
			r.sendResponse(conn, resp)
		}
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp := r.forwardToService(ctx, serviceName, conn, &req)
	r.sendResponse(conn, resp)
}

func (r *MessageRouter) getServiceName(cmd string) string {
	r.routesMu.RLock()
	defer r.routesMu.RUnlock()

	if service, ok := r.routes[cmd]; ok {
		return service
	}

	return ""
}

func (r *MessageRouter) forwardToService(ctx context.Context, serviceName string, conn *connection.Connection, req *message.Request) *message.Response {
	client, err := r.serviceDiscovery.GetClient(serviceName)
	if err != nil {
		logger.Error("failed to get service client",
			"service", serviceName,
			"error", err)
		return message.NewErrorResponse(req.Cmd, req.RequestID, message.CodeSystemError)
	}

	userID := ""
	if conn != nil {
		userID = conn.UserID
	}

	forwardReq := &commonPb.ForwardRequest{
		UserId:    userID,
		Cmd:       req.Cmd,
		RequestId: req.RequestID,
		Data:      req.Data,
		Timestamp: req.Timestamp,
	}

	logger.Debug("[Gateway->Server] sending request",
		"service", serviceName,
		"user_id", forwardReq.UserId,
		"cmd", forwardReq.Cmd,
		"request_id", forwardReq.RequestId,
		"data", string(forwardReq.Data))

	resp, err := client.Forward(ctx, forwardReq)
	if err != nil {
		logger.Error("failed to forward request",
			"service", serviceName,
			"cmd", req.Cmd,
			"error", err)
		return message.NewErrorResponse(req.Cmd, req.RequestID, message.CodeSystemError)
	}

	logger.Debug("[Gateway<-Server] received response",
		"cmd", resp.Cmd,
		"request_id", resp.RequestId,
		"code", resp.Code,
		"msg", resp.Msg,
		"data", string(resp.Data))

	var data interface{}
	if len(resp.Data) > 0 {
		var jsonData interface{}
		if err := json.Unmarshal(resp.Data, &jsonData); err == nil {
			data = jsonData
		} else {
			data = resp.Data
		}
	}

	return &message.Response{
		Cmd:       resp.Cmd,
		RequestID: resp.RequestId,
		Code:      int(resp.Code),
		Msg:       resp.Msg,
		Data:      data,
		Timestamp: resp.Timestamp,
	}
}

func (r *MessageRouter) handleLocalCommand(conn *connection.Connection, req *message.Request) *message.Response {
	switch req.Cmd {
	case message.CmdPing:
		conn.UpdateHeartbeat()
		return message.NewSuccessResponse("pong", req.RequestID, &message.PingResponse{
			ServerTime: time.Now().UnixMilli(),
		})
	default:
		return message.NewErrorResponse(req.Cmd, req.RequestID, message.CodeUnknownCommand)
	}
}

func (r *MessageRouter) sendResponse(conn *connection.Connection, resp *message.Response) {
	if conn == nil {
		logger.Debug("server-initiated notification, skip sending response", "cmd", resp.Cmd)
		return
	}

	data, err := resp.ToJSON()
	if err != nil {
		logger.Error("failed to marshal response", "error", err, "conn_id", conn.ConnID)
		return
	}

	if !conn.Send(data) {
		logger.Warn("failed to send response, send queue full", "conn_id", conn.ConnID)
	}
}

func (r *MessageRouter) sendError(conn *connection.Connection, cmd, requestID string, code int) {
	resp := message.NewErrorResponse(cmd, requestID, code)
	r.sendResponse(conn, resp)
}

func generateRequestID() string {
	return fmt.Sprintf("req_%d_%s", time.Now().UnixMilli(), uuid.New().String()[:8])
}
