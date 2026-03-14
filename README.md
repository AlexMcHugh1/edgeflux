# EdgeFlux SecureOS — Zero-Touch Enrollment Platform

Minimal, observable, secure device provisioning for Alpine-based edge devices.
Inspired by Mainflux's microservice IoT architecture.

Beginner setup guide (Ubuntu): `README.ubuntu.md`

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                CLOUD  (edgeflux-server)                   │
│                                                          │
│  ┌──────────┐  ┌───────────────┐  ┌──────────────────┐  │
│  │ Root CA  │  │  Enrollment   │  │  MQTTS Broker    │  │
│  │ + IntCA  │──│  Service      │──│  (mTLS on 8883)  │  │
│  └──────────┘  └───────┬───────┘  └────────┬─────────┘  │
│                        │                    │            │
│  ┌──────────┐  ┌───────┴───────┐  ┌────────┴─────────┐  │
│  │ OS Image │  │  Container    │  │  SSH Relay        │  │
│  │ Server   │  │  Registry     │  │  (reverse tunnel) │  │
│  └──────────┘  └───────────────┘  └──────────────────┘  │
│                        │                                 │
│  ┌─────────────────────┴──────────────────────────────┐  │
│  │  Observability: SSE /events + REST /api/v1         │  │
│  └────────────────────────────────────────────────────┘  │
└────────────────────────┬─────────────────────────────────┘
                         │  mTLS (8443) + MQTTS (8883)
┌────────────────────────┴─────────────────────────────────┐
│                EDGE  (edgeflux-agent)                     │
│                                                          │
│  ┌──────────┐  ┌───────────────┐  ┌──────────────────┐  │
│  │ TPM /    │  │  MQTTS Client │  │  containerd      │  │
│  │ Identity │──│  + mTLS       │──│  (pull + run)    │  │
│  └──────────┘  └───────────────┘  └──────────────────┘  │
│                                                          │
│  SecureOS Alpine (read-only rootfs, dm-verity)           │
└──────────────────────────────────────────────────────────┘
```

## Quick Start

```bash
# Option A: Run directly (requires Go 1.22+)
make ui-build                 # Build the React dashboard once
go run ./cmd/server &         # Start cloud platform
go run ./cmd/agent             # Enroll a device
open http://localhost:8080     # Observability dashboard

# Option B: Docker Compose
./scripts/bootstrap-pki.sh     # Generate certificates
docker compose up              # Start everything (server + agent + mosquitto + Vault)

# Option C: Local simulator profile (device create can auto-spawn simulator containers)
# Runs simulator-enabled server on http://localhost:18080
docker compose --profile local-sim up -d edgeflux-server-sim sqlite vault

# If 18080 is already used, pick another host port:
LOCAL_SIM_SERVER_PORT=18082 docker compose --profile local-sim up -d edgeflux-server-sim sqlite vault

# Enroll multiple devices
DEVICE_ID=edge-001 go run ./cmd/agent
DEVICE_ID=edge-002 go run ./cmd/agent
DEVICE_ID=edge-003 go run ./cmd/agent
```

## What Happens

### Server Boot
1. Generates Root CA (EC P-384, 10yr)
2. Generates Intermediate Enrollment CA (EC P-384, 5yr, signed by Root)
3. Generates Server TLS cert (EC P-256, 1yr, signed by Intermediate)
4. Starts REST API + SSE event stream on :8080
5. All events broadcast in real-time to the dashboard

### Agent Enrollment (7 phases)
1. **Identity** — generates EC P-256 keypair + CSR
2. **Attestation** — collects TPM endorsement key + firmware hash
3. **Enroll** — POST CSR to server, receives signed device certificate
4. **mTLS** — reconnects using device cert (mutual TLS)
5. **OS** — requests deployment manifest (Alpine hardened image)
6. **Containers** — pulls container manifests from mTLS registry
7. **SSH** — receives authorized keys + reverse tunnel config

### Every step emits events to the SSE stream, visible in the dashboard.

### Certificate Lifecycle
- Approved device certificates are short-lived by default (`10m`).
- The TTL is configurable with `DEVICE_CERT_TTL_MINUTES` on the server.
- Approved cert records are persisted in Vault KV v2 under:
    - `secret/data/edgeflux/devices/<device_id>`
- Revocations are recorded under:
    - `secret/data/edgeflux/revocations/<device_id>`

Inspect a device cert record in Vault:

```bash
curl -s \
    -H "X-Vault-Token: root" \
    http://localhost:8200/v1/secret/data/edgeflux/devices/edge-001 | jq
```

## Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/` | GET | Observability dashboard |
| `/events` | GET | SSE real-time event stream |
| `/healthz` | GET | Health check |
| `/api/v1/stats` | GET | Aggregate platform stats |
| `/api/v1/devices` | GET | All device states |
| `/api/v1/events` | GET | Stored events (paginated) |
| `/api/v1/pki/info` | GET | PKI certificate info |
| `/api/v1/enroll` | POST | Device enrollment |
| `/api/v1/devices/{id}/simulate` | POST | Start local simulator container for device |
| `/api/v1/deploy/{id}/os` | GET | OS deployment manifest |
| `/api/v1/deploy/{id}/containers` | GET | Container manifest |
| `/api/v1/config/{id}/ssh` | GET | SSH configuration |

`POST /api/v1/devices` also accepts `simulate: true` to start a simulator immediately after creation when local simulator mode is enabled.

## Frontend

The dashboard is now a Vite-built React app under `ui/`.

```bash
cd ui
npm install
npm run build      # writes static assets to ui/dist for the Go server

# Optional: run the frontend separately during development
npm run dev
```

The Go server serves the built assets from `ui/dist` at `/` with an SPA fallback. Docker image builds include the frontend automatically.

## Local Simulator Mode

Use this mode to have each newly created device spawn its own local simulator container.

1. Start simulator profile:

```bash
docker compose --profile local-sim up -d edgeflux-server-sim sqlite vault

# Alternate port if 18080 is busy:
LOCAL_SIM_SERVER_PORT=18082 docker compose --profile local-sim up -d edgeflux-server-sim sqlite vault
```

2. Open dashboard at `http://localhost:${LOCAL_SIM_SERVER_PORT:-18080}`.
3. Create a device from UI with `Start local simulator container after create` enabled.

Notes:
- This profile mounts `/var/run/docker.sock` into the server container; use it only on trusted local development machines.
- Keep either `edgeflux-server` (`:8080`) or `edgeflux-server-sim` (`:18080`) as your active UI/API endpoint to avoid confusion.

## Project Structure

```
edgeflux/
├── cmd/
│   ├── server/main.go        # Cloud platform entry point
│   └── agent/main.go         # Edge agent entry point
├── internal/
│   ├── pki/pki.go            # Real x509 certificate generation
│   ├── store/events.go       # Event store + SSE broadcasting
│   ├── enrollment/service.go # Enrollment logic + manifests
│   └── api/server.go         # REST API + SSE server
├── ui/
│   ├── src/                  # React application source
│   ├── index.html            # Vite entry template
│   └── package.json          # Frontend build scripts
├── configs/mosquitto.conf    # MQTTS broker config (mTLS)
├── scripts/
│   ├── bootstrap-pki.sh      # OpenSSL PKI generation
│   └── enroll-device.sh      # Convenience enrollment script
├── deployments/
│   ├── Dockerfile.server
│   └── Dockerfile.agent
├── docker-compose.yml
└── go.mod
```

## Security Model

- **PKI**: Root CA → Intermediate CA → Device Certs (real x509, EC curves)
- **mTLS**: Bidirectional TLS 1.3 authentication
- **MQTTS**: MQTT over TLS with client certificate auth
- **OS**: dm-verity, read-only rootfs, tmpfs overlays, secure boot
- **Containers**: read-only rootfs, no-new-privileges, seccomp, AppArmor
- **SSH**: key-only auth, no password, no forwarding, reverse tunnel
- **Bootstrap certs**: 24hr expiry, replaced with device cert after enrollment
