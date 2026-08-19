// Package hub fans collector output out to WebSocket subscribers.
//
// Collectors publish to a topic; subscribers receive every message on the
// topics they asked for. The hub also remembers the last message per topic so
// a client that connects mid-stream renders immediately instead of waiting for
// the next tick.
package hub

import (
	"encoding/json"
	"sync"
	"time"
)

// Message is one published update.
type Message struct {
	Topic string          `json:"topic"`
	At    time.Time       `json:"at"`
	Data  json.RawMessage `json:"data"`
}

// Subscription delivers messages for a set of topics.
type Subscription struct {
	id     int64
	topics map[string]bool
	ch     chan Message
	hub    *Hub
	once   sync.Once
}

// C is the receive channel. It is closed when the subscription is closed.
func (s *Subscription) C() <-chan Message { return s.ch }

// Close detaches the subscription. Safe to call more than once.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, s.id)
		s.hub.mu.Unlock()
		close(s.ch)
	})
}

type Hub struct {
	mu       sync.RWMutex
	subs     map[int64]*Subscription
	nextID   int64
	retained map[string]Message

	// Dropped counts messages discarded because a subscriber was not keeping
	// up. Exposed for the health endpoint rather than logged per occurrence.
	dropped uint64
}

func New() *Hub {
	return &Hub{
		subs:     make(map[int64]*Subscription),
		retained: make(map[string]Message),
	}
}

// Publish encodes data and delivers it to every matching subscriber.
//
// Delivery is non-blocking: a subscriber whose buffer is full loses the
// message rather than stalling the collector. Metrics are a stream of
// snapshots, so the next one supersedes whatever was dropped.
func (h *Hub) Publish(topic string, data any) {
	blob, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := Message{Topic: topic, At: time.Now().UTC(), Data: blob}

	h.mu.Lock()
	h.retained[topic] = msg
	for _, sub := range h.subs {
		if !sub.topics[topic] {
			continue
		}
		select {
		case sub.ch <- msg:
		default:
			h.dropped++
		}
	}
	h.mu.Unlock()
}

// Subscribe attaches to the given topics. The returned subscription is
// pre-loaded with the most recent message on each topic that has one.
func (h *Hub) Subscribe(topics []string, buffer int) *Subscription {
	if buffer < 1 {
		buffer = 32
	}
	set := make(map[string]bool, len(topics))
	for _, t := range topics {
		set[t] = true
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextID++
	sub := &Subscription{
		id:     h.nextID,
		topics: set,
		ch:     make(chan Message, buffer),
		hub:    h,
	}
	h.subs[sub.id] = sub

	// Replay retained state so the client has something to draw at once.
	for topic := range set {
		if msg, ok := h.retained[topic]; ok {
			select {
			case sub.ch <- msg:
			default:
			}
		}
	}
	return sub
}

// Retained returns the most recent message on a topic, for REST callers that
// want a snapshot without opening a socket.
func (h *Hub) Retained(topic string) (Message, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	msg, ok := h.retained[topic]
	return msg, ok
}

// Stats reports hub health.
func (h *Hub) Stats() (subscribers int, topics int, dropped uint64) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs), len(h.retained), h.dropped
}
