#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose config >/tmp/compose-config
docker compose build dashboard
./scripts/install-agent.sh
docker compose up -d dashboard
./scripts/verify.sh
