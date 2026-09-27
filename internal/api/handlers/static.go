package handlers

import (
	"io/fs"
	"net/http"
)

// RegisterStaticRoutes serves the embedded SPA from fsys: paths that name
// a real file are served as-is, and any other path falls back to the
// root (the SPA router takes over client-side).
func RegisterStaticRoutes(mux *http.ServeMux, fsys fs.FS) {
	fileServer := http.FileServer(http.FS(fsys))

	mux.HandleFunc("GET /{path...}", func(w http.ResponseWriter, r *http.Request) {
		path := r.PathValue("path")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}

		f, err := fsys.Open(path)
		if err != nil {
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		_ = f.Close()

		fileServer.ServeHTTP(w, r)
	})
}
