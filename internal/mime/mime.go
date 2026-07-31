package mime

import "slices"

import "strings"

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

const ImagePrefix = "image/"

type MimeInfo struct {
	MimeType  string `json:"mime_type"`
	Extension string `json:"extension"`
	Label     string `json:"label"`
}

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

func ExtensionsFor(mimeType string) []string {
	for _, m := range Supported {
		if m.MimeType == mimeType {
			return append([]string{m.Extension}, extensionAliases[mimeType]...)
		}
	}
	return nil
}

func IsPDF(mimeType string) bool {
	return mimeType == PDF
}

func IsImage(mimeType string) bool {
	return strings.HasPrefix(mimeType, ImagePrefix)
}

func IsOfficeDoc(mimeType string) bool {
	return mimeType == DOCX || mimeType == ODT
}

func ExtensionFromMimeType(mimeType string) string {
	for _, m := range Supported {
		if m.MimeType == mimeType {
			return m.Extension
		}
	}
	return ".pdf"
}

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

func BuildExtensionSet(exts []string) map[string]bool {
	supported := make(map[string]bool, len(exts))
	for _, ext := range exts {
		supported[strings.ToLower(ext)] = true
	}
	return supported
}
