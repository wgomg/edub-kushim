// Package mime is the vocabulary of the document formats the pipeline
// ingests: canonical MIME types, their extensions, and the format filters
// routing logic is based on. Actual file-type detection lives in the storage
// package; everything else must classify through this package.
package mime

import "slices"

import "strings"

// The canonical MIME type for every format the pipeline accepts. Detection
// and configuration must use these values verbatim.
const (
	PDF         = "application/pdf"
	DOCX        = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	ODT         = "application/vnd.oasis.opendocument.text"
	TIFF        = "image/tiff"
	JPEG        = "image/jpeg"
	PNG         = "image/png"
	ZIP         = "application/zip"
	OctetStream = "application/octet-stream"
)

// ImagePrefix is the prefix shared by every raster image MIME type.
const ImagePrefix = "image/"

// MimeInfo describes one supported format: its canonical MIME type, the
// canonical file extension, and the short label shown in the UI.
type MimeInfo struct {
	MimeType  string `json:"mime_type"`
	Extension string `json:"extension"`
	Label     string `json:"label"`
}

// Supported lists every format the pipeline ingests. ExtensionsFor and
// ExtensionFromMimeType resolve against this list.
var Supported = []MimeInfo{
	{MimeType: PDF, Extension: ".pdf", Label: "PDF"},
	{MimeType: DOCX, Extension: ".docx", Label: "DOCX"},
	{MimeType: ODT, Extension: ".odt", Label: "ODT"},
	{MimeType: TIFF, Extension: ".tiff", Label: "TIFF"},
	{MimeType: JPEG, Extension: ".jpg", Label: "JPEG"},
	{MimeType: PNG, Extension: ".png", Label: "PNG"},
}

// extensionAliases holds accepted alternative extensions per MIME type; content
// detection can return either form (e.g. .jpeg vs .jpg), so filters must
// accept both.
var extensionAliases = map[string][]string{
	TIFF: {".tif"},
	JPEG: {".jpeg"},
}

// ExtensionsFor returns every accepted extension for a MIME type, canonical
// first, aliases after; nil for an unsupported type. Content detection may
// report either form (jpeg or jpg), so filters must accept all of them.
func ExtensionsFor(mimeType string) []string {
	for _, m := range Supported {
		if m.MimeType == mimeType {
			return append([]string{m.Extension}, extensionAliases[mimeType]...)
		}
	}
	return nil
}

// IsPDF reports whether the type is the canonical PDF type.
func IsPDF(mimeType string) bool {
	return mimeType == PDF
}

// IsImage reports whether the type is any raster image.
func IsImage(mimeType string) bool {
	return strings.HasPrefix(mimeType, ImagePrefix)
}

// IsOfficeDoc reports whether the type is one of the editable office
// formats, DOCX or ODT, which need conversion before text extraction.
func IsOfficeDoc(mimeType string) bool {
	return mimeType == DOCX || mimeType == ODT
}

// ExtensionFromMimeType returns the canonical extension of a supported
// MIME type. Unknown types fall back to ".pdf".
func ExtensionFromMimeType(mimeType string) string {
	for _, m := range Supported {
		if m.MimeType == mimeType {
			return m.Extension
		}
	}
	return ".pdf"
}

// MimeTypeFromExtension resolves a file extension, case-insensitive and
// including its leading dot, to the supported MIME type it names, accepting
// the known aliases. Anything else falls back to application/octet-stream.
func MimeTypeFromExtension(ext string) string {
	ext = strings.ToLower(ext)
	for _, m := range Supported {
		if m.Extension == ext {
			return m.MimeType
		}
		if slices.Contains(extensionAliases[m.MimeType], ext) {
			return m.MimeType
		}
	}
	return OctetStream
}

// BuildExtensionSet lowercases a list of extensions into a membership set
// for the allowed-extension filters.
func BuildExtensionSet(exts []string) map[string]bool {
	supported := make(map[string]bool, len(exts))
	for _, ext := range exts {
		supported[strings.ToLower(ext)] = true
	}
	return supported
}
