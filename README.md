# Termux NodePanel

A lightweight, modular, environment-aware Node.js process and server management panel engineered specifically for:
1. **Termux Native** on Android devices.
2. **Ubuntu** via `proot-distro`.
3. **Standard Linux** environments (Debian, Arch, Alpine, Fedora, etc.).

Inspired by server operational ergonomics (such as Pterodactyl), **Termux NodePanel** eliminates heavy container requirements: **No Docker, No Kubernetes, No cloud dependencies, No external telemetry.** It executes processes directly on the local host with native Go process supervision, goroutine tracking, process-group isolation, SQLite metadata persistence, and real-time WebSocket console streaming.

---

## 1. System Architecture

```
                      +---------------------------------------+
                      |       Web Dashboard (React + TS)      |
                      |  Terminal Console | Files | Packages  |
                      +-------------------+-------------------+
                                          |
                        HTTP REST / WebSocket (/ws)
                                          |
                      +-------------------v-------------------+
                      |      Go Backend Daemon (nodepanel)    |
                      +---------------------------------------+
                        |        |            |             |
        +---------------+   +----+----+   +---+---+   +-----+-----+
        |                   |         |   |       |   |           |
  [Runtime Detector]  [Process Mgr] [Git] [SQLite] [Pkg Manager]
  - Termux native      - os/exec     - Clone      - Native pkg
  - Ubuntu proot       - pgid signals - Pull       - apt-get
  - Linux generic      - CPU/RAM stats - Inspect   - Concurrency lock
```

### Key Modules:
- **`cmd/panel/`**: Standalone Go CLI providing `start`, `doctor`, `runtime`, and `version`.
- **`internal/runtime/`**: Multi-signal runtime inspector detecting Termux `$PREFIX`, Android kernel markers, proot ptrace/mounts, OS release, and system limits.
- **`internal/packages/`**: Environment-aware system package manager with a concurrency mutex lock to prevent concurrent `pkg` or `apt-get` conflicts.
- **`internal/process/`**: Native child process manager handling `created -> installing -> starting -> running -> stopping -> stopped -> crashed -> failed` lifecycles, circular log ring buffers (1,000 lines), and process group termination (`SIGTERM` -> graceful wait -> `SIGKILL`).
- **`internal/storage/`**: SQLite metadata store (`servers`, `activity_logs`), isolated `data/servers/<id>/` directories, and strict path traversal protection.
- **`internal/repository/`**: Git clone and pull executor using direct argument lists (no shell string concatenation) with protocol safety validation.
- **`internal/api/`**: RESTful API endpoints and zero-dependency RFC 6455 WebSocket streaming upgrader.

---

## 2. Getting Started & Installation

### Option A: Running in Termux Native on Android
```bash
# 1. Update Termux packages and install prerequisites
pkg update && pkg install -y git golang nodejs sqlite

# 2. Clone repository
git clone https://github.com/termux-nodepanel/nodepanel.git
cd nodepanel

# 3. Build frontend and compile Go daemon
npm install
npm run build
go build -o bin/nodepanel ./cmd/panel

# 4. Run diagnostic check
./bin/nodepanel doctor

# 5. Start daemon (default loopback: http://127.0.0.1:8080)
./bin/nodepanel start --port=8080
```

### Option B: Running inside Ubuntu (`proot-distro`)
```bash
# Inside proot-distro login ubuntu
apt-get update && apt-get install -y git golang nodejs npm sqlite3 build-essential

cd nodepanel
npm install && npm run build
go build -o bin/nodepanel ./cmd/panel
./bin/nodepanel start --port=8080
```

### Option C: Development Mode (Port 3000)
```bash
npm install
npm run dev
# Launches Express proxy bridge + Vite HMR on http://localhost:3000 connected to Go daemon on port 8080
```

---

## 3. CLI Commands

| Command | Description |
|---|---|
| `nodepanel doctor` | Inspects environment, host architecture, core executables, limits, and capabilities |
| `nodepanel runtime` | Outputs machine-readable JSON runtime detection payload |
| `nodepanel version` | Displays version and build target |
| `nodepanel start [--port=8080] [--host=127.0.0.1] [--data=./data]` | Starts HTTP and WebSocket daemon |

---

## 4. API Endpoints

- `GET /api/health`: Health status, daemon uptime, and environment flag.
- `GET /api/runtime`: Detailed environment diagnostic payload.
- `GET /api/servers`: Lists all server instances with live CPU and RSS memory metrics.
- `POST /api/servers`: Creates a new server (Templates: Express API, Basic HTTP, Worker, or Blank).
- `GET /api/servers/:id`: Detailed server state, live metrics, and recent console logs.
- `PUT /api/servers/:id`: Updates server port, entry file, auto-restart policy, or environment variables.
- `DELETE /api/servers/:id`: Terminates process and safely removes server directory.
- `POST /api/servers/:id/start`: Launches server process.
- `POST /api/servers/:id/stop`: Sends graceful `SIGTERM` followed by fallback `SIGKILL`.
- `POST /api/servers/:id/restart`: Reboots process with port release delay.
- `POST /api/servers/:id/install`: Runs concurrency-locked `npm install` inside server directory.
- `GET /api/servers/:id/logs`: Retrieves in-memory ring-buffer logs.
- `GET /api/servers/:id/files?path=<relPath>&content=<bool>`: Reads file content or lists directory.
- `POST /api/servers/:id/files`: Saves or creates file.
- `DELETE /api/servers/:id/files?path=<relPath>`: Deletes file.
- `POST /api/git/clone`: Clones public Git repository with progress stream.
- `GET /api/packages`: Lists managed system packages (`git`, `ffmpeg`, `python3`, `imagemagick`, etc.).
- `POST /api/packages/install`: Triggers environment package manager (`pkg` or `apt-get`).
- `POST /api/packages/uninstall`: Safely uninstalls non-critical packages.
- `GET /api/activity`: Returns SQLite audit log timeline.
- `WS /ws`: Real-time WebSocket connection for bidirectional stdin/stdout streaming and status events.

---

## 5. Testing Pipeline

Termux NodePanel includes a full 5-stage automated test suite:

```bash
# Run all test stages automatically:
./scripts/test_pipeline.sh
```

### Stage Details:
1. **Stage 1 — Static Tests**: `gofmt` compliance check, `go vet ./...`, TypeScript compilation (`tsc --noEmit`), and Vite static build validation.
2. **Stage 2 — Unit Tests**:
   - `runtime_test.go`: Environment classifier & Termux `$PREFIX` simulation.
   - `packages_test.go`: Registry resolution & concurrency lock mutex.
   - `storage_test.go`: SQLite table schemas & path traversal security defense.
   - `repository_test.go`: URL protocol sanitization & package.json inspector.
3. **Stage 3 — Integration Tests**:
   - `process_test.go`: Real child process lifecycle, signal trapping, and circular ring buffer.
   - `server_test.go`: Template generation, port allocation, and database updates.
   - `api_test.go`: HTTP endpoints and router validation.
4. **Stage 4 — Concurrency & E2E Tests**:
   - `tests/integration_test.go`: Tests two real Node.js processes running simultaneously, verifies process isolation (terminating Server A does not affect Server B), and executes a 100-request concurrent API load test.
5. **Stage 5 — Real Runtime Verification**: Verifies `./bin/nodepanel doctor`, `./bin/nodepanel runtime`, and executable health.

---

## 6. Android Termux Operational Tips

1. **Preventing Background Kills**:
   Android's Low Memory Killer (OOM) and Doze mode can kill background processes. To prevent this:
   - Run `termux-wake-lock` in Termux before starting servers.
   - In Android Settings &gt; Apps &gt; Termux &gt; Battery, set Battery usage to **Unrestricted**.
2. **Auto-Start on Device Boot**:
   Install `Termux:Boot` from F-Droid and add a script inside `~/.termux/boot/start-nodepanel.sh` invoking `nodepanel start`.
3. **Security**:
   By default, NodePanel binds to `127.0.0.1` (loopback). If accessing from another computer on the same Wi-Fi, run `./bin/nodepanel start --host=0.0.0.0` and protect your network perimeter with firewall rules or VPN (e.g. Tailscale).
