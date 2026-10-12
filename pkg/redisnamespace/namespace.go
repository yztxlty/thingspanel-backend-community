package redisnamespace

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

// 未配置时保持既有环境键名；生产通过显式前缀隔离共享实例。
func Key(key string) string {
	prefix := os.Getenv("YGSOUL_REDIS_KEY_PREFIX")
	if prefix == "" || strings.HasPrefix(key, prefix) {
		return key
	}
	return prefix + key
}
func Original(key string) string {
	return strings.TrimPrefix(key, os.Getenv("YGSOUL_REDIS_KEY_PREFIX"))
}

type Hook struct{ prefixValue string }

func New(prefix string) (*Hook, error) {
	if !strings.HasPrefix(prefix, "yomi:") || !strings.HasSuffix(prefix, ":") || strings.ContainsAny(prefix, "*?[]\\ \r\n") {
		return nil, fmt.Errorf("Redis前缀必须以yomi:开头、冒号结尾且不含通配符或空白")
	}
	return &Hook{prefixValue: prefix}, nil
}
func (h *Hook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h *Hook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if err := h.prefix(cmd); err != nil {
			return err
		}
		return next(ctx, cmd)
	}
}
func (h *Hook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			if err := h.prefix(cmd); err != nil {
				return err
			}
		}
		return next(ctx, cmds)
	}
}
func (h *Hook) prefix(cmd redis.Cmder) error {
	args := cmd.Args()
	indexes := []int{}
	switch strings.ToLower(cmd.Name()) {
	case "ping", "hello", "auth", "select", "client", "multi", "exec", "discard":
		return nil
	case "get", "set", "setnx", "incr", "decr", "expire", "pexpire", "ttl", "pttl", "hset", "hget", "hgetall", "hincrby", "hdel", "lpush", "rpush", "lrange", "llen", "ltrim", "lrem", "lset", "publish":
		indexes = []int{1}
	case "del", "exists":
		for i := 1; i < len(args); i++ {
			indexes = append(indexes, i)
		}
	case "scan":
		for i := 2; i+1 < len(args); i += 2 {
			if strings.EqualFold(fmt.Sprint(args[i]), "match") {
				indexes = append(indexes, i+1)
			}
		}
		if len(indexes) != 1 {
			return fmt.Errorf("共享Redis扫描必须限定前缀匹配")
		}
	default:
		return fmt.Errorf("共享Redis不允许未审阅命令: %s", cmd.Name())
	}
	for _, i := range indexes {
		if i >= len(args) {
			return fmt.Errorf("Redis命令缺少键")
		}
		key, ok := args[i].(string)
		if !ok {
			return fmt.Errorf("Redis键必须是字符串")
		}
		if !strings.HasPrefix(key, h.prefixValue) {
			args[i] = h.prefixValue + key
		}
	}
	return nil
}
