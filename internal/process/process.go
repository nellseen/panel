package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/termux-nodepanel/nodepanel/internal/storage"
)

// ProcessState lifecycle constants.
const (
	StateCreated    = "created"
	StateInstalling = "installing"
	StateStarting   = "starting"
	StateRunning    = "running"
	StateStopping   = "stopping"
	StateStopped    = "stopped"
	StateCrashed    = "crashed"
	StateFailed     = "failed"
)

// LogLine represents a timestamped console output line.
type LogLine struct {
	Timestamp time.Time `json:"timestamp"`
	Stream    string    `json:"stream"` // "stdout", "stderr", "system"
	Text      string    `json:"text"`
}

// LogSubscriber is a function invoked whenever a new log line arrives.
type LogSubscriber func(serverID string, line LogLine)

// StatusSubscriber is a function invoked whenever server status changes.
type StatusSubscriber func(serverID string, oldStatus, newStatus string, exitCode int)

// RingBuffer stores a bounded number of log lines in memory.
type RingBuffer struct {
	mu       sync.RWMutex
	capacity int
	lines    []LogLine
}

func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		capacity: capacity,
		lines:    make([]LogLine, 0, capacity),
	}
}

func (rb *RingBuffer) Append(line LogLine) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.lines) >= rb.capacity {
		rb.lines = rb.lines[1:]
	}
	rb.lines = append(rb.lines, line)
}

func (rb *RingBuffer) GetAll() []LogLine {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	cp := make([]LogLine, len(rb.lines))
	copy(cp, rb.lines)
	return cp
}

// ActiveProcess maintains state for a single running Node.js process.
type ActiveProcess struct {
	mu              sync.Mutex
	serverID        string
	cmd             *exec.Cmd
	cancel          context.CancelFunc
	stdin           io.WriteCloser
	pid             int
	startTime       time.Time
	status          string
	exitCode        int
	logs            *RingBuffer
	logFile         *os.File
	restartCount    int
	intentionalStop bool
	lastCPUTime     int64
	lastSample      time.Time
}

// Manager orchestrates all child processes, log streaming, and lifecycle events.
type Manager struct {
	mu                sync.RWMutex
	processes         map[string]*ActiveProcess
	store             *storage.Store
	installSem        chan struct{} // Concurrency limiter for dependency installs
	logSubscribers    []LogSubscriber
	statusSubscribers []StatusSubscriber
	subMu             sync.RWMutex
}

var (
	ErrProcessAlreadyRunning = errors.New("process is already running")
	ErrProcessNotRunning     = errors.New("process is not running")
)

// NewManager creates a new native process manager with dependency install concurrency limit.
func NewManager(store *storage.Store, maxConcurrentInstalls int) *Manager {
	if maxConcurrentInstalls <= 0 {
		maxConcurrentInstalls = 1 // Android/Termux safe default
	}
	return &Manager{
		processes:  make(map[string]*ActiveProcess),
		store:      store,
		installSem: make(chan struct{}, maxConcurrentInstalls),
	}
}

// SubscribeLogs registers a listener for real-time console lines.
func (m *Manager) SubscribeLogs(sub LogSubscriber) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.logSubscribers = append(m.logSubscribers, sub)
}

// SubscribeStatus registers a listener for status transitions.
func (m *Manager) SubscribeStatus(sub StatusSubscriber) {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	m.statusSubscribers = append(m.statusSubscribers, sub)
}

func (m *Manager) broadcastLog(serverID string, line LogLine) {
	m.subMu.RLock()
	defer m.subMu.RUnlock()
	for _, sub := range m.logSubscribers {
		go sub(serverID, line)
	}
}

func (m *Manager) broadcastStatus(serverID string, oldStatus, newStatus string, exitCode int) {
	m.subMu.RLock()
	defer m.subMu.RUnlock()
	for _, sub := range m.statusSubscribers {
		go sub(serverID, oldStatus, newStatus, exitCode)
	}
}

// Start launches a Node.js server according to its configuration.
func (m *Manager) Start(serverID string) error {
	m.mu.Lock()
	p, exists := m.processes[serverID]
	if exists && (p.status == StateRunning || p.status == StateStarting) {
		m.mu.Unlock()
		return ErrProcessAlreadyRunning
	}

	server, err := m.store.GetServer(serverID)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	srvDir, err := m.store.GetServerDir(serverID)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	// Prepare log file
	logDirPath := filepath.Join(srvDir, "logs")
	_ = os.MkdirAll(logDirPath, 0755)
	logFile, _ := os.OpenFile(filepath.Join(logDirPath, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)

	ringBuf := NewRingBuffer(1000)
	ctx, cancel := context.WithCancel(context.Background())

	ap := &ActiveProcess{
		serverID:        serverID,
		cancel:          cancel,
		status:          StateStarting,
		logs:            ringBuf,
		logFile:         logFile,
		startTime:       time.Now(),
		intentionalStop: false,
	}
	m.processes[serverID] = ap
	m.mu.Unlock()

	oldStatus := server.Status
	server.Status = StateStarting
	_ = m.store.SaveServer(server)
	m.broadcastStatus(serverID, oldStatus, StateStarting, 0)

	// Determine command: check if package.json has start script, or use entry file directly
	var cmd *exec.Cmd
	entryFile := strings.TrimSpace(server.EntryFile)
	if entryFile == "" {
		entryFile = "index.js"
	}

	// If entry file starts with npm run, parse arguments safely without shell injection
	if strings.HasPrefix(entryFile, "npm run ") {
		scriptName := strings.TrimSpace(strings.TrimPrefix(entryFile, "npm run "))
		cmd = exec.CommandContext(ctx, "npm", "run", scriptName)
	} else if entryFile == "npm start" {
		cmd = exec.CommandContext(ctx, "npm", "start")
	} else {
		// Run node <entryFile>
		cmd = exec.CommandContext(ctx, "node", entryFile)
	}

	cmd.Dir = srvDir

	// Setup Environment Variables (inheriting current PATH and system env, adding custom envs)
	customEnv := os.Environ()
	for k, v := range server.EnvVars {
		customEnv = append(customEnv, fmt.Sprintf("%s=%s", k, v))
	}
	if server.Port > 0 {
		customEnv = append(customEnv, fmt.Sprintf("PORT=%d", server.Port))
	}
	cmd.Env = customEnv

	// Process group isolation for clean signal delivery across Linux/Android
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		m.updateStatus(serverID, StateFailed, -1)
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		m.updateStatus(serverID, StateFailed, -1)
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	stdinPipe, _ := cmd.StdinPipe()

	ap.cmd = cmd
	ap.stdin = stdinPipe

	if err := cmd.Start(); err != nil {
		cancel()
		m.updateStatus(serverID, StateFailed, -1)
		m.appendLog(ap, "system", fmt.Sprintf("[nodepanel] Process execution failed to start: %v", err))
		return fmt.Errorf("failed to start process: %w", err)
	}

	ap.pid = cmd.Process.Pid
	m.updateStatus(serverID, StateRunning, 0)
	m.appendLog(ap, "system", fmt.Sprintf("[nodepanel] Process started (PID: %d)", ap.pid))

	// Stream stdout & stderr
	go m.streamPipe(ap, stdoutPipe, "stdout")
	go m.streamPipe(ap, stderrPipe, "stderr")

	// Monitor exit in background goroutine
	go m.watchProcess(ap)

	return nil
}

func (m *Manager) streamPipe(ap *ActiveProcess, r io.Reader, stream string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		text := scanner.Text()
		m.appendLog(ap, stream, text)
	}
}

func (m *Manager) appendLog(ap *ActiveProcess, stream, text string) {
	line := LogLine{
		Timestamp: time.Now(),
		Stream:    stream,
		Text:      text,
	}
	ap.logs.Append(line)

	if ap.logFile != nil {
		fmt.Fprintf(ap.logFile, "[%s][%s] %s\n", line.Timestamp.Format(time.RFC3339), stream, text)
	}

	m.broadcastLog(ap.serverID, line)
}

func (m *Manager) watchProcess(ap *ActiveProcess) {
	err := ap.cmd.Wait()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	ap.mu.Lock()
	ap.exitCode = exitCode
	wasIntentional := ap.intentionalStop
	ap.mu.Unlock()

	var newStatus string
	if wasIntentional {
		newStatus = StateStopped
		m.appendLog(ap, "system", fmt.Sprintf("[nodepanel] Process stopped gracefully with exit code %d", exitCode))
	} else if exitCode == 0 {
		newStatus = StateStopped
		m.appendLog(ap, "system", fmt.Sprintf("[nodepanel] Process completed cleanly with code 0"))
	} else {
		newStatus = StateCrashed
		m.appendLog(ap, "system", fmt.Sprintf("[nodepanel] Process crashed with exit code %d", exitCode))
	}

	m.updateStatus(ap.serverID, newStatus, exitCode)

	// Evaluate auto-restart policy if not intentionally stopped
	if !wasIntentional {
		srv, err := m.store.GetServer(ap.serverID)
		if err == nil {
			shouldRestart := false
			if srv.AutoRestart == storage.RestartAlways {
				shouldRestart = true
			} else if srv.AutoRestart == storage.RestartOnFailure && exitCode != 0 {
				shouldRestart = true
			}

			if shouldRestart {
				ap.mu.Lock()
				ap.restartCount++
				count := ap.restartCount
				ap.mu.Unlock()

				if count <= 5 {
					backoff := time.Duration(count*2) * time.Second
					m.appendLog(ap, "system", fmt.Sprintf("[nodepanel] Auto-restarting in %v (attempt %d/5)...", backoff, count))
					go func() {
						time.Sleep(backoff)
						_ = m.Start(ap.serverID)
					}()
				} else {
					m.appendLog(ap, "system", "[nodepanel] Max auto-restart attempts reached (5). Halting.")
				}
			}
		}
	}
}

func (m *Manager) updateStatus(serverID, status string, exitCode int) {
	m.mu.Lock()
	if ap, ok := m.processes[serverID]; ok {
		ap.status = status
	}
	m.mu.Unlock()

	srv, err := m.store.GetServer(serverID)
	if err == nil {
		old := srv.Status
		srv.Status = status
		srv.LastExitCode = exitCode
		_ = m.store.SaveServer(srv)
		m.broadcastStatus(serverID, old, status, exitCode)
	}
}

// Stop terminates a server process gracefully with fallback SIGKILL.
func (m *Manager) Stop(serverID string) error {
	m.mu.Lock()
	ap, exists := m.processes[serverID]
	if !exists || ap.cmd == nil || ap.cmd.Process == nil {
		m.mu.Unlock()
		return ErrProcessNotRunning
	}
	ap.intentionalStop = true
	ap.status = StateStopping
	m.mu.Unlock()

	m.appendLog(ap, "system", "[nodepanel] Stopping process...")

	// 1. Send SIGTERM to process group
	pgid, err := syscall.Getpgid(ap.pid)
	if err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		_ = ap.cmd.Process.Signal(syscall.SIGTERM)
	}

	// 2. Wait up to 3 seconds for graceful shutdown
	done := make(chan error, 1)
	go func() {
		// Poll if process exited
		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			if err := ap.cmd.Process.Signal(syscall.Signal(0)); err != nil {
				done <- nil
				return
			}
		}
		done <- errors.New("timeout")
	}()

	select {
	case <-done:
		// Graceful exit success
	case <-time.After(3 * time.Second):
		// 3. Force kill if still active
		m.appendLog(ap, "system", "[nodepanel] Graceful timeout exceeded. Sending SIGKILL...")
		if pgid, err := syscall.Getpgid(ap.pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			_ = ap.cmd.Process.Kill()
		}
	}

	ap.cancel()
	m.updateStatus(serverID, StateStopped, 0)
	return nil
}

// Restart stops and restarts the server process.
func (m *Manager) Restart(serverID string) error {
	_ = m.Stop(serverID)
	// Brief wait to ensure ports are freed
	time.Sleep(300 * time.Millisecond)
	return m.Start(serverID)
}

// SendStdin writes text to server process stdin.
func (m *Manager) SendStdin(serverID, input string) error {
	m.mu.RLock()
	ap, ok := m.processes[serverID]
	m.mu.RUnlock()

	if !ok || ap.stdin == nil || ap.status != StateRunning {
		return ErrProcessNotRunning
	}

	if !strings.HasSuffix(input, "\n") {
		input += "\n"
	}
	_, err := ap.stdin.Write([]byte(input))
	return err
}

// GetLogs returns in-memory circular logs for a server.
func (m *Manager) GetLogs(serverID string) []LogLine {
	m.mu.RLock()
	ap, ok := m.processes[serverID]
	m.mu.RUnlock()

	if ok && ap.logs != nil {
		return ap.logs.GetAll()
	}

	// If process is not active, attempt to read from log file
	srvDir, err := m.store.GetServerDir(serverID)
	if err != nil {
		return nil
	}
	logFile := filepath.Join(srvDir, "logs", "server.log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	result := make([]LogLine, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		result = append(result, LogLine{
			Timestamp: time.Now(),
			Stream:    "file",
			Text:      l,
		})
	}
	return result
}

// ServerMetrics represents CPU and memory usage of a running server.
type ServerMetrics struct {
	PID        int     `json:"pid"`
	Status     string  `json:"status"`
	UptimeSec  int64   `json:"uptime_sec"`
	MemoryMB   float64 `json:"memory_mb"`
	CPUPercent float64 `json:"cpu_percent"`
}

// GetMetrics inspects /proc or ps to calculate CPU and memory usage.
func (m *Manager) GetMetrics(serverID string) (*ServerMetrics, error) {
	m.mu.RLock()
	ap, ok := m.processes[serverID]
	m.mu.RUnlock()

	if !ok || ap.status != StateRunning || ap.pid <= 0 {
		return &ServerMetrics{
			Status: StateStopped,
		}, nil
	}

	metrics := &ServerMetrics{
		PID:       ap.pid,
		Status:    ap.status,
		UptimeSec: int64(time.Since(ap.startTime).Seconds()),
	}

	// Read /proc/[pid]/statm or /proc/[pid]/status on Linux / Termux
	statusPath := fmt.Sprintf("/proc/%d/status", ap.pid)
	if data, err := os.ReadFile(statusPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, l := range lines {
			if strings.HasPrefix(l, "VmRSS:") {
				parts := strings.Fields(l)
				if len(parts) >= 2 {
					if kb, err := strconv.ParseFloat(parts[1], 64); err == nil {
						metrics.MemoryMB = kb / 1024.0
					}
				}
			}
		}
	} else {
		// Fallback to ps -o rss= -p <pid>
		cmd := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(ap.pid))
		if out, err := cmd.Output(); err == nil {
			if kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil {
				metrics.MemoryMB = kb / 1024.0
			}
		}
	}

	return metrics, nil
}

// RunNpmInstall executes 'npm install' with concurrency limits and progress streaming.
func (m *Manager) RunNpmInstall(ctx context.Context, serverID string, outWriter io.Writer) error {
	// Acquire semaphore lock to protect low-memory Android/Termux devices
	fmt.Fprintf(outWriter, "[nodepanel-deps] Queuing dependency installation (concurrency slot)...\n")
	select {
	case m.installSem <- struct{}{}:
		defer func() { <-m.installSem }()
	case <-ctx.Done():
		return ctx.Err()
	}

	srvDir, err := m.store.GetServerDir(serverID)
	if err != nil {
		return err
	}

	m.updateStatus(serverID, StateInstalling, 0)
	defer m.updateStatus(serverID, StateStopped, 0)

	fmt.Fprintf(outWriter, "[nodepanel-deps] Running npm install in %s...\n", srvDir)

	cmd := exec.CommandContext(ctx, "npm", "install", "--no-audit", "--no-fund")
	cmd.Dir = srvDir

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		fmt.Fprintf(outWriter, "%s\n", scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("npm install failed: %w", err)
	}

	fmt.Fprintf(outWriter, "[nodepanel-deps] npm install completed successfully!\n")
	return nil
}
