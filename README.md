# Server Dashboard

Private, passkey-only dashboard for the host behind `dash.marcusson.dev`.

Go backend, React frontend, one static binary. Services and actions are
discovered live from Docker and systemd — nothing about this server is
hardcoded.

> Rewrite complete through phase 6. The old Rust dashboard in `backend/` and
> `frontend/` still serves production; `scripts/cutover.sh` switches over and
> deletes it. Run `scripts/cutover.sh --check` first — it verifies everything
> and changes nothing.

## Layout

    cmd/dashboardd/     entrypoint and CLI
    internal/api/       routes, middleware, websocket, embedded frontend
    internal/auth/      webauthn ceremonies, credentials, sessions
    internal/collect/   host, docker and systemd collectors
    internal/config/    environment-driven settings
    internal/hub/       fan-out from collectors to subscribers
    internal/store/     sqlite, migrations, time-series
    internal/legacy/    passkey import from the Rust dashboard
    web/                react frontend (builds into internal/api/dist)
    deploy/             systemd unit

## Build

The frontend is embedded, so build it first:

    cd web && npm install && npm run build
    cd .. && go build -o dashboardd ./cmd/dashboardd

## Run

Configuration is environment-driven; defaults match the live deployment.

| Variable | Default | Meaning |
| --- | --- | --- |
| `DASHBOARD_ADDR` | `127.0.0.1:13000` | listen address, behind Caddy |
| `DASHBOARD_DB` | `/var/lib/dashboard/dashboard.sqlite` | database file |
| `DASHBOARD_ORIGIN` | `https://dash.marcusson.dev` | public origin |
| `DASHBOARD_RP_ID` | host of the origin | WebAuthn relying party |
| `DASHBOARD_USER` | `oliver` | account name |
| `DASHBOARD_SESSION_TTL` | `720h` | session lifetime |

    sudo install -m0755 dashboardd /usr/local/bin/
    sudo install -m0644 deploy/dashboard.service /etc/systemd/system/
    sudo systemctl daemon-reload && sudo systemctl enable --now dashboard

## API

Everything except `/api/health` and the auth ceremonies needs a session.

| Endpoint | Returns |
| --- | --- |
| `GET /api/overview` | host vitals snapshot |
| `GET /api/services` | containers grouped into stacks |
| `GET /api/units` | systemd service state |
| `GET /api/metrics/range` | stored history for one series |
| `GET /api/storage` | docker reclaim and SMART health |
| `GET /api/edge` | certificate expiry, public IP, tailnet |
| `GET /api/updates` | image drift, dnf updates, reboot flag |
| `GET /api/games` | discovered game servers and their status |
| `GET /api/actions` | actions available on a target |
| `POST /api/actions/run` | run one action (confirmation required) |
| `GET /api/jobs` | execution history |
| `POST /api/games/{container}/console` | run one game command |
| `GET /ws?topics=…` | live stream |
| `GET /ws/logs?kind=&target=` | tail one container or unit |

`/api/metrics/range` takes `kind` (`host`, `disk`), `subject` (mount point, for
disks), `metric`, and `minutes`. The store picks the resolution from the window
asked for — raw within 24h, five-minute averages within 30 days, hourly beyond.

### Live stream

    ws://…/ws?topics=host.metrics,docker.services,systemd.units

Messages are `{topic, at, data}`. A new subscriber immediately receives the
latest message on each topic it asked for, so a page renders without waiting
for the next tick. Unknown topics are ignored rather than erroring.

Publishing never blocks: a subscriber that falls behind loses messages rather
than stalling a collector, and the drop count shows up in `/api/health`.

## Collectors

| Collector | Cadence | Source |
| --- | --- | --- |
| host | 2s publish, 10s persist | gopsutil — cpu, memory, load, disks, net, sensors |
| docker | 5s + event stream | Docker API — containers grouped by compose project |
| systemd | 5s | D-Bus — service unit state |
| games | 15s | adapters claiming containers, polled over RCON |
| probe.storage | 15m | docker disk usage, smartctl |
| probe.edge | 1h | Caddy admin API, TLS dial, tailscale |
| probe.updates | 1h | registry digests, dnf |

Nothing about this host is hardcoded. Containers are grouped by their
`com.docker.compose.project` label; one without that label becomes a stack of
its own rather than disappearing. Stacks that are not fully running sort first.

## Actions

Actions are derived, never declared. A container can be started, stopped, and
restarted because it *is* a container; a compose project because it is a
stack; a unit because it is a unit. There is no action list to keep in step
with the host.

Every action passes through one confirmation step — the same step, whether it
restarts a Minecraft server or stops Docker. There are no danger tiers,
because a uniform rule is the only kind that stays correct when the action
list is generated at runtime. The API enforces it too: a run request without
`"confirmed": true` is refused.

Nothing shells out. Containers are driven through the Docker API and units
through D-Bus, so a crafted target is a nonexistent name rather than an
injection.

## Game servers

A game is a Go package implementing `game.Adapter` and registering itself.
The registry offers every discovered container to every adapter; whichever
claims it owns that server.

Adding a game costs one package. Adding another server of a game already
supported costs nothing — `itzg/minecraft-server` and `itzg/bungeecord`
containers announce their type, version, and RCON credentials in their own
environment, so they are found with no configuration.

A network is a compose project, so a proxy and the servers behind it group
together and the proxy heads the list. RCON credentials are read from the
container environment, used in-process, and never serialised to the browser.

## Passkeys

Bring the existing passkeys across from the Rust dashboard — no
re-enrollment, the same credentials keep working:

    sudo dashboardd import-legacy \
      /var/lib/docker/volumes/dashboard_dashboard-data/_data/dashboard.sqlite

Or enroll a new one. The code authorizes registration only; the password
manager creates and keeps the passkey:

    sudo dashboardd enroll
    # then open https://dash.marcusson.dev/enroll

A passkey cannot be revoked if it is the last one — a passkey-only dashboard
has no recovery path.

## Development

    cd web && npm run dev      # vite on 5173, proxies /api to 13000
    go run ./cmd/dashboardd    # backend on 13000

## Tests

    go test ./...

Some tests exercise real host services and are skipped unless asked for:

    DOCKER_LIVE=1  go test ./internal/collect/docker -v    # the local daemon
    SYSTEMD_LIVE=1 go test ./internal/collect/systemd -v   # host systemd
    LEGACY_DB=/path/to/old/dashboard.sqlite go test ./internal/legacy -v

The legacy import test verifies rebuilt COSE keys parse back through the same
verifier the login path uses.
