package kb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wgomg/edub-kushim/internal/database"
)

// lfsBatchEndpoint is the Git LFS Batch API of the repository that publishes the artifacts. The artifacts
// are LFS-tracked, so the raw URL serves a small pointer and the batch API resolves the object's download
// URL; no git-lfs client is needed at runtime.
const lfsBatchEndpoint = "https://github.com/wgomg/edub-kushim.git/info/lfs/objects/batch"

// lfsMediaType is the Git LFS JSON media type both the request and the Accept header carry.
const lfsMediaType = "application/vnd.git-lfs+json"

// lfsPointerVersion is the only pointer format version the downloader understands.
const lfsPointerVersion = "https://git-lfs.github.com/spec/v1"

// pin is one published artifact's identity and location: the source and version it installs, the converter
// that produced it, the committed filename, the pointer URL on the publishing branch, and the artifact's
// sha256. The hash is the integrity guarantee — a pointer or download that does not match it is refused.
type pin struct {
	source           string
	version          string
	converterVersion string
	filename         string
	pointerURL       string
	sha256           string
}

// pins is the published artifact set. A new upstream edition or a converter change bumps the affected pin
// and ships the new artifact in the same commit; the pointer URL never changes across regenerations.
var pins = []pin{
	{
		source:           "oewn",
		version:          "2025",
		converterVersion: "1",
		filename:         "oewn-2025.tar.gz",
		pointerURL:       "https://raw.githubusercontent.com/wgomg/edub-kushim/dev/assets/kb/oewn-2025.tar.gz",
		sha256:           "3d7dab7b04006e8f137e3db4e7ebfa695f563b00f872ed130cb9d6aeaebb60f9",
	},
	{
		source:           "conceptnet",
		version:          "5.7.0-en",
		converterVersion: "1",
		filename:         "conceptnet-5.7.0-en.tar.gz",
		pointerURL:       "https://raw.githubusercontent.com/wgomg/edub-kushim/dev/assets/kb/conceptnet-5.7.0-en.tar.gz",
		sha256:           "bfc15a537e95418aeb59ac5a1a7d0b5170128414711fb0b4a04902ea0e661358",
	},
}

// pinnedSource returns the pin published for a source name.
func pinnedSource(name string) (pin, bool) {
	for _, p := range pins {
		if p.source == name {
			return p, true
		}
	}
	return pin{}, false
}

// Ensure makes the named configured source current. When the store already records the pinned source version
// and converter version it does nothing; otherwise it downloads and hash-checks the pinned artifact into the
// store's cache and installs it, replacing the source's previous rows. An unknown source, a hash mismatch,
// or a malformed artifact fails loudly and installs nothing.
func (s *Store) Ensure(ctx context.Context, source string) error {
	pinned, ok := pinnedSource(source)
	if !ok {
		return fmt.Errorf("unknown knowledge base source %q", source)
	}

	current, err := s.client.GetKBSource(ctx, source)
	switch {
	case err == nil:
		if atPin(current, pinned) {
			return nil
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return fmt.Errorf("read installed kb source %q: %w", source, err)
	}

	artifactPath := filepath.Join(s.dir, pinned.filename)
	if err := ensureArtifact(ctx, s.dir, artifactPath, pinned); err != nil {
		return err
	}
	if err := Install(ctx, s.client, artifactPath); err != nil {
		return fmt.Errorf("install kb source %q: %w", source, err)
	}
	return nil
}

// Missing returns the configured source names that are not installed at their pinned version, in the order
// given. A name without a pin is reported missing, so an unknown source cannot silently look current.
func (s *Store) Missing(ctx context.Context, configured []string) ([]string, error) {
	rows, err := s.client.ListKBSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("list kb sources: %w", err)
	}
	installed := make(map[string]database.KbSource, len(rows))
	for _, row := range rows {
		installed[row.Source] = row
	}

	var missing []string
	for _, name := range configured {
		pinned, ok := pinnedSource(name)
		if !ok {
			missing = append(missing, name)
			continue
		}
		if row, ok := installed[name]; ok && atPin(row, pinned) {
			continue
		}
		missing = append(missing, name)
	}
	return missing, nil
}

// atPin reports whether an installed source row records the pinned source and converter versions. It is the
// single definition of "current" shared by Ensure and Missing, so the installer and the status report cannot
// disagree about whether a source needs re-ensuring.
func atPin(row database.KbSource, pinned pin) bool {
	return row.SourceVersion == pinned.version && row.ConverterVersion == pinned.converterVersion
}

// ensureArtifact leaves a hash-matching artifact at dest, reusing the cached copy when it matches and
// downloading through the LFS pointer when it does not. The download lands in a temp file that is renamed
// into place only after the hash passes, so dest never holds a partial or wrong generation.
func ensureArtifact(ctx context.Context, dir, dest string, pinned pin) error {
	if sum, err := fileSHA256(dest); err == nil && strings.EqualFold(sum, pinned.sha256) {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("hash cached kb artifact %s: %w", dest, err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create kb artifact dir: %w", err)
	}

	pointer, err := fetchPointer(ctx, pinned.pointerURL)
	if err != nil {
		return fmt.Errorf("fetch kb artifact pointer for %q: %w", pinned.source, err)
	}
	if !strings.EqualFold(pointer.OID, pinned.sha256) {
		return fmt.Errorf("kb artifact pointer for %q carries sha256 %s, pin holds %s — the published artifact does not match this build",
			pinned.source, pointer.OID, pinned.sha256)
	}

	href, err := lfsDownloadURL(ctx, pointer)
	if err != nil {
		return fmt.Errorf("resolve kb artifact download for %q: %w", pinned.source, err)
	}

	tmp, err := os.CreateTemp(dir, "."+pinned.filename+"-*")
	if err != nil {
		return fmt.Errorf("create kb artifact temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	sum, err := downloadTo(ctx, href, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("download kb artifact for %q: %w", pinned.source, err)
	}
	if !strings.EqualFold(sum, pinned.sha256) {
		return fmt.Errorf("kb artifact for %q downloaded with sha256 %s, pin holds %s — refusing to install",
			pinned.source, sum, pinned.sha256)
	}

	if err := os.Chmod(tmpPath, 0o644); err != nil {
		return fmt.Errorf("set kb artifact mode: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("install kb artifact file: %w", err)
	}
	return nil
}

// lfsPointer is the subset of a Git LFS pointer the downloader needs.
type lfsPointer struct {
	OID  string
	Size int64
}

// fetchPointer reads the LFS pointer served for an artifact path.
func fetchPointer(ctx context.Context, url string) (lfsPointer, error) {
	reqCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return lfsPointer{}, err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return lfsPointer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return lfsPointer{}, fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return lfsPointer{}, fmt.Errorf("read pointer: %w", err)
	}
	return parseLFSPointer(body)
}

// parseLFSPointer parses the three-line pointer format and refuses anything that is not a v1 pointer (a
// misconfigured raw URL may serve the object itself, or an HTML error page).
func parseLFSPointer(body []byte) (lfsPointer, error) {
	var pointer lfsPointer
	var version, oid, size string

	for line := range strings.SplitSeq(string(body), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		switch key {
		case "version":
			version = value
		case "oid":
			oid = value
		case "size":
			size = value
		}
	}

	if version != lfsPointerVersion {
		return lfsPointer{}, fmt.Errorf("not a Git LFS pointer (version %q)", version)
	}
	hexOID, ok := strings.CutPrefix(oid, "sha256:")
	if !ok || len(hexOID) != sha256.Size*2 {
		return lfsPointer{}, fmt.Errorf("pointer oid %q is not a sha256", oid)
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil || n <= 0 {
		return lfsPointer{}, fmt.Errorf("pointer size %q is not a positive integer", size)
	}

	pointer.OID = hexOID
	pointer.Size = n
	return pointer, nil
}

// lfsBatchObject is one object of the Batch API request and response.
type lfsBatchObject struct {
	OID   string `json:"oid"`
	Size  int64  `json:"size"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Actions map[string]struct {
		Href string `json:"href"`
	} `json:"actions"`
}

// lfsDownloadURL posts the pointer's object to the Batch API and returns its anonymous download URL.
func lfsDownloadURL(ctx context.Context, pointer lfsPointer) (string, error) {
	body, err := json.Marshal(map[string]any{
		"operation": "download",
		"transfers": []string{"basic"},
		"objects":   []map[string]any{{"oid": pointer.OID, "size": pointer.Size}},
	})
	if err != nil {
		return "", fmt.Errorf("encode batch request: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, lfsBatchEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", lfsMediaType)
	req.Header.Set("Content-Type", lfsMediaType)

	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("batch API returned %s: %s", resp.Status, strings.TrimSpace(string(detail)))
	}

	var parsed struct {
		Message string           `json:"message"`
		Objects []lfsBatchObject `json:"objects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("decode batch response: %w", err)
	}
	if parsed.Message != "" {
		return "", fmt.Errorf("batch API: %s", parsed.Message)
	}
	if len(parsed.Objects) != 1 {
		return "", fmt.Errorf("batch API returned %d objects, want 1", len(parsed.Objects))
	}

	object := parsed.Objects[0]
	if object.Error != nil {
		return "", fmt.Errorf("batch API object error %d: %s", object.Error.Code, object.Error.Message)
	}
	action, ok := object.Actions["download"]
	if !ok || action.Href == "" {
		return "", fmt.Errorf("batch API returned no download action")
	}
	return action.Href, nil
}

// downloadTo copies the response body into f and returns its sha256. A non-200 answer is an error; the body
// of a failed answer is not part of the artifact.
func downloadTo(ctx context.Context, url string, f *os.File) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET artifact: %s", resp.Status)
	}

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, hasher), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// fileSHA256 returns the lowercase hex digest of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// downloadClient has no overall timeout — an artifact download is bounded by its caller's context — but
// fails a silent connection instead of hanging on response headers.
var downloadClient = &http.Client{
	Transport: func() *http.Transport {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = time.Minute
		return transport
	}(),
}
