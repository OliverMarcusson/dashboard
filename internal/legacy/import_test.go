package legacy

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// TestImportRoundTrip checks that every credential we rebuild is accepted by
// the same parser the login path uses. Skipped unless LEGACY_DB points at a
// real database.
func TestImportRoundTrip(t *testing.T) {
	path := os.Getenv("LEGACY_DB")
	if path == "" {
		t.Skip("set LEGACY_DB to run")
	}

	creds, err := Read(context.Background(), path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(creds) == 0 {
		t.Fatal("no credentials read")
	}

	for _, c := range creds {
		key, err := webauthncose.ParsePublicKey(c.Cred.PublicKey)
		if err != nil {
			t.Fatalf("%s: public key does not parse back: %v", c.Name, err)
		}
		ec2, ok := key.(webauthncose.EC2PublicKeyData)
		if !ok {
			t.Fatalf("%s: parsed as %T, want EC2", c.Name, key)
		}
		if ec2.Algorithm != -7 || ec2.Curve != 1 {
			t.Errorf("%s: alg=%d curve=%d, want -7/1", c.Name, ec2.Algorithm, ec2.Curve)
		}
		if len(ec2.XCoord) != 32 || len(ec2.YCoord) != 32 {
			t.Errorf("%s: coords %d/%d bytes, want 32/32", c.Name, len(ec2.XCoord), len(ec2.YCoord))
		}
		if len(c.Cred.ID) == 0 {
			t.Errorf("%s: empty credential id", c.Name)
		}
		t.Logf("%-20s id=%d bytes  key=%d bytes  signcount=%d  backedup=%v",
			c.Name, len(c.Cred.ID), len(c.Cred.PublicKey), c.Cred.Authenticator.SignCount, c.Cred.Flags.BackupState)
	}
}

// TestUserHandleIsTheLegacyUUIDBytes guards the mistake that made the first
// cutover attempt fail: the credentials imported cleanly but were attached to a
// freshly generated identity, so every assertion was rejected on user-handle
// mismatch. webauthn-rs uses the sixteen raw bytes of the UUID, not its text.
func TestUserHandleIsTheLegacyUUIDBytes(t *testing.T) {
	path := os.Getenv("LEGACY_DB")
	if path == "" {
		t.Skip("set LEGACY_DB to run")
	}

	handle, err := UserHandle(context.Background(), path)
	if err != nil {
		t.Fatalf("UserHandle: %v", err)
	}
	if len(handle) != 16 {
		t.Fatalf("handle is %d bytes, want the 16 raw bytes of a UUID", len(handle))
	}
	// The old dashboard pinned a single fixed account id.
	want := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	if !bytes.Equal(handle, want) {
		t.Errorf("handle = %x, want %x", handle, want)
	}
	// The text form would be 36 bytes — the shape of the original bug.
	if len(handle) == 36 {
		t.Error("handle is the UUID's text, not its bytes")
	}
}
