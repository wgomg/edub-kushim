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

func WebFS() fs.FS {
	sub, err := fs.Sub(buildFS, "build")
	if err != nil {
		panic(err)
	}
	return sub
}
