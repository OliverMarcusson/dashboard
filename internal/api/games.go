package api

import (
	"encoding/json"
	"net/http"

	"github.com/olivermarcusson/dashboard/internal/auth"
	"github.com/olivermarcusson/dashboard/internal/game"
)

// gameConsole runs one command against a discovered game server.
//
// The command goes to whichever adapter claimed the container, so a new game
// gains a working console without this handler changing.
func (s *Server) gameConsole(w http.ResponseWriter, r *http.Request) {
	if s.games == nil {
		writeErr(w, http.StatusServiceUnavailable, "game discovery is not running")
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request")
		return
	}

	server, ok := s.games.Server(r.PathValue("container"))
	if !ok {
		writeErr(w, http.StatusNotFound, "no game server by that name")
		return
	}
	adapter, ok := game.AdapterFor(server.Adapter)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "the adapter for this server is gone")
		return
	}

	out, err := adapter.Console(r.Context(), server, req.Command)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"output": "", "error": err.Error()})
		return
	}

	actor, _ := auth.FromContext(r.Context())
	detail, _ := json.Marshal(map[string]string{"server": server.Container, "command": req.Command})
	s.db.Audit(r.Context(), "game.console", actor.Username, string(detail))

	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}
