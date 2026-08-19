// Package legacy imports credentials from the Rust dashboard's database so the
// rewrite does not force a re-enrollment.
package legacy

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/go-webauthn/webauthn/webauthn"
	_ "modernc.org/sqlite"
)

// rsPasskey mirrors the shape webauthn-rs serialises into passkeys.credential_json.
type rsPasskey struct {
	Cred struct {
		CredID string `json:"cred_id"`
		Cred   struct {
			Type string `json:"type_"`
			Key  struct {
				EC2 *struct {
					Curve string `json:"curve"`
					X     string `json:"x"`
					Y     string `json:"y"`
				} `json:"EC_EC2"`
			} `json:"key"`
		} `json:"cred"`
		Counter        uint32 `json:"counter"`
		UserVerified   bool   `json:"user_verified"`
		BackupEligible bool   `json:"backup_eligible"`
		BackupState    bool   `json:"backup_state"`
	} `json:"cred"`
}

// Credential is one imported passkey, ready to store.
type Credential struct {
	Name string
	Cred webauthn.Credential
}

func b64(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// curveID maps webauthn-rs curve names onto COSE curve identifiers.
func curveID(name string) (int64, error) {
	switch name {
	case "SECP256R1", "P-256", "P256":
		return 1, nil
	case "SECP384R1", "P-384":
		return 2, nil
	case "SECP521R1", "P-521":
		return 3, nil
	}
	return 0, fmt.Errorf("unsupported curve %q", name)
}

// Read extracts every non-revoked credential from the old database.
func Read(ctx context.Context, path string) ([]Credential, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open legacy db: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT name, credential_json FROM passkeys WHERE revoked_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("read legacy passkeys: %w", err)
	}
	defer rows.Close()

	var out []Credential
	for rows.Next() {
		var name, blob string
		if err := rows.Scan(&name, &blob); err != nil {
			return nil, err
		}

		var pk rsPasskey
		if err := json.Unmarshal([]byte(blob), &pk); err != nil {
			return nil, fmt.Errorf("parse %q: %w", name, err)
		}
		if pk.Cred.Cred.Key.EC2 == nil {
			return nil, fmt.Errorf("%q uses a key type this importer does not handle", name)
		}

		id, err := b64(pk.Cred.CredID)
		if err != nil {
			return nil, fmt.Errorf("%q credential id: %w", name, err)
		}
		x, err := b64(pk.Cred.Cred.Key.EC2.X)
		if err != nil {
			return nil, fmt.Errorf("%q key x: %w", name, err)
		}
		y, err := b64(pk.Cred.Cred.Key.EC2.Y)
		if err != nil {
			return nil, fmt.Errorf("%q key y: %w", name, err)
		}
		curve, err := curveID(pk.Cred.Cred.Key.EC2.Curve)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", name, err)
		}

		// Rebuild the COSE public key the verifier expects. Using the library's
		// own struct means the encoding matches what it will later parse.
		key := webauthncose.EC2PublicKeyData{
			PublicKeyData: webauthncose.PublicKeyData{
				KeyType:   2,  // EC2
				Algorithm: -7, // ES256
			},
			Curve:  curve,
			XCoord: x,
			YCoord: y,
		}
		encoded, err := cbor.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("%q encode key: %w", name, err)
		}

		out = append(out, Credential{
			Name: name,
			Cred: webauthn.Credential{
				ID:              id,
				PublicKey:       encoded,
				AttestationType: "none",
				Authenticator:   webauthn.Authenticator{SignCount: pk.Cred.Counter},
				Flags: webauthn.CredentialFlags{
					UserPresent:    true,
					UserVerified:   pk.Cred.UserVerified,
					BackupEligible: pk.Cred.BackupEligible,
					BackupState:    pk.Cred.BackupState,
				},
			},
		})
	}
	return out, rows.Err()
}
