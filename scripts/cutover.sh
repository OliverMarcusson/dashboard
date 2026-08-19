#!/usr/bin/env bash
# Cut dash.marcusson.dev over from the Rust dashboard to this one.
#
# Run with --check first: it verifies everything without changing anything.
# The old container and agent are stopped but not deleted, and the old binary
# is kept, so a rollback is one script away.
set -euo pipefail

CHECK_ONLY=0
[[ "${1:-}" == "--check" ]] && CHECK_ONLY=1

BINARY=/usr/local/bin/dashboardd
UNIT=/etc/systemd/system/dashboard.service
STATE=/var/lib/dashboard
OLD_DB=/var/lib/docker/volumes/dashboard_dashboard-data/_data/dashboard.sqlite
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ok()   { printf '  \033[32m✓\033[0m %s\n' "$1"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$1"; }
die()  { printf '  \033[31m✗\033[0m %s\n' "$1" >&2; exit 1; }

echo
echo "Preflight"

[[ $EUID -eq 0 ]] || die "run as root"
command -v go >/dev/null || die "go is not installed"
command -v npm >/dev/null || die "npm is not installed"
[[ -f "$OLD_DB" ]] || warn "old database not found at $OLD_DB — passkeys cannot be imported"

# The old dashboard must still be answering, or this is not a cutover.
if docker ps --format '{{.Names}}' | grep -qx oliver-dashboard; then
  ok "old dashboard is running"
else
  warn "old dashboard is not running"
fi

ss -lntp 2>/dev/null | grep -q ':13000 ' && ok "port 13000 is in use (old dashboard)" || warn "nothing on port 13000"

echo
echo "Build"
( cd "$REPO/web" && npm ci --silent && npm run build >/dev/null ) && ok "frontend built"
( cd "$REPO" && go build -o /tmp/dashboardd.new ./cmd/dashboardd ) && ok "binary built"
( cd "$REPO" && go test ./... >/dev/null ) && ok "tests pass"

if [[ $CHECK_ONLY -eq 1 ]]; then
  echo
  echo "Check only — nothing was changed. Re-run without --check to cut over."
  exit 0
fi

echo
echo "Install"
install -d -m 0750 "$STATE"
install -m 0755 /tmp/dashboardd.new "$BINARY"                 && ok "installed $BINARY"
install -m 0644 "$REPO/deploy/dashboard.service" "$UNIT"      && ok "installed $UNIT"
systemctl daemon-reload

# Bring the passkeys across before the first start so sign-in works immediately.
if [[ -f "$OLD_DB" ]] && [[ ! -f "$STATE/dashboard.sqlite" ]]; then
  "$BINARY" import-legacy "$OLD_DB" && ok "passkeys imported"
fi

echo
echo "Switch"
# Free port 13000 before the new service claims it.
docker compose -f "$REPO/compose.yml" stop dashboard 2>/dev/null && ok "old dashboard stopped"
systemctl disable --now dashboard-agent.service 2>/dev/null && ok "old host agent stopped" || true

systemctl enable --now dashboard.service && ok "dashboard.service started"

echo
echo "Verify"
for i in $(seq 30); do
  if curl -sf http://127.0.0.1:13000/api/health >/dev/null; then
    ok "new dashboard answering on 13000"
    break
  fi
  [[ $i -eq 30 ]] && die "new dashboard did not come up — run: journalctl -u dashboard -n 50"
  sleep 1
done

curl -sf https://dash.marcusson.dev/api/health >/dev/null \
  && ok "reachable through Caddy at dash.marcusson.dev" \
  || warn "not reachable through Caddy yet — check the edge config"

echo
echo "Done. Sign in at https://dash.marcusson.dev with your existing passkey."
echo
echo "If something is wrong, roll back with:"
echo "  systemctl disable --now dashboard.service"
echo "  systemctl enable --now dashboard-agent.service"
echo "  docker compose -f $REPO/compose.yml start dashboard"
echo
echo "Once you are happy, the old tree can go:"
echo "  docker compose -f $REPO/compose.yml rm -f dashboard"
echo "  rm -rf $REPO/backend $REPO/frontend $REPO/Dockerfile $REPO/compose.yml"
