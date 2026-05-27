package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/**
var static embed.FS

// FS returns the embedded static file subtree for HTTP serving.
func FS() (fs.FS, error) {
	return fs.Sub(static, "static")
}

// MustFS returns FS or panics on failure (startup only).
func MustFS() fs.FS {
	sub, err := FS()
	if err != nil {
		panic(err)
	}
	return sub
}

// ContentType returns a MIME type for known static extensions.
func ContentType(name string) string {
	switch {
	case len(name) > 4 && name[len(name)-4:] == ".css":
		return "text/css; charset=utf-8"
	case len(name) > 3 && name[len(name)-3:] == ".js":
		return "application/javascript; charset=utf-8"
	case len(name) > 5 && name[len(name)-5:] == ".html":
		return "text/html; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// FileServer returns an http.Handler for embedded static assets.
func FileServer() http.Handler {
	return http.FileServer(http.FS(MustFS()))
}
