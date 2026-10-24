package repository

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidRepoURL  = errors.New("invalid or unsafe repository URL: only http, https, and git protocols allowed")
	ErrDangerousGitArg = errors.New("repository URL contains potentially malicious git option flags")
)

// ProjectInspection contains discovered details about a Node.js project.
type ProjectInspection struct {
	HasPackageJSON bool              `json:"has_package_json"`
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Main           string            `json:"main"`
	Scripts        map[string]string `json:"scripts"`
	SuggestedEntry string            `json:"suggested_entry"`
	Dependencies   []string          `json:"dependencies"`
}

// ValidateRepoURL strictly checks against protocol attacks, local paths, and argument injection.
func ValidateRepoURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return errors.New("repository URL cannot be empty")
	}

	// Prevent git option injection (e.g. --upload-pack, -u, etc.)
	if strings.HasPrefix(trimmed, "-") {
		return ErrDangerousGitArg
	}

	// Parse URL
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ErrInvalidRepoURL
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "https" && scheme != "http" && scheme != "git" && scheme != "ssh" {
		return ErrInvalidRepoURL
	}

	// Prevent file:// or localhost / local directory escapes
	if scheme == "file" || parsed.Host == "" {
		return ErrInvalidRepoURL
	}

	return nil
}

// Clone clones a git repository into targetDir using argument list without shell evaluation.
func Clone(ctx context.Context, repoURL, branch, targetDir string, outWriter io.Writer) error {
	if err := ValidateRepoURL(repoURL); err != nil {
		return err
	}

	args := []string{"clone", "--depth", "1"}
	if branch != "" {
		// Clean branch name
		branch = strings.TrimSpace(branch)
		if strings.HasPrefix(branch, "-") {
			return errors.New("invalid branch name")
		}
		args = append(args, "-b", branch)
	}

	args = append(args, repoURL, targetDir)

	fmt.Fprintf(outWriter, "[nodepanel-git] Cloning repository into %s...\n", targetDir)

	cmd := exec.CommandContext(ctx, "git", args...)
	// Prevent interactive credentials prompt hangs in automated tasks
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start git clone: %w", err)
	}

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		fmt.Fprintf(outWriter, "%s\n", scanner.Text())
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}

	fmt.Fprintf(outWriter, "[nodepanel-git] Repository clone completed successfully.\n")
	return nil
}

// Pull updates the repository in targetDir.
func Pull(ctx context.Context, targetDir string, outWriter io.Writer) error {
	cmd := exec.CommandContext(ctx, "git", "pull")
	cmd.Dir = targetDir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

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

	return cmd.Wait()
}

// InspectProject reads package.json in the target directory to detect start scripts and entry points.
func InspectProject(dir string) (*ProjectInspection, error) {
	pkgPath := filepath.Join(dir, "package.json")
	data, err := os.ReadFile(pkgPath)
	if err != nil {
		// If no package.json, check common entry files
		entries := []string{"index.js", "server.js", "app.js", "main.js"}
		suggested := "index.js"
		for _, e := range entries {
			if _, err := os.Stat(filepath.Join(dir, e)); err == nil {
				suggested = e
				break
			}
		}
		return &ProjectInspection{
			HasPackageJSON: false,
			SuggestedEntry: suggested,
		}, nil
	}

	var raw struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		Main         string            `json:"main"`
		Scripts      map[string]string `json:"scripts"`
		Dependencies map[string]string `json:"dependencies"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse package.json: %w", err)
	}

	deps := make([]string, 0, len(raw.Dependencies))
	for k := range raw.Dependencies {
		deps = append(deps, k)
	}

	suggested := raw.Main
	if suggested == "" {
		if _, ok := raw.Scripts["start"]; ok {
			suggested = "npm start"
		} else if _, ok := raw.Scripts["dev"]; ok {
			suggested = "npm run dev"
		} else {
			suggested = "index.js"
		}
	}

	return &ProjectInspection{
		HasPackageJSON: true,
		Name:           raw.Name,
		Version:        raw.Version,
		Main:           raw.Main,
		Scripts:        raw.Scripts,
		SuggestedEntry: suggested,
		Dependencies:   deps,
	}, nil
}
