package repository

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRepoURL(t *testing.T) {
	// Valid URLs
	validCases := []string{
		"https://github.com/expressjs/express.git",
		"http://gitlab.com/group/repo.git",
		"git://github.com/user/project",
	}
	for _, u := range validCases {
		if err := ValidateRepoURL(u); err != nil {
			t.Errorf("expected valid URL for '%s', got error: %v", u, err)
		}
	}

	// Malicious or invalid URLs
	invalidCases := []string{
		"",
		"-u git-upload-pack",
		"--upload-pack=/bin/sh",
		"file:///etc/passwd",
		"ftp://example.com/repo",
		"javascript:alert(1)",
	}
	for _, u := range invalidCases {
		if err := ValidateRepoURL(u); err == nil {
			t.Errorf("expected rejection for malicious/invalid URL '%s'", u)
		}
	}
}

func TestInspectProject(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "nodepanel_inspect_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	pkgJSON := `{
		"name": "sample-node-service",
		"version": "1.0.0",
		"main": "server.js",
		"scripts": {
			"start": "node server.js",
			"test": "echo test"
		},
		"dependencies": {
			"express": "^4.18.0"
		}
	}`
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(pkgJSON), 0644); err != nil {
		t.Fatal(err)
	}

	inspection, err := InspectProject(tempDir)
	if err != nil {
		t.Fatalf("failed to inspect project: %v", err)
	}

	if !inspection.HasPackageJSON {
		t.Error("expected has_package_json to be true")
	}
	if inspection.Name != "sample-node-service" {
		t.Errorf("expected name sample-node-service, got %s", inspection.Name)
	}
	if inspection.SuggestedEntry != "server.js" {
		t.Errorf("expected suggested entry server.js, got %s", inspection.SuggestedEntry)
	}
	if len(inspection.Dependencies) != 1 || inspection.Dependencies[0] != "express" {
		t.Errorf("unexpected dependencies: %+v", inspection.Dependencies)
	}
}
