package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureSpec = `
info:
  title: Fixture API
  version: "9.9.9"
servers:
  - url: http://localhost:3000
    description: local fixture server
paths:
  /health:
    get:
      summary: Health serves the liveness answer.
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                type: object
                properties:
                  status:
                    type: string
  /api/v1/documents:
    get:
      summary: ListDocuments serves the paged document listing.
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
            default: "50"
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/DocumentList'
    post:
      summary: CreateDocument stores one document.
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/DocumentInput'
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Document'
components:
  schemas:
    DocumentList:
      type: object
      required:
        - items
      properties:
        items:
          type: array
          items:
            $ref: '#/components/schemas/Document'
    Document:
      type: object
      required:
        - id
      properties:
        id:
          type: string
        label:
          $ref: '#/components/schemas/Label'
    DocumentInput:
      type: object
      required:
        - title
      properties:
        title:
          type: string
    Label:
      anyOf:
        - type: string
        - type: "null"
`

func renderFixture(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(fixtureSpec), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	doc, err := loadSpec(path)
	if err != nil {
		t.Fatalf("loadSpec: %v", err)
	}
	out, err := renderSpec(doc)
	if err != nil {
		t.Fatalf("renderSpec: %v", err)
	}
	return string(out)
}

// The CI drift check compares regeneration output byte for byte, so the renderer must never let
// map iteration order leak into the document.
func TestRenderSpec_IsByteIdenticalAcrossRuns(t *testing.T) {
	if renderFixture(t) != renderFixture(t) {
		t.Error("two renders of the same spec differ; output is not deterministic")
	}
}

func TestRenderSpec_CoversGroupsParamsRequestsResponsesAndSchemas(t *testing.T) {
	out := renderFixture(t)

	for _, want := range []string{
		"# Fixture API",
		"- API version: `9.9.9`",
		"### Documents\n\n#### `GET /api/v1/documents`",
		"### Meta\n\n#### `GET /health`",
		"| `limit` | query | integer | no | `50` |",
		"| `id` | path | string | yes | — |",
		"Request body (required) (`application/json`): [DocumentInput](#documentinput)",
		"| 200 | [DocumentList](#documentlist) |",
		"### DocumentList",
		"| `items` | array of [Document](#document) | yes |",
		"| `label` | [Label](#label) | no |",
		"### DocumentInput",
		"| `title` | string | yes |",
		"### Label\n\nType: string (nullable)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output is missing %q", want)
		}
	}

	documents := strings.Index(out, "### Documents")
	meta := strings.Index(out, "### Meta")
	if documents == -1 || meta == -1 {
		t.Fatalf("expected both Documents and Meta groups (documents=%d, meta=%d)", documents, meta)
	}
	if documents > meta {
		t.Errorf("group order wrong: Documents at %d, Meta at %d", documents, meta)
	}
}

func TestLoadSpec_RejectsDocumentWithoutPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte("info:\n  title: empty\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := loadSpec(path); err == nil {
		t.Error("expected an error for a spec without paths")
	}
}
