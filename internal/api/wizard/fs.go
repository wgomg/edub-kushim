package wizard

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFS embed.FS

// WebFS returns the embedded wizard SPA as a filesystem root.
func WebFS() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
