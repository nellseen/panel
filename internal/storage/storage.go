package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AutoRestartPolicy defines restart conditions.
type AutoRestartPolicy string

const (
	RestartNever     AutoRestartPolicy = "never"
	RestartAlways    AutoRestartPolicy = "always"
	RestartOnFailure AutoRestartPolicy = "on-failure"
)

// ServerModel represents metadata for a managed server.
type ServerModel struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Port         int               `json:"port"`
	EntryFile    string            `json:"entry_file"`
	StartScript  string            `json:"start_script"`
	Status       string            `json:"status"` // created, installing, starting, running, stopping, stopped, crashed, failed
	AutoRestart  AutoRestartPolicy `json:"auto_restart"`
	EnvVars      map[string]string `json:"env_vars"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	RepoURL      string            `json:"repo_url,omitempty"`
	LastExitCode int               `json:"last_exit_code"`
	PID          int               `json:"pid,omitempty"`
	CPUPercent   float64           `json:"cpu_percent,omitempty"`
	MemoryMB     float64           `json:"memory_mb,omitempty"`
	UptimeSec    int64             `json:"uptime_sec,omitempty"`
}

// ActivityLog represents an audit log entry.
type ActivityLog struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	ServerID  string    `json:"server_id"`
	Details   string    `json:"details"`
	Status    string    `json:"status"`
}

// FileEntry represents a file or directory inside a server directory.
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// Store encapsulates SQLite database persistence and file management.
type Store struct {
	dbPath     string
	baseDir    string
	mu         sync.RWMutex
	servers    map[string]*ServerModel
	activities []ActivityLog
}

var (
	ErrServerNotFound  = errors.New("server not found")
	ErrPathTraversal   = errors.New("access denied: path traversal outside server directory detected")
	ErrPortConflict    = errors.New("specified port is already assigned to another running server")
	ErrInvalidServerID = errors.New("invalid server ID format")
)

// NewStore creates or opens the SQLite database and base storage directories.
func NewStore(baseDir string) (*Store, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base dir: %w", err)
	}

	serversDir := filepath.Join(baseDir, "servers")
	if err := os.MkdirAll(serversDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create servers dir: %w", err)
	}

	dbPath := filepath.Join(baseDir, "nodepanel.db")
	s := &Store{
		dbPath:     dbPath,
		baseDir:    baseDir,
		servers:    make(map[string]*ServerModel),
		activities: make([]ActivityLog, 0),
	}

	if err := s.initSQLite(); err != nil {
		return nil, fmt.Errorf("failed to init SQLite: %w", err)
	}

	if err := s.loadAll(); err != nil {
		return nil, fmt.Errorf("failed to load data from SQLite: %w", err)
	}

	return s, nil
}

func (s *Store) initSQLite() error {
	schema := `
	CREATE TABLE IF NOT EXISTS servers (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT,
		port INTEGER,
		entry_file TEXT,
		start_script TEXT,
		status TEXT,
		auto_restart TEXT,
		env_vars TEXT,
		created_at TEXT,
		updated_at TEXT,
		repo_url TEXT,
		last_exit_code INTEGER
	);

	CREATE TABLE IF NOT EXISTS activity_logs (
		id TEXT PRIMARY KEY,
		timestamp TEXT,
		action TEXT,
		server_id TEXT,
		details TEXT,
		status TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_servers_status ON servers(status);
	CREATE INDEX IF NOT EXISTS idx_activity_timestamp ON activity_logs(timestamp);
	`
	return s.execSQL(schema)
}

func (s *Store) execSQL(sqlQuery string) error {
	// Execute via sqlite3 CLI
	cmd := exec.Command("sqlite3", s.dbPath, sqlQuery)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite error (%v): %s", err, string(out))
	}
	return nil
}

func (s *Store) loadAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Load servers using sqlite3 JSON output
	cmd := exec.Command("sqlite3", "-json", s.dbPath, "SELECT * FROM servers;")
	out, err := cmd.CombinedOutput()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		var rawList []map[string]interface{}
		if err := json.Unmarshal(out, &rawList); err == nil {
			for _, item := range rawList {
				id, _ := item["id"].(string)
				name, _ := item["name"].(string)
				desc, _ := item["description"].(string)
				portF, _ := item["port"].(float64)
				entry, _ := item["entry_file"].(string)
				script, _ := item["start_script"].(string)
				status, _ := item["status"].(string)
				restart, _ := item["auto_restart"].(string)
				envStr, _ := item["env_vars"].(string)
				repoURL, _ := item["repo_url"].(string)
				exitCodeF, _ := item["last_exit_code"].(float64)

				envVars := make(map[string]string)
				_ = json.Unmarshal([]byte(envStr), &envVars)

				srv := &ServerModel{
					ID:           id,
					Name:         name,
					Description:  desc,
					Port:         int(portF),
					EntryFile:    entry,
					StartScript:  script,
					Status:       status,
					AutoRestart:  AutoRestartPolicy(restart),
					EnvVars:      envVars,
					RepoURL:      repoURL,
					LastExitCode: int(exitCodeF),
					CreatedAt:    time.Now(),
					UpdatedAt:    time.Now(),
				}
				if srv.AutoRestart == "" {
					srv.AutoRestart = RestartNever
				}
				s.servers[srv.ID] = srv
			}
		}
	}

	// Load recent activities
	cmdAct := exec.Command("sqlite3", "-json", s.dbPath, "SELECT * FROM activity_logs ORDER BY timestamp DESC LIMIT 100;")
	if outAct, err := cmdAct.CombinedOutput(); err == nil && len(strings.TrimSpace(string(outAct))) > 0 {
		var rawAct []map[string]interface{}
		if err := json.Unmarshal(outAct, &rawAct); err == nil {
			for _, it := range rawAct {
				id, _ := it["id"].(string)
				act, _ := it["action"].(string)
				sID, _ := it["server_id"].(string)
				dt, _ := it["details"].(string)
				st, _ := it["status"].(string)
				tStr, _ := it["timestamp"].(string)
				ts, _ := time.Parse(time.RFC3339, tStr)
				if ts.IsZero() {
					ts = time.Now()
				}
				s.activities = append(s.activities, ActivityLog{
					ID:        id,
					Timestamp: ts,
					Action:    act,
					ServerID:  sID,
					Details:   dt,
					Status:    st,
				})
			}
		}
	}

	return nil
}

// SaveServer inserts or updates a server record in memory and SQLite.
func (s *Store) SaveServer(server *ServerModel) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	server.UpdatedAt = time.Now()
	if server.CreatedAt.IsZero() {
		server.CreatedAt = server.UpdatedAt
	}

	s.servers[server.ID] = server

	envBytes, _ := json.Marshal(server.EnvVars)
	envEscaped := strings.ReplaceAll(string(envBytes), "'", "''")
	nameEscaped := strings.ReplaceAll(server.Name, "'", "''")
	descEscaped := strings.ReplaceAll(server.Description, "'", "''")
	entryEscaped := strings.ReplaceAll(server.EntryFile, "'", "''")
	scriptEscaped := strings.ReplaceAll(server.StartScript, "'", "''")
	repoEscaped := strings.ReplaceAll(server.RepoURL, "'", "''")

	query := fmt.Sprintf(`
		INSERT INTO servers (id, name, description, port, entry_file, start_script, status, auto_restart, env_vars, created_at, updated_at, repo_url, last_exit_code)
		VALUES ('%s', '%s', '%s', %d, '%s', '%s', '%s', '%s', '%s', '%s', '%s', '%s', %d)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			description=excluded.description,
			port=excluded.port,
			entry_file=excluded.entry_file,
			start_script=excluded.start_script,
			status=excluded.status,
			auto_restart=excluded.auto_restart,
			env_vars=excluded.env_vars,
			updated_at=excluded.updated_at,
			repo_url=excluded.repo_url,
			last_exit_code=excluded.last_exit_code;
	`,
		server.ID, nameEscaped, descEscaped, server.Port, entryEscaped, scriptEscaped,
		server.Status, server.AutoRestart, envEscaped, server.CreatedAt.Format(time.RFC3339),
		server.UpdatedAt.Format(time.RFC3339), repoEscaped, server.LastExitCode,
	)

	return s.execSQL(query)
}

// GetServer returns a server model copy.
func (s *Store) GetServer(id string) (*ServerModel, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	srv, ok := s.servers[id]
	if !ok {
		return nil, ErrServerNotFound
	}
	cp := *srv
	return &cp, nil
}

// ListServers returns all tracked servers.
func (s *Store) ListServers() []*ServerModel {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*ServerModel, 0, len(s.servers))
	for _, srv := range s.servers {
		cp := *srv
		list = append(list, &cp)
	}
	return list
}

// DeleteServer removes server record and deletes its directory.
func (s *Store) DeleteServer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.servers[id]; !ok {
		return ErrServerNotFound
	}

	delete(s.servers, id)
	_ = s.execSQL(fmt.Sprintf("DELETE FROM servers WHERE id = '%s';", id))

	// Delete project directory safely
	dir := s.getServerDirPath(id)
	return os.RemoveAll(dir)
}

// RecordActivity logs a user or system action into SQLite.
func (s *Store) RecordActivity(action, serverID, details, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := ActivityLog{
		ID:        strconv.FormatInt(time.Now().UnixNano(), 10),
		Timestamp: time.Now(),
		Action:    action,
		ServerID:  serverID,
		Details:   details,
		Status:    status,
	}

	s.activities = append([]ActivityLog{entry}, s.activities...)
	if len(s.activities) > 200 {
		s.activities = s.activities[:200]
	}

	detailsEscaped := strings.ReplaceAll(details, "'", "''")
	query := fmt.Sprintf(`
		INSERT INTO activity_logs (id, timestamp, action, server_id, details, status)
		VALUES ('%s', '%s', '%s', '%s', '%s', '%s');
	`, entry.ID, entry.Timestamp.Format(time.RFC3339), action, serverID, detailsEscaped, status)

	_ = s.execSQL(query)
}

// ListActivities returns latest activity logs.
func (s *Store) ListActivities() []ActivityLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]ActivityLog, len(s.activities))
	copy(res, s.activities)
	return res
}

// GetServerDir returns the validated directory path for a server.
func (s *Store) GetServerDir(serverID string) (string, error) {
	if !isValidID(serverID) {
		return "", ErrInvalidServerID
	}
	dir := s.getServerDirPath(serverID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *Store) getServerDirPath(serverID string) string {
	return filepath.Join(s.baseDir, "servers", serverID)
}

func isValidID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, ch := range id {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}

// SafeResolvePath verifies that the requested relative path stays strictly within the server directory.
func (s *Store) SafeResolvePath(serverID, relPath string) (string, error) {
	base, err := s.GetServerDir(serverID)
	if err != nil {
		return "", err
	}

	baseClean := filepath.Clean(base)
	full := filepath.Clean(filepath.Join(baseClean, relPath))

	// Ensure full path has base as prefix and does not escape with ..
	rel, err := filepath.Rel(baseClean, full)
	if err != nil || strings.HasPrefix(rel, "..") || rel == "." && strings.Contains(relPath, "..") {
		return "", ErrPathTraversal
	}
	return full, nil
}

// ListFiles lists directory contents within server root.
func (s *Store) ListFiles(serverID, subDir string) ([]FileEntry, error) {
	target, err := s.SafeResolvePath(serverID, subDir)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}

	serverRoot, _ := s.GetServerDir(serverID)
	result := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fullChild := filepath.Join(target, e.Name())
		relFromRoot, _ := filepath.Rel(serverRoot, fullChild)

		result = append(result, FileEntry{
			Name:    e.Name(),
			Path:    relFromRoot,
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return result, nil
}

// ReadFile safely reads file contents within server root.
func (s *Store) ReadFile(serverID, filePath string) (string, error) {
	target, err := s.SafeResolvePath(serverID, filePath)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile safely writes or overwrites file contents.
func (s *Store) WriteFile(serverID, filePath, content string) error {
	target, err := s.SafeResolvePath(serverID, filePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0644)
}

// DeleteFile safely removes a file or directory within server root.
func (s *Store) DeleteFile(serverID, filePath string) error {
	target, err := s.SafeResolvePath(serverID, filePath)
	if err != nil {
		return err
	}
	return os.RemoveAll(target)
}
