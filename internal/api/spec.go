package api

import (
	_ "embed"
	"net/http"
)

// openAPISpec is the generated OpenAPI document for the routes registerRoutes installs. It is derived
// from the source by `make api-spec` and committed, so a checkout serves it without a generation step;
// `make api-spec-check` fails as soon as the committed copy of either artifact drifts from the code.

//go:embed spec/openapi.yaml
var openAPISpec []byte

// ServeOpenAPISpec writes the embedded OpenAPI document to the response as YAML, under the same path
// the document itself lists. The bytes are generated, not authored: never edit them by hand.
func ServeOpenAPISpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	// write errors are unrecoverable here: the status line is already on the wire
	_, _ = w.Write(openAPISpec)
}
