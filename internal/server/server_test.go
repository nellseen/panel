package server

import (
	"os"
	"testing"

	"github.com/termux-nodepanel/nodepanel/internal/process"
	"github.com/termux-nodepanel/nodepanel/internal/storage"
)

func TestServerServiceLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nodepanel_svc_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	procMgr := process.NewManager(store, 1)
	svc := NewService(store, procMgr)

	// 1. Create Server using Express Template
	created, err := svc.CreateServer(CreateServerRequest{
		Name:        "Test API Service",
		Description: "A test service with express template",
		Port:        3090,
		Template:    "basic_http",
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	if created.ID == "" || created.Port != 3090 {
		t.Errorf("unexpected created server properties: %+v", created)
	}

	// 2. Fetch server detail
	detail, err := svc.GetServerDetail(created.ID)
	if err != nil {
		t.Fatalf("failed to get detail: %v", err)
	}
	if detail.Name != "Test API Service" {
		t.Errorf("mismatch name in detail: %s", detail.Name)
	}

	// 3. Start server
	if err := svc.StartServer(created.ID); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	// 4. Detail with live metrics
	detailRunning, _ := svc.GetServerDetail(created.ID)
	if detailRunning.Status != process.StateRunning {
		t.Logf("current status: %s", detailRunning.Status)
	}

	// 5. Stop server
	if err := svc.StopServer(created.ID); err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	// 6. Delete server
	if err := svc.DeleteServer(created.ID); err != nil {
		t.Fatalf("failed to delete server: %v", err)
	}
}
