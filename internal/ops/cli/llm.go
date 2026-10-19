package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/wgomg/edub-kushim/internal/llm"
	"github.com/wgomg/edub-kushim/internal/ops/container"
	"github.com/wgomg/edub-kushim/internal/ops/flags"
)

// fieldSpec binds one settable catalog field name to its getter and setter; a catalog's whole field set
// lives in exactly one such table, shared by key parsing, get, set and the dump.
type fieldSpec[T any] struct {
	name string
	get  func(T) any
	set  func(*T, string) error
}

// textSetter assigns a string field, coercing the raw value with scalarText.
func textSetter[T any](name string, field func(*T) *string) func(*T, string) error {
	return func(v *T, raw string) error {
		s, err := scalarText(name, raw)
		if err != nil {
			return err
		}
		*field(v) = s
		return nil
	}
}

// intSetter assigns a non-negative integer field.
func intSetter[T any](name string, field func(*T) *int) func(*T, string) error {
	return func(v *T, raw string) error {
		n, err := scalarInt(name, raw)
		if err != nil {
			return err
		}
		*field(v) = n
		return nil
	}
}

// boolSetter assigns a boolean field.
func boolSetter[T any](name string, field func(*T) *bool) func(*T, string) error {
	return func(v *T, raw string) error {
		b, err := scalarBool(name, raw)
		if err != nil {
			return err
		}
		*field(v) = b
		return nil
	}
}

// providerFieldSpecs and modelFieldSpecs are the catalogs' settable fields, in dump order.
var providerFieldSpecs = []fieldSpec[llm.ProviderInput]{
	{name: "adapter", get: func(p llm.ProviderInput) any { return p.Adapter },
		set: textSetter("adapter", func(p *llm.ProviderInput) *string { return &p.Adapter })},
	{name: "official_url", get: func(p llm.ProviderInput) any { return p.OfficialURL },
		set: textSetter("official_url", func(p *llm.ProviderInput) *string { return &p.OfficialURL })},
	{name: "session_header", get: func(p llm.ProviderInput) any { return p.SessionHeader },
		set: textSetter("session_header", func(p *llm.ProviderInput) *string { return &p.SessionHeader })},
}

var modelFieldSpecs = []fieldSpec[llm.ModelInput]{
	{name: "display_name", get: func(m llm.ModelInput) any { return m.DisplayName },
		set: textSetter("display_name", func(m *llm.ModelInput) *string { return &m.DisplayName })},
	{name: "max_input_tokens", get: func(m llm.ModelInput) any { return m.MaxInputTokens },
		set: intSetter("max_input_tokens", func(m *llm.ModelInput) *int { return &m.MaxInputTokens })},
	{name: "max_output_tokens", get: func(m llm.ModelInput) any { return m.MaxOutputTokens },
		set: intSetter("max_output_tokens", func(m *llm.ModelInput) *int { return &m.MaxOutputTokens })},
	{name: "disable_reasoning", get: func(m llm.ModelInput) any { return m.DisableReasoning },
		set: boolSetter("disable_reasoning", func(m *llm.ModelInput) *bool { return &m.DisableReasoning })},
	{name: "reasoning_efforts", get: func(m llm.ModelInput) any { return m.ReasoningEfforts },
		set: func(m *llm.ModelInput, raw string) error { m.ReasoningEfforts = listStrings(raw); return nil }},
	{name: "supports_structured_output", get: func(m llm.ModelInput) any { return m.SupportsStructuredOutput },
		set: boolSetter("supports_structured_output", func(m *llm.ModelInput) *bool { return &m.SupportsStructuredOutput })},
	{name: "supports_temperature", get: func(m llm.ModelInput) any { return m.SupportsTemperature },
		set: boolSetter("supports_temperature", func(m *llm.ModelInput) *bool { return &m.SupportsTemperature })},
	{name: "supports_prompt_caching", get: func(m llm.ModelInput) any { return m.SupportsPromptCaching },
		set: boolSetter("supports_prompt_caching", func(m *llm.ModelInput) *bool { return &m.SupportsPromptCaching })},
	{name: "uses_max_completion_tokens", get: func(m llm.ModelInput) any { return m.UsesMaxCompletionTokens },
		set: boolSetter("uses_max_completion_tokens", func(m *llm.ModelInput) *bool { return &m.UsesMaxCompletionTokens })},
	{name: "uses_responses_endpoint", get: func(m llm.ModelInput) any { return m.UsesResponsesEndpoint },
		set: boolSetter("uses_responses_endpoint", func(m *llm.ModelInput) *bool { return &m.UsesResponsesEndpoint })},
}

// providerFields and modelFields are the catalogs' valid field names, derived from the spec tables.
var providerFields = fieldNames(providerFieldSpecs)
var modelFields = fieldNames(modelFieldSpecs)

// fieldNames lists a spec table's field names, in table order.
func fieldNames[T any](specs []fieldSpec[T]) []string {
	names := make([]string, len(specs))
	for i, spec := range specs {
		names[i] = spec.name
	}
	return names
}

// findFieldSpec looks a field name up in a spec table.
func findFieldSpec[T any](specs []fieldSpec[T], field string) (fieldSpec[T], bool) {
	for _, spec := range specs {
		if spec.name == field {
			return spec, true
		}
	}
	return fieldSpec[T]{}, false
}

// applyField assigns the named field of a catalog entry, rejecting an unknown name.
func applyField[T any](kind string, specs []fieldSpec[T], v *T, field, raw string) error {
	spec, ok := findFieldSpec(specs, field)
	if !ok {
		return unknownField(kind, field)
	}
	return spec.set(v, raw)
}

// fieldValue returns the named field's value from a catalog entry, rejecting an unknown name.
func fieldValue[T any](kind string, specs []fieldSpec[T], v T, field string) (any, error) {
	spec, ok := findFieldSpec(specs, field)
	if !ok {
		return nil, unknownField(kind, field)
	}
	return spec.get(v), nil
}

// catalogKey is one parsed dot-notation catalog key: the catalog kind ("providers" or "models"), the
// provider name (or the provider of a model), the model id for models, and the field; an empty field
// selects the whole entry.
type catalogKey struct {
	kind    string
	name    string
	modelID string
	field   string
}

func llmHandler(c *container.Container, args []string) error {
	fp := flags.NewFlagParser(args)
	if fp.Help("Usage: kushim llm [<key> [<value>]]\n\n" +
		"View and edit the provider and model catalogs.\n\n" +
		"  kushim llm                                   Dump the catalog\n" +
		"  kushim llm <key>                             Get a value\n" +
		"  kushim llm <key> <value>                     Set a value\n" +
		"  kushim llm --unset <key>                     Remove an entry (--cascade its models too)\n" +
		"  kushim llm --reset [--providers] [--models]  Restore the embedded defaults\n\n" +
		"Keys use dot notation, e.g. providers.groq.adapter or\n" +
		"models.groq/llama-4-scout.max_input_tokens. A whole entry is set from a JSON\n" +
		"object value, a field from a plain one.") {
		return nil
	}

	var unsetKey string
	var reset, providersOnly, modelsOnly, cascade bool
	if err := fp.String("--unset", &unsetKey); err != nil {
		return err
	}
	fp.Bool("--reset", &reset)
	fp.Bool("--providers", &providersOnly)
	fp.Bool("--models", &modelsOnly)
	fp.Bool("--cascade", &cascade)

	rest := fp.Rest()

	if reset {
		if unsetKey != "" || len(rest) > 0 {
			return fmt.Errorf("unknown arguments: %v", rest)
		}
		return resetCatalog(c, providersOnly, modelsOnly)
	}
	if providersOnly || modelsOnly {
		return fmt.Errorf("--providers and --models require --reset")
	}
	if cascade && unsetKey == "" {
		return fmt.Errorf("--cascade requires --unset")
	}
	if unsetKey != "" {
		if len(rest) > 0 {
			return fmt.Errorf("unknown arguments: %v", rest)
		}
		return unsetCatalogValue(c, unsetKey, cascade)
	}

	switch len(rest) {
	case 0:
		return dumpCatalog(c)
	case 1:
		return getCatalogValue(c, rest[0])
	default:
		return setCatalogValue(c, rest[0], strings.Join(rest[1:], " "))
	}
}

// parseCatalogKey parses providers.<name>[.<field>] and models.<provider>/<model>[.<field>] keys; the
// field must be one of the catalog's settable names.
func parseCatalogKey(key string) (catalogKey, error) {
	kind, rest, ok := strings.Cut(key, ".")
	if !ok || rest == "" {
		return catalogKey{}, fmt.Errorf("invalid key %q: expected providers.<name>[.<field>] or models.<provider>/<model>[.<field>]", key)
	}
	switch kind {
	case "providers":
		name, field := trailingField(rest, providerFields)
		if field == "" && strings.Contains(name, ".") {
			return catalogKey{}, fmt.Errorf("invalid key %q: %q is neither a provider name nor a field; valid fields: %s",
				key, name, strings.Join(providerFields, ", "))
		}
		return catalogKey{kind: kind, name: name, field: field}, nil
	case "models":
		provider, remainder, ok := strings.Cut(rest, "/")
		if !ok || provider == "" || remainder == "" {
			return catalogKey{}, fmt.Errorf("invalid key %q: expected models.<provider>/<model>[.<field>]", key)
		}
		modelID, field := trailingField(remainder, modelFields)
		return catalogKey{kind: kind, name: provider, modelID: modelID, field: field}, nil
	default:
		return catalogKey{}, fmt.Errorf("invalid key %q: expected providers. or models. prefix", key)
	}
}

// trailingField splits a known trailing field name off a model key remainder so model ids containing
// dots stay addressable.
func trailingField(remainder string, fields []string) (string, string) {
	for _, f := range fields {
		if strings.HasSuffix(remainder, "."+f) && len(remainder) > len(f)+1 {
			return strings.TrimSuffix(remainder, "."+f), f
		}
	}
	return remainder, ""
}

// unknownField rejects a field name outside its catalog kind, listing the valid names.
func unknownField(kind, field string) error {
	fields := providerFields
	if kind == "model" {
		fields = modelFields
	}
	return fmt.Errorf("unknown %s field %q; valid fields: %s", kind, field, strings.Join(fields, ", "))
}

// dumpCatalog prints every catalog entry value as a sorted key = value line, mirroring the dumpAllConfig
// output.
func dumpCatalog(c *container.Container) error {
	reg, err := catalogRegistry(c)
	if err != nil {
		return err
	}
	catalog := reg.Catalog()

	var lines []string
	for _, p := range catalog.Providers {
		base := "providers." + p.Provider + "."
		for _, spec := range providerFieldSpecs {
			lines = append(lines, base+spec.name+" = "+dumpValue(spec.get(p)))
		}
	}
	for _, m := range catalog.Models {
		base := "models." + m.Provider + "/" + m.ModelID + "."
		for _, spec := range modelFieldSpecs {
			lines = append(lines, base+spec.name+" = "+dumpValue(spec.get(m)))
		}
	}
	sort.Strings(lines)
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

// dumpValue renders one field value as its dump text.
func dumpValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case []string:
		return strings.Join(t, ", ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

// getCatalogValue prints one catalog value: a field as a scalar, a whole entry as YAML.
func getCatalogValue(c *container.Container, key string) error {
	parsed, err := parseCatalogKey(key)
	if err != nil {
		return err
	}
	reg, err := catalogRegistry(c)
	if err != nil {
		return err
	}

	switch parsed.kind {
	case "providers":
		p, ok := findProviderInput(reg, parsed.name)
		if !ok {
			return fmt.Errorf("provider %q is not cataloged", parsed.name)
		}
		if parsed.field == "" {
			printValue(providerMap(p))
			return nil
		}
		value, err := fieldValue("provider", providerFieldSpecs, p, parsed.field)
		if err != nil {
			return err
		}
		printValue(value)
		return nil
	default:
		m, ok := findModelInput(reg, parsed.name, parsed.modelID)
		if !ok {
			return fmt.Errorf("model %s/%s is not cataloged", parsed.name, parsed.modelID)
		}
		if parsed.field == "" {
			printValue(modelMap(m))
			return nil
		}
		value, err := fieldValue("model", modelFieldSpecs, m, parsed.field)
		if err != nil {
			return err
		}
		printValue(value)
		return nil
	}
}

// setCatalogValue assigns one catalog value: a field on its entry — creating a missing model with its
// defaults — or a whole entry from a JSON object.
func setCatalogValue(c *container.Container, key, rawValue string) error {
	parsed, err := parseCatalogKey(key)
	if err != nil {
		return err
	}
	reg, err := catalogRegistry(c)
	if err != nil {
		return err
	}

	switch parsed.kind {
	case "providers":
		if parsed.field == "" {
			return setProviderEntry(reg, parsed.name, rawValue)
		}
		p, ok := findProviderInput(reg, parsed.name)
		if !ok {
			return fmt.Errorf("provider %q is not cataloged; create it as a whole entry: "+
				"kushim llm providers.%s '{\"adapter\":\"openai-compatible\",\"official_url\":\"https://...\"}'",
				parsed.name, parsed.name)
		}
		if err := applyField("provider", providerFieldSpecs, &p, parsed.field, rawValue); err != nil {
			return err
		}
		if err := reg.SetProvider(parsed.name, p); err != nil {
			return err
		}
		value, err := fieldValue("provider", providerFieldSpecs, p, parsed.field)
		if err != nil {
			return err
		}
		fmt.Printf("%s = %v\n", key, value)
		return nil
	default:
		if parsed.field == "" {
			return setModelEntry(reg, parsed.name, parsed.modelID, rawValue)
		}
		m, ok := findModelInput(reg, parsed.name, parsed.modelID)
		if !ok {
			m = llm.ModelInput{Provider: parsed.name, ModelID: parsed.modelID}
			if err := applyField("model", modelFieldSpecs, &m, parsed.field, rawValue); err != nil {
				return err
			}
			if err := reg.AddModel(m); err != nil {
				return err
			}
		} else {
			if err := applyField("model", modelFieldSpecs, &m, parsed.field, rawValue); err != nil {
				return err
			}
			if err := reg.SetModel(parsed.name, parsed.modelID, m); err != nil {
				return err
			}
		}
		value, err := fieldValue("model", modelFieldSpecs, m, parsed.field)
		if err != nil {
			return err
		}
		fmt.Printf("%s = %v\n", key, value)
		return nil
	}
}

// setProviderEntry creates or replaces a whole provider from a JSON object value.
func setProviderEntry(reg *llm.Registry, name, rawValue string) error {
	var in llm.ProviderInput
	if err := json.Unmarshal([]byte(rawValue), &in); err != nil {
		return fmt.Errorf("providers.%s expects a JSON object with the entry fields: %w", name, err)
	}
	in.Provider = name

	if _, ok := findProviderInput(reg, name); ok {
		if err := reg.SetProvider(name, in); err != nil {
			return err
		}
	} else if err := reg.AddProvider(in); err != nil {
		return err
	}

	normalized, err := json.Marshal(in)
	if err != nil {
		return err
	}
	fmt.Printf("providers.%s = %s\n", name, normalized)
	return nil
}

// setModelEntry creates or replaces a whole model from a JSON object value.
func setModelEntry(reg *llm.Registry, provider, modelID, rawValue string) error {
	var in llm.ModelInput
	if err := json.Unmarshal([]byte(rawValue), &in); err != nil {
		return fmt.Errorf("models.%s/%s expects a JSON object with the entry fields: %w", provider, modelID, err)
	}
	in.Provider, in.ModelID = provider, modelID

	if _, ok := findModelInput(reg, provider, modelID); ok {
		if err := reg.SetModel(provider, modelID, in); err != nil {
			return err
		}
	} else if err := reg.AddModel(in); err != nil {
		return err
	}

	normalized, err := json.Marshal(in)
	if err != nil {
		return err
	}
	fmt.Printf("models.%s/%s = %s\n", provider, modelID, normalized)
	return nil
}

// unsetCatalogValue removes one whole entry; the registry applies the in-use and orphan-model guards.
func unsetCatalogValue(c *container.Container, key string, cascade bool) error {
	parsed, err := parseCatalogKey(key)
	if err != nil {
		return err
	}
	if parsed.field != "" {
		return fmt.Errorf("unset removes whole entries; clear %s by setting it to \"\"", key)
	}
	reg, err := catalogRegistry(c)
	if err != nil {
		return err
	}

	switch parsed.kind {
	case "providers":
		if err := reg.RemoveProvider(parsed.name, cascade, c.GetConfig()); err != nil {
			return err
		}
	default:
		if err := reg.RemoveModel(parsed.name, parsed.modelID, c.GetConfig()); err != nil {
			return err
		}
	}
	fmt.Printf("unset %s\n", key)
	return nil
}

// resetCatalog restores the embedded defaults for the selected catalogs, both by default.
func resetCatalog(c *container.Container, providers, models bool) error {
	if !providers && !models {
		providers, models = true, true
	}
	if err := llm.ResetCatalogFiles(c.GetConfig().App.ConfigDir, providers, models); err != nil {
		return err
	}
	fmt.Println("catalog reset to embedded defaults")
	return nil
}

// catalogRegistry loads the provider and model catalogs for a CLI invocation.
func catalogRegistry(c *container.Container) (*llm.Registry, error) {
	reg, err := llm.NewRegistry(c.GetConfig().App.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("load model catalogs: %w", err)
	}
	return reg, nil
}

// findProviderInput looks a provider up in the loaded catalog.
func findProviderInput(reg *llm.Registry, name string) (llm.ProviderInput, bool) {
	for _, p := range reg.Catalog().Providers {
		if p.Provider == name {
			return p, true
		}
	}
	return llm.ProviderInput{}, false
}

// findModelInput looks a model up in the loaded catalog.
func findModelInput(reg *llm.Registry, provider, modelID string) (llm.ModelInput, bool) {
	for _, m := range reg.Catalog().Models {
		if m.Provider == provider && m.ModelID == modelID {
			return m, true
		}
	}
	return llm.ModelInput{}, false
}

// providerMap renders a provider entry, identity included, for a whole-entry get.
func providerMap(p llm.ProviderInput) map[string]any {
	entry := map[string]any{"provider": p.Provider}
	for _, spec := range providerFieldSpecs {
		entry[spec.name] = spec.get(p)
	}
	return entry
}

// modelMap renders a model entry, identity included, for a whole-entry get.
func modelMap(m llm.ModelInput) map[string]any {
	entry := map[string]any{"provider": m.Provider, "model_id": m.ModelID}
	for _, spec := range modelFieldSpecs {
		entry[spec.name] = spec.get(m)
	}
	return entry
}

// scalarText coerces a raw value to text, rejecting list values.
func scalarText(field, raw string) (string, error) {
	switch v := parseValue(raw).(type) {
	case string:
		return v, nil
	case bool:
		return strconv.FormatBool(v), nil
	case int:
		return strconv.Itoa(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("%s expects a single value, not a list", field)
	}
}

// scalarInt coerces a raw value to a non-negative integer.
func scalarInt(field, raw string) (int, error) {
	v, ok := parseValue(raw).(int)
	if !ok || v < 0 {
		return 0, fmt.Errorf("%s expects a non-negative integer", field)
	}
	return v, nil
}

// scalarBool coerces a raw value to a boolean.
func scalarBool(field, raw string) (bool, error) {
	v, ok := parseValue(raw).(bool)
	if !ok {
		return false, fmt.Errorf("%s expects true or false", field)
	}
	return v, nil
}

// listStrings coerces a raw value to a comma list; an empty value yields an empty list, clearing the
// field.
func listStrings(raw string) []string {
	switch v := parseValue(raw).(type) {
	case []string:
		return v
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return []string{fmt.Sprintf("%v", v)}
	}
}
