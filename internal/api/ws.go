package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// Topics a client may subscribe to. An explicit allowlist keeps a client from
// probing for topics it should not see.
var allowedTopics = map[string]bool{
	"host.metrics":    true,
	"docker.services": true,
	"systemd.units":   true,
	"jobs":            true,
	"probe.storage":   true,
	"probe.edge":      true,
	"probe.updates":   true,
	"games":           true,
}

const (
	pingInterval = 30 * time.Second
	writeTimeout = 10 * time.Second
)

// handleWS streams hub messages for the requested topics.
//
// The upgrade is same-origin only: coder/websocket rejects a cross-origin
// handshake by default, which matters because the browser attaches the session
// cookie to a WebSocket upgrade regardless of who initiated it.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	topics := parseTopics(r.URL.Query().Get("topics"))
	if len(topics) == 0 {
		writeErr(w, http.StatusBadRequest, "name at least one topic to subscribe to")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept has already written a response.
	}
	defer conn.CloseNow()

	ctx := r.Context()
	sub := s.hub.Subscribe(topics, 64)
	defer sub.Close()

	// A reader is required for the connection to observe close frames and for
	// pongs to be processed.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return

		case msg, ok := <-sub.C():
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := wsjson.Write(writeCtx, conn, msg)
			cancel()
			if err != nil {
				return
			}

		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func parseTopics(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] || !allowedTopics[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// snapshot serves the most recent message on a topic over plain HTTP, so a
// page can render before its socket is up.
func (s *Server) snapshot(topic string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		msg, ok := s.hub.Retained(topic)
		if !ok {
			writeJSON(w, http.StatusOK, map[string]any{"pending": true, "topic": topic})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(msg.Data)
	}
}

// hubStats is exposed on the health endpoint to make a wedged collector or a
// leaking subscription visible without attaching a debugger.
func (s *Server) hubStats() map[string]any {
	subs, topics, dropped := s.hub.Stats()
	return map[string]any{"subscribers": subs, "topics": topics, "dropped": dropped}
}
