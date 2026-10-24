package storage

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nodepanel_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	server := &ServerModel{
		ID:          "srv-test-1",
		Name:        "Test API Server",
		Description: "A test server for unit testing",
		Port:        3001,
		EntryFile:   "index.js",
		StartScript: "start",
		Status:      "created",
		AutoRestart: RestartNever,
		EnvVars:     map[string]string{"NODE_ENV": "production", "PORT": "3001"},
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	// 1. Save Server
	if err := store.SaveServer(server); err != nil {
		t.Fatalf("failed to save server: %v", err)
	}

	// 2. Retrieve Server
	retrieved, err := store.GetServer("srv-test-1")
	if err != nil {
		t.Fatalf("failed to get server: %v", err)
	}
	if retrieved.Name != "Test API Server" || retrieved.Port != 3001 {
		t.Errorf("mismatch server data: %+v", retrieved)
	}

	// 3. List Servers
	all := store.ListServers()
	if len(all) != 1 {
		t.Errorf("expected 1 server, got %d", len(all))
	}

	// 4. File operations & path traversal defense
	srvDir, err := store.GetServerDir("srv-test-1")
	if err != nil {
		t.Fatalf("failed to get server dir: %v", err)
	}
	if _, err := os.Stat(srvDir); err != nil {
		t.Fatalf("server directory not created: %v", err)
	}

	// Write file
	if err := store.WriteFile("srv-test-1", "index.js", "console.log('hello world');"); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Read file
	content, err := store.ReadFile("srv-test-1", "index.js")
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if content != "console.log('hello world');" {
		t.Errorf("unexpected file content: %s", content)
	}

	// Path Traversal Attack Test
	_, err = store.SafeResolvePath("srv-test-1", "../../etc/passwd")
	if err != ErrPathTraversal {
		t.Errorf("expected ErrPathTraversal, got: %v", err)
	}

	// Activity logging
	store.RecordActivity("create_server", "srv-test-1", "Created server srv-test-1", "success")
	acts := store.ListActivities()
	if len(acts) == 0 {
		t.Error("expected activity log to be recorded")
	}

	// Delete Server
	if err := store.DeleteServer("srv-test-1"); err != nil {
		t.Fatalf("failed to delete server: %v", err)
	}

	_, err = store.GetServer("srv-test-1")
	if err != ErrServerNotFound {
		t.Errorf("expected ErrServerNotFound, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(tempDir, "servers", "srv-test-1")); !os.IsNotExist(err) {
		t.Error("expected server directory to be deleted")
	}
}
