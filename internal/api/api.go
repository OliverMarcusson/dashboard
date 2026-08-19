// Package api assembles the HTTP surface: authentication, the protected API,
// and the embedded frontend.
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"

	"github.com/olivermarcusson/dashboard/internal/action"
	"github.com/olivermarcusson/dashboard/internal/auth"
	"github.com/olivermarcusson/dashboard/internal/collect/games"
	"github.com/olivermarcusson/dashboard/internal/hub"
	"github.com/olivermarcusson/dashboard/internal/store"
)

type Server struct {
	db     *store.DB
	auth   *auth.Service
	hub    *hub.Hub
	runner *action.Runner
	docker *client.Client
	games  *games.Collector
}

func New(db *store.DB, a *auth.Service, h *hub.Hub, runner *action.Runner,
	docker *client.Client, gameCollector *games.Collector) *Server {
	return &Server{db: db, auth: a, hub: h, runner: runner, docker: docker, games: gameCollector}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Handler builds the complete mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public: liveness and the authentication ceremonies themselves.
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "hub": s.hubStats()})
	})
	s.auth.Routes(mux)

	// Protected: everything else under /api.
	protected := http.NewServeMux()
	protected.HandleFunc("GET /api/security/passkeys", s.listPasskeys)
	protected.HandleFunc("PATCH /api/security/passkeys/{id}", s.patchPasskey)
	protected.HandleFunc("GET /api/audit", s.listAudit)
	protected.HandleFunc("GET /api/overview", s.snapshot("host.metrics"))
	protected.HandleFunc("GET /api/services", s.snapshot("docker.services"))
	protected.HandleFunc("GET /api/units", s.snapshot("systemd.units"))
	protected.HandleFunc("GET /api/storage", s.snapshot("probe.storage"))
	protected.HandleFunc("GET /api/edge", s.snapshot("probe.edge"))
	protected.HandleFunc("GET /api/updates", s.snapshot("probe.updates"))
	protected.HandleFunc("GET /api/games", s.snapshot("games"))
	protected.HandleFunc("POST /api/games/{container}/console", s.gameConsole)
	protected.HandleFunc("GET /api/metrics/range", s.metricsRange)
	protected.HandleFunc("GET /api/actions", s.listActions)
	protected.HandleFunc("POST /api/actions/run", s.runAction)
	protected.HandleFunc("GET /api/jobs", s.listJobs)
	protected.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.Handle("/api/", s.auth.Require(protected))

	// Live stream. Same session cookie, same origin.
	mux.Handle("GET /ws", s.auth.Require(http.HandlerFunc(s.handleWS)))
	mux.Handle("GET /ws/logs", s.auth.Require(http.HandlerFunc(s.handleLogs)))

	// Everything else is the single-page app.
	mux.Handle("/", spaHandler())

	return securityHeaders(mux)
}

// securityHeaders applies the headers a private dashboard should send even
// though Caddy terminates TLS in front of it.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-cache")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listPasskeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.auth.ListPasskeys(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read passkeys")
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

func (s *Server) patchPasskey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name    *string `json:"name"`
		Revoked *bool   `json:"revoked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request")
		return
	}
	actor, _ := auth.FromContext(r.Context())

	if body.Name != nil {
		if err := s.auth.RenamePasskey(r.Context(), id, *body.Name); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.db.Audit(r.Context(), "passkey.renamed", actor.Username, "{}")
	}
	if body.Revoked != nil && *body.Revoked {
		if err := s.auth.RevokePasskey(r.Context(), id); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.db.Audit(r.Context(), "passkey.revoked", actor.Username, "{}")
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, event, actor, detail_json, created_at
		   FROM audit_events ORDER BY created_at DESC LIMIT 200`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read the audit log")
		return
	}
	defer rows.Close()

	type event struct {
		ID        string          `json:"id"`
		Event     string          `json:"event"`
		Actor     string          `json:"actor"`
		Detail    json.RawMessage `json:"detail"`
		CreatedAt string          `json:"created_at"`
	}
	out := []event{}
	for rows.Next() {
		var e event
		var detail string
		if err := rows.Scan(&e.ID, &e.Event, &e.Actor, &detail, &e.CreatedAt); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not read the audit log")
			return
		}
		e.Detail = json.RawMessage(detail)
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

// metricsRange serves stored history for one series. The tier is chosen by the
// store from the window requested.
func (s *Server) metricsRange(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metric := q.Get("metric")
	if metric == "" {
		writeErr(w, http.StatusBadRequest, "name the metric to read")
		return
	}
	kind := q.Get("kind")
	if kind == "" {
		kind = "host"
	}

	to := time.Now().UTC()
	from := to.Add(-time.Hour)
	if raw := q.Get("minutes"); raw != "" {
		if mins, err := strconv.Atoi(raw); err == nil && mins > 0 && mins <= 525600 {
			from = to.Add(-time.Duration(mins) * time.Minute)
		}
	}

	points, err := s.db.QueryRange(r.Context(), kind, q.Get("subject"), metric, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read metric history")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": kind, "subject": q.Get("subject"), "metric": metric,
		"from": from.Unix(), "to": to.Unix(), "points": points,
	})
}
