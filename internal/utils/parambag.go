package utils

import (
	"context"
	"net/http"
	"strconv"
	"strings"
)

type ParamBag struct {
	queryValues map[string]string
	pathValues  map[string]string
}

func NewParamBag(r *http.Request) *ParamBag {
	pb := &ParamBag{
		queryValues: make(map[string]string),
		pathValues:  make(map[string]string),
	}

	for key, values := range r.URL.Query() {
		if len(values) > 0 {
			pb.queryValues[key] = values[0]
		}
	}

	// extract path parameters (if using path variables)
	// these would be populated by the router/middleware
	// for now, we'll leave this empty

	return pb
}

func (pb *ParamBag) Get(key, defaultValue string) string {
	if val, ok := pb.queryValues[key]; ok {
		return val
	}
	if val, ok := pb.pathValues[key]; ok {
		return val
	}
	return defaultValue
}

func (pb *ParamBag) GetInt(key string, defaultValue, min, max int) int {
	strVal := pb.Get(key, "")
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

func (pb *ParamBag) GetInt64(key string, defaultValue, min, max int64) int64 {
	strVal := pb.Get(key, "")
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

func (pb *ParamBag) GetBool(key string, defaultValue bool) bool {
	strVal := pb.Get(key, "")
	if strVal == "" {
		return defaultValue
	}

	val, err := strconv.ParseBool(strVal)
	if err != nil {
		return defaultValue
	}

	return val
}

func (pb *ParamBag) GetStrings(key string) []string {
	strVal := pb.Get(key, "")
	if strVal == "" {
		return nil
	}

	return strings.Split(strVal, ",")
}

func (pb *ParamBag) SetPathValue(key, value string) {
	pb.pathValues[key] = value
}

type contextKey string

const paramBagKey contextKey = "parambag"

func WithParamBag(r *http.Request, pb *ParamBag) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), paramBagKey, pb))
}

func GetParamBag(r *http.Request) *ParamBag {
	if pb, ok := r.Context().Value(paramBagKey).(*ParamBag); ok {
		return pb
	}
	return nil
}
