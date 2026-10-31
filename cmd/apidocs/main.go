// Command apidocs renders the committed OpenAPI spec into the human-readable API reference under docs.
// The spec is generated from the code by `make api-spec`; this command only renders it, so that the
// reference cannot make claims the specification does not carry.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func main() {
	specPath := flag.String("spec", "internal/api/spec/openapi.yaml", "OpenAPI spec to render")
	outPath := flag.String("out", "docs/api.md", "markdown file to write")
	flag.Parse()

	spec, err := loadSpec(*specPath)
	if err != nil {
		fail("read spec: %v", err)
	}

	out, err := renderSpec(spec)
	if err != nil {
		fail("render: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fail("create output directory: %v", err)
	}
	if err := os.WriteFile(*outPath, out, 0o644); err != nil {
		fail("write %s: %v", *outPath, err)
	}
}

// loadSpec decodes the OpenAPI document. The renderer only reads the fields the reference displays:
// anything else in the document is deliberately ignored, keeping the model as small as the output.
func loadSpec(path string) (*specDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var doc specDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Paths) == 0 {
		return nil, fmt.Errorf("%s holds no paths", path)
	}
	return &doc, nil
}

// fail reports a render failure and exits non-zero: the generator runs in CI, where a silent shortfall
// would publish a stale or empty reference, and in local runs where the same mistake should stop.
func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "apidocs: "+format+"\n", args...)
	os.Exit(1)
}
