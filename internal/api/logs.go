package api

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/olivermarcusson/dashboard/internal/logs"
)

// handleLogs tails one container or unit over a WebSocket.
//
// Logs get their own socket rather than a hub topic: a tail is per-viewer and
// per-target, so there is nothing to fan out.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src := logs.Source{Kind: q.Get("kind"), Target: q.Get("target")}
	src.Tail, _ = strconv.Atoi(q.Get("tail"))

	if src.Kind != "container" && src.Kind != "unit" {
		writeErr(w, http.StatusBadRequest, "kind must be container or unit")
		return
	}
	if src.Target == "" {
		writeErr(w, http.StatusBadRequest, "name the container or unit to tail")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// The reader exists so a client closing the tab cancels the tail promptly
	// instead of leaving journalctl running until the next write fails.
	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	lines := make(chan logs.Line, 256)
	go func() {
		if err := logs.Stream(ctx, s.docker, src, lines); err != nil && ctx.Err() == nil {
			writeCtx, c := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			wsjson.Write(writeCtx, conn, logs.Line{Text: err.Error(), Stream: "error"})
			c()
		}
	}()

	for line := range lines {
		writeCtx, c := context.WithTimeout(ctx, 10*time.Second)
		err := wsjson.Write(writeCtx, conn, line)
		c()
		if err != nil {
			cancel()
			// Drain so the producer is not left blocked on a full channel.
			for range lines {
			}
			return
		}
	}
	conn.Close(websocket.StatusNormalClosure, "")
}
