package process

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/termux-nodepanel/nodepanel/internal/storage"
)

func TestProcessLifecycleAndIsolation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nodepanel_proc_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	pm := NewManager(store, 1)

	// Create a mock server that runs a simple node script
	srvID := "srv-mock-node"
	srv := &storage.ServerModel{
		ID:          srvID,
		Name:        "Mock Node Server",
		Port:        3456,
		EntryFile:   "index.js",
		Status:      StateCreated,
		AutoRestart: storage.RestartNever,
		EnvVars:     map[string]string{"TEST_VAR": "HELLO_NODEPANEL"},
	}
	if err := store.SaveServer(srv); err != nil {
		t.Fatal(err)
	}

	// Write mock node script that prints logs and listens for a brief period
	srvDir, _ := store.GetServerDir(srvID)
	scriptContent := `
console.log("Server started with TEST_VAR=" + process.env.TEST_VAR);
const interval = setInterval(() => {
  console.log("Heartbeat tick");
}, 200);
process.on('SIGTERM', () => {
  console.log("Received SIGTERM, shutting down cleanly");
  clearInterval(interval);
  process.exit(0);
});
`
	if err := os.WriteFile(filepath.Join(srvDir, "index.js"), []byte(scriptContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Start process
	if err := pm.Start(srvID); err != nil {
		t.Fatalf("failed to start process: %v", err)
	}

	// Verify running state
	time.Sleep(500 * time.Millisecond)
	updatedSrv, _ := store.GetServer(srvID)
	if updatedSrv.Status != StateRunning {
		t.Errorf("expected status %s, got %s", StateRunning, updatedSrv.Status)
	}

	// Verify logs captured
	logs := pm.GetLogs(srvID)
	if len(logs) == 0 {
		t.Error("expected non-empty logs")
	}

	foundLog := false
	for _, l := range logs {
		if l.Text == "Server started with TEST_VAR=HELLO_NODEPANEL" {
			foundLog = true
		}
	}
	if !foundLog {
		t.Logf("logs retrieved: %+v", logs)
	}

	// 2. Metrics check
	metrics, err := pm.GetMetrics(srvID)
	if err != nil {
		t.Errorf("metrics error: %v", err)
	}
	if metrics.PID <= 0 {
		t.Errorf("expected valid PID, got %d", metrics.PID)
	}

	// 3. Stop process
	if err := pm.Stop(srvID); err != nil {
		t.Fatalf("failed to stop process: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	stoppedSrv, _ := store.GetServer(srvID)
	if stoppedSrv.Status != StateStopped {
		t.Errorf("expected status %s, got %s", StateStopped, stoppedSrv.Status)
	}
}

func TestRingBuffer(t *testing.T) {
	rb := NewRingBuffer(3)
	rb.Append(LogLine{Text: "line 1"})
	rb.Append(LogLine{Text: "line 2"})
	rb.Append(LogLine{Text: "line 3"})
	rb.Append(LogLine{Text: "line 4"})

	all := rb.GetAll()
	if len(all) != 3 {
		t.Fatalf("expected 3 items, got %d", len(all))
	}
	if all[0].Text != "line 2" || all[2].Text != "line 4" {
		t.Errorf("unexpected ring buffer elements: %+v", all)
	}
}
