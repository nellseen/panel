package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/termux-nodepanel/nodepanel/internal/api"
	"github.com/termux-nodepanel/nodepanel/internal/packages"
	"github.com/termux-nodepanel/nodepanel/internal/process"
	"github.com/termux-nodepanel/nodepanel/internal/runtime"
	"github.com/termux-nodepanel/nodepanel/internal/server"
	"github.com/termux-nodepanel/nodepanel/internal/storage"
	"github.com/termux-nodepanel/nodepanel/internal/web"
)

// Version info
const (
	Version   = "1.0.0"
	BuildDate = "2026-10-24"
)

func main() {
	if len(os.Args) > 1 {
		cmd := os.Args[1]
		switch cmd {
		case "version", "--version", "-v":
			fmt.Printf("Termux NodePanel v%s (built %s)\n", Version, BuildDate)
			fmt.Printf("OS/Arch: %s/%s\n", os.Getenv("GOOS"), os.Getenv("GOARCH"))
			return

		case "runtime":
			info := runtime.DetectEnvironment()
			fmt.Println(info.ToJSON())
			return

		case "doctor":
			info := runtime.DetectEnvironment()
			fmt.Print(info.FormatDoctorOutput())
			return

		case "help", "--help", "-h":
			printHelp()
			return
		}
	}

	// Parse flags for 'start' or default run
	fsCmd := flag.NewFlagSet("nodepanel", flag.ExitOnError)
	port := fsCmd.Int("port", 8080, "HTTP server port (default: 8080)")
	host := fsCmd.String("host", "127.0.0.1", "Bind address (default 127.0.0.1 for local security)")
	dataDir := fsCmd.String("data", "", "Data directory path (default: data/ or ~/.nodepanel/data)")

	args := os.Args[1:]
	if len(args) > 0 && args[0] == "start" {
		args = args[1:]
	}
	_ = fsCmd.Parse(args)

	realDataDir := runtime.GetWorkingDataDir(*dataDir)
	_ = os.MkdirAll(realDataDir, 0755)

	envInfo := runtime.DetectEnvironment()
	fmt.Printf("[*] Initializing Termux NodePanel v%s...\n", Version)
	fmt.Printf("[*] Detected Environment : %s (%s)\n", envInfo.TypeName, envInfo.Type)
	fmt.Printf("[*] Storage Directory    : %s\n", realDataDir)

	store, err := storage.NewStore(realDataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] Storage initialization failed: %v\n", err)
		os.Exit(1)
	}

	procMgr := process.NewManager(store, 1)
	pkgMgr := packages.NewPackageManager(envInfo)
	srvSvc := server.NewService(store, procMgr)

	serverApp := api.NewServerApp(envInfo, store, procMgr, pkgMgr, srvSvc)
	apiHandler := serverApp.Router()

	// Top level mux combining API routes and frontend assets
	rootMux := http.NewServeMux()
	rootMux.Handle("/api/", apiHandler)
	rootMux.Handle("/ws", apiHandler)

	// Serve static frontend files: check local directory first, then embedded
	serveFrontend(rootMux)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	fmt.Printf("[*] Web Dashboard ready at: http://%s\n", addr)
	fmt.Println("[*] Press Ctrl+C to stop NodePanel.")

	if err := http.ListenAndServe(addr, rootMux); err != nil {
		fmt.Fprintf(os.Stderr, "[-] Server failed to start: %v\n", err)
		os.Exit(1)
	}
}

func serveFrontend(mux *http.ServeMux) {
	candidates := []string{"dist", "./dist", "../dist", "web/dist", "/app/applet/dist"}
	var targetDir string
	for _, c := range candidates {
		if stat, err := os.Stat(c); err == nil && stat.IsDir() {
			targetDir = c
			break
		}
	}

	if targetDir != "" {
		fsHandler := http.FileServer(http.Dir(targetDir))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") || r.URL.Path == "/ws" {
				return
			}
			path := filepath.Join(targetDir, filepath.Clean(r.URL.Path))
			if _, err := os.Stat(path); os.IsNotExist(err) {
				http.ServeFile(w, r, filepath.Join(targetDir, "index.html"))
				return
			}
			fsHandler.ServeHTTP(w, r)
		})
		return
	}

	// Serve embedded assets directly compiled into the Go binary
	embeddedHandler := web.AssetHandler()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") || r.URL.Path == "/ws" {
			return
		}
		embeddedHandler.ServeHTTP(w, r)
	})
}

func printHelp() {
	fmt.Printf("Termux NodePanel v%s\n", Version)
	fmt.Println("A lightweight, modular Node.js process and server manager for Termux, proot-distro, and Linux.")
	fmt.Println("\nUsage:")
	fmt.Println("  nodepanel start [--port=8080] [--host=127.0.0.1] [--data=./data]   Start panel daemon")
	fmt.Println("  nodepanel doctor                                                   Inspect environment and dependencies")
	fmt.Println("  nodepanel runtime                                                  Output runtime detection JSON")
	fmt.Println("  nodepanel version                                                  Show version and build info")
	fmt.Println("  nodepanel help                                                     Display this help message")
}
