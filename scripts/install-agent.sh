#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
sudo mkdir -p /var/lib/oliver-dashboard
if [ ! -s /var/lib/oliver-dashboard/agent-token ]; then
  openssl rand -hex 32 | sudo tee /var/lib/oliver-dashboard/agent-token >/dev/null
fi
sudo chmod 0644 /var/lib/oliver-dashboard/agent-token
CID=$(docker create oliver-dashboard:local)
docker cp "$CID:/usr/local/bin/dashboard-agent" /tmp/dashboard-agent
docker rm "$CID" >/dev/null
sudo install -m 0755 /tmp/dashboard-agent /usr/local/bin/dashboard-agent
sudo install -m 0644 deploy/dashboard-agent.service /etc/systemd/system/dashboard-agent.service
sudo systemctl daemon-reload
sudo systemctl enable --now dashboard-agent
sudo systemctl restart dashboard-agent
systemctl --no-pager --full status dashboard-agent | head -30
