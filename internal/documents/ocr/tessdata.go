package ocr

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wgomg/edub-kushim/internal/log"
)

var ensureOnce sync.Once

// EnsureLanguages makes the tessdata directory exist, downloads any
// configured language pack that is missing from the tessdata_fast
// repository, and points Tesseract at the directory through
// TESSDATA_PREFIX. The work runs once per process; later calls return the
// first call's result.
func EnsureLanguages(logger *log.Logger, dataDir string, languages []string) error {
	var err error
	ensureOnce.Do(func() {
		if err = os.MkdirAll(dataDir, 0755); err != nil {
			err = fmt.Errorf("create tessdata dir: %w", err)
			return
		}

		for _, lang := range languages {
			dest := filepath.Join(dataDir, lang+".traineddata")
			if _, statErr := os.Stat(dest); statErr == nil {
				continue
			}

			url := fmt.Sprintf(
				"https://github.com/tesseract-ocr/tessdata_fast/raw/main/%s.traineddata",
				lang,
			)
			logger.Info(nil, "downloading OCR language: %s", lang)
			if dlErr := downloadTessdata(url, dest); dlErr != nil {
				_ = os.Remove(dest)
				err = fmt.Errorf("download %s: %w", lang, dlErr)
				return
			}
		}

		_ = os.Setenv("TESSDATA_PREFIX", dataDir)
		logger.Debug(nil, "TESSDATA_PREFIX=%s", dataDir)
	})
	return err
}

func downloadTessdata(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d (check language code)", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	if written < 500_000 {
		return fmt.Errorf("downloaded file too small (%d bytes)", written)
	}

	return nil
}

// LangString joins language codes the way Tesseract expects them.
func LangString(languages []string) string {
	return strings.Join(languages, "+")
}
