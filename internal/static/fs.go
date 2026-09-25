// Package static embeds the main web SPA's production build and serves it
// as a filesystem root.
package static

import (
	"embed"
	"io/fs"
)

// WebUI contains the SvelteKit production build output,
// copied from web/build/ by the Makefile.
//
//go:embed build
var buildFS embed.FS

// WebFS returns the embedded SPA build as a filesystem root.
func WebFS() fs.FS {
	sub, err := fs.Sub(buildFS, "build")
	if err != nil {
		panic(err)
	}
	return sub
}
