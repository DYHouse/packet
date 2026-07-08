package idgen

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
)

// 全局实例注册，提供 Init / InitWithAutoAlloc / GetGenerator / Shutdown API。
// 规约参考 CODING_STANDARD.md §20 SID-7（显式初始化，禁止懒加载）。
var (
	globalGenerator IDGenerator
	globalNodeID    int64
	globalAllocator *NodeAllocator
	initOnce        sync.Once
)

// Init 初始化全局 ID 生成器（显式指定 nodeID）。
// 必须在 bootstrap 层启动时调用，禁止在业务代码中调用（规约 SID-7）。
// nodeID 必须在 [0, 1023] 范围内。
// 适用于单实例部署、测试环境。
func Init(nodeID int64) error {
	var initErr error
	initOnce.Do(func() {
		gen, err := NewSnowflakeGenerator(nodeID)
		if err != nil {
			initErr = err
			return
		}
		globalGenerator = gen
		globalNodeID = nodeID
		logger.Info("id generator initialized with explicit node_id",
			"node_id", nodeID,
		)
	})
	return initErr
}

// InitWithAutoAlloc 初始化全局 ID 生成器（Redis 自动分配 nodeID）。
// 流程：Redis 自动分配 nodeID → 创建 SnowflakeGenerator → 启动心跳续约。
// 适用于多实例生产环境（nacos 配置 node_id: 0 触发）。
// 返回 NodeAllocator 供调用方在退出时调用 Release。
// 必须传入 appCtx 派生的 context，禁止使用 context.Background()（规约 §6）。
func InitWithAutoAlloc(ctx context.Context, redis cRedis.RedisClient) (*NodeAllocator, error) {
	var initErr error
	var allocator *NodeAllocator
	initOnce.Do(func() {
		allocator = NewNodeAllocator(redis)
		nodeID, err := allocator.Allocate(ctx)
		if err != nil {
			initErr = fmt.Errorf("allocate node_id: %w", err)
			return
		}
		gen, err := NewSnowflakeGenerator(nodeID)
		if err != nil {
			initErr = err
			return
		}
		globalGenerator = gen
		globalNodeID = nodeID
		globalAllocator = allocator
		allocator.StartRenewal(ctx)
		logger.Info("id generator initialized with auto-allocated node_id",
			"node_id", nodeID,
		)
	})
	return allocator, initErr
}

// GetGenerator 获取全局 ID 生成器。
// 未初始化时返回 ErrGeneratorNotInitialized（fail-fast，禁止懒加载，规约 SID-7）。
func GetGenerator() (IDGenerator, error) {
	if globalGenerator == nil {
		return nil, ErrGeneratorNotInitialized
	}
	return globalGenerator, nil
}

// GetNodeID 获取全局 nodeID。
// 未初始化时返回 ErrGeneratorNotInitialized。
func GetNodeID() (int64, error) {
	if globalGenerator == nil {
		return 0, ErrGeneratorNotInitialized
	}
	return globalNodeID, nil
}

// GetNodeIDString 获取全局 nodeID 的字符串表示。
// 未初始化时返回 "0"（向后兼容 gateway 等调用方，避免 panic）。
func GetNodeIDString() string {
	if globalGenerator == nil {
		return "0"
	}
	return strconv.FormatInt(globalNodeID, 10)
}

// Shutdown 优雅关闭，释放自动分配的 nodeID。
// 必须在 bootstrap 层退出时调用。
// 仅当使用 InitWithAutoAlloc 初始化时才需要调用。
func Shutdown(ctx context.Context) error {
	if globalAllocator != nil {
		return globalAllocator.Release(ctx)
	}
	return nil
}
