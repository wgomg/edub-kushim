package main

import (
	"fmt"
	"sort"
	"strings"
)

// methodOrder fixes how the operations of a path are listed, so the output is stable across runs.
var methodOrder = []string{"GET", "POST", "PUT", "DELETE", "PATCH"}

// groupOrder fixes the resource groups' order; a group not named here follows alphabetically.
var groupOrder = []string{
	"Documents",
	"Tags",
	"People",
	"Document types",
	"Ingestion",
	"Tasks",
	"Batches",
	"Trash",
	"Orphaned & errored",
	"Saved searches",
	"LLM catalogs",
	"Users",
	"Session",
	"Me",
	"Logs",
	"Wizard & settings",
	"Meta",
}

// endpoint is one operation paired with the method and path it serves.
type endpoint struct {
	method string
	path   string
	op     *specOperation
}

// group is a resource section of the reference.
type group struct {
	name      string
	endpoints []endpoint
}

// renderSpec renders the whole reference. Every collection is sorted or fixed-order, so the same spec
// always renders byte-identically; the drift check in CI relies on that guarantee.
func renderSpec(doc *specDoc) ([]byte, error) {
	var b strings.Builder

	writeHeader(&b, doc)
	writeOverview(&b, doc)
	writeEndpoints(&b, doc)
	writeSchemas(&b, doc)

	return []byte(b.String()), nil
}

// writeHeader emits the title and the generation notice.
func writeHeader(b *strings.Builder, doc *specDoc) {
	fmt.Fprintf(b, "# %s\n\n", doc.Info.Title)
	b.WriteString("This reference is generated: the specification is derived from the Go source by `make api-spec`,\n")
	b.WriteString("and this file is rendered from the specification by `make api-docs`. Do not edit either one by\n")
	b.WriteString("hand; change the code and regenerate.\n\n")
	fmt.Fprintf(b, "- API version: `%s`\n", doc.Info.Version)
	fmt.Fprintf(b, "- Specification: [`internal/api/spec/openapi.yaml`](../internal/api/spec/openapi.yaml)\n\n")
	if desc := cleanProse(doc.Info.Description); desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}
}

// writeOverview emits the hand-authored front matter: what the API is, how to authenticate, the error
// contract and the pagination convention. None of this is expressible in the OpenAPI document itself.
func writeOverview(b *strings.Builder, doc *specDoc) {
	b.WriteString("## Overview\n\n")

	if len(doc.Servers) > 0 {
		fmt.Fprintf(b, "Base URL: `%s`", doc.Servers[0].URL)
		if desc := cleanProse(doc.Servers[0].Description); desc != "" {
			fmt.Fprintf(b, " (%s)", desc)
		}
		b.WriteString(". Every endpoint below is relative to it; the port comes from `server.port` in the config.\n\n")
	}

	b.WriteString("### Authentication\n\n")
	b.WriteString("Every endpoint authenticates through the `Authorization` header, either with a session JWT minted by\n")
	b.WriteString("`POST /api/v1/auth/login` or with a personal API key (`ek_` prefix) managed through the `/api/v1/me/api-key`\n")
	b.WriteString("endpoints. Browser sessions also authenticate through the `edub_token` HttpOnly cookie, which the web UI\n")
	b.WriteString("sets on login and clears on logout. `GET /health`, `POST /api/v1/auth/login`, `POST /api/v1/auth/logout`\n")
	b.WriteString("and `GET /wizard/bootstrap` are public; everything else needs a credential. With `auth_enabled: false`\n")
	b.WriteString("every request passes as an administrator.\n\n")

	b.WriteString("Roles gate the operations:\n\n")
	b.WriteString("- `viewer` — read-only access: documents, search, tags, people, document types, tasks, batches,\n")
	b.WriteString("  the dashboard, the trash listing, the provisional tag queue and reconciliation report, audit flags\n")
	b.WriteString("  and the sibling worklist.\n")
	b.WriteString("- `editor` — everything a viewer sees, plus the mutations: document edits and deletion, tag, people\n")
	b.WriteString("  and document-type changes, uploads and ingestion, trash restore and purge, orphaned and errored file\n")
	b.WriteString("  handling, batch resume and cancel, re-enrichment, tag promotion and audit decisions.\n")
	b.WriteString("- `admin` — everything an editor does, plus user management, API keys, LLM catalogs, logs and the\n")
	b.WriteString("  settings surface (`/wizard/*`).\n\n")

	b.WriteString("### Errors\n\n")
	b.WriteString("Failure responses carry a JSON object with a single `error` field:\n\n")
	b.WriteString("```json\n{\"error\": \"document not found\"}\n```\n\n")
	b.WriteString("Some handlers answer through Go's `http.Error`, which writes the message as `text/plain`. The status\n")
	b.WriteString("codes of each endpoint are listed below; `404` means the addressed row does not exist, `409` a\n")
	b.WriteString("conflict with the current state, `503` an unavailable dependency (the tag matcher, a task handler).\n\n")

	b.WriteString("### Pagination\n\n")
	b.WriteString("Listing endpoints take `limit` and `offset`; each endpoint's parameter table states its defaults.\n")
	b.WriteString("The `q` parameter, where present, is a name-prefix search.\n\n")
}

// writeEndpoints emits every operation, grouped by resource. Groups follow groupOrder; endpoints inside
// a group follow path order and, within one path, methodOrder.
func writeEndpoints(b *strings.Builder, doc *specDoc) {
	groups := collectGroups(doc)

	b.WriteString("## Endpoints\n\n")

	for _, g := range groups {
		fmt.Fprintf(b, "### %s\n\n", g.name)
		for _, ep := range g.endpoints {
			writeEndpoint(b, ep)
		}
	}
}

// collectGroups buckets the document's operations by resource and returns them in display order.
func collectGroups(doc *specDoc) []group {
	byName := map[string]*group{}

	for path, item := range doc.Paths {
		name := groupFor(path)
		g, ok := byName[name]
		if !ok {
			g = &group{name: name}
			byName[name] = g
		}
		for _, m := range methodOrder {
			if op := operationFor(item, m); op != nil {
				g.endpoints = append(g.endpoints, endpoint{method: m, path: path, op: op})
			}
		}
	}

	for _, g := range byName {
		sort.Slice(g.endpoints, func(i, j int) bool {
			if g.endpoints[i].path != g.endpoints[j].path {
				return g.endpoints[i].path < g.endpoints[j].path
			}
			return methodRank(g.endpoints[i].method) < methodRank(g.endpoints[j].method)
		})
	}

	return sortGroups(byName)
}

// sortGroups places the well-known groups first, in groupOrder, and the rest alphabetically.
func sortGroups(byName map[string]*group) []group {
	out := make([]group, 0, len(byName))
	seen := map[string]bool{}

	for _, name := range groupOrder {
		if g, ok := byName[name]; ok {
			out = append(out, *g)
			seen[name] = true
		}
	}

	var rest []string
	for name := range byName {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		out = append(out, *byName[name])
	}
	return out
}

// groupFor names the resource section a path belongs to.
func groupFor(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 3 || segments[0] != "api" || segments[1] != "v1" {
		if len(segments) > 0 && segments[0] == "wizard" {
			return "Wizard & settings"
		}
		return "Meta"
	}

	switch segments[2] {
	case "documents", "filter-languages", "supported-mime-types":
		return "Documents"
	case "tags":
		return "Tags"
	case "people", "people-types":
		return "People"
	case "document-types":
		return "Document types"
	case "consume":
		return "Ingestion"
	case "tasks", "dashboard":
		return "Tasks"
	case "batches":
		return "Batches"
	case "trash":
		return "Trash"
	case "orphaned", "errored":
		return "Orphaned & errored"
	case "saved-searches":
		return "Saved searches"
	case "llm":
		return "LLM catalogs"
	case "users":
		return "Users"
	case "me":
		return "Me"
	case "logs":
		return "Logs"
	case "openapi.yaml":
		return "Meta"
	case "auth":
		return "Session"
	default:
		return "Other"
	}
}

// operationFor returns the operation bound to the method, if the path serves it.
func operationFor(item specPathItem, method string) *specOperation {
	switch method {
	case "GET":
		return item.Get
	case "POST":
		return item.Post
	case "PUT":
		return item.Put
	case "DELETE":
		return item.Delete
	case "PATCH":
		return item.Patch
	}
	return nil
}

// methodRank is the position of a method in methodOrder.
func methodRank(method string) int {
	for i, m := range methodOrder {
		if m == method {
			return i
		}
	}
	return len(methodOrder)
}

// writeEndpoint emits one operation's heading, prose, parameters, request body and responses.
func writeEndpoint(b *strings.Builder, ep endpoint) {
	fmt.Fprintf(b, "#### `%s %s`\n\n", ep.method, ep.path)

	if summary := cleanProse(ep.op.Summary); summary != "" {
		b.WriteString(summary)
		b.WriteString("\n\n")
	}
	if desc := cleanProse(ep.op.Description); desc != "" && desc != cleanProse(ep.op.Summary) {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}

	writeParameters(b, ep.op.Parameters)
	writeRequestBody(b, ep.op.RequestBody)
	writeResponses(b, ep.op.Responses)
}

// writeParameters emits the parameter table when the operation takes any.
func writeParameters(b *strings.Builder, params []specParameter) {
	if len(params) == 0 {
		return
	}

	b.WriteString("| Parameter | In | Type | Required | Default |\n|---|---|---|---|---|\n")
	for _, p := range params {
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n",
			cell(p.Name), p.In, cell(schemaText(p.Schema)), requiredWord(p.Required), defaultText(p.Schema))
	}
	b.WriteString("\n")
}

// writeRequestBody emits the request payload summary, when the operation has one.
func writeRequestBody(b *strings.Builder, body *specRequestBody) {
	if body == nil || len(body.Content) == 0 {
		return
	}

	media := sortedKeys(body.Content)
	schema := body.Content[media[0]].Schema

	b.WriteString("Request body")
	if body.Required {
		b.WriteString(" (required)")
	}
	fmt.Fprintf(b, " (`%s`): %s\n\n", media[0], schemaText(schema))
}

// writeResponses emits the status table of an operation.
func writeResponses(b *strings.Builder, responses map[string]specResponse) {
	if len(responses) == 0 {
		return
	}

	b.WriteString("| Status | Body |\n|---|---|\n")
	for _, status := range sortedKeys(responses) {
		resp := responses[status]
		body := "no body"
		if media := sortedKeys(resp.Content); len(media) > 0 {
			body = schemaText(resp.Content[media[0]].Schema)
		}
		fmt.Fprintf(b, "| %s | %s |\n", status, body)
	}
	b.WriteString("\n")
}

// writeSchemas emits the component appendix: one section per named schema, sorted by name, so that the
// links in the endpoint tables stay stable across regeneration.
func writeSchemas(b *strings.Builder, doc *specDoc) {
	if len(doc.Components.Schemas) == 0 {
		return
	}

	b.WriteString("## Schemas\n\n")

	for _, name := range sortedKeys(doc.Components.Schemas) {
		writeSchema(b, name, doc.Components.Schemas[name])
	}
}

// writeSchema emits one named schema: its description, and either a field table or its shape.
func writeSchema(b *strings.Builder, name string, s specSchema) {
	fmt.Fprintf(b, "### %s\n\n", name)

	if desc := cleanProse(s.Description); desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}

	if len(s.Properties) == 0 {
		fmt.Fprintf(b, "Type: %s\n\n", schemaText(s))
		if len(s.Enum) > 0 {
			fmt.Fprintf(b, "Values: %s\n\n", enumText(s.Enum))
		}
		return
	}

	required := map[string]bool{}
	for _, field := range s.Required {
		required[field] = true
	}

	b.WriteString("| Field | Type | Required |\n|---|---|---|\n")
	for _, field := range sortedKeys(s.Properties) {
		prop := s.Properties[field]
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", cell(field), cell(schemaText(prop)), requiredWord(required[field]))
	}
	b.WriteString("\n")
}

// schemaText renders a schema as one line of prose. References become links into the appendix.
func schemaText(s specSchema) string {
	if s.Ref != "" {
		name := refName(s.Ref)
		return fmt.Sprintf("[%s](#%s)", name, anchor(name))
	}

	if len(s.AnyOf) > 0 {
		return anyOfText(s.AnyOf)
	}

	switch {
	case s.Type == "array" && s.Items != nil:
		return "array of " + schemaText(*s.Items)
	case s.Type == "object" || len(s.Properties) > 0:
		return "object"
	case s.Type != "" && s.Format != "":
		return s.Type + " (" + s.Format + ")"
	case s.Type != "":
		return s.Type
	default:
		return "any JSON"
	}
}

// anyOfText renders the anyOf form so a nullable reference stays readable instead of turning into a
// three-way union a reader has to parse, and identical variants are collapsed into a single label.
func anyOfText(variants []specSchema) string {
	var parts []string
	seen := map[string]bool{}
	nullable := false
	for _, v := range variants {
		if v.Type == "null" {
			nullable = true
			continue
		}
		text := schemaText(v)
		if seen[text] {
			continue
		}
		seen[text] = true
		parts = append(parts, text)
	}

	text := strings.Join(parts, " or ")
	if nullable {
		text += " (nullable)"
	}
	return text
}

// refName extracts the schema name from a local $ref.
func refName(ref string) string {
	const prefix = "#/components/schemas/"
	return strings.TrimPrefix(ref, prefix)
}

// anchor renders the GitHub heading anchor of a schema name.
func anchor(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '.':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// enumText renders schema enum values as a comma-separated list.
func enumText(values []any) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprintf("`%v`", v)
	}
	return strings.Join(parts, ", ")
}

// defaultText renders a schema default for the parameter table, or an em dash when there is none.
func defaultText(s specSchema) string {
	if s.Default == nil {
		return "—"
	}
	if text := fmt.Sprint(s.Default); text != "" {
		return "`" + text + "`"
	}
	return `""`
}

// requiredWord renders the required column.
func requiredWord(required bool) string {
	if required {
		return "yes"
	}
	return "no"
}

// sortedKeys returns a map's keys in stable order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// cell makes a value safe inside a markdown table cell.
func cell(text string) string {
	text = strings.ReplaceAll(text, "|", `\|`)
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}

// cleanProse collapses a YAML block scalar into a single line so prose fits in the generated tables and
// paragraphs; descriptions from Go doc comments never carry meaningful line breaks, so this is safe.
func cleanProse(text string) string {
	fields := strings.Fields(text)
	return strings.Join(fields, " ")
}
