// Package auth implements passkey-only authentication: WebAuthn ceremonies,
// credential storage, and session cookies.
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// user adapts the single dashboard account to the webauthn.User interface.
type user struct {
	id          string
	handle      []byte
	username    string
	displayName string
	creds       []webauthn.Credential
}

// WebAuthnID returns the stored user handle. It is deliberately not derived
// from the row id: authenticators keep this value alongside the credential and
// return it on every assertion, so it has to survive a rebuild of the database.
func (u *user) WebAuthnID() []byte                         { return u.handle }
func (u *user) WebAuthnName() string                       { return u.username }
func (u *user) WebAuthnDisplayName() string                { return u.displayName }
func (u *user) WebAuthnCredentials() []webauthn.Credential { return u.creds }

// loadUser fetches the account and its live (non-revoked) credentials.
func (s *Service) loadUser(ctx context.Context) (*user, error) {
	u := &user{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, webauthn_id, username, display_name FROM users WHERE username = ?`,
		s.username).Scan(&u.id, &u.handle, &u.username, &u.displayName)
	if err != nil {
		return nil, fmt.Errorf("load user %q: %w", s.username, err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT credential_id, public_key, attestation_type, transports, aaguid,
		        sign_count, user_verified, backup_eligible, backup_state
		   FROM passkeys WHERE user_id = ? AND revoked_at IS NULL`, u.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			c          webauthn.Credential
			transports string
			aaguid     []byte
			signCount  int64
			uv, be, bs bool
		)
		if err := rows.Scan(&c.ID, &c.PublicKey, &c.AttestationType, &transports,
			&aaguid, &signCount, &uv, &be, &bs); err != nil {
			return nil, err
		}
		var ts []string
		if err := json.Unmarshal([]byte(transports), &ts); err == nil {
			for _, t := range ts {
				c.Transport = append(c.Transport, protocol.AuthenticatorTransport(t))
			}
		}
		c.Authenticator = webauthn.Authenticator{AAGUID: aaguid, SignCount: uint32(signCount)}
		c.Flags = webauthn.CredentialFlags{
			UserPresent:    true,
			UserVerified:   uv,
			BackupEligible: be,
			BackupState:    bs,
		}
		u.creds = append(u.creds, c)
	}
	return u, rows.Err()
}

// SaveCredential persists a freshly registered credential under a friendly name.
func (s *Service) SaveCredential(ctx context.Context, userID, name string, c *webauthn.Credential) error {
	ts := make([]string, 0, len(c.Transport))
	for _, t := range c.Transport {
		ts = append(ts, string(t))
	}
	transports, _ := json.Marshal(ts)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO passkeys (id, user_id, name, credential_id, public_key, attestation_type,
		                       transports, aaguid, sign_count, user_verified, backup_eligible, backup_state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), userID, name, c.ID, c.PublicKey, c.AttestationType,
		string(transports), c.Authenticator.AAGUID, int64(c.Authenticator.SignCount),
		c.Flags.UserVerified, c.Flags.BackupEligible, c.Flags.BackupState)
	if err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	return nil
}

// touchCredential records a successful assertion. The sign counter is advanced
// so a cloned authenticator shows up as a counter regression next time.
func (s *Service) touchCredential(ctx context.Context, c *webauthn.Credential) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE passkeys SET sign_count = ?, backup_state = ?, last_used_at = datetime('now')
		  WHERE credential_id = ?`,
		int64(c.Authenticator.SignCount), c.Flags.BackupState, c.ID)
	return err
}

// PasskeyInfo is the browser-facing view of a credential. It deliberately
// carries no key material.
type PasskeyInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  string     `json:"created_at"`
	LastUsedAt *string    `json:"last_used_at"`
	Revoked    bool       `json:"revoked"`
	SignCount  int64      `json:"sign_count"`
	BackedUp   bool       `json:"backed_up"`
	Expires    *time.Time `json:"-"`
}

func (s *Service) ListPasskeys(ctx context.Context) ([]PasskeyInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at, last_used_at, revoked_at, sign_count, backup_state
		   FROM passkeys ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PasskeyInfo{}
	for rows.Next() {
		var (
			p       PasskeyInfo
			last    sql.NullString
			revoked sql.NullString
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &last, &revoked, &p.SignCount, &p.BackedUp); err != nil {
			return nil, err
		}
		if last.Valid {
			p.LastUsedAt = &last.String
		}
		p.Revoked = revoked.Valid
		out = append(out, p)
	}
	return out, rows.Err()
}

// RevokePasskey refuses to remove the last usable credential — locking yourself
// out of a passkey-only dashboard has no recovery path short of the CLI.
func (s *Service) RevokePasskey(ctx context.Context, id string) error {
	var live int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM passkeys WHERE revoked_at IS NULL`).Scan(&live); err != nil {
		return err
	}
	if live <= 1 {
		return fmt.Errorf("this is the only usable passkey; enroll another before revoking it")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE passkeys SET revoked_at = datetime('now') WHERE id = ? AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no such active passkey")
	}
	return nil
}

func (s *Service) RenamePasskey(ctx context.Context, id, name string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE passkeys SET name = ? WHERE id = ?`, name, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no such passkey")
	}
	return nil
}
