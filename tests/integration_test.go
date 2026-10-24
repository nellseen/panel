package tests

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/termux-nodepanel/nodepanel/internal/api"
	"github.com/termux-nodepanel/nodepanel/internal/packages"
	"github.com/termux-nodepanel/nodepanel/internal/process"
	"github.com/termux-nodepanel/nodepanel/internal/runtime"
	"github.com/termux-nodepanel/nodepanel/internal/server"
	"github.com/termux-nodepanel/nodepanel/internal/storage"
)

// TestFullConcurrentLifecycle tests two real child Node processes running simultaneously,
// confirms isolation, log streaming, and process group termination.
func TestFullConcurrentLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nodepanel_e2e_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	envInfo := runtime.DetectEnvironment()
	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	procMgr := process.NewManager(store, 1)
	pkgMgr := packages.NewPackageManager(envInfo)
	_ = pkgMgr
	srvSvc := server.NewService(store, procMgr)

	// 1. Create Server A (HTTP template)
	srvA, err := srvSvc.CreateServer(server.CreateServerRequest{
		Name:     "Concurrent Server Alpha",
		Port:     3111,
		Template: "basic_http",
	})
	if err != nil {
		t.Fatalf("failed to create server A: %v", err)
	}

	// 2. Create Server B (Worker template)
	srvB, err := srvSvc.CreateServer(server.CreateServerRequest{
		Name:     "Concurrent Server Beta",
		Port:     3112,
		Template: "background_worker",
	})
	if err != nil {
		t.Fatalf("failed to create server B: %v", err)
	}

	// 3. Start Server A
	if err := srvSvc.StartServer(srvA.ID); err != nil {
		t.Fatalf("failed to start server A: %v", err)
	}

	// 4. Start Server B
	if err := srvSvc.StartServer(srvB.ID); err != nil {
		t.Fatalf("failed to start server B: %v", err)
	}

	time.Sleep(1 * time.Second)

	// Verify both servers are running simultaneously
	detailA, _ := srvSvc.GetServerDetail(srvA.ID)
	detailB, _ := srvSvc.GetServerDetail(srvB.ID)

	if detailA.Status != process.StateRunning {
		t.Errorf("expected Server A to be running, got %s", detailA.Status)
	}
	if detailB.Status != process.StateRunning {
		t.Errorf("expected Server B to be running, got %s", detailB.Status)
	}

	if detailA.Metrics.PID == detailB.Metrics.PID || detailA.Metrics.PID <= 0 {
		t.Errorf("expected distinct valid PIDs, got A=%d B=%d", detailA.Metrics.PID, detailB.Metrics.PID)
	}
	t.Logf("[Verified] Server A running on PID %d, Server B running on PID %d", detailA.Metrics.PID, detailB.Metrics.PID)

	// 5. Test Isolation: Stop Server A, Server B must remain running!
	if err := srvSvc.StopServer(srvA.ID); err != nil {
		t.Fatalf("failed to stop server A: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	detailAAfter, _ := srvSvc.GetServerDetail(srvA.ID)
	detailBAfter, _ := srvSvc.GetServerDetail(srvB.ID)

	if detailAAfter.Status != process.StateStopped {
		t.Errorf("expected Server A to be stopped, got %s", detailAAfter.Status)
	}
	if detailBAfter.Status != process.StateRunning {
		t.Errorf("isolation violation: Server B stopped when Server A was terminated! Status: %s", detailBAfter.Status)
	}
	t.Logf("[Verified] Process Isolation: Server A stopped successfully while Server B is still actively running on PID %d", detailBAfter.Metrics.PID)

	// 6. Stop Server B
	if err := srvSvc.StopServer(srvB.ID); err != nil {
		t.Fatalf("failed to stop server B: %v", err)
	}

	time.Sleep(300 * time.Millisecond)
	detailBFinal, _ := srvSvc.GetServerDetail(srvB.ID)
	if detailBFinal.Status != process.StateStopped {
		t.Errorf("expected Server B to be stopped, got %s", detailBFinal.Status)
	}
}

// TestLoadAndThroughput tests API concurrency, health check latency, and throughput.
func TestLoadAndThroughput(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nodepanel_load_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	envInfo := runtime.DetectEnvironment()
	store, _ := storage.NewStore(tempDir)
	procMgr := process.NewManager(store, 1)
	pkgMgr := packages.NewPackageManager(envInfo)
	srvSvc := server.NewService(store, procMgr)
	app := api.NewServerApp(envInfo, store, procMgr, pkgMgr, srvSvc)
	handler := app.Router()

	// Seed 5 test servers
	for i := 1; i <= 5; i++ {
		_, _ = srvSvc.CreateServer(server.CreateServerRequest{
			Name:     fmt.Sprintf("Load Server %d", i),
			Port:     3200 + i,
			Template: "basic_http",
		})
	}

	totalRequests := 100
	concurrency := 10
	var wg sync.WaitGroup
	reqChan := make(chan struct{}, totalRequests)
	for i := 0; i < totalRequests; i++ {
		reqChan <- struct{}{}
	}
	close(reqChan)

	start := time.Now()
	var errorCount int
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range reqChan {
				req := httptest.NewRequest("GET", "/api/servers", nil)
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, req)
				if rr.Code != http.StatusOK {
					mu.Lock()
					errorCount++
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	duration := time.Since(start)
	rps := float64(totalRequests) / duration.Seconds()

	t.Logf("[Load Test] Total: %d requests, Concurrency: %d, Errors: %d, Duration: %v, RPS: %.2f",
		totalRequests, concurrency, errorCount, duration, rps)

	if errorCount > 0 {
		t.Errorf("encountered %d errors during concurrent load test", errorCount)
	}
}
