// Package docker discovers services from the Docker daemon.
//
// Nothing about the services on this host is written down here: containers,
// their stacks, and the actions available on them are all derived from what
// the daemon reports.
package docker

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/olivermarcusson/dashboard/internal/hub"
)

const (
	TopicServices = "docker.services"
	TopicStats    = "docker.stats"

	labelProject = "com.docker.compose.project"
	labelService = "com.docker.compose.service"
)

// Container is one discovered container.
type Container struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	State   string            `json:"state"`  // running, exited, ...
	Status  string            `json:"status"` // "Up 2 weeks (healthy)"
	Health  string            `json:"health,omitempty"`
	Created int64             `json:"created"`
	Ports   []string          `json:"ports,omitempty"`
	Project string            `json:"project"`
	Service string            `json:"service"`
	Labels  map[string]string `json:"-"`
}

// Stack groups containers by their compose project. A container with no
// project label forms a stack of its own so nothing is hidden.
type Stack struct {
	Name       string      `json:"name"`
	Containers []Container `json:"containers"`
	Running    int         `json:"running"`
	Total      int         `json:"total"`
	Unmanaged  bool        `json:"unmanaged,omitempty"`
}

// Snapshot is the published view of everything Docker is running.
type Snapshot struct {
	Stacks     []Stack   `json:"stacks"`
	Running    int       `json:"running"`
	Total      int       `json:"total"`
	At         time.Time `json:"at"`
	DaemonUp   bool      `json:"daemon_up"`
	DaemonErr  string    `json:"daemon_error,omitempty"`
	APIVersion string    `json:"api_version,omitempty"`
}

type Collector struct {
	hub      *hub.Hub
	cli      *client.Client
	interval time.Duration
}

// New connects to the daemon. API version is negotiated so the dashboard keeps
// working across Docker upgrades.
func New(h *hub.Hub, interval time.Duration) (*Collector, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Collector{hub: h, cli: cli, interval: interval}, nil
}

func (c *Collector) Name() string { return "docker" }

func (c *Collector) Close() error { return c.cli.Close() }

func (c *Collector) Run(ctx context.Context) error {
	// A periodic refresh keeps uptime strings current; the event stream makes
	// state changes appear immediately rather than up to one interval later.
	go c.watchEvents(ctx)

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

// watchEvents reconnects on failure: the daemon restarting must not leave the
// dashboard permanently blind to state changes.
func (c *Collector) watchEvents(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		stream := c.cli.Events(ctx, client.EventsListOptions{
			Filters: make(client.Filters).Add("type", "container"),
		})
		msgs, errs := stream.Messages, stream.Err

	stream:
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-msgs:
				switch ev.Action {
				case "start", "die", "stop", "kill", "pause", "unpause", "destroy", "create", "health_status":
					backoff = time.Second
					c.publish(ctx)
				}
			case err := <-errs:
				if ctx.Err() != nil {
					return
				}
				slog.Warn("docker event stream ended", "err", err, "retry_in", backoff)
				break stream
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Collector) publish(ctx context.Context) {
	snap, err := c.Snapshot(ctx)
	if err != nil {
		c.hub.Publish(TopicServices, Snapshot{
			At: time.Now().UTC(), DaemonUp: false, DaemonErr: err.Error(), Stacks: []Stack{},
		})
		return
	}
	c.hub.Publish(TopicServices, snap)
}

// Snapshot enumerates every container and groups it into its stack.
func (c *Collector) Snapshot(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	list, err := c.cli.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return Snapshot{}, err
	}

	byProject := map[string][]Container{}
	snap := Snapshot{At: time.Now().UTC(), DaemonUp: true, APIVersion: c.cli.ClientVersion()}

	for _, raw := range list.Items {
		ctr := Container{
			ID:      raw.ID,
			Name:    primaryName(raw.Names),
			Image:   raw.Image,
			State:   string(raw.State),
			Status:  raw.Status,
			Health:  healthFrom(raw.Status),
			Created: raw.Created,
			Ports:   portStrings(raw),
			Labels:  raw.Labels,
		}
		ctr.Project = raw.Labels[labelProject]
		ctr.Service = raw.Labels[labelService]

		project := ctr.Project
		if project == "" {
			// Not compose-managed: it still gets a home rather than vanishing.
			project = ctr.Name
		}
		byProject[project] = append(byProject[project], ctr)

		snap.Total++
		if raw.State == container.StateRunning {
			snap.Running++
		}
	}

	for name, containers := range byProject {
		sort.Slice(containers, func(i, j int) bool { return containers[i].Name < containers[j].Name })

		stack := Stack{Name: name, Containers: containers, Total: len(containers)}
		for _, ctr := range containers {
			if ctr.State == "running" {
				stack.Running++
			}
			if ctr.Project == "" {
				stack.Unmanaged = true
			}
		}
		snap.Stacks = append(snap.Stacks, stack)
	}

	// Stacks needing attention sort first; the rest alphabetically.
	sort.Slice(snap.Stacks, func(i, j int) bool {
		a, b := snap.Stacks[i], snap.Stacks[j]
		if (a.Running < a.Total) != (b.Running < b.Total) {
			return a.Running < a.Total
		}
		return a.Name < b.Name
	})
	return snap, nil
}

func primaryName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

// healthFrom pulls the health word out of a status string like
// "Up 2 weeks (healthy)". The API exposes it only on inspect, and this view
// is built from a single list call.
func healthFrom(status string) string {
	for _, h := range []string{"healthy", "unhealthy", "starting"} {
		if strings.Contains(status, "("+h+")") {
			return h
		}
	}
	return ""
}

func portStrings(raw container.Summary) []string {
	out := make([]string, 0, len(raw.Ports))
	seen := map[string]bool{}
	for _, p := range raw.Ports {
		var s string
		if p.PublicPort != 0 {
			s = formatPort(p.IP.String(), p.PublicPort, p.PrivatePort, p.Type)
		} else {
			s = formatPrivate(p.PrivatePort, p.Type)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Client exposes the connected daemon client so the action runner can share
// one connection rather than opening a second.
func (c *Collector) Client() *client.Client { return c.cli }
