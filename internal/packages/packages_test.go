package packages

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/termux-nodepanel/nodepanel/internal/runtime"
)

func TestListPackages(t *testing.T) {
	env := runtime.DetectEnvironment()
	pm := NewPackageManager(env)

	list := pm.ListPackages()
	if len(list) == 0 {
		t.Fatal("expected registry packages to be listed")
	}

	foundGit := false
	for _, p := range list {
		if p.ID == "git" {
			foundGit = true
			if !p.Installed {
				t.Log("git not found in test env PATH")
			}
		}
	}
	if !foundGit {
		t.Error("expected git to be in package registry")
	}
}

func TestGetPackage(t *testing.T) {
	env := runtime.DetectEnvironment()
	pm := NewPackageManager(env)

	pkg, err := pm.GetPackage("git")
	if err != nil {
		t.Fatalf("unexpected error getting git: %v", err)
	}
	if pkg.ID != "git" {
		t.Errorf("expected git, got %s", pkg.ID)
	}

	_, err = pm.GetPackage("non-existent-package-xyz")
	if err != ErrPackageNotFound {
		t.Errorf("expected ErrPackageNotFound, got %v", err)
	}
}

func TestConcurrencyProtection(t *testing.T) {
	env := runtime.DetectEnvironment()
	pm := NewPackageManager(env)

	// Simulate lock
	pm.mu.Lock()
	pm.running = true
	pm.activeOp = "install:test"
	pm.mu.Unlock()

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := pm.Install(ctx, "curl", &buf)
	if err != ErrOperationInProgress {
		t.Errorf("expected ErrOperationInProgress, got %v", err)
	}

	busy, op := pm.IsBusy()
	if !busy || op != "install:test" {
		t.Errorf("expected busy=true with op install:test, got %v %s", busy, op)
	}

	// Release lock
	pm.mu.Lock()
	pm.running = false
	pm.mu.Unlock()

	busyAfter, _ := pm.IsBusy()
	if busyAfter {
		t.Error("expected busy=false after release")
	}
}

func TestUninstallForbiddenForGit(t *testing.T) {
	env := runtime.DetectEnvironment()
	pm := NewPackageManager(env)

	var buf bytes.Buffer
	err := pm.Uninstall(context.Background(), "git", &buf)
	if err != ErrUninstallForbidden {
		t.Errorf("expected ErrUninstallForbidden for git, got %v", err)
	}
}
