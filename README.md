# Oliver Server Dashboard

Private passkey-only server dashboard.

- Backend: Rust + Axum + SQLite + webauthn-rs
- Host agent: Rust + Axum, bound to Docker bridge `172.17.0.1:13001` with bearer-token auth
- Frontend: React + Vite + Tailwind
- Deployment: host Caddy on `dash.olivermarcusson.se`, app published on `127.0.0.1:13000`

## Bootstrap auth

```bash
dashctl enroll
# open https://dash.olivermarcusson.se/enroll and enter the one-time code
```

The code only authorizes WebAuthn registration. The browser/password manager, e.g. Proton Pass, creates and stores the passkey.

## Containerized deployment

Build, install the host agent, restart the dashboard, and verify:

```bash
./scripts/deploy.sh
```

This creates `/var/lib/oliver-dashboard/agent-token`, installs `/usr/local/bin/dashboard-agent`, and starts `dashboard-agent.service` on `172.17.0.1:13001`.

Generate a passkey enrollment code inside the dashboard container:

```bash
docker compose exec dashboard dashctl enroll
```

Then open `https://dash.olivermarcusson.se/enroll`.

The dashboard container publishes only `127.0.0.1:13000` on the host. Management APIs require a passkey-backed session; the host agent also requires the shared bearer token. This server already has host Caddy on ports 80/443, so the live edge Caddy proxies `dash.olivermarcusson.se` to `127.0.0.1:13000`. The hostname is public via Cloudflare DDNS.

If deploying on a host without existing Caddy, use the optional Compose Caddy profile:

```bash
docker compose --profile standalone-caddy up -d --build
```

## Development

Rust is required for local backend development.

```bash
cd backend
cargo run
```

```bash
cd frontend
npm install
npm run dev
```
