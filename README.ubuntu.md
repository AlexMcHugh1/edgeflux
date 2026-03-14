# EdgeFlux Beginner Setup on Ubuntu

This guide helps a beginner install Go, Docker, Node.js (npm), and Vite on Ubuntu and run EdgeFlux successfully.

## Who this is for

- You are new to Go, Docker, and Node.js
- You are on Ubuntu 22.04 or 24.04
- You want the quickest path to a working dashboard and test device

## 1. Install required packages

Open a terminal and run:

```bash
sudo apt update
sudo apt install -y ca-certificates curl git make jq
```

## 2. Install Go 1.22+

Ubuntu package repos can lag behind, so use the official Go tarball.

```bash
cd /tmp
curl -LO https://go.dev/dl/go1.22.12.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.22.12.linux-amd64.tar.gz
```

Add Go to your shell PATH:

```bash
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
```

Verify:

```bash
go version
```

Expected: `go version go1.22.x linux/amd64` (or newer).

## 3. Install Docker Engine + Compose plugin

```bash
sudo apt install -y docker.io docker-compose-v2
sudo systemctl enable --now docker
```

Allow your user to run Docker without sudo:

```bash
sudo usermod -aG docker "$USER"
newgrp docker
```

Verify:

```bash
docker version
docker compose version
```

## 4. Install Node.js (npm) and Vite

Install Node.js (includes npm):

```bash
curl -fsSL https://deb.nodesource.com/setup_20.x | sudo -E bash -
sudo apt install -y nodejs
```

Verify:

```bash
node --version
npm --version
```

Install Vite globally:

```bash
npm install -g vite
```

Verify:

```bash
vite --version
```

## 5. Get the project

```bash
git clone <your-edgeflux-repo-url> edgeflux
cd edgeflux
```

If you already have the repository, just `cd` into it.

## 6. Install frontend dependencies

If the dashboard uses Vite and npm, install dependencies:

```bash
cd ui
npm install
```

## 7. Start EdgeFlux (recommended: Docker Compose)

```bash
docker compose up -d --build
```

Check health:

```bash
curl -sf http://localhost:8080/healthz && echo "server ok"
```

Open dashboard:

- http://localhost:8080

If you want to run the dashboard frontend separately with Vite:

```bash
cd ui
npm run dev
```

## 8. Enroll a test device

Run an agent from source:

```bash
DEVICE_ID=edge-demo01 SERVER_URL=http://localhost:8080 go run ./cmd/agent
```

Then in dashboard:

1. Find `edge-demo01`
2. Click `Approve`
3. The device should continue enrollment and start sending health updates

## 9. Useful commands

```bash
# List running containers
docker ps

# Follow logs from server container
docker logs -f edgeflux-edgeflux-server-1

# Run Go checks
go test ./...

# Stop stack
docker compose down

# Run frontend dev server
cd ui && npm run dev
```

## 10. Ubuntu compatibility notes

This repository is configured to work on Ubuntu with Docker Compose by default.

- Simulator containers use the Compose network (`edgeflux_default`) so they can reach the server reliably on Linux.
- Simulator server URL defaults are set to service names instead of `host.docker.internal` for better Linux behavior.

If you run the server directly with `go run ./cmd/server` and use simulator mode, set these env vars explicitly:

```bash
LOCAL_SIMULATOR_ENABLED=true \
LOCAL_SIMULATOR_SERVER_URL=http://172.17.0.1:8080 \
LOCAL_SIMULATOR_NETWORK=bridge \
go run ./cmd/server
```

## 11. Troubleshooting

`docker: permission denied`:

```bash
sudo usermod -aG docker "$USER"
# then log out and back in
```

`curl http://localhost:8080/healthz` fails:

```bash
docker compose ps
docker compose logs edgeflux-server --tail=100
```

Agent exits with pending/revoked status:

- Device must be approved in the UI before enrollment can complete.
- If revoked, start the agent again to trigger a new pending approval request.

## 12. Optional: run without Docker

You can run only Go binaries locally:

```bash
go run ./cmd/server
DEVICE_ID=edge-local01 SERVER_URL=http://localhost:8080 go run ./cmd/agent
```

For the frontend dashboard:

```bash
cd <dashboard-directory>
npm install
vite
```

This is useful for development, but Compose is easier for first-time setup.

