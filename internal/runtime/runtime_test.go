package runtime

import (
	"os"
	"strings"
	"testing"
)

func TestDetectEnvironment(t *testing.T) {
	info := DetectEnvironment()
	if info == nil {
		t.Fatal("expected non-nil EnvironmentInfo")
	}
	if info.OS == "" {
		t.Error("expected non-empty OS")
	}
	if info.Arch == "" {
		t.Error("expected non-empty Arch")
	}
	if info.TypeName == "" {
		t.Error("expected non-empty TypeName")
	}
	if len(info.Capabilities) == 0 {
		t.Error("expected at least one capability")
	}
}

func TestFormatDoctorOutput(t *testing.T) {
	info := DetectEnvironment()
	doc := info.FormatDoctorOutput()
	if !strings.Contains(doc, "TERMUX NODEPANEL DOCTOR REPORT") {
		t.Errorf("doctor output missing header: %s", doc)
	}
	if !strings.Contains(doc, info.TypeName) {
		t.Errorf("doctor output missing type name: %s", doc)
	}
}

func TestToJSON(t *testing.T) {
	info := DetectEnvironment()
	jsonStr := info.ToJSON()
	if !strings.Contains(jsonStr, `"type"`) {
		t.Errorf("JSON output missing type field: %s", jsonStr)
	}
}

func TestGetWorkingDataDir(t *testing.T) {
	dir := GetWorkingDataDir("custom/dir")
	if dir != "custom/dir" {
		t.Errorf("expected custom/dir, got %s", dir)
	}

	defaultDir := GetWorkingDataDir("")
	if defaultDir == "" {
		t.Error("expected non-empty default directory")
	}
}

func TestTermuxEnvSimulation(t *testing.T) {
	origPrefix := os.Getenv("PREFIX")
	origVer := os.Getenv("TERMUX_VERSION")
	defer func() {
		os.Setenv("PREFIX", origPrefix)
		os.Setenv("TERMUX_VERSION", origVer)
	}()

	os.Setenv("PREFIX", "/data/data/com.termux/files/usr")
	os.Setenv("TERMUX_VERSION", "0.118.0")

	info := DetectEnvironment()
	if info.Type != EnvTermuxNative {
		t.Errorf("expected EnvTermuxNative under simulated Termux env, got %s", info.Type)
	}
}
