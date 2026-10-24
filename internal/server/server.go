package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/termux-nodepanel/nodepanel/internal/process"
	"github.com/termux-nodepanel/nodepanel/internal/repository"
	"github.com/termux-nodepanel/nodepanel/internal/storage"
)

type CreateServerRequest struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Port        int                       `json:"port"`
	EntryFile   string                    `json:"entry_file"`
	AutoRestart storage.AutoRestartPolicy `json:"auto_restart"`
	EnvVars     map[string]string         `json:"env_vars"`
	Template    string                    `json:"template"` // "blank", "express_api", "basic_http", "background_worker"
	RepoURL     string                    `json:"repo_url,omitempty"`
	Branch      string                    `json:"branch,omitempty"`
}

type ServerDetailResponse struct {
	storage.ServerModel
	Metrics *process.ServerMetrics `json:"metrics"`
	Logs    []process.LogLine      `json:"recent_logs"`
}

type Service struct {
	store          *storage.Store
	procMgr        *process.Manager
	activeCloneOps map[string]string
}

func NewService(store *storage.Store, procMgr *process.Manager) *Service {
	return &Service{
		store:          store,
		procMgr:        procMgr,
		activeCloneOps: make(map[string]string),
	}
}

func (s *Service) GenerateServerID() string {
	bytes := make([]byte, 4)
	_, _ = rand.Read(bytes)
	return fmt.Sprintf("srv-%d-%s", time.Now().Unix()%100000, hex.EncodeToString(bytes))
}

// CreateServer provisions a new server directory, seeds files according to template, and saves metadata.
func (s *Service) CreateServer(req CreateServerRequest) (*storage.ServerModel, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("server name cannot be empty")
	}

	id := s.GenerateServerID()
	srvDir, err := s.store.GetServerDir(id)
	if err != nil {
		return nil, err
	}

	entryFile := strings.TrimSpace(req.EntryFile)
	if entryFile == "" {
		entryFile = "index.js"
	}

	port := req.Port
	if port <= 0 {
		port = s.findAvailablePort(3001)
	}

	envVars := req.EnvVars
	if envVars == nil {
		envVars = make(map[string]string)
	}
	envVars["PORT"] = fmt.Sprintf("%d", port)
	if _, ok := envVars["NODE_ENV"]; !ok {
		envVars["NODE_ENV"] = "production"
	}

	// Seed template files if not a Git clone
	if req.RepoURL == "" {
		if err := s.seedTemplate(srvDir, req.Template, name, port); err != nil {
			return nil, fmt.Errorf("failed to seed template: %w", err)
		}
	}

	srv := &storage.ServerModel{
		ID:          id,
		Name:        name,
		Description: req.Description,
		Port:        port,
		EntryFile:   entryFile,
		StartScript: "start",
		Status:      process.StateCreated,
		AutoRestart: req.AutoRestart,
		EnvVars:     envVars,
		RepoURL:     req.RepoURL,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if srv.AutoRestart == "" {
		srv.AutoRestart = storage.RestartNever
	}

	if err := s.store.SaveServer(srv); err != nil {
		return nil, err
	}

	s.store.RecordActivity("create_server", id, fmt.Sprintf("Created server '%s' on port %d", name, port), "success")
	return srv, nil
}

func (s *Service) findAvailablePort(startPort int) int {
	existingServers := s.store.ListServers()
	usedPorts := make(map[int]bool)
	for _, srv := range existingServers {
		if srv.Port > 0 {
			usedPorts[srv.Port] = true
		}
	}
	p := startPort
	for {
		if !usedPorts[p] {
			return p
		}
		p++
	}
}

func (s *Service) seedTemplate(dir, template, name string, port int) error {
	switch template {
	case "express_api":
		pkg := fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "description": "Express REST API managed by Termux NodePanel",
  "main": "index.js",
  "scripts": {
    "start": "node index.js"
  },
  "dependencies": {
    "express": "^4.21.2"
  }
}`, strings.ToLower(strings.ReplaceAll(name, " ", "-")))

		code := fmt.Sprintf(`const express = require('express');
const app = express();
const PORT = process.env.PORT || %d;

app.use(express.json());

app.get('/', (req, res) => {
  res.json({
    status: 'online',
    app: '%s',
    environment: process.env.NODE_ENV || 'development',
    time: new Date().toISOString()
  });
});

app.get('/api/ping', (req, res) => {
  res.json({ pong: true, timestamp: Date.now() });
});

app.listen(PORT, '0.0.0.0', () => {
  console.log('[%s] Express Server listening on http://localhost:' + PORT);
});
`, port, name, name)

		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)
		return os.WriteFile(filepath.Join(dir, "index.js"), []byte(code), 0644)

	case "background_worker":
		pkg := fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "main": "index.js",
  "scripts": {
    "start": "node index.js"
  }
}`, strings.ToLower(strings.ReplaceAll(name, " ", "-")))

		code := fmt.Sprintf(`console.log('[%s] Worker initialized. Starting periodic processing...');

let counter = 0;
const interval = setInterval(() => {
  counter++;
  console.log('[' + new Date().toISOString() + '] Task #' + counter + ' processed successfully.');
}, 3000);

process.on('SIGTERM', () => {
  console.log('[%s] Received SIGTERM signal. Performing cleanup...');
  clearInterval(interval);
  process.exit(0);
});
`, name, name)

		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)
		return os.WriteFile(filepath.Join(dir, "index.js"), []byte(code), 0644)

	default: // basic_http or blank
		pkg := fmt.Sprintf(`{
  "name": "%s",
  "version": "1.0.0",
  "main": "index.js",
  "scripts": {
    "start": "node index.js"
  }
}`, strings.ToLower(strings.ReplaceAll(name, " ", "-")))

		code := fmt.Sprintf(`const http = require('http');
const PORT = process.env.PORT || %d;

const server = http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ message: 'Hello from %s!', time: new Date() }));
});

server.listen(PORT, '0.0.0.0', () => {
  console.log('[%s] HTTP server running on port ' + PORT);
});
`, port, name, name)

		_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkg), 0644)
		return os.WriteFile(filepath.Join(dir, "index.js"), []byte(code), 0644)
	}
}

// CloneAndImport provisions a server by cloning a Git repository.
func (s *Service) CloneAndImport(ctx context.Context, repoURL, branch, name string, autoInstall bool, out io.Writer) (*storage.ServerModel, error) {
	if name == "" {
		parts := strings.Split(strings.TrimSuffix(repoURL, ".git"), "/")
		if len(parts) > 0 {
			name = parts[len(parts)-1]
		}
		if name == "" {
			name = "Git Imported Server"
		}
	}

	id := s.GenerateServerID()
	srvDir, err := s.store.GetServerDir(id)
	if err != nil {
		return nil, err
	}

	if err := repository.Clone(ctx, repoURL, branch, srvDir, out); err != nil {
		_ = os.RemoveAll(srvDir)
		return nil, fmt.Errorf("clone failed: %w", err)
	}

	inspection, _ := repository.InspectProject(srvDir)
	entryFile := "index.js"
	if inspection != nil && inspection.SuggestedEntry != "" {
		entryFile = inspection.SuggestedEntry
	}

	port := s.findAvailablePort(3001)
	srv := &storage.ServerModel{
		ID:          id,
		Name:        name,
		Description: fmt.Sprintf("Imported from %s (branch: %s)", repoURL, branch),
		Port:        port,
		EntryFile:   entryFile,
		StartScript: "start",
		Status:      process.StateCreated,
		AutoRestart: storage.RestartNever,
		EnvVars: map[string]string{
			"PORT":     fmt.Sprintf("%d", port),
			"NODE_ENV": "production",
		},
		RepoURL:   repoURL,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.store.SaveServer(srv); err != nil {
		return nil, err
	}

	s.store.RecordActivity("git_clone", id, fmt.Sprintf("Cloned repository %s", repoURL), "success")

	// Optionally install dependencies
	if autoInstall && inspection != nil && inspection.HasPackageJSON {
		_ = s.procMgr.RunNpmInstall(ctx, id, out)
	}

	return srv, nil
}

// GetServerDetail returns server model along with real-time process metrics and in-memory logs.
func (s *Service) GetServerDetail(serverID string) (*ServerDetailResponse, error) {
	srv, err := s.store.GetServer(serverID)
	if err != nil {
		return nil, err
	}

	metrics, _ := s.procMgr.GetMetrics(serverID)
	logs := s.procMgr.GetLogs(serverID)

	return &ServerDetailResponse{
		ServerModel: *srv,
		Metrics:     metrics,
		Logs:        logs,
	}, nil
}

func (s *Service) StartServer(serverID string) error {
	err := s.procMgr.Start(serverID)
	if err == nil {
		s.store.RecordActivity("start_server", serverID, "Server started", "success")
	} else {
		s.store.RecordActivity("start_server", serverID, fmt.Sprintf("Start failed: %v", err), "failed")
	}
	return err
}

func (s *Service) StopServer(serverID string) error {
	err := s.procMgr.Stop(serverID)
	if err == nil {
		s.store.RecordActivity("stop_server", serverID, "Server stopped gracefully", "success")
	} else {
		s.store.RecordActivity("stop_server", serverID, fmt.Sprintf("Stop failed: %v", err), "failed")
	}
	return err
}

func (s *Service) RestartServer(serverID string) error {
	err := s.procMgr.Restart(serverID)
	if err == nil {
		s.store.RecordActivity("restart_server", serverID, "Server restarted", "success")
	}
	return err
}

func (s *Service) DeleteServer(serverID string) error {
	// First ensure process is stopped
	_ = s.procMgr.Stop(serverID)
	err := s.store.DeleteServer(serverID)
	if err == nil {
		s.store.RecordActivity("delete_server", serverID, "Server deleted", "success")
	}
	return err
}
