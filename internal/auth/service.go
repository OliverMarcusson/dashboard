package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/olivermarcusson/dashboard/internal/config"
	"github.com/olivermarcusson/dashboard/internal/store"
)

const (
	sessionCookie = "dash_session"
	enrollTTL     = 15 * time.Minute
	ceremonyTTL   = 5 * time.Minute
)

func newID() string { return uuid.NewString() }

type Service struct {
	db         *store.DB
	wa         *webauthn.WebAuthn
	username   string
	sessionTTL time.Duration
	secure     bool
}

// New builds the auth service and ensures the account row exists.
func New(ctx context.Context, db *store.DB, cfg config.Config) (*Service, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPDisplayName: cfg.RPDisplayName,
		RPID:          cfg.RPID,
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn config: %w", err)
	}

	s := &Service{
		db:         db,
		wa:         wa,
		username:   cfg.Username,
		sessionTTL: cfg.SessionTTL,
		secure:     cfg.SecureCookies,
	}
	if err := s.ensureUser(ctx, cfg.Username); err != nil {
		return nil, err
	}
	return s, nil
}

// ensureUser creates the account if it is missing and gives it a user handle
// if it has none.
func (s *Service) ensureUser(ctx context.Context, username string) error {
	handle := make([]byte, 16)
	if _, err := rand.Read(handle); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO users (id, webauthn_id, username, display_name) VALUES (?, ?, ?, ?)
		 ON CONFLICT(username) DO NOTHING`,
		newID(), handle, username, username); err != nil {
		return fmt.Errorf("ensure user: %w", err)
	}
	// A row that predates the handle column gets one now, before any
	// credential is registered against it.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET webauthn_id = ? WHERE username = ? AND webauthn_id IS NULL`,
		handle, username); err != nil {
		return fmt.Errorf("backfill user handle: %w", err)
	}
	return nil
}

// SetUserHandle pins the account's WebAuthn user handle. Used by the legacy
// import to adopt the handle the existing passkeys were registered against.
func (s *Service) SetUserHandle(ctx context.Context, handle []byte) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET webauthn_id = ? WHERE username = ?`, handle, s.username)
	return err
}

// ---------- ceremony state ----------

func (s *Service) putState(ctx context.Context, kind string, sd *webauthn.SessionData) (string, error) {
	blob, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	id := newID()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO webauthn_states (id, kind, state_json, expires_at) VALUES (?, ?, ?, ?)`,
		id, kind, string(blob), time.Now().UTC().Add(ceremonyTTL).Format(time.RFC3339))
	return id, err
}

// takeState consumes a ceremony state. It is single-use: a replayed state_id
// finds nothing.
func (s *Service) takeState(ctx context.Context, kind, id string) (*webauthn.SessionData, error) {
	var blob, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT state_json, expires_at FROM webauthn_states WHERE id = ? AND kind = ?`,
		id, kind).Scan(&blob, &expires)
	if err != nil {
		return nil, errors.New("this sign-in attempt expired; start again")
	}
	s.db.ExecContext(ctx, `DELETE FROM webauthn_states WHERE id = ?`, id)

	if exp, err := time.Parse(time.RFC3339, expires); err == nil && time.Now().UTC().After(exp) {
		return nil, errors.New("this sign-in attempt expired; start again")
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal([]byte(blob), &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

// takeStatePrefix consumes a ceremony state whose kind begins with prefix,
// returning the state and whatever the kind carried after it (the device name
// chosen at enrollment start).
func (s *Service) takeStatePrefix(ctx context.Context, prefix, id string) (*webauthn.SessionData, string, error) {
	var kind, blob, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT kind, state_json, expires_at FROM webauthn_states WHERE id = ?`,
		id).Scan(&kind, &blob, &expires)
	if err != nil || !strings.HasPrefix(kind, prefix) {
		return nil, "", errors.New("this registration attempt expired; start again")
	}
	s.db.ExecContext(ctx, `DELETE FROM webauthn_states WHERE id = ?`, id)

	if exp, err := time.Parse(time.RFC3339, expires); err == nil && time.Now().UTC().After(exp) {
		return nil, "", errors.New("this registration attempt expired; start again")
	}
	var sd webauthn.SessionData
	if err := json.Unmarshal([]byte(blob), &sd); err != nil {
		return nil, "", err
	}
	return &sd, strings.TrimPrefix(kind, prefix), nil
}

// ---------- enrollment codes ----------

var codeAlphabet = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

func hash(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }

func normalizeCode(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
}

// CreateEnrollmentCode mints a one-time code that authorizes registering one
// new passkey. Only its hash is stored.
func (s *Service) CreateEnrollmentCode(ctx context.Context) (string, time.Time, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	code := codeAlphabet.EncodeToString(raw)[:12]
	expires := time.Now().UTC().Add(enrollTTL)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO enrollment_codes (id, code_hash, expires_at) VALUES (?, ?, ?)`,
		newID(), hash(code), expires.Format(time.RFC3339)); err != nil {
		return "", time.Time{}, err
	}
	return code[:4] + "-" + code[4:8] + "-" + code[8:], expires, nil
}

// consumeEnrollmentCode validates and burns a code in one step.
func (s *Service) consumeEnrollmentCode(ctx context.Context, code string) error {
	code = normalizeCode(code)
	if code == "" {
		return errors.New("enter the enrollment code")
	}

	var id, expires string
	var used *string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, expires_at, used_at FROM enrollment_codes WHERE code_hash = ?`,
		hash(code)).Scan(&id, &expires, &used)
	if err != nil {
		// Constant-time-ish: the lookup already leaked timing, but the message must not
		// distinguish "wrong code" from "expired code".
		return errors.New("that code is not valid; generate a new one with `dashboardd enroll`")
	}
	if used != nil {
		return errors.New("that code was already used; generate a new one with `dashboardd enroll`")
	}
	if exp, err := time.Parse(time.RFC3339, expires); err == nil && time.Now().UTC().After(exp) {
		return errors.New("that code expired; generate a new one with `dashboardd enroll`")
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE enrollment_codes SET used_at = datetime('now') WHERE id = ? AND used_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("that code was already used; generate a new one with `dashboardd enroll`")
	}
	return nil
}

// ---------- sessions ----------

func (s *Service) issueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	expires := time.Now().UTC().Add(s.sessionTTL)

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (id, token_hash, user_id, user_agent, expires_at) VALUES (?, ?, ?, ?, ?)`,
		newID(), hash(token), userID, r.UserAgent(), expires.Format(time.RFC3339)); err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Service) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Identity is the authenticated caller.
type Identity struct {
	UserID   string
	Username string
}

type ctxKey struct{}

// FromContext returns the identity attached by Require, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}

func (s *Service) lookupSession(ctx context.Context, r *http.Request) (Identity, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return Identity{}, false
	}

	var (
		id       Identity
		sid      string
		expires  string
		tokHash  []byte
		wantHash = hash(c.Value)
	)
	err = s.db.QueryRowContext(ctx,
		`SELECT s.id, s.token_hash, s.expires_at, u.id, u.username
		   FROM sessions s JOIN users u ON u.id = s.user_id
		  WHERE s.token_hash = ?`, wantHash).Scan(&sid, &tokHash, &expires, &id.UserID, &id.Username)
	if err != nil {
		return Identity{}, false
	}
	if subtle.ConstantTimeCompare(tokHash, wantHash) != 1 {
		return Identity{}, false
	}
	if exp, err := time.Parse(time.RFC3339, expires); err == nil && time.Now().UTC().After(exp) {
		s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sid)
		return Identity{}, false
	}
	return id, true
}

// Require rejects unauthenticated requests and attaches the identity otherwise.
func (s *Service) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := s.lookupSession(r.Context(), r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "sign in with your passkey")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// UserID returns the account's identifier, creating nothing.
func (s *Service) UserID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, s.username).Scan(&id)
	return id, err
}
