#!/usr/bin/env bash
set -euo pipefail
TOKEN=$(sudo cat /var/lib/oliver-dashboard/agent-token)
echo "agent unauth should be 401:"
curl -sS -o /tmp/dashboard-agent-noauth -w '%{http_code}\n' http://172.17.0.1:13001/v1/health | grep -q '^401$'
echo "agent auth health:"
curl -fsS -H "Authorization: Bearer $TOKEN" http://172.17.0.1:13001/v1/health >/dev/null
echo "dashboard health:"
curl -fsS http://127.0.0.1:13000/api/health >/dev/null
echo "dashboard protected route should be 401:"
curl -sS -o /tmp/dashboard-noauth -w '%{http_code}\n' http://127.0.0.1:13000/api/services | grep -q '^401$'
echo "container health:"
test "$(docker inspect --format '{{.State.Health.Status}}' oliver-dashboard)" = healthy
echo "agent bind:"
ss -ltn '( sport = :13001 )' | grep -q '172.17.0.1:13001'
echo ok
