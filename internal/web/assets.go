package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var StaticAssets embed.FS

// AssetHandler returns an http.Handler serving the embedded React frontend with SPA fallback.
func AssetHandler() http.Handler {
	distFS, err := fs.Sub(StaticAssets, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}

	fileServer := http.FileServer(http.FS(distFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Try opening file in embedded filesystem
		f, err := distFS.Open(path)
		if err != nil {
			// If not found and not an API route, serve index.html for SPA router
			data, err := StaticAssets.ReadFile("dist/index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
		_ = f.Close()

		fileServer.ServeHTTP(w, r)
	})
}
