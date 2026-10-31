package main

// specDoc is the subset of the OpenAPI document the reference renders.
type specDoc struct {
	Info       specInfo                `yaml:"info"`
	Servers    []specServer            `yaml:"servers"`
	Paths      map[string]specPathItem `yaml:"paths"`
	Components specComponents          `yaml:"components"`
}

// specInfo is the document metadata block.
type specInfo struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Version     string `yaml:"version"`
}

// specServer is one base URL entry.
type specServer struct {
	URL         string `yaml:"url"`
	Description string `yaml:"description"`
}

// specPathItem maps the HTTP methods of one path to their operations.
type specPathItem struct {
	Get    *specOperation `yaml:"get"`
	Post   *specOperation `yaml:"post"`
	Put    *specOperation `yaml:"put"`
	Delete *specOperation `yaml:"delete"`
	Patch  *specOperation `yaml:"patch"`
}

// specOperation is one endpoint as the specification states it.
type specOperation struct {
	Summary     string                  `yaml:"summary"`
	Description string                  `yaml:"description"`
	Parameters  []specParameter         `yaml:"parameters"`
	RequestBody *specRequestBody        `yaml:"requestBody"`
	Responses   map[string]specResponse `yaml:"responses"`
}

// specParameter is one path, query, header or cookie parameter.
type specParameter struct {
	Name     string     `yaml:"name"`
	In       string     `yaml:"in"`
	Required bool       `yaml:"required"`
	Schema   specSchema `yaml:"schema"`
}

// specRequestBody is an operation's request payload.
type specRequestBody struct {
	Required bool                     `yaml:"required"`
	Content  map[string]specMediaType `yaml:"content"`
}

// specMediaType is one media type of a request or response body.
type specMediaType struct {
	Schema specSchema `yaml:"schema"`
}

// specResponse is one status code of an operation.
type specResponse struct {
	Content map[string]specMediaType `yaml:"content"`
}

// specComponents holds the named schemas the document references.
type specComponents struct {
	Schemas map[string]specSchema `yaml:"schemas"`
}

// specSchema is the schema shape the reference renders: scalars, arrays, objects and references.
type specSchema struct {
	Ref         string                `yaml:"$ref"`
	Type        string                `yaml:"type"`
	Format      string                `yaml:"format"`
	Description string                `yaml:"description"`
	Default     any                   `yaml:"default"`
	Enum        []any                 `yaml:"enum"`
	Required    []string              `yaml:"required"`
	Properties  map[string]specSchema `yaml:"properties"`
	Items       *specSchema           `yaml:"items"`
	AnyOf       []specSchema          `yaml:"anyOf"`
}
