#!/bin/bash
set -euo pipefail
SERVER_URL="${SERVER_URL:-http://localhost:8080}"
DEVICE_ID="${DEVICE_ID:-edge-$(head -c4 /dev/urandom | xxd -p)}"

echo "Enrolling device $DEVICE_ID against $SERVER_URL..."
curl -sf "$SERVER_URL/healthz" > /dev/null || { echo "Server unreachable"; exit 1; }

SERVER_URL="$SERVER_URL" DEVICE_ID="$DEVICE_ID" PROFILE="${PROFILE:-alpine-edge-secure}" \
  go run ./cmd/agent

echo ""
echo "Device: $SERVER_URL/api/v1/devices"
echo "Events: $SERVER_URL/api/v1/events"
echo "Dashboard: $SERVER_URL"
