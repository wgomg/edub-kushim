package mime

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
	MimeType     string `json:"mime_type"`
	Extension    string `json:"extension"`
	Label        string `json:"label"`
	Viewable     bool   `json:"viewable"`
	Viewer       string `json:"viewer"`
	OfficeFormat string `json:"office_format"`
}

var Supported = []MimeInfo{
	{MimeType: PDF, Extension: ".pdf", Label: "PDF", Viewable: true, Viewer: "iframe"},
	{MimeType: DOCX, Extension: ".docx", Label: "DOCX", Viewable: true, Viewer: "office", OfficeFormat: "docx"},
	{MimeType: ODT, Extension: ".odt", Label: "ODT", Viewable: true, Viewer: "office", OfficeFormat: "odt"},
	{MimeType: TIFF, Extension: ".tiff", Label: "TIFF"},
	{MimeType: JPEG, Extension: ".jpg", Label: "JPEG"},
	{MimeType: PNG, Extension: ".png", Label: "PNG"},
}

var (
	viewableExtMap map[string]string
	viewableSet    map[string]bool
)

func init() {
	viewableExtMap = make(map[string]string, len(Supported))
	viewableSet = make(map[string]bool, len(Supported))
	for _, m := range Supported {
		viewableExtMap[m.MimeType] = m.Extension
		if m.Viewable {
			viewableSet[m.MimeType] = true
		}
	}
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

func IsViewable(mimeType string) bool {
	return viewableSet[mimeType]
}

func ExtensionFromMimeType(mimeType string) string {
	if ext, ok := viewableExtMap[mimeType]; ok {
		return ext
	}
	return ".pdf"
}

func MimeTypeFromExtension(ext string) string {
	ext = strings.ToLower(ext)
	switch ext {
	case ".pdf":
		return PDF
	case ".docx":
		return DOCX
	case ".odt":
		return ODT
	case ".tiff", ".tif":
		return TIFF
	case ".jpg", ".jpeg":
		return JPEG
	case ".png":
		return PNG
	default:
		return OctetStream
	}
}

func BuildExtensionSet(exts []string) map[string]bool {
	supported := make(map[string]bool, len(exts))
	for _, ext := range exts {
		supported[strings.ToLower(ext)] = true
	}
	return supported
}


