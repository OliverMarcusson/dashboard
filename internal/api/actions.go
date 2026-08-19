package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/olivermarcusson/dashboard/internal/action"
	"github.com/olivermarcusson/dashboard/internal/auth"
)

// listActions returns the actions available on one target. They are derived
// from the target's kind, so this never consults a list.
func (s *Server) listActions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := action.Kind(q.Get("kind"))
	target := q.Get("target")

	if target == "" {
		writeErr(w, http.StatusBadRequest, "name the target to list actions for")
		return
	}
	switch kind {
	case action.KindContainer, action.KindStack, action.KindUnit:
	default:
		writeErr(w, http.StatusBadRequest, "kind must be container, stack, or unit")
		return
	}
	writeJSON(w, http.StatusOK, action.For(kind, target, q.Get("name")))
}

type runRequest struct {
	ActionID  string `json:"action_id"`
	Confirmed bool   `json:"confirmed"`
}

// runAction executes an action. Confirmation is part of the protocol, not just
// the interface: every action requires it, uniformly.
func (s *Server) runAction(w http.ResponseWriter, r *http.Request) {
	if s.runner == nil {
		writeErr(w, http.StatusServiceUnavailable, "actions are unavailable while Docker is unreachable")
		return
	}
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request")
		return
	}
	if req.ActionID == "" {
		writeErr(w, http.StatusBadRequest, "name the action to run")
		return
	}
	if !req.Confirmed {
		writeErr(w, http.StatusBadRequest, "this action needs to be confirmed before it runs")
		return
	}

	actor, _ := auth.FromContext(r.Context())
	job, err := s.runner.Run(r.Context(), req.ActionID, actor.Username)
	if err != nil {
		if job == nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// The action ran and failed; that is a result, not a request error.
		writeJSON(w, http.StatusOK, job)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	if s.runner == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := s.runner.Recent(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not read job history")
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	if s.runner == nil {
		writeErr(w, http.StatusNotFound, "no such job")
		return
	}
	job, err := s.runner.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such job")
		return
	}
	writeJSON(w, http.StatusOK, job)
}
