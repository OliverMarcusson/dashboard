// Package games discovers game servers by offering every container to the
// adapter registry.
package games

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"github.com/olivermarcusson/dashboard/internal/game"
	_ "github.com/olivermarcusson/dashboard/internal/game/minecraft" // register adapter
	"github.com/olivermarcusson/dashboard/internal/hub"
)

const Topic = "games"

type Snapshot struct {
	Networks []game.Network         `json:"networks"`
	Statuses map[string]game.Status `json:"statuses"`
	Adapters []string               `json:"adapters"`
	Servers  int                    `json:"servers"`
	At       time.Time              `json:"at"`
}

type Collector struct {
	hub      *hub.Hub
	docker   *client.Client
	interval time.Duration

	mu      sync.RWMutex
	servers map[string]game.Server
}

func New(h *hub.Hub, docker *client.Client, interval time.Duration) *Collector {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &Collector{hub: h, docker: docker, interval: interval, servers: map[string]game.Server{}}
}

func (c *Collector) Name() string { return "games" }

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

// Server returns a claimed server by container name, for the console endpoint.
func (c *Collector) Server(name string) (game.Server, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.servers[name]
	return s, ok
}

func (c *Collector) publish(ctx context.Context) {
	snap := c.Snapshot(ctx)
	c.hub.Publish(Topic, snap)
}

func (c *Collector) Snapshot(ctx context.Context) Snapshot {
	snap := Snapshot{
		At:       time.Now().UTC(),
		Adapters: game.Registered(),
		Networks: []game.Network{},
		Statuses: map[string]game.Status{},
	}
	if c.docker == nil {
		return snap
	}

	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	list, err := c.docker.ContainerList(listCtx, client.ContainerListOptions{All: true})
	cancel()
	if err != nil {
		return snap
	}

	var claimed []game.Server
	adapters := map[string]game.Adapter{}
	servers := map[string]game.Server{}

	for _, raw := range list.Items {
		name := ""
		if len(raw.Names) > 0 {
			name = strings.TrimPrefix(raw.Names[0], "/")
		}

		// Environment is only on inspect, and it is where these images publish
		// everything worth knowing about themselves.
		env := map[string]string{}
		ip := ""
		inspectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		info, err := c.docker.ContainerInspect(inspectCtx, name, client.ContainerInspectOptions{})
		cancel()
		if err == nil {
			if info.Container.Config != nil {
				for _, entry := range info.Container.Config.Env {
					if eq := strings.IndexByte(entry, '='); eq > 0 {
						env[entry[:eq]] = entry[eq+1:]
					}
				}
			}
			if info.Container.NetworkSettings != nil {
				for _, n := range info.Container.NetworkSettings.Networks {
					if n != nil && n.IPAddress.IsValid() {
						ip = n.IPAddress.String()
						break
					}
				}
			}
		}

		ports := map[string]string{}
		for _, p := range raw.Ports {
			if p.PublicPort != 0 {
				ports[strconv.Itoa(int(p.PrivatePort))+"/"+p.Type] =
					p.IP.String() + ":" + strconv.Itoa(int(p.PublicPort))
			}
		}

		candidate := game.Container{
			Name:    name,
			Image:   raw.Image,
			Project: raw.Labels["com.docker.compose.project"],
			Service: raw.Labels["com.docker.compose.service"],
			State:   string(raw.State),
			Env:     env,
			Ports:   ports,
			IP:      ip,
		}

		server, adapter, ok := game.Claim(candidate)
		if !ok {
			continue
		}
		claimed = append(claimed, server)
		adapters[server.Container] = adapter
		servers[server.Container] = server

		// A stopped server is claimed but not polled.
		if raw.State != "running" {
			snap.Statuses[server.Container] = game.Status{
				Container: server.Container, At: time.Now().UTC(),
				Players: []game.Player{}, Error: "container is not running",
			}
		}
	}

	c.mu.Lock()
	c.servers = servers
	c.mu.Unlock()

	// Poll live servers concurrently: one unresponsive server should not delay
	// the rest behind its RCON timeout.
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, server := range claimed {
		if _, done := snap.Statuses[server.Container]; done {
			continue
		}
		wg.Add(1)
		go func(server game.Server, adapter game.Adapter) {
			defer wg.Done()
			pollCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()

			status, err := adapter.Status(pollCtx, server)
			if err != nil {
				status = game.Status{
					Container: server.Container, At: time.Now().UTC(),
					Players: []game.Player{}, Error: err.Error(),
				}
			}
			mu.Lock()
			snap.Statuses[server.Container] = status
			mu.Unlock()
		}(server, adapters[server.Container])
	}
	wg.Wait()

	snap.Networks = game.Group(claimed)
	snap.Servers = len(claimed)
	return snap
}
