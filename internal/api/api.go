package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/termux-nodepanel/nodepanel/internal/packages"
	"github.com/termux-nodepanel/nodepanel/internal/process"
	"github.com/termux-nodepanel/nodepanel/internal/runtime"
	"github.com/termux-nodepanel/nodepanel/internal/server"
	"github.com/termux-nodepanel/nodepanel/internal/storage"
)

// ServerApp aggregates all services and handlers.
type ServerApp struct {
	envInfo   *runtime.EnvironmentInfo
	store     *storage.Store
	procMgr   *process.Manager
	pkgMgr    *packages.PackageManager
	srvSvc    *server.Service
	startTime time.Time

	// Connected WebSocket clients
	wsMu    sync.RWMutex
	wsConns map[*WSConn]map[string]bool // connection -> subscribed serverIDs
}

func NewServerApp(env *runtime.EnvironmentInfo, store *storage.Store, procMgr *process.Manager, pkgMgr *packages.PackageManager, srvSvc *server.Service) *ServerApp {
	app := &ServerApp{
		envInfo:   env,
		store:     store,
		procMgr:   procMgr,
		pkgMgr:    pkgMgr,
		srvSvc:    srvSvc,
		startTime: time.Now(),
		wsConns:   make(map[*WSConn]map[string]bool),
	}

	// Wire process manager logs and status changes to active WebSocket clients
	procMgr.SubscribeLogs(func(serverID string, line process.LogLine) {
		app.broadcastWSEvent("log", map[string]interface{}{
			"server_id": serverID,
			"timestamp": line.Timestamp.Format(time.RFC3339),
			"stream":    line.Stream,
			"text":      line.Text,
		}, serverID)
	})

	procMgr.SubscribeStatus(func(serverID string, oldStatus, newStatus string, exitCode int) {
		app.broadcastWSEvent("status", map[string]interface{}{
			"server_id":  serverID,
			"old_status": oldStatus,
			"new_status": newStatus,
			"exit_code":  exitCode,
		}, "")
	})

	return app
}

// Router returns an http.Handler implementing all endpoints.
func (app *ServerApp) Router() http.Handler {
	mux := http.NewServeMux()

	// Base endpoints
	mux.HandleFunc("/api/health", app.handleHealth)
	mux.HandleFunc("/api/runtime", app.handleRuntime)
	mux.HandleFunc("/api/activity", app.handleActivity)

	// Packages
	mux.HandleFunc("/api/packages", app.handlePackages)
	mux.HandleFunc("/api/packages/install", app.handlePackageInstall)
	mux.HandleFunc("/api/packages/uninstall", app.handlePackageUninstall)

	// Git
	mux.HandleFunc("/api/git/clone", app.handleGitClone)

	// Servers
	mux.HandleFunc("/api/servers", app.handleServers)
	mux.HandleFunc("/api/servers/", app.handleServerDispatch)

	// WebSocket
	mux.HandleFunc("/ws", app.handleWebSocket)
	mux.HandleFunc("/api/ws", app.handleWebSocket)

	return app.withCORS(mux)
}

func (app *ServerApp) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (app *ServerApp) jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (app *ServerApp) errorResponse(w http.ResponseWriter, status int, message string) {
	app.jsonResponse(w, status, map[string]interface{}{
		"error":   true,
		"message": message,
	})
}

// GET /api/health
func (app *ServerApp) handleHealth(w http.ResponseWriter, r *http.Request) {
	app.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":         "healthy",
		"version":        "1.0.0",
		"uptime_seconds": int64(time.Since(app.startTime).Seconds()),
		"environment":    app.envInfo.Type,
		"timestamp":      time.Now().Format(time.RFC3339),
	})
}

// GET /api/runtime
func (app *ServerApp) handleRuntime(w http.ResponseWriter, r *http.Request) {
	// Refresh executable statuses
	info := runtime.DetectEnvironment()
	app.jsonResponse(w, http.StatusOK, info)
}

// GET /api/activity
func (app *ServerApp) handleActivity(w http.ResponseWriter, r *http.Request) {
	logs := app.store.ListActivities()
	app.jsonResponse(w, http.StatusOK, logs)
}

// GET /api/packages
func (app *ServerApp) handlePackages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	pkgs := app.pkgMgr.ListPackages()
	busy, op := app.pkgMgr.IsBusy()
	app.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"packages":  pkgs,
		"is_busy":   busy,
		"active_op": op,
	})
}

// POST /api/packages/install
func (app *ServerApp) handlePackageInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		PackageID string `json:"package_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageID == "" {
		app.errorResponse(w, http.StatusBadRequest, "Invalid request body: package_id required")
		return
	}

	var logBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	err := app.pkgMgr.Install(ctx, req.PackageID, &logBuf)
	output := logBuf.String()

	if err != nil {
		app.store.RecordActivity("install_package", "", fmt.Sprintf("Failed installing %s: %v", req.PackageID, err), "failed")
		app.jsonResponse(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"output":  output,
		})
		return
	}

	app.store.RecordActivity("install_package", "", fmt.Sprintf("Installed package %s", req.PackageID), "success")
	app.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"output":  output,
	})
}

// POST /api/packages/uninstall
func (app *ServerApp) handlePackageUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		PackageID string `json:"package_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PackageID == "" {
		app.errorResponse(w, http.StatusBadRequest, "Invalid request body: package_id required")
		return
	}

	var logBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	err := app.pkgMgr.Uninstall(ctx, req.PackageID, &logBuf)
	output := logBuf.String()

	if err != nil {
		app.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"output":  output,
		})
		return
	}

	app.store.RecordActivity("uninstall_package", "", fmt.Sprintf("Uninstalled package %s", req.PackageID), "success")
	app.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"output":  output,
	})
}

// POST /api/git/clone
func (app *ServerApp) handleGitClone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		RepoURL     string `json:"repo_url"`
		Branch      string `json:"branch"`
		Name        string `json:"name"`
		AutoInstall bool   `json:"auto_install"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.RepoURL == "" {
		app.errorResponse(w, http.StatusBadRequest, "Invalid request body: repo_url is required")
		return
	}

	var logBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	srv, err := app.srvSvc.CloneAndImport(ctx, req.RepoURL, req.Branch, req.Name, req.AutoInstall, &logBuf)
	output := logBuf.String()

	if err != nil {
		app.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
			"output":  output,
		})
		return
	}

	app.jsonResponse(w, http.StatusCreated, map[string]interface{}{
		"success": true,
		"server":  srv,
		"output":  output,
	})
}

// GET /api/servers or POST /api/servers
func (app *ServerApp) handleServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		servers := app.store.ListServers()
		// Attach live metrics to running servers
		type ServerWithMetrics struct {
			*storage.ServerModel
			Metrics *process.ServerMetrics `json:"metrics"`
		}
		res := make([]ServerWithMetrics, 0, len(servers))
		for _, s := range servers {
			metrics, _ := app.procMgr.GetMetrics(s.ID)
			res = append(res, ServerWithMetrics{
				ServerModel: s,
				Metrics:     metrics,
			})
		}
		app.jsonResponse(w, http.StatusOK, res)

	case http.MethodPost:
		var req server.CreateServerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			app.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
			return
		}
		created, err := app.srvSvc.CreateServer(req)
		if err != nil {
			app.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		app.jsonResponse(w, http.StatusCreated, created)

	default:
		app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// Dispatcher for /api/servers/:id and sub-routes
func (app *ServerApp) handleServerDispatch(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/servers/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		app.errorResponse(w, http.StatusNotFound, "Server ID missing")
		return
	}

	serverID := parts[0]

	// Sub-routes:
	// /api/servers/:id
	// /api/servers/:id/start
	// /api/servers/:id/stop
	// /api/servers/:id/restart
	// /api/servers/:id/logs
	// /api/servers/:id/install
	// /api/servers/:id/files
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			detail, err := app.srvSvc.GetServerDetail(serverID)
			if err != nil {
				app.errorResponse(w, http.StatusNotFound, "Server not found")
				return
			}
			app.jsonResponse(w, http.StatusOK, detail)

		case http.MethodDelete:
			if err := app.srvSvc.DeleteServer(serverID); err != nil {
				app.errorResponse(w, http.StatusInternalServerError, err.Error())
				return
			}
			app.jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})

		case http.MethodPut:
			// Update server configuration
			var req storage.ServerModel
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				app.errorResponse(w, http.StatusBadRequest, "Invalid JSON payload")
				return
			}
			existing, err := app.store.GetServer(serverID)
			if err != nil {
				app.errorResponse(w, http.StatusNotFound, "Server not found")
				return
			}
			if req.Name != "" {
				existing.Name = req.Name
			}
			if req.Description != "" {
				existing.Description = req.Description
			}
			if req.Port > 0 {
				existing.Port = req.Port
			}
			if req.EntryFile != "" {
				existing.EntryFile = req.EntryFile
			}
			if req.AutoRestart != "" {
				existing.AutoRestart = req.AutoRestart
			}
			if req.EnvVars != nil {
				existing.EnvVars = req.EnvVars
			}
			_ = app.store.SaveServer(existing)
			app.jsonResponse(w, http.StatusOK, existing)

		default:
			app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	subAction := parts[1]
	switch subAction {
	case "start":
		if r.Method != http.MethodPost {
			app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if err := app.srvSvc.StartServer(serverID); err != nil {
			app.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		app.jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "status": "starting"})

	case "stop":
		if r.Method != http.MethodPost {
			app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if err := app.srvSvc.StopServer(serverID); err != nil {
			app.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		app.jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "status": "stopping"})

	case "restart":
		if r.Method != http.MethodPost {
			app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if err := app.srvSvc.RestartServer(serverID); err != nil {
			app.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		app.jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true, "status": "restarting"})

	case "logs":
		if r.Method != http.MethodGet {
			app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		logs := app.procMgr.GetLogs(serverID)
		app.jsonResponse(w, http.StatusOK, logs)

	case "install":
		if r.Method != http.MethodPost {
			app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		var logBuf bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		err := app.procMgr.RunNpmInstall(ctx, serverID, &logBuf)
		output := logBuf.String()
		if err != nil {
			app.jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
				"success": false,
				"error":   err.Error(),
				"output":  output,
			})
			return
		}
		app.jsonResponse(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"output":  output,
		})

	case "files":
		app.handleServerFiles(w, r, serverID)

	default:
		app.errorResponse(w, http.StatusNotFound, "Unknown server sub-route")
	}
}

// Handles file operations for a server: GET, POST (save), DELETE
func (app *ServerApp) handleServerFiles(w http.ResponseWriter, r *http.Request, serverID string) {
	relPath := r.URL.Query().Get("path")

	switch r.Method {
	case http.MethodGet:
		// If query param "content" is true, read single file; otherwise list directory
		isContent, _ := strconv.ParseBool(r.URL.Query().Get("content"))
		if isContent {
			content, err := app.store.ReadFile(serverID, relPath)
			if err != nil {
				app.errorResponse(w, http.StatusBadRequest, err.Error())
				return
			}
			app.jsonResponse(w, http.StatusOK, map[string]interface{}{
				"path":    relPath,
				"content": content,
			})
		} else {
			files, err := app.store.ListFiles(serverID, relPath)
			if err != nil {
				app.errorResponse(w, http.StatusBadRequest, err.Error())
				return
			}
			app.jsonResponse(w, http.StatusOK, files)
		}

	case http.MethodPost:
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Path == "" {
			app.errorResponse(w, http.StatusBadRequest, "Invalid request: path is required")
			return
		}
		if err := app.store.WriteFile(serverID, req.Path, req.Content); err != nil {
			app.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		app.jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})

	case http.MethodDelete:
		targetPath := r.URL.Query().Get("path")
		if targetPath == "" {
			app.errorResponse(w, http.StatusBadRequest, "path parameter required")
			return
		}
		if err := app.store.DeleteFile(serverID, targetPath); err != nil {
			app.errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		app.jsonResponse(w, http.StatusOK, map[string]interface{}{"success": true})

	default:
		app.errorResponse(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// WebSocket handler for console streaming, status, and stdin
func (app *ServerApp) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	ws, err := UpgradeToWS(w, r)
	if err != nil {
		return
	}

	app.wsMu.Lock()
	app.wsConns[ws] = make(map[string]bool)
	app.wsMu.Unlock()

	defer func() {
		app.wsMu.Lock()
		delete(app.wsConns, ws)
		app.wsMu.Unlock()
		ws.Close()
	}()

	// Send initial hello
	_ = app.sendWS(ws, map[string]interface{}{
		"event": "connected",
		"time":  time.Now().Format(time.RFC3339),
	})

	for {
		msg, err := ws.ReadTextMessage()
		if err != nil {
			break
		}

		var payload struct {
			Action   string `json:"action"` // "subscribe", "unsubscribe", "stdin"
			ServerID string `json:"serverId"`
			Data     string `json:"data"`
		}
		if err := json.Unmarshal(msg, &payload); err != nil {
			continue
		}

		switch payload.Action {
		case "subscribe":
			if payload.ServerID != "" {
				app.wsMu.Lock()
				app.wsConns[ws][payload.ServerID] = true
				app.wsMu.Unlock()

				// Send recent logs immediately
				logs := app.procMgr.GetLogs(payload.ServerID)
				for _, l := range logs {
					_ = app.sendWS(ws, map[string]interface{}{
						"event": "log",
						"data": map[string]interface{}{
							"server_id": payload.ServerID,
							"timestamp": l.Timestamp.Format(time.RFC3339),
							"stream":    l.Stream,
							"text":      l.Text,
						},
					})
				}
			}

		case "unsubscribe":
			if payload.ServerID != "" {
				app.wsMu.Lock()
				delete(app.wsConns[ws], payload.ServerID)
				app.wsMu.Unlock()
			}

		case "stdin":
			if payload.ServerID != "" {
				_ = app.procMgr.SendStdin(payload.ServerID, payload.Data)
			}
		}
	}
}

func (app *ServerApp) sendWS(ws *WSConn, data interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return ws.WriteTextMessage(b)
}

func (app *ServerApp) broadcastWSEvent(event string, payload map[string]interface{}, filterServerID string) {
	app.wsMu.RLock()
	defer app.wsMu.RUnlock()

	packet := map[string]interface{}{
		"event": event,
		"data":  payload,
	}
	bytes, err := json.Marshal(packet)
	if err != nil {
		return
	}

	for ws, subs := range app.wsConns {
		if filterServerID != "" {
			if !subs[filterServerID] {
				continue
			}
		}
		_ = ws.WriteTextMessage(bytes)
	}
}
