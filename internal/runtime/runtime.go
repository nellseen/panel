package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvironmentType represents the detected host environment.
type EnvironmentType string

const (
	EnvTermuxNative EnvironmentType = "termux_native"
	EnvUbuntuProot  EnvironmentType = "ubuntu_proot"
	EnvLinuxGeneric EnvironmentType = "linux_generic"
	EnvUnknown      EnvironmentType = "unknown"
)

// EnvironmentInfo contains detailed system and runtime diagnostics.
type EnvironmentInfo struct {
	Type           EnvironmentType   `json:"type"`
	TypeName       string            `json:"type_name"`
	OS             string            `json:"os"`
	Arch           string            `json:"arch"`
	Kernel         string            `json:"kernel"`
	Hostname       string            `json:"hostname"`
	Distro         string            `json:"distro"`
	DistroVersion  string            `json:"distro_version"`
	Prefix         string            `json:"prefix"`
	TermuxVersion  string            `json:"termux_version"`
	IsProot        bool              `json:"is_proot"`
	PackageManager string            `json:"package_manager"`
	Executables    map[string]string `json:"executables"`
	Capabilities   []string          `json:"capabilities"`
	Limitations    []string          `json:"limitations"`
	DiskFreeMB     int64             `json:"disk_free_mb"`
	MemoryTotalMB  int64             `json:"memory_total_mb"`
}

// DetectEnvironment inspects multiple independent signals to determine the runtime environment.
func DetectEnvironment() *EnvironmentInfo {
	info := &EnvironmentInfo{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Executables: make(map[string]string),
	}

	// Hostname
	if host, err := os.Hostname(); err == nil {
		info.Hostname = host
	}

	// Kernel version via uname
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		info.Kernel = strings.TrimSpace(string(out))
	}

	// Termux environment variables
	prefix := os.Getenv("PREFIX")
	info.Prefix = prefix
	info.TermuxVersion = os.Getenv("TERMUX_VERSION")

	// Check /etc/os-release
	distroID, distroPretty, distroVer := parseOSRelease()
	info.Distro = distroPretty
	info.DistroVersion = distroVer

	// Proot detection: check environment variables, /proc, and known markers
	isProot := checkProotIndicators()
	info.IsProot = isProot

	// Termux Native check:
	// 1. PREFIX points to /data/data/com.termux/files/usr
	// 2. Or /data/data/com.termux directory exists and no proot indicators
	// 3. Or TERMUX_VERSION is populated
	isTermux := false
	if strings.Contains(prefix, "com.termux") || info.TermuxVersion != "" {
		isTermux = true
	} else if _, err := os.Stat("/data/data/com.termux/files/usr/bin"); err == nil {
		isTermux = true
	}

	// Categorize environment
	if isTermux && !isProot {
		info.Type = EnvTermuxNative
		info.TypeName = "Termux (Native Android)"
	} else if isProot || (strings.Contains(strings.ToLower(distroID), "ubuntu") && checkProotMounts()) {
		info.Type = EnvUbuntuProot
		info.TypeName = "Ubuntu (proot-distro)"
	} else if runtime.GOOS == "linux" {
		info.Type = EnvLinuxGeneric
		info.TypeName = fmt.Sprintf("Linux (%s)", distroPretty)
	} else {
		info.Type = EnvUnknown
		info.TypeName = "Unsupported / Unknown"
	}

	// Check available executables and their versions
	checkTools(info)

	// Determine package manager
	info.PackageManager = detectPackageManager(info)

	// Calculate capabilities and limitations based on detected environment
	evaluateCapabilities(info)

	return info
}

func parseOSRelease() (id, prettyName, version string) {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "linux", "Linux", ""
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID=") {
			id = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
		} else if strings.HasPrefix(line, "PRETTY_NAME=") {
			prettyName = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
		} else if strings.HasPrefix(line, "VERSION_ID=") {
			version = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
		}
	}
	if prettyName == "" {
		prettyName = id
	}
	return
}

func checkProotIndicators() bool {
	// 1. Proot-specific environment variables
	if os.Getenv("PROOT_TMP_DIR") != "" || os.Getenv("PROOT_FORCE_KILL_SIGNALS") != "" {
		return true
	}
	// 2. Check /etc/issue or /etc/environment for proot markers
	if data, err := os.ReadFile("/etc/issue"); err == nil {
		if strings.Contains(strings.ToLower(string(data)), "proot") {
			return true
		}
	}
	// 3. Check cmdline of PID 1 or parent
	if data, err := os.ReadFile("/proc/1/cmdline"); err == nil {
		if strings.Contains(string(data), "proot") {
			return true
		}
	}
	// 4. Check if /proot directory exists
	if _, err := os.Stat("/proot"); err == nil {
		return true
	}
	return false
}

func checkProotMounts() bool {
	// Look at /proc/mounts to see if fake root or proot mounts are active
	if data, err := os.ReadFile("/proc/mounts"); err == nil {
		content := string(data)
		if strings.Contains(content, "proot") || strings.Contains(content, "/data/data/com.termux") {
			return true
		}
	}
	return false
}

func checkTools(info *EnvironmentInfo) {
	tools := []string{"node", "npm", "git", "pkg", "apt-get", "apt", "python3", "python", "ffmpeg", "sqlite3", "curl", "tar"}
	for _, tool := range tools {
		if path, err := exec.LookPath(tool); err == nil {
			info.Executables[tool] = path
		}
	}
}

func detectPackageManager(info *EnvironmentInfo) string {
	if info.Type == EnvTermuxNative {
		if _, ok := info.Executables["pkg"]; ok {
			return "pkg"
		}
		if _, ok := info.Executables["apt"]; ok {
			return "apt"
		}
	} else if info.Type == EnvUbuntuProot || info.Type == EnvLinuxGeneric {
		if _, ok := info.Executables["apt-get"]; ok {
			return "apt-get"
		}
		if _, ok := info.Executables["apt"]; ok {
			return "apt"
		}
		if path, err := exec.LookPath("pacman"); err == nil {
			info.Executables["pacman"] = path
			return "pacman"
		}
		if path, err := exec.LookPath("apk"); err == nil {
			info.Executables["apk"] = path
			return "apk"
		}
	}
	return "none"
}

func evaluateCapabilities(info *EnvironmentInfo) {
	caps := []string{
		"Node.js Process Lifecycle Management",
		"Real-time Console Streaming via WebSocket",
		"Git Repository Clone & Fetch",
		"Per-server Isolated Data Directories",
	}
	limits := []string{}

	if info.Type == EnvTermuxNative {
		caps = append(caps, "Termux Package Management (pkg)")
		limits = append(limits, "Android battery optimization may kill background processes unless Termux:Boot or Wake Lock is acquired")
		limits = append(limits, "No root / systemd access (runs with Android app user permissions)")
		limits = append(limits, "Limited shared memory / IPC capabilities on certain Android kernels")
	} else if info.Type == EnvUbuntuProot {
		caps = append(caps, "Debian/Ubuntu APT Package Management", "Full glibc compatibility")
		limits = append(limits, "No systemd daemon support inside proot")
		limits = append(limits, "pkill/kill signals may have emulation overhead under proot ptrace")
		limits = append(limits, "Disk I/O might be slower through PRoot translation layer")
	} else {
		caps = append(caps, "Full Linux Kernel & Standard Syscall Execution")
	}

	// Executable capabilities
	if _, ok := info.Executables["node"]; ok {
		caps = append(caps, "Node.js Runtime Available")
	} else {
		limits = append(limits, "Node.js is not found in PATH; install Node.js to run applications")
	}

	if _, ok := info.Executables["git"]; ok {
		caps = append(caps, "Git Version Control Available")
	} else {
		limits = append(limits, "Git is not installed; Git clone will be disabled until installed")
	}

	info.Capabilities = caps
	info.Limitations = limits
}

// FormatDoctorOutput formats a human-readable diagnostic report for 'nodepanel doctor'.
func (info *EnvironmentInfo) FormatDoctorOutput() string {
	var sb strings.Builder
	sb.WriteString("====================================================\n")
	sb.WriteString("            TERMUX NODEPANEL DOCTOR REPORT          \n")
	sb.WriteString("====================================================\n\n")

	sb.WriteString(fmt.Sprintf("[*] Environment Type : %s (%s)\n", info.TypeName, info.Type))
	sb.WriteString(fmt.Sprintf("[*] Operating System : %s / %s (Arch: %s)\n", info.OS, info.Distro, info.Arch))
	if info.Kernel != "" {
		sb.WriteString(fmt.Sprintf("[*] Kernel           : %s\n", info.Kernel))
	}
	sb.WriteString(fmt.Sprintf("[*] Hostname         : %s\n", info.Hostname))
	if info.Prefix != "" {
		sb.WriteString(fmt.Sprintf("[*] Termux PREFIX    : %s\n", info.Prefix))
	}
	sb.WriteString(fmt.Sprintf("[*] Package Manager  : %s\n", info.PackageManager))
	sb.WriteString(fmt.Sprintf("[*] Proot Detected   : %t\n\n", info.IsProot))

	sb.WriteString("--- Installed Core Executables ---\n")
	required := []string{"node", "npm", "git", "sqlite3"}
	for _, tool := range required {
		if path, ok := info.Executables[tool]; ok {
			ver := getToolVersion(tool)
			sb.WriteString(fmt.Sprintf("  [OK] %-10s : %s (%s)\n", tool, path, ver))
		} else {
			sb.WriteString(fmt.Sprintf("  [!!] %-10s : NOT FOUND (Install to enable full features)\n", tool))
		}
	}

	sb.WriteString("\n--- Optional Tools ---\n")
	optional := []string{"ffmpeg", "python3", "curl", "tar"}
	for _, tool := range optional {
		if path, ok := info.Executables[tool]; ok {
			sb.WriteString(fmt.Sprintf("  [+] %-10s : %s\n", tool, path))
		} else {
			sb.WriteString(fmt.Sprintf("  [-] %-10s : Not installed\n", tool))
		}
	}

	sb.WriteString("\n--- Capabilities ---\n")
	for _, c := range info.Capabilities {
		sb.WriteString(fmt.Sprintf("  + %s\n", c))
	}

	if len(info.Limitations) > 0 {
		sb.WriteString("\n--- Environment Limitations & Warnings ---\n")
		for _, l := range info.Limitations {
			sb.WriteString(fmt.Sprintf("  ! %s\n", l))
		}
	}

	sb.WriteString("\n====================================================\n")
	return sb.String()
}

func getToolVersion(tool string) string {
	var cmd *exec.Cmd
	switch tool {
	case "node":
		cmd = exec.Command("node", "--version")
	case "npm":
		cmd = exec.Command("npm", "--version")
	case "git":
		cmd = exec.Command("git", "--version")
	case "sqlite3":
		cmd = exec.Command("sqlite3", "--version")
	default:
		return "available"
	}
	out, err := cmd.Output()
	if err != nil {
		return "available"
	}
	return strings.TrimSpace(string(out))
}

// ToJSON formats the EnvironmentInfo as indented JSON.
func (info *EnvironmentInfo) ToJSON() string {
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}

// GetWorkingDataDir returns a safe, writable base directory for server files.
func GetWorkingDataDir(customPath string) string {
	if customPath != "" {
		return customPath
	}
	// If in Termux, check if $HOME/nodepanel_data is preferred
	if prefix := os.Getenv("PREFIX"); strings.Contains(prefix, "com.termux") {
		home := os.Getenv("HOME")
		if home != "" {
			return filepath.Join(home, ".nodepanel", "data")
		}
	}
	return "data"
}
