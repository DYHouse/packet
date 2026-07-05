package idgen

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/logger"
	cRedis "github.com/cashparty/backend/common/redis"
	"github.com/cashparty/backend/common/rediskeys"
	"github.com/google/uuid"
)

const (
	// nodeIDAllocRetryMax 最大重试次数（覆盖所有可能的 nodeID 0~1023）
	nodeIDAllocRetryMax = 1024
	// nodeIDAllocTTL 分配记录 TTL（秒），1 小时
	// 实例异常宕机后，TTL 过期自动回收 nodeID
	nodeIDAllocTTL = 3600
	// nodeIDAllocRenewInterval 心跳续约间隔，每 5 分钟 EXPIRE
	// 1 小时 TTL 容忍网络抖动，5 分钟续约频率确保足够缓冲
	nodeIDAllocRenewInterval = 5 * time.Minute
)

// NodeAllocator 通过 Redis 自动分配唯一 nodeID。
// 适用于 nacos 配置中心场景：多实例读同一份配置，node_id=0 触发自动分配。
//
// 分配流程：
//  1. INCR cashparty:idgen:node_id_seq 获取递增序列
//  2. candidateID = (seq - 1) % 1024，确保在 [0, 1023] 范围内循环
//  3. SET NX cashparty:idgen:node_id:alloc:<candidateID> <instanceID> EX 3600
//     - 成功：分配完成
//     - 失败：INCR 重试下一个（最多 1024 次）
//
// 心跳续约：每 5 分钟 EXPIRE，防止 TTL 过期导致 nodeID 被回收。
// 优雅退出：Release 通过 Lua 脚本 DEL（仅当 value==instanceID，防止误删他人锁）。
type NodeAllocator struct {
	redis      *cRedis.Client
	instanceID string // 实例唯一标识（UUID），用于持有者校验
	nodeID     int64
	cancel     context.CancelFunc
}

// NewNodeAllocator 创建 nodeID 分配器。
func NewNodeAllocator(redis *cRedis.Client) *NodeAllocator {
	return &NodeAllocator{
		redis:      redis,
		instanceID: uuid.NewString(),
	}
}

// Allocate 分配唯一 nodeID。
// 流程：INCR 获取候选 nodeID → SET NX 抢占 → 失败则重试。
// 返回分配到的 nodeID（[0, 1023] 范围内）。
func (a *NodeAllocator) Allocate(ctx context.Context) (int64, error) {
	for i := 0; i < nodeIDAllocRetryMax; i++ {
		// INCR 获取递增序列
		seq, err := a.redis.Incr(ctx, rediskeys.KeyIDGenNodeIDSeq).Result()
		if err != nil {
			return 0, fmt.Errorf("incr node_id_seq: %w", err)
		}

		// 候选 nodeID = (seq - 1) % 1024，确保在 [0, 1023] 范围内循环
		candidateID := (seq - 1) % (nodeIDMax + 1)

		// SET NX 抢占（key 带 TTL，实例宕机后自动回收）
		allocKey := rediskeys.IDGenNodeIDAllocKey(candidateID)
		ok, err := a.redis.SetNX(ctx, allocKey, a.instanceID, nodeIDAllocTTL*time.Second).Result()
		if err != nil {
			return 0, fmt.Errorf("setnx node_id_alloc %d: %w", candidateID, err)
		}
		if ok {
			a.nodeID = candidateID
			logger.Info("node_id allocated",
				"node_id", candidateID,
				"instance_id", a.instanceID,
			)
			return candidateID, nil
		}
		// 抢占失败，重试下一个
	}
	return 0, fmt.Errorf("no available node_id after %d retries", nodeIDAllocRetryMax)
}

// StartRenewal 启动心跳续约 goroutine。
// 每 5 分钟续约一次，防止 TTL 过期导致 nodeID 被回收。
// 必须传入 appCtx 派生的 context，禁止使用 context.Background()（规约 §6）。
func (a *NodeAllocator) StartRenewal(ctx context.Context) {
	renewCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	go func() {
		ticker := time.NewTicker(nodeIDAllocRenewInterval)
		defer ticker.Stop()

		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				allocKey := rediskeys.IDGenNodeIDAllocKey(a.nodeID)
				if err := a.redis.Expire(renewCtx, allocKey, nodeIDAllocTTL*time.Second).Err(); err != nil {
					logger.Warn("failed to renew node_id allocation",
						"node_id", a.nodeID,
						"error", err,
					)
				}
			}
		}
	}()
}

// Release 释放 nodeID（优雅退出时调用）。
// 通过 Lua 脚本 DEL：仅当 value == instanceID 时才删除，防止误删他人锁。
// 规约参考 CODING_STANDARD.md §19 L-1（NewScript 注册）。
func (a *NodeAllocator) Release(ctx context.Context) error {
	if a.cancel != nil {
		a.cancel()
	}
	allocKey := rediskeys.IDGenNodeIDAllocKey(a.nodeID)
	_, err := releaseNodeScript.Run(ctx, a.redis, []string{allocKey}, a.instanceID).Result()
	if err != nil {
		return fmt.Errorf("release node_id_alloc %d: %w", a.nodeID, err)
	}
	logger.Info("node_id released",
		"node_id", a.nodeID,
		"instance_id", a.instanceID,
	)
	return nil
}

// GetNodeID 返回已分配的 nodeID。
func (a *NodeAllocator) GetNodeID() int64 {
	return a.nodeID
}

// GetInstanceID 返回实例 ID。
func (a *NodeAllocator) GetInstanceID() string {
	return a.instanceID
}
