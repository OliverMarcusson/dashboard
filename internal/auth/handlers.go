package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Routes mounts the public authentication endpoints. Everything here is
// deliberately reachable without a session.
func (s *Service) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/session", s.handleSession)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /api/auth/enroll/start", s.handleEnrollStart)
	mux.HandleFunc("POST /api/auth/enroll/finish", s.handleEnrollFinish)
	mux.HandleFunc("POST /api/auth/login/start", s.handleLoginStart)
	mux.HandleFunc("POST /api/auth/login/finish", s.handleLoginFinish)
}

func (s *Service) handleSession(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.lookupSession(r.Context(), r); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": true,
			"username":      id.Username,
		})
		return
	}

	// Tell the browser whether enrollment is even possible, so the sign-in page
	// can point a first-time visitor at the right place.
	var enrolled int
	s.db.QueryRowContext(r.Context(),
		`SELECT count(*) FROM passkeys WHERE revoked_at IS NULL`).Scan(&enrolled)

	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": false,
		"has_passkeys":  enrolled > 0,
	})
}

func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		s.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE token_hash = ?`, hash(c.Value))
	}
	s.clearSession(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---------- enrollment ----------

type enrollStartReq struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
}

func (s *Service) handleEnrollStart(w http.ResponseWriter, r *http.Request) {
	var req enrollStartReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request")
		return
	}
	if err := s.consumeEnrollmentCode(r.Context(), req.Code); err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	u, err := s.loadUser(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the account")
		return
	}

	// Exclude credentials already registered so an authenticator offers to
	// create a new passkey rather than silently reusing one.
	exclude := make([]protocol.CredentialDescriptor, 0, len(u.creds))
	for _, c := range u.creds {
		exclude = append(exclude, c.Descriptor())
	}

	options, sd, err := s.wa.BeginRegistration(u,
		webauthn.WithExclusions(exclude),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
	)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start registration: "+err.Error())
		return
	}

	stateID, err := s.putState(r.Context(), "enroll:"+req.DeviceName, sd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start registration")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state_id": stateID, "options": options})
}

type finishReq struct {
	StateID    string          `json:"state_id"`
	Credential json.RawMessage `json:"credential"`
}

func (s *Service) handleEnrollFinish(w http.ResponseWriter, r *http.Request) {
	var req finishReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request")
		return
	}

	sd, name, err := s.takeStatePrefix(r.Context(), "enroll:", req.StateID)
	if err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(req.Credential))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the authenticator response could not be read")
		return
	}

	u, err := s.loadUser(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the account")
		return
	}

	cred, err := s.wa.CreateCredential(u, *sd, parsed)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "registration failed: "+err.Error())
		return
	}

	if name == "" {
		name = "Passkey"
	}
	if err := s.SaveCredential(r.Context(), u.id, name, cred); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.db.Audit(r.Context(), "passkey.enrolled", u.username, fmt.Sprintf(`{"name":%q}`, name))

	if err := s.issueSession(r.Context(), w, r, u.id); err != nil {
		writeErr(w, http.StatusInternalServerError, "registered, but the session could not be created")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": u.username})
}

// ---------- login ----------

func (s *Service) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	u, err := s.loadUser(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the account")
		return
	}
	if len(u.creds) == 0 {
		writeErr(w, http.StatusForbidden, "no passkey is enrolled yet; run `dashboardd enroll` on the server")
		return
	}

	options, sd, err := s.wa.BeginLogin(u)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start sign-in: "+err.Error())
		return
	}
	stateID, err := s.putState(r.Context(), "login", sd)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not start sign-in")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state_id": stateID, "options": options})
}

func (s *Service) handleLoginFinish(w http.ResponseWriter, r *http.Request) {
	var req finishReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request")
		return
	}

	sd, err := s.takeState(r.Context(), "login", req.StateID)
	if err != nil {
		writeErr(w, http.StatusForbidden, err.Error())
		return
	}

	parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(req.Credential))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "the authenticator response could not be read")
		return
	}

	u, err := s.loadUser(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load the account")
		return
	}

	cred, err := s.wa.ValidateLogin(u, *sd, parsed)
	if err != nil {
		writeErr(w, http.StatusForbidden, "that passkey was not accepted")
		return
	}
	if cred.Authenticator.CloneWarning {
		s.db.Audit(r.Context(), "passkey.clone_warning", u.username, "{}")
	}
	if err := s.touchCredential(r.Context(), cred); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not record the sign-in")
		return
	}
	if err := s.issueSession(r.Context(), w, r, u.id); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not create the session")
		return
	}
	s.db.Audit(r.Context(), "auth.login", u.username, "{}")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": u.username})
}
