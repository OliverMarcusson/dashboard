package hub

import (
	"encoding/json"
	"testing"
	"time"
)

func recv(t *testing.T, sub *Subscription) Message {
	t.Helper()
	select {
	case msg, ok := <-sub.C():
		if !ok {
			t.Fatal("subscription closed unexpectedly")
		}
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a message")
		return Message{}
	}
}

func TestPublishReachesMatchingSubscribers(t *testing.T) {
	h := New()
	want := h.Subscribe([]string{"host.metrics"}, 4)
	defer want.Close()
	other := h.Subscribe([]string{"docker.services"}, 4)
	defer other.Close()

	h.Publish("host.metrics", map[string]int{"cpu": 34})

	msg := recv(t, want)
	if msg.Topic != "host.metrics" {
		t.Errorf("topic = %q", msg.Topic)
	}
	var payload map[string]int
	json.Unmarshal(msg.Data, &payload)
	if payload["cpu"] != 34 {
		t.Errorf("cpu = %d, want 34", payload["cpu"])
	}

	select {
	case m := <-other.C():
		t.Errorf("unrelated subscriber received %q", m.Topic)
	default:
	}
}

func TestSubscribeReplaysRetainedState(t *testing.T) {
	h := New()
	h.Publish("host.metrics", map[string]int{"cpu": 12})

	// Subscribing after the fact must still yield the latest snapshot.
	sub := h.Subscribe([]string{"host.metrics"}, 4)
	defer sub.Close()

	var payload map[string]int
	json.Unmarshal(recv(t, sub).Data, &payload)
	if payload["cpu"] != 12 {
		t.Errorf("cpu = %d, want the retained 12", payload["cpu"])
	}
}

func TestSlowSubscriberDropsRatherThanBlocks(t *testing.T) {
	h := New()
	sub := h.Subscribe([]string{"t"}, 1)
	defer sub.Close()

	// One retained replay plus many more than the buffer holds. If Publish
	// blocked on a full subscriber this would deadlock the test.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Publish("t", i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	if _, _, dropped := h.Stats(); dropped == 0 {
		t.Error("expected dropped messages to be counted")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	h := New()
	sub := h.Subscribe([]string{"t"}, 1)
	sub.Close()
	sub.Close()

	if subs, _, _ := h.Stats(); subs != 0 {
		t.Errorf("subscribers = %d, want 0", subs)
	}
}
