package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ParamBag is the per-request lookup of query parameters, installed by
// middleware and read by the handlers. Path values are the router's
// business: handlers read them through r.PathValue.
type ParamBag struct {
	queryValues url.Values
}

// NewParamBag snapshots every value of each query parameter key, in arrival order, so that a repeated
// key reaches GetStrings intact; the scalar accessors read the first value of the key.
func NewParamBag(r *http.Request) *ParamBag {
	return &ParamBag{queryValues: r.URL.Query()}
}

// Get returns the parameter's value or the default.
func (pb *ParamBag) Get(key, defaultValue string) string {
	if values := pb.queryValues[key]; len(values) > 0 {
		return values[0]
	}
	return defaultValue
}

// first returns the key's first value in arrival order, or an empty string when the key is absent or empty.
// It must not delegate to Get: apispec's paramPatterns trace the nested Get call and retype every GetInt and
// GetInt64 parameter as a string, drifting the generated spec.
func (pb *ParamBag) first(key string) string {
	if values := pb.queryValues[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}

// GetInt returns the parsed integer parameter clamped to the bounds.
func (pb *ParamBag) GetInt(key string, defaultValue, min, max int) int {
	strVal := pb.first(key)
	if strVal == "" {
		return defaultValue
	}

	val, err := strconv.Atoi(strVal)
	if err != nil {
		return defaultValue
	}

	if min != 0 && val < min {
		return defaultValue
	}
	if max != 0 && val > max {
		return defaultValue
	}

	return val
}

// GetInt64 returns the parsed int64 parameter clamped to the bounds.
func (pb *ParamBag) GetInt64(key string, defaultValue, min, max int64) int64 {
	strVal := pb.first(key)
	if strVal == "" {
		return defaultValue
	}

	val, err := strconv.ParseInt(strVal, 10, 64)
	if err != nil {
		return defaultValue
	}

	if min != 0 && val < min {
		return defaultValue
	}

	if max != 0 && val > max {
		return defaultValue
	}

	return val
}

// GetBool returns the parsed boolean parameter.
func (pb *ParamBag) GetBool(key string, defaultValue bool) bool {
	strVal := pb.first(key)
	if strVal == "" {
		return defaultValue
	}

	val, err := strconv.ParseBool(strVal)
	if err != nil {
		return defaultValue
	}

	return val
}

// GetStrings returns the parameter's entries from both of the encodings a client may send: repeated
// keys (?size=a&size=b, the OpenAPI form default) and a single comma-separated value (?size=a,b) yield
// the same slice of entries, in arrival order, empty entries included. A key that is absent or carries
// a single empty value has no entries and returns nil.
func (pb *ParamBag) GetStrings(key string) []string {
	joined := strings.Join(pb.queryValues[key], ",")
	if joined == "" {
		return nil
	}

	return strings.Split(joined, ",")
}

type contextKey string

const paramBagKey contextKey = "parambag"

// WithParamBag installs the bag in the request context, where the
// handlers read it from.
func WithParamBag(r *http.Request, pb *ParamBag) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), paramBagKey, pb))
}

// GetParamBag returns the request's bag, nil when the middleware did not
// install one.
func GetParamBag(r *http.Request) *ParamBag {
	if pb, ok := r.Context().Value(paramBagKey).(*ParamBag); ok {
		return pb
	}
	return nil
}
