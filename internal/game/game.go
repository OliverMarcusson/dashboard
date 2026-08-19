// Package game turns running containers into game servers the dashboard can
// show and control.
//
// A game is an adapter: one type implementing Adapter and registering itself.
// Adding a game costs one package. Adding another server of a game already
// supported costs nothing — the registry offers every discovered container to
// every adapter, and whichever claims it owns that server from then on.
package game

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Container is what the registry knows about a candidate, independent of any
// Docker types so adapters do not depend on the daemon client.
type Container struct {
	Name    string
	Image   string
	Project string
	Service string
	State   string
	Env     map[string]string
	Ports   map[string]string

	// IP is the container's address on its Docker network. The dashboard runs
	// on the host, where container names do not resolve, so this is how an
	// adapter reaches a port that is not published.
	IP string
}

// Server is a claimed game server.
type Server struct {
	Container string `json:"container"`
	Adapter   string `json:"adapter"`
	Game      string `json:"game"`
	Edition   string `json:"edition,omitempty"`
	Version   string `json:"version,omitempty"`

	// Network groups a proxy with the servers behind it. It is the compose
	// project, so a proxy and its backends group with no configuration.
	Network string `json:"network"`
	Proxy   bool   `json:"proxy"`

	Address string            `json:"address,omitempty"`
	Extra   map[string]string `json:"extra,omitempty"`

	// conn carries whatever the adapter needs to reach the server. It never
	// leaves the process: RCON passwords are not sent to the browser.
	conn map[string]string
}

// Conn reads a connection detail stored by the claiming adapter.
func (s Server) Conn(key string) string { return s.conn[key] }

// WithConn returns a copy carrying connection details.
func (s Server) WithConn(conn map[string]string) Server {
	s.conn = conn
	return s
}

// Player is one connected player.
type Player struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

// Status is the live state of one server.
type Status struct {
	Container  string            `json:"container"`
	Online     bool              `json:"online"`
	Players    []Player          `json:"players"`
	MaxPlayers int               `json:"max_players"`
	TPS        float64           `json:"tps,omitempty"`
	Version    string            `json:"version,omitempty"`
	Extra      map[string]string `json:"extra,omitempty"`
	Error      string            `json:"error,omitempty"`
	At         time.Time         `json:"at"`
}

// Adapter teaches the dashboard about one kind of game server.
type Adapter interface {
	// Name identifies the adapter.
	Name() string

	// Claims reports whether this adapter handles the container, and extracts
	// everything needed to reach it.
	Claims(c Container) (Server, bool)

	// Status polls live state.
	Status(ctx context.Context, s Server) (Status, error)

	// Console runs one command and returns its raw output.
	Console(ctx context.Context, s Server, command string) (string, error)
}

var (
	mu       sync.RWMutex
	adapters []Adapter
)

// Register adds an adapter. Adapters call this from init.
func Register(a Adapter) {
	mu.Lock()
	defer mu.Unlock()
	adapters = append(adapters, a)
}

// Registered lists adapter names, for diagnostics.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name())
	}
	sort.Strings(names)
	return names
}

// Claim offers a container to every adapter and returns the first claim.
func Claim(c Container) (Server, Adapter, bool) {
	mu.RLock()
	defer mu.RUnlock()
	for _, a := range adapters {
		if server, ok := a.Claims(c); ok {
			server.Adapter = a.Name()
			return server, a, true
		}
	}
	return Server{}, nil, false
}

// AdapterFor returns the adapter that owns a server.
func AdapterFor(name string) (Adapter, bool) {
	mu.RLock()
	defer mu.RUnlock()
	for _, a := range adapters {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// Network groups servers that belong together, proxy first.
type Network struct {
	Name    string   `json:"name"`
	Servers []Server `json:"servers"`
}

// Group arranges claimed servers into networks by compose project.
func Group(servers []Server) []Network {
	byNetwork := map[string][]Server{}
	for _, s := range servers {
		byNetwork[s.Network] = append(byNetwork[s.Network], s)
	}

	out := make([]Network, 0, len(byNetwork))
	for name, members := range byNetwork {
		// A proxy heads its network; the rest follow alphabetically.
		sort.Slice(members, func(i, j int) bool {
			if members[i].Proxy != members[j].Proxy {
				return members[i].Proxy
			}
			return members[i].Container < members[j].Container
		})
		out = append(out, Network{Name: name, Servers: members})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
