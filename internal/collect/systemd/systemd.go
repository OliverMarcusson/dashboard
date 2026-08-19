// Package systemd reports the state of host services over D-Bus.
package systemd

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"

	"github.com/olivermarcusson/dashboard/internal/hub"
)

const Topic = "systemd.units"

// Unit is one systemd service.
type Unit struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Load        string `json:"load"`   // loaded, not-found, masked
	Active      string `json:"active"` // active, inactive, failed
	Sub         string `json:"sub"`    // running, dead, exited
	Failed      bool   `json:"failed"`
}

type Snapshot struct {
	Units   []Unit    `json:"units"`
	Failed  int       `json:"failed"`
	Running int       `json:"running"`
	At      time.Time `json:"at"`
	Error   string    `json:"error,omitempty"`
}

type Collector struct {
	hub      *hub.Hub
	interval time.Duration
}

func New(h *hub.Hub, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Collector{hub: h, interval: interval}
}

func (c *Collector) Name() string { return "systemd" }

func (c *Collector) Run(ctx context.Context) error {
	t := time.NewTicker(c.interval)
	defer t.Stop()

	c.publish(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			c.publish(ctx)
		}
	}
}

func (c *Collector) publish(ctx context.Context) {
	snap, err := c.Snapshot(ctx)
	if err != nil {
		slog.Warn("systemd snapshot failed", "err", err)
		c.hub.Publish(Topic, Snapshot{At: time.Now().UTC(), Error: err.Error(), Units: []Unit{}})
		return
	}
	c.hub.Publish(Topic, snap)
}

// Snapshot lists service units worth showing.
//
// A connection is opened per snapshot rather than held: a long-lived D-Bus
// connection is one more thing to detect and repair when systemd restarts, and
// the call is cheap at this cadence.
func (c *Collector) Snapshot(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, err := dbus.NewSystemdConnectionContext(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer conn.Close()

	statuses, err := conn.ListUnitsByPatternsContext(ctx, nil, []string{"*.service"})
	if err != nil {
		return Snapshot{}, err
	}

	snap := Snapshot{At: time.Now().UTC(), Units: []Unit{}}
	for _, s := range statuses {
		if skip(s.Name, s.LoadState) {
			continue
		}
		u := Unit{
			Name:        strings.TrimSuffix(s.Name, ".service"),
			Description: s.Description,
			Load:        s.LoadState,
			Active:      s.ActiveState,
			Sub:         s.SubState,
			Failed:      s.ActiveState == "failed",
		}
		if u.Failed {
			snap.Failed++
		}
		if s.SubState == "running" {
			snap.Running++
		}
		snap.Units = append(snap.Units, u)
	}

	// Failures first, then running, then the rest alphabetically.
	sort.Slice(snap.Units, func(i, j int) bool {
		a, b := snap.Units[i], snap.Units[j]
		if a.Failed != b.Failed {
			return a.Failed
		}
		if (a.Sub == "running") != (b.Sub == "running") {
			return a.Sub == "running"
		}
		return a.Name < b.Name
	})
	return snap, nil
}

// skip filters out noise: units systemd generated for something else, and
// one-shot helpers that are never interesting on a dashboard.
func skip(name, load string) bool {
	if load == "not-found" || load == "masked" {
		return true
	}
	// Per-container and per-session units churn constantly and are already
	// represented by the Docker view.
	for _, prefix := range []string{"docker-", "user@", "user-runtime-dir@", "session-", "getty@", "serial-getty@"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, suffix := range []string{"@.service"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
