package event

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPublishDispatch(t *testing.T) {
	b := NewBus()
	var got int
	b.Subscribe("t.a", func(ctx context.Context, e Event) error {
		got++
		return nil
	})
	b.Publish(context.Background(), Event{Topic: "t.a", TenantID: bson.NewObjectID()})
	if got != 1 {
		t.Fatalf("handler not called")
	}
}

func TestPublishPanicIsolated(t *testing.T) {
	b := NewBus()
	var ok bool
	b.Subscribe("t.b", func(ctx context.Context, e Event) error { panic("boom") })
	b.Subscribe("t.b", func(ctx context.Context, e Event) error { ok = true; return nil })
	b.Publish(context.Background(), Event{Topic: "t.b"})
	if !ok {
		t.Fatalf("panic in one subscriber must not break others")
	}
}
