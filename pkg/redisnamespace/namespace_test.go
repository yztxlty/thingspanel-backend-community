package redisnamespace

import (
	"context"
	"github.com/redis/go-redis/v9"
	"reflect"
	"testing"
)

func TestNamespaceCommands(t *testing.T) {
	h, err := New("yomi:prod:device-cloud:")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ in, want []interface{} }{
		{[]interface{}{"set", "token", "value"}, []interface{}{"set", "yomi:prod:device-cloud:token", "value"}},
		{[]interface{}{"del", "a", "b"}, []interface{}{"del", "yomi:prod:device-cloud:a", "yomi:prod:device-cloud:b"}},
		{[]interface{}{"hset", "hash", "field", "value"}, []interface{}{"hset", "yomi:prod:device-cloud:hash", "field", "value"}},
		{[]interface{}{"scan", 0, "match", "device:*", "count", 100}, []interface{}{"scan", 0, "match", "yomi:prod:device-cloud:device:*", "count", 100}},
		{[]interface{}{"publish", "device:1:status", "payload"}, []interface{}{"publish", "yomi:prod:device-cloud:device:1:status", "payload"}},
		{[]interface{}{"get", "yomi:prod:device-cloud:a"}, []interface{}{"get", "yomi:prod:device-cloud:a"}},
	}
	for _, c := range cases {
		cmd := redis.NewCmd(context.Background(), c.in...)
		if err := h.prefix(cmd); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cmd.Args(), c.want) {
			t.Fatalf("got %v want %v", cmd.Args(), c.want)
		}
	}
	for _, args := range [][]interface{}{{"flushdb"}, {"client", "kill", "type", "normal"}, {"config", "set", "notify-keyspace-events", "Ex"}, {"eval", "return 1", 0}, {"scan", 0}} {
		if err := h.prefix(redis.NewCmd(context.Background(), args...)); err == nil {
			t.Fatalf("unsafe command allowed: %v", args)
		}
	}
}

func TestNamespacePipelineAndCompatibility(t *testing.T) {
	h, _ := New("yomi:prod:device-cloud:")
	called := false
	next := h.ProcessPipelineHook(func(_ context.Context, cmds []redis.Cmder) error { called = true; return nil })
	cmds := []redis.Cmder{redis.NewCmd(context.Background(), "lpush", "logs", "entry"), redis.NewCmd(context.Background(), "expire", "logs", 30)}
	if err := next(context.Background(), cmds); err != nil {
		t.Fatal(err)
	}
	if !called || cmds[0].Args()[1] != "yomi:prod:device-cloud:logs" || cmds[1].Args()[1] != "yomi:prod:device-cloud:logs" {
		t.Fatal("pipeline not namespaced")
	}
	t.Setenv("YGSOUL_REDIS_KEY_PREFIX", "yomi:prod:device-cloud:")
	if Key("device:1:status") != "yomi:prod:device-cloud:device:1:status" || Original(Key("device:1:status")) != "device:1:status" {
		t.Fatal("channel namespace mismatch")
	}
	t.Setenv("YGSOUL_REDIS_KEY_PREFIX", "")
	if Key("old") != "old" {
		t.Fatal("legacy keys changed")
	}
	if _, err := New("other:"); err == nil {
		t.Fatal("invalid production prefix accepted")
	}
}
