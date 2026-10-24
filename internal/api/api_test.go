package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/termux-nodepanel/nodepanel/internal/packages"
	"github.com/termux-nodepanel/nodepanel/internal/process"
	"github.com/termux-nodepanel/nodepanel/internal/runtime"
	"github.com/termux-nodepanel/nodepanel/internal/server"
	"github.com/termux-nodepanel/nodepanel/internal/storage"
)

func setupTestApp(t *testing.T) (*ServerApp, func()) {
	tempDir, err := os.MkdirTemp("", "nodepanel_api_test_*")
	if err != nil {
		t.Fatal(err)
	}

	envInfo := runtime.DetectEnvironment()
	store, err := storage.NewStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	procMgr := process.NewManager(store, 1)
	pkgMgr := packages.NewPackageManager(envInfo)
	srvSvc := server.NewService(store, procMgr)

	app := NewServerApp(envInfo, store, procMgr, pkgMgr, srvSvc)

	cleanup := func() {
		os.RemoveAll(tempDir)
	}
	return app, cleanup
}

func TestHealthEndpoint(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/health", nil)
	rr := httptest.NewRecorder()
	app.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["status"] != "healthy" {
		t.Errorf("expected status healthy, got %v", res["status"])
	}
}

func TestRuntimeEndpoint(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/runtime", nil)
	rr := httptest.NewRecorder()
	app.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var res map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if _, ok := res["type"]; !ok {
		t.Error("expected type in runtime response")
	}
}

func TestServerCrudEndpoints(t *testing.T) {
	app, cleanup := setupTestApp(t)
	defer cleanup()

	// 1. Create Server via POST /api/servers
	body := []byte(`{
		"name": "Integration Test Server",
		"description": "Created during API test",
		"port": 3999,
		"template": "basic_http"
	}`)
	req := httptest.NewRequest("POST", "/api/servers", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	app.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rr.Code, rr.Body.String())
	}

	var created map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	srvID, _ := created["id"].(string)
	if srvID == "" {
		t.Fatal("expected non-empty server ID")
	}

	// 2. GET /api/servers
	reqList := httptest.NewRequest("GET", "/api/servers", nil)
	rrList := httptest.NewRecorder()
	app.Router().ServeHTTP(rrList, reqList)

	if rrList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rrList.Code)
	}

	// 3. GET /api/servers/:id
	reqGet := httptest.NewRequest("GET", "/api/servers/"+srvID, nil)
	rrGet := httptest.NewRecorder()
	app.Router().ServeHTTP(rrGet, reqGet)

	if rrGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for server detail, got %d", rrGet.Code)
	}

	// 4. Start Server: POST /api/servers/:id/start
	reqStart := httptest.NewRequest("POST", "/api/servers/"+srvID+"/start", nil)
	rrStart := httptest.NewRecorder()
	app.Router().ServeHTTP(rrStart, reqStart)

	if rrStart.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for start, got %d: %s", rrStart.Code, rrStart.Body.String())
	}

	// 5. Stop Server: POST /api/servers/:id/stop
	reqStop := httptest.NewRequest("POST", "/api/servers/"+srvID+"/stop", nil)
	rrStop := httptest.NewRecorder()
	app.Router().ServeHTTP(rrStop, reqStop)

	if rrStop.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for stop, got %d", rrStop.Code)
	}

	// 6. DELETE /api/servers/:id
	reqDel := httptest.NewRequest("DELETE", "/api/servers/"+srvID, nil)
	rrDel := httptest.NewRecorder()
	app.Router().ServeHTTP(rrDel, reqDel)

	if rrDel.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for delete, got %d", rrDel.Code)
	}
}
