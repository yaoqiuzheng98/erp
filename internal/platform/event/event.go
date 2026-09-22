package event

import (
	"context"
	"log/slog"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Event 进程内事件；Topic 约定 {domain}.{action}，如 sales.order.approved。
type Event struct {
	Topic    string
	TenantID bson.ObjectID
	Payload  any
}

type Handler func(ctx context.Context, e Event) error

// Bus 观察者模式实现：插件间通过订阅事件联动，禁止直接 import。
type Bus struct {
	mu   sync.RWMutex
	subs map[string][]Handler
}

func NewBus() *Bus {
	return &Bus{subs: map[string][]Handler{}}
}

func (b *Bus) Subscribe(topic string, h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], h)
}

// Publish 同步分发；单个订阅者 panic 不影响其他订阅者。
func (b *Bus) Publish(ctx context.Context, e Event) {
	b.mu.RLock()
	handlers := append([]Handler(nil), b.subs[e.Topic]...)
	b.mu.RUnlock()
	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("event handler panic", "topic", e.Topic, "err", r)
				}
			}()
			if err := h(ctx, e); err != nil {
				slog.Error("event handler error", "topic", e.Topic, "err", err)
			}
		}()
	}
}
