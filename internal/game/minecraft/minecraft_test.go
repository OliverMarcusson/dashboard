package minecraft

import (
	"testing"

	"github.com/olivermarcusson/dashboard/internal/game"
)

// These mirror the three servers actually running on the host.
func TestClaimsRealServers(t *testing.T) {
	cases := []struct {
		name    string
		c       game.Container
		edition string
		proxy   bool
		network string
	}{
		{
			name: "vanilla",
			c: game.Container{
				Name: "mc-18", Image: "itzg/minecraft-server:java8", Project: "mc18",
				Env: map[string]string{"TYPE": "VANILLA", "VERSION": "1.8.9", "ENABLE_RCON": "true", "RCON_PASSWORD": "secret"},
			},
			edition: "Vanilla", network: "mc18",
		},
		{
			name: "paper",
			c: game.Container{
				Name: "mc-pvptest", Image: "itzg/minecraft-server:latest", Project: "pvptest",
				Env: map[string]string{"TYPE": "PAPER", "VERSION": "26.2", "ENABLE_RCON": "true", "RCON_PASSWORD": "secret"},
			},
			edition: "Paper", network: "pvptest",
		},
		{
			name: "velocity proxy",
			c: game.Container{
				Name: "velocity-pvptest", Image: "itzg/bungeecord:latest", Project: "pvptest",
				Env: map[string]string{"TYPE": "VELOCITY", "RCON_PORT": "25575"},
			},
			edition: "Velocity", proxy: true, network: "pvptest",
		},
	}

	a := &Adapter{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, ok := a.Claims(tc.c)
			if !ok {
				t.Fatal("adapter did not claim the container")
			}
			if s.Edition != tc.edition {
				t.Errorf("edition = %q, want %q", s.Edition, tc.edition)
			}
			if s.Proxy != tc.proxy {
				t.Errorf("proxy = %v, want %v", s.Proxy, tc.proxy)
			}
			if s.Network != tc.network {
				t.Errorf("network = %q, want %q", s.Network, tc.network)
			}
			if s.Conn("address") == "" || s.Conn("password") == "" {
				t.Error("connection details were not extracted")
			}
		})
	}
}

func TestIgnoresOtherContainers(t *testing.T) {
	a := &Adapter{}
	for _, c := range []game.Container{
		{Name: "apps-postgres-1", Image: "postgres:17"},
		{Name: "edge-caddy-1", Image: "localhost/caddy-layer4:2.11.4"},
		{Name: "pwn-00-warmup", Image: "pwn-challenges:latest"},
	} {
		if _, ok := a.Claims(c); ok {
			t.Errorf("adapter wrongly claimed %q", c.Name)
		}
	}
}

func TestConnectionDetailsStayServerSide(t *testing.T) {
	a := &Adapter{}
	s, _ := a.Claims(game.Container{
		Name: "mc-18", Image: "itzg/minecraft-server:java8",
		Env: map[string]string{"RCON_PASSWORD": "hunter2"},
	})
	// The password must be reachable in-process but never serialised.
	if s.Conn("password") != "hunter2" {
		t.Fatal("adapter did not capture the password")
	}
	blob := mustJSON(t, s)
	if contains(blob, "hunter2") {
		t.Errorf("the RCON password was serialised to JSON: %s", blob)
	}
}

func TestPlayerNames(t *testing.T) {
	cases := map[string][]string{
		"There are 3 of a max of 20 players online: alice, bob, carol": {"alice", "bob", "carol"},
		"There are 0 of a max of 20 players online:":                   nil,
		"There are 1 of a max of 100 players online: solo":             {"solo"},
	}
	for input, want := range cases {
		got := playerNames(input)
		if len(got) != len(want) {
			t.Errorf("playerNames(%q) = %v, want %v", input, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("playerNames(%q)[%d] = %q, want %q", input, i, got[i], want[i])
			}
		}
	}
}

func TestGroupPutsProxyFirst(t *testing.T) {
	networks := game.Group([]game.Server{
		{Container: "mc-pvptest", Network: "pvptest"},
		{Container: "velocity-pvptest", Network: "pvptest", Proxy: true},
		{Container: "mc-18", Network: "mc18"},
	})
	if len(networks) != 2 {
		t.Fatalf("got %d networks, want 2", len(networks))
	}
	for _, n := range networks {
		if n.Name == "pvptest" {
			if len(n.Servers) != 2 {
				t.Fatalf("pvptest has %d servers, want 2", len(n.Servers))
			}
			if !n.Servers[0].Proxy {
				t.Error("the proxy should head its network")
			}
		}
	}
}

func TestUsesContainerIPNotName(t *testing.T) {
	// The dashboard runs on the host, where container names do not resolve.
	a := &Adapter{}
	s, _ := a.Claims(game.Container{
		Name: "mc-18", Image: "itzg/minecraft-server:java8", IP: "172.23.0.2",
		Env: map[string]string{"RCON_PASSWORD": "x"},
	})
	if got := s.Conn("address"); got != "172.23.0.2:25575" {
		t.Errorf("address = %q, want the container IP", got)
	}

	// A published RCON port wins, since it survives a network change.
	s, _ = a.Claims(game.Container{
		Name: "mc-x", Image: "itzg/minecraft-server:latest", IP: "172.23.0.9",
		Env:   map[string]string{"RCON_PORT": "25575"},
		Ports: map[string]string{"25575/tcp": "127.0.0.1:34567"},
	})
	if got := s.Conn("address"); got != "127.0.0.1:34567" {
		t.Errorf("address = %q, want the published port", got)
	}
}

func TestParseTPSIgnoresWindowLabels(t *testing.T) {
	cases := map[string]float64{
		// The window labels 1m/5m/15m precede the reading.
		"§6TPS from last 1m, 5m, 15m: §a20.0§r, §a20.0§r, §a20.0": 20.0,
		"TPS from last 1m, 5m, 15m: 19.4, 20.0, 20.0":             19.4,
		"TPS from last 1m, 5m, 15m: 7.2, 11.0, 18.3":              7.2,
		// Vanilla has no such command.
		"Unknown command. Try /help for a list of commands": 0,
		"":                       0,
		"TPS from last 1m: 25.0": 0, // implausible, rejected
	}
	for input, want := range cases {
		if got := parseTPS(input); got != want {
			t.Errorf("parseTPS(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestProxiesReportNoRCON(t *testing.T) {
	a := &Adapter{}
	s, ok := a.Claims(game.Container{
		Name: "velocity-pvptest", Image: "itzg/bungeecord:latest", Project: "pvptest",
		Env: map[string]string{"TYPE": "VELOCITY", "RCON_PORT": "25575"},
	})
	if !ok {
		t.Fatal("proxy not claimed")
	}
	if s.Conn("enabled") != "unsupported" {
		t.Errorf("proxy RCON state = %q, want unsupported", s.Conn("enabled"))
	}
	if _, err := a.Console(t.Context(), s, "glist"); err == nil {
		t.Error("console should refuse on a proxy with no RCON")
	}
}
