package docker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/olivermarcusson/dashboard/internal/hub"
)

// TestLiveDiscovery runs against the real daemon. It asserts the shape of what
// discovery returns rather than specific containers, so it stays true as the
// host changes. Set DOCKER_LIVE=1 to run.
func TestLiveDiscovery(t *testing.T) {
	if os.Getenv("DOCKER_LIVE") == "" {
		t.Skip("set DOCKER_LIVE=1 to run against the local daemon")
	}

	c, err := New(hub.New(), time.Second)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Close()

	snap, err := c.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snap.DaemonUp {
		t.Fatal("daemon reported down")
	}
	if snap.Total == 0 {
		t.Fatal("no containers discovered")
	}

	t.Logf("api %s — %d containers in %d stacks, %d running",
		snap.APIVersion, snap.Total, len(snap.Stacks), snap.Running)

	var counted int
	for _, s := range snap.Stacks {
		counted += s.Total
		if s.Total == 0 {
			t.Errorf("stack %q has no containers", s.Name)
		}
		if s.Running > s.Total {
			t.Errorf("stack %q: running %d > total %d", s.Name, s.Running, s.Total)
		}
		for _, ctr := range s.Containers {
			if ctr.Name == "" {
				t.Errorf("stack %q has a container with no name", s.Name)
			}
		}
		t.Logf("  %-14s %d/%d  %s", s.Name, s.Running, s.Total, sample(s))
	}
	if counted != snap.Total {
		t.Errorf("stacks hold %d containers but total says %d", counted, snap.Total)
	}
}

func sample(s Stack) string {
	if len(s.Containers) == 0 {
		return ""
	}
	c := s.Containers[0]
	out := c.Name
	if c.Health != "" {
		out += " (" + c.Health + ")"
	}
	return out
}
