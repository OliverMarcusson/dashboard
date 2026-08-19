package systemd

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/olivermarcusson/dashboard/internal/hub"
)

// TestLiveUnits runs against the host's systemd. Set SYSTEMD_LIVE=1 to run.
func TestLiveUnits(t *testing.T) {
	if os.Getenv("SYSTEMD_LIVE") == "" {
		t.Skip("set SYSTEMD_LIVE=1 to run against host systemd")
	}

	snap, err := New(hub.New(), time.Second).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap.Units) == 0 {
		t.Fatal("no units discovered")
	}
	t.Logf("%d units, %d running, %d failed", len(snap.Units), snap.Running, snap.Failed)

	for _, u := range snap.Units {
		if u.Name == "" {
			t.Error("unit with empty name")
		}
		if u.Failed && snap.Failed == 0 {
			t.Error("failed unit not counted")
		}
	}
	for i, u := range snap.Units {
		if i >= 8 {
			break
		}
		t.Logf("  %-24s %-9s %s", u.Name, u.Active, u.Sub)
	}
}
