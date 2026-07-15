package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/cashparty/backend/common/config"
	"github.com/cashparty/backend/common/logger"
	"github.com/redis/go-redis/v9"
)

type Z = redis.Z

type ZRangeBy = redis.ZRangeBy

// RedisClient Redis 客户端接口，支持 mock 测试。
// 封装 go-redis 操作，统一 Redis 访问入口。
type RedisClient interface {
	Raw() *redis.Client
	Close() error
	Get(ctx context.Context, key string) *redis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	SetEX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	Exists(ctx context.Context, keys ...string) *redis.IntCmd
	Incr(ctx context.Context, key string) *redis.IntCmd
	Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd
	SAdd(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
	SRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
	SMembers(ctx context.Context, key string) *redis.StringSliceCmd
	SCard(ctx context.Context, key string) *redis.IntCmd
	SIsMember(ctx context.Context, key string, member interface{}) *redis.BoolCmd
	SPop(ctx context.Context, key string) *redis.StringCmd
	SInter(ctx context.Context, keys ...string) *redis.StringSliceCmd
	Pipeline() redis.Pipeliner
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
	HGet(ctx context.Context, key, field string) *redis.StringCmd
	HGetAll(ctx context.Context, key string) *redis.MapStringStringCmd
	HKeys(ctx context.Context, key string) *redis.StringSliceCmd
	HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	HIncrBy(ctx context.Context, key, field string, incr int64) *redis.IntCmd
	HDel(ctx context.Context, key string, fields ...string) *redis.IntCmd
	ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) *redis.ZSliceCmd
	ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd
	ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd
	ZRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd
	ZScore(ctx context.Context, key, member string) *redis.FloatCmd
	IncrBy(ctx context.Context, key string, value int64) *redis.IntCmd
	Decr(ctx context.Context, key string) *redis.IntCmd
	ZCard(ctx context.Context, key string) *redis.IntCmd
	ZRemRangeByScore(ctx context.Context, key, min, max string) *redis.IntCmd
	LPop(ctx context.Context, key string) *redis.StringCmd
	LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd
	BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
}

// client Redis 客户端实现
type client struct {
	rdb *redis.Client
}

// NewClient 创建Redis客户端
func NewClient(cfg *config.RedisConfig) (RedisClient, error) {
	var rdb *redis.Client
	var err error

	if cfg.UseEvalSHA != nil {
		SetUseEvalSHA(*cfg.UseEvalSHA)
	}

	switch cfg.Mode {
	case "sentinel":
		rdb, err = newSentinelClient(cfg)
	case "standalone", "":
		rdb, err = newStandaloneClient(cfg)
	default:
		return nil, fmt.Errorf("unsupported redis mode: %s", cfg.Mode)
	}

	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	logger.Info("redis connected", "mode", cfg.Mode, "db", cfg.DB)
	return &client{rdb: rdb}, nil
}

// newStandaloneClient 创建单机模式客户端
func newStandaloneClient(cfg *config.RedisConfig) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: cfg.MinIdleConns,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})

	logger.Info("redis standalone mode", "addr", cfg.Addr)
	return rdb, nil
}

// newSentinelClient 创建Sentinel模式客户端
func newSentinelClient(cfg *config.RedisConfig) (*redis.Client, error) {
	if len(cfg.SentinelAddrs) == 0 {
		return nil, fmt.Errorf("sentinel_addrs is required for sentinel mode")
	}
	if cfg.MasterName == "" {
		return nil, fmt.Errorf("master_name is required for sentinel mode")
	}

	rdb := redis.NewFailoverClient(&redis.FailoverOptions{
		MasterName:    cfg.MasterName,
		SentinelAddrs: cfg.SentinelAddrs,
		Password:      cfg.Password,
		DB:            cfg.DB,
		PoolSize:      cfg.PoolSize,
		MinIdleConns:  cfg.MinIdleConns,
		DialTimeout:   cfg.DialTimeout,
		ReadTimeout:   cfg.ReadTimeout,
		WriteTimeout:  cfg.WriteTimeout,
	})

	logger.Info("redis sentinel mode",
		"master_name", cfg.MasterName,
		"sentinel_addrs", cfg.SentinelAddrs)
	return rdb, nil
}

// Raw 获取原生redis客户端
func (c *client) Raw() *redis.Client {
	return c.rdb
}

// NewClientFromRaw 包装原生 redis.Client 为 RedisClient。
// 用于脚本/工具场景复用既有连接，不触发 Ping 校验。
func NewClientFromRaw(rdb *redis.Client) RedisClient {
	return &client{rdb: rdb}
}

// Close 关闭连接
func (c *client) Close() error {
	return c.rdb.Close()
}

// Get 获取值
func (c *client) Get(ctx context.Context, key string) *redis.StringCmd {
	return c.rdb.Get(ctx, key)
}

// Set 设置值
func (c *client) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	return c.rdb.Set(ctx, key, value, expiration)
}

// SetEX 设置值并指定过期时间
func (c *client) SetEX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	return c.rdb.SetEx(ctx, key, value, expiration)
}

// SetNX 设置值（仅当key不存在时）
func (c *client) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	return c.rdb.SetNX(ctx, key, value, expiration)
}

// Del 删除key
func (c *client) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	return c.rdb.Del(ctx, keys...)
}

// Exists 检查key是否存在
func (c *client) Exists(ctx context.Context, keys ...string) *redis.IntCmd {
	return c.rdb.Exists(ctx, keys...)
}

// Incr 自增
func (c *client) Incr(ctx context.Context, key string) *redis.IntCmd {
	return c.rdb.Incr(ctx, key)
}

// Expire 设置过期时间
func (c *client) Expire(ctx context.Context, key string, expiration time.Duration) *redis.BoolCmd {
	return c.rdb.Expire(ctx, key, expiration)
}

// SAdd 集合添加
func (c *client) SAdd(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return c.rdb.SAdd(ctx, key, members...)
}

// SRem 集合移除
func (c *client) SRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return c.rdb.SRem(ctx, key, members...)
}

// SMembers 获取集合所有成员
func (c *client) SMembers(ctx context.Context, key string) *redis.StringSliceCmd {
	return c.rdb.SMembers(ctx, key)
}

// SCard 获取集合大小
func (c *client) SCard(ctx context.Context, key string) *redis.IntCmd {
	return c.rdb.SCard(ctx, key)
}

// SIsMember 检查是否是集合成员
func (c *client) SIsMember(ctx context.Context, key string, member interface{}) *redis.BoolCmd {
	return c.rdb.SIsMember(ctx, key, member)
}

// SPop 原子弹出并删除集合中的一个成员
func (c *client) SPop(ctx context.Context, key string) *redis.StringCmd {
	return c.rdb.SPop(ctx, key)
}

// SInter 求多个集合的交集
func (c *client) SInter(ctx context.Context, keys ...string) *redis.StringSliceCmd {
	return c.rdb.SInter(ctx, keys...)
}

// Pipeline 创建Pipeline
func (c *client) Pipeline() redis.Pipeliner {
	return c.rdb.Pipeline()
}

// Eval 执行Lua脚本
func (c *client) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	return c.rdb.Eval(ctx, script, keys, args...)
}

// Scan 扫描key
func (c *client) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	return c.rdb.Scan(ctx, cursor, match, count)
}

// HGet 获取hash字段
func (c *client) HGet(ctx context.Context, key, field string) *redis.StringCmd {
	return c.rdb.HGet(ctx, key, field)
}

// HGetAll 获取hash所有字段
func (c *client) HGetAll(ctx context.Context, key string) *redis.MapStringStringCmd {
	return c.rdb.HGetAll(ctx, key)
}

// HKeys 获取hash所有字段名
func (c *client) HKeys(ctx context.Context, key string) *redis.StringSliceCmd {
	return c.rdb.HKeys(ctx, key)
}

// HSet 设置hash字段
func (c *client) HSet(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	return c.rdb.HSet(ctx, key, values...)
}

// HIncrBy hash字段自增
func (c *client) HIncrBy(ctx context.Context, key, field string, incr int64) *redis.IntCmd {
	return c.rdb.HIncrBy(ctx, key, field, incr)
}

// HDel 删除hash字段
func (c *client) HDel(ctx context.Context, key string, fields ...string) *redis.IntCmd {
	return c.rdb.HDel(ctx, key, fields...)
}

// ZRevRangeWithScores 有序集合倒序获取
func (c *client) ZRevRangeWithScores(ctx context.Context, key string, start, stop int64) *redis.ZSliceCmd {
	return c.rdb.ZRevRangeWithScores(ctx, key, start, stop)
}

// ZAdd 有序集合添加
func (c *client) ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	return c.rdb.ZAdd(ctx, key, members...)
}

// ZRangeByScore 按分数范围获取有序集合成员
func (c *client) ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd {
	return c.rdb.ZRangeByScore(ctx, key, opt)
}

// ZRem 有序集合移除成员
func (c *client) ZRem(ctx context.Context, key string, members ...interface{}) *redis.IntCmd {
	return c.rdb.ZRem(ctx, key, members...)
}

// ZScore 获取有序集合成员分数
func (c *client) ZScore(ctx context.Context, key, member string) *redis.FloatCmd {
	return c.rdb.ZScore(ctx, key, member)
}

// IncrBy 自增指定值
func (c *client) IncrBy(ctx context.Context, key string, value int64) *redis.IntCmd {
	return c.rdb.IncrBy(ctx, key, value)
}

// Decr 自减
func (c *client) Decr(ctx context.Context, key string) *redis.IntCmd {
	return c.rdb.Decr(ctx, key)
}

// ZCard 获取有序集合成员数
func (c *client) ZCard(ctx context.Context, key string) *redis.IntCmd {
	return c.rdb.ZCard(ctx, key)
}

// ZRemRangeByScore 按分数范围移除有序集合成员
func (c *client) ZRemRangeByScore(ctx context.Context, key, min, max string) *redis.IntCmd {
	return c.rdb.ZRemRangeByScore(ctx, key, min, max)
}

// LPop 从列表左侧弹出元素
func (c *client) LPop(ctx context.Context, key string) *redis.StringCmd {
	return c.rdb.LPop(ctx, key)
}

// LPush 从列表左侧推入元素
func (c *client) LPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	return c.rdb.LPush(ctx, key, values...)
}

// RPush 从列表右侧推入元素
func (c *client) RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	return c.rdb.RPush(ctx, key, values...)
}

// LRange 获取列表指定范围内的元素
func (c *client) LRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	return c.rdb.LRange(ctx, key, start, stop)
}

// BRPop 从列表右侧阻塞弹出元素
func (c *client) BRPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd {
	return c.rdb.BRPop(ctx, timeout, keys...)
}

// Subscribe 订阅频道
func (c *client) Subscribe(ctx context.Context, channels ...string) *redis.PubSub {
	return c.rdb.Subscribe(ctx, channels...)
}

// Publish 发布消息
func (c *client) Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd {
	return c.rdb.Publish(ctx, channel, message)
}
