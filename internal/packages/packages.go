package packages

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/termux-nodepanel/nodepanel/internal/runtime"
)

// PackageSpec defines logical metadata and platform-specific package names.
type PackageSpec struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Executable  string `json:"executable"`
	Category    string `json:"category"`
	// Mapping from package manager key ("pkg", "apt-get", "pacman", "apk") to package name
	PkgMap    map[string]string `json:"pkg_map"`
	Installed bool              `json:"installed"`
	Version   string            `json:"version,omitempty"`
	Path      string            `json:"path,omitempty"`
}

// PackageManager handles safe, environment-aware package operations with concurrency locks.
type PackageManager struct {
	envInfo  *runtime.EnvironmentInfo
	mu       sync.Mutex // Prevents concurrent package manager runs
	running  bool
	activeOp string
}

var (
	ErrOperationInProgress = errors.New("a package manager operation is already running")
	ErrPackageNotFound     = errors.New("package not recognized in registry")
	ErrUnsupportedPkgMgr   = errors.New("no supported package manager found in this environment")
	ErrUninstallForbidden  = errors.New("uninstalling this core system package is disabled for safety")
)

// Default registry mapping logical names to OS package names across Termux, Ubuntu/Debian, Arch, Alpine
var Registry = map[string]PackageSpec{
	"git": {
		ID:          "git",
		Name:        "Git Version Control",
		Description: "Fast, distributed version control system required for cloning repositories.",
		Executable:  "git",
		Category:    "Version Control",
		PkgMap: map[string]string{
			"pkg":     "git",
			"apt-get": "git",
			"pacman":  "git",
			"apk":     "git",
		},
	},
	"ffmpeg": {
		ID:          "ffmpeg",
		Name:        "FFmpeg Multimedia Framework",
		Description: "Complete, cross-platform solution to record, convert and stream audio and video.",
		Executable:  "ffmpeg",
		Category:    "Media Processing",
		PkgMap: map[string]string{
			"pkg":     "ffmpeg",
			"apt-get": "ffmpeg",
			"pacman":  "ffmpeg",
			"apk":     "ffmpeg",
		},
	},
	"python3": {
		ID:          "python3",
		Name:        "Python 3",
		Description: "Interpreted programming language often required for native Node.js addons (node-gyp).",
		Executable:  "python3",
		Category:    "Runtime & Build",
		PkgMap: map[string]string{
			"pkg":     "python",
			"apt-get": "python3",
			"pacman":  "python",
			"apk":     "python3",
		},
	},
	"imagemagick": {
		ID:          "imagemagick",
		Name:        "ImageMagick",
		Description: "Robust suite and libraries to create, edit, compose, or convert bitmap images.",
		Executable:  "convert",
		Category:    "Image Processing",
		PkgMap: map[string]string{
			"pkg":     "imagemagick",
			"apt-get": "imagemagick",
			"pacman":  "imagemagick",
			"apk":     "imagemagick",
		},
	},
	"curl": {
		ID:          "curl",
		Name:        "cURL",
		Description: "Command line tool and library for transferring data with URLs.",
		Executable:  "curl",
		Category:    "Networking",
		PkgMap: map[string]string{
			"pkg":     "curl",
			"apt-get": "curl",
			"pacman":  "curl",
			"apk":     "curl",
		},
	},
	"jq": {
		ID:          "jq",
		Name:        "jq JSON Processor",
		Description: "Lightweight and flexible command-line JSON processor.",
		Executable:  "jq",
		Category:    "Utilities",
		PkgMap: map[string]string{
			"pkg":     "jq",
			"apt-get": "jq",
			"pacman":  "jq",
			"apk":     "jq",
		},
	},
	"tmux": {
		ID:          "tmux",
		Name:        "tmux Terminal Multiplexer",
		Description: "Terminal multiplexer enabling background session management.",
		Executable:  "tmux",
		Category:    "Terminal",
		PkgMap: map[string]string{
			"pkg":     "tmux",
			"apt-get": "tmux",
			"pacman":  "tmux",
			"apk":     "tmux",
		},
	},
	"build-essential": {
		ID:          "build-essential",
		Name:        "C/C++ Build Essentials",
		Description: "GCC/Clang compilers and make tools for building native Node.js modules.",
		Executable:  "make",
		Category:    "Compilers",
		PkgMap: map[string]string{
			"pkg":     "build-essential",
			"apt-get": "build-essential",
			"pacman":  "base-devel",
			"apk":     "build-base",
		},
	},
}

// NewPackageManager initializes package manager with detected environment.
func NewPackageManager(env *runtime.EnvironmentInfo) *PackageManager {
	return &PackageManager{
		envInfo: env,
	}
}

// ListPackages returns all registry packages with updated installation status and versions.
func (pm *PackageManager) ListPackages() []PackageSpec {
	list := make([]PackageSpec, 0, len(Registry))
	for _, spec := range Registry {
		s := spec
		if path, err := exec.LookPath(s.Executable); err == nil {
			s.Installed = true
			s.Path = path
			s.Version = pm.getExecutableVersion(s.Executable)
		} else {
			// Fallback check for alternate binary name (e.g. python vs python3)
			if s.ID == "python3" {
				if path, err := exec.LookPath("python"); err == nil {
					s.Installed = true
					s.Path = path
					s.Version = pm.getExecutableVersion("python")
				}
			} else if s.ID == "imagemagick" {
				if path, err := exec.LookPath("magick"); err == nil {
					s.Installed = true
					s.Path = path
					s.Version = pm.getExecutableVersion("magick")
				}
			}
		}
		list = append(list, s)
	}
	return list
}

func (pm *PackageManager) getExecutableVersion(executable string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var args []string
	switch executable {
	case "git", "ffmpeg", "curl", "jq", "tmux", "make":
		args = []string{"--version"}
	case "python", "python3":
		args = []string{"--version"}
	case "convert", "magick":
		args = []string{"-version"}
	default:
		args = []string{"--version"}
	}

	cmd := exec.CommandContext(ctx, executable, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "Installed"
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0])
	}
	return "Installed"
}

// GetPackage returns single package spec by ID.
func (pm *PackageManager) GetPackage(pkgID string) (*PackageSpec, error) {
	spec, ok := Registry[pkgID]
	if !ok {
		return nil, ErrPackageNotFound
	}
	if path, err := exec.LookPath(spec.Executable); err == nil {
		spec.Installed = true
		spec.Path = path
		spec.Version = pm.getExecutableVersion(spec.Executable)
	}
	return &spec, nil
}

// Install executes the package installation with streaming log callback.
func (pm *PackageManager) Install(ctx context.Context, pkgID string, outWriter io.Writer) error {
	pm.mu.Lock()
	if pm.running {
		pm.mu.Unlock()
		return ErrOperationInProgress
	}
	pm.running = true
	pm.activeOp = "install:" + pkgID
	pm.mu.Unlock()

	defer func() {
		pm.mu.Lock()
		pm.running = false
		pm.activeOp = ""
		pm.mu.Unlock()
	}()

	spec, ok := Registry[pkgID]
	if !ok {
		return ErrPackageNotFound
	}

	pkgMgr := pm.envInfo.PackageManager
	if pkgMgr == "" || pkgMgr == "none" {
		return ErrUnsupportedPkgMgr
	}

	targetPkgName, ok := spec.PkgMap[pkgMgr]
	if !ok {
		return fmt.Errorf("package '%s' is not mapped for package manager '%s'", pkgID, pkgMgr)
	}

	var cmd *exec.Cmd
	switch pkgMgr {
	case "pkg":
		// Termux native package manager
		cmd = exec.CommandContext(ctx, "pkg", "install", "-y", targetPkgName)
	case "apt-get":
		// Ubuntu proot or Debian/Ubuntu
		cmd = exec.CommandContext(ctx, "apt-get", "install", "-y", targetPkgName)
	case "apt":
		cmd = exec.CommandContext(ctx, "apt", "install", "-y", targetPkgName)
	case "pacman":
		cmd = exec.CommandContext(ctx, "pacman", "-S", "--noconfirm", targetPkgName)
	case "apk":
		cmd = exec.CommandContext(ctx, "apk", "add", targetPkgName)
	default:
		return ErrUnsupportedPkgMgr
	}

	fmt.Fprintf(outWriter, "[nodepanel-pkg] Starting installation of '%s' using '%s %s'...\n", spec.Name, cmd.Path, strings.Join(cmd.Args[1:], " "))

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start install command: %w", err)
	}

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		fmt.Fprintf(outWriter, "%s\n", scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("installation failed with error: %w", err)
	}

	fmt.Fprintf(outWriter, "[nodepanel-pkg] Successfully installed '%s'!\n", spec.Name)
	return nil
}

// Uninstall safely uninstalls non-critical packages.
func (pm *PackageManager) Uninstall(ctx context.Context, pkgID string, outWriter io.Writer) error {
	// Guard: Do not allow uninstalling git if it's the core system VCS
	if pkgID == "git" {
		return ErrUninstallForbidden
	}

	pm.mu.Lock()
	if pm.running {
		pm.mu.Unlock()
		return ErrOperationInProgress
	}
	pm.running = true
	pm.activeOp = "uninstall:" + pkgID
	pm.mu.Unlock()

	defer func() {
		pm.mu.Lock()
		pm.running = false
		pm.activeOp = ""
		pm.mu.Unlock()
	}()

	spec, ok := Registry[pkgID]
	if !ok {
		return ErrPackageNotFound
	}

	pkgMgr := pm.envInfo.PackageManager
	if pkgMgr == "" || pkgMgr == "none" {
		return ErrUnsupportedPkgMgr
	}

	targetPkgName, ok := spec.PkgMap[pkgMgr]
	if !ok {
		return fmt.Errorf("package '%s' is not mapped for package manager '%s'", pkgID, pkgMgr)
	}

	var cmd *exec.Cmd
	switch pkgMgr {
	case "pkg":
		cmd = exec.CommandContext(ctx, "pkg", "uninstall", "-y", targetPkgName)
	case "apt-get", "apt":
		cmd = exec.CommandContext(ctx, "apt-get", "remove", "-y", targetPkgName)
	default:
		return ErrUnsupportedPkgMgr
	}

	fmt.Fprintf(outWriter, "[nodepanel-pkg] Starting removal of '%s'...\n", spec.Name)
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
		return fmt.Errorf("uninstall failed: %w", err)
	}

	fmt.Fprintf(outWriter, "[nodepanel-pkg] Removed '%s'.\n", spec.Name)
	return nil
}

// IsBusy returns whether a package manager task is currently in flight.
func (pm *PackageManager) IsBusy() (bool, string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.running, pm.activeOp
}
