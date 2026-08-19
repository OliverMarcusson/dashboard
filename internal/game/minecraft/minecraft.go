package minecraft

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/olivermarcusson/dashboard/internal/game"
)

func init() { game.Register(&Adapter{}) }

// Adapter claims itzg/minecraft-server and itzg/bungeecord containers.
//
// Those images describe themselves entirely through their environment — type,
// version, RCON port and password — so a server is discovered with no
// configuration at all.
type Adapter struct{}

func (a *Adapter) Name() string { return "minecraft" }

// editions maps the TYPE environment variable onto something readable.
var editions = map[string]string{
	"VANILLA":    "Vanilla",
	"PAPER":      "Paper",
	"SPIGOT":     "Spigot",
	"BUKKIT":     "Bukkit",
	"PURPUR":     "Purpur",
	"FABRIC":     "Fabric",
	"FORGE":      "Forge",
	"NEOFORGE":   "NeoForge",
	"QUILT":      "Quilt",
	"VELOCITY":   "Velocity",
	"BUNGEECORD": "BungeeCord",
	"WATERFALL":  "Waterfall",
}

func (a *Adapter) Claims(c game.Container) (game.Server, bool) {
	image := strings.ToLower(c.Image)
	isServer := strings.Contains(image, "itzg/minecraft-server")
	isProxy := strings.Contains(image, "itzg/bungeecord")
	if !isServer && !isProxy {
		return game.Server{}, false
	}

	kind := strings.ToUpper(c.Env["TYPE"])
	edition := editions[kind]
	if edition == "" && kind != "" {
		edition = strings.ToTitle(strings.ToLower(kind))
	}
	proxy := isProxy || kind == "VELOCITY" || kind == "BUNGEECORD" || kind == "WATERFALL"

	network := c.Project
	if network == "" {
		network = c.Name
	}

	port := c.Env["RCON_PORT"]
	if port == "" {
		port = "25575"
	}
	password := c.Env["RCON_PASSWORD"]
	if password == "" {
		// The image's own default when none is configured.
		password = "minecraft"
	}

	server := game.Server{
		Container: c.Name,
		Game:      "Minecraft",
		Edition:   edition,
		Version:   c.Env["VERSION"],
		Network:   network,
		Proxy:     proxy,
		Address:   c.Ports["25565/tcp"],
	}
	rconEnabled := c.Env["ENABLE_RCON"]
	if rconEnabled == "false" {
		server.Extra = map[string]string{"rcon": "disabled"}
	}
	// Velocity and BungeeCord ship no RCON server, whatever RCON_PORT the
	// image happens to set. Saying so beats a connection-refused every poll.
	if proxy {
		rconEnabled = "unsupported"
		server.Extra = map[string]string{"rcon": "not supported by " + edition}
	}

	// RCON is reached at the container's bridge address. The port is not
	// published to the host and does not need to be — the host routes to the
	// Docker networks directly. A published RCON port is preferred if one
	// exists, since that survives the container moving networks.
	rcon := c.Ports[port+"/tcp"]
	if rcon == "" {
		host := c.IP
		if host == "" {
			host = c.Name
		}
		rcon = net.JoinHostPort(host, port)
	}
	return server.WithConn(map[string]string{
		"address":  rcon,
		"password": password,
		"enabled":  rconEnabled,
	}), true
}

var (
	// "There are 3 of a max of 20 players online: alice, bob, carol"
	listPattern = regexp.MustCompile(`(\d+)\s*(?:of a max(?:imum)? of|/)\s*(\d+)`)
	// Velocity: "There are 2 players online."
	proxyPattern = regexp.MustCompile(`There (?:are|is) (\d+) player`)
	// Paper answers "TPS from last 1m, 5m, 15m: 20.0, 20.0, 20.0". The reading
	// is the first number after the colon — the ones before it are the window
	// labels, which is exactly what a naive "first number" match picks up.
	tpsPattern = regexp.MustCompile(`:\s*(\d+\.?\d*)`)
	colorCodes = regexp.MustCompile(`§.`)
)

func (a *Adapter) Status(ctx context.Context, s game.Server) (game.Status, error) {
	status := game.Status{Container: s.Container, At: time.Now().UTC(), Players: []game.Player{}}

	switch s.Conn("enabled") {
	case "false":
		status.Error = "RCON is disabled on this server"
		return status, nil
	case "unsupported":
		status.Error = s.Edition + " does not provide RCON; player counts come from the servers behind it"
		return status, nil
	}

	command := "list"
	if s.Proxy {
		command = "glist"
	}

	out, err := execute(ctx, s.Conn("address"), s.Conn("password"), command)
	if err != nil {
		status.Error = err.Error()
		return status, nil
	}
	status.Online = true

	clean := colorCodes.ReplaceAllString(out, "")
	if m := listPattern.FindStringSubmatch(clean); len(m) == 3 {
		status.MaxPlayers, _ = strconv.Atoi(m[2])
	} else if m := proxyPattern.FindStringSubmatch(clean); len(m) == 2 {
		// A proxy reports a count without a maximum.
		if n, err := strconv.Atoi(m[1]); err == nil && n == 0 {
			return status, nil
		}
	}

	for _, name := range playerNames(clean) {
		status.Players = append(status.Players, game.Player{Name: name})
	}

	// Paper and its forks answer /tps; vanilla does not, and a failure here
	// must not make the server look offline.
	if !s.Proxy {
		if tps, err := execute(ctx, s.Conn("address"), s.Conn("password"), "tps"); err == nil {
			status.TPS = parseTPS(tps)
		}
	}
	return status, nil
}

// parseTPS reads the current tick rate, returning zero when the server does
// not implement the command.
func parseTPS(out string) float64 {
	m := tpsPattern.FindStringSubmatch(colorCodes.ReplaceAllString(out, ""))
	if len(m) != 2 {
		return 0
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil || v <= 0 || v > 20.5 {
		return 0
	}
	return v
}

// playerNames pulls the comma-separated list that follows the colon.
func playerNames(out string) []string {
	colon := strings.LastIndex(out, ":")
	if colon < 0 || colon+1 >= len(out) {
		return nil
	}
	var names []string
	for _, part := range strings.Split(out[colon+1:], ",") {
		part = strings.TrimSpace(part)
		// Proxy output decorates entries as "name (server)".
		if space := strings.IndexByte(part, ' '); space > 0 {
			part = part[:space]
		}
		if part != "" && part != "and" {
			names = append(names, part)
		}
	}
	return names
}

func (a *Adapter) Console(ctx context.Context, s game.Server, command string) (string, error) {
	command = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "/"))
	if command == "" {
		return "", fmt.Errorf("type a command to run")
	}
	switch s.Conn("enabled") {
	case "false":
		return "", fmt.Errorf("RCON is disabled on this server")
	case "unsupported":
		return "", fmt.Errorf("%s does not provide an RCON console", s.Edition)
	}
	return execute(ctx, s.Conn("address"), s.Conn("password"), command)
}
