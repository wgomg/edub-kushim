package config

import (
	"bufio"
	"fmt"
	"os/exec"
	"strings"
)

type ExternalTool struct {
	Engine       string            `json:"engine"`
	Category     string            `json:"category"`
	Command      string            `json:"command"`
	Available    bool              `json:"available"`
	InstallHints map[string]string `json:"install_hints"`
	Purpose      string            `json:"purpose,omitempty"`
	Languages    []string          `json:"languages,omitempty"`
	LangHints    []LangHint        `json:"lang_hints,omitempty"`
	Companions   []CompanionTool   `json:"companions,omitempty"`
}

type LangHint struct {
	Language     string            `json:"language"`
	InstallHints map[string]string `json:"install_hints"`
}

type CompanionTool struct {
	Command      string            `json:"command"`
	Purpose      string            `json:"purpose"`
	Available    bool              `json:"available"`
	Required     bool              `json:"required"`
	InstallHints map[string]string `json:"install_hints"`
}

var engineInstallHints = map[string]map[string]string{
	"ocrmypdf": {
		"Debian/Ubuntu": "sudo apt install ocrmypdf",
		"Arch":          "sudo pacman -S ocrmypdf",
		"Fedora":        "sudo dnf install ocrmypdf",
		"Alpine":        "sudo apk add ocrmypdf",
		"macOS":         "brew install ocrmypdf",
		"pip":           "pip install ocrmypdf",
	},
	"gs": {
		"Debian/Ubuntu": "sudo apt install ghostscript",
		"Arch":          "sudo pacman -S ghostscript",
		"Fedora":        "sudo dnf install ghostscript",
		"Alpine":        "sudo apk add ghostscript",
		"macOS":         "brew install ghostscript",
	},
	"pdftotext": {
		"Debian/Ubuntu": "sudo apt install poppler-utils",
		"Arch":          "sudo pacman -S poppler",
		"Fedora":        "sudo dnf install poppler-utils",
		"Alpine":        "sudo apk add poppler-utils",
		"macOS":         "brew install poppler",
	},
	"curl": {
		"Debian/Ubuntu": "sudo apt install curl",
		"Arch":          "sudo pacman -S curl",
		"Fedora":        "sudo dnf install curl",
		"Alpine":        "sudo apk add curl",
		"macOS":         "brew install curl",
	},
	"libreoffice": {
		"Debian/Ubuntu": "sudo apt install libreoffice-writer-nogui",
		"Arch":          "sudo pacman -S libreoffice-fresh",
		"Fedora":        "sudo dnf install libreoffice-writer",
		"Alpine":        "sudo apk add libreoffice",
		"macOS":         "brew install --cask libreoffice",
	},
	"imagemagick": {
		"Debian/Ubuntu": "sudo apt install imagemagick",
		"Arch":          "sudo pacman -S imagemagick",
		"Fedora":        "sudo dnf install imagemagick",
		"Alpine":        "sudo apk add imagemagick",
		"macOS":         "brew install imagemagick",
	},
}

var companionInstallHints = map[string]map[string]string{
	"tesseract": {
		"Debian/Ubuntu": "sudo apt install tesseract-ocr",
		"Arch":          "sudo pacman -S tesseract",
		"Fedora":        "sudo dnf install tesseract",
		"Alpine":        "sudo apk add tesseract-ocr",
		"macOS":         "brew install tesseract",
	},
	"unpaper": {
		"Debian/Ubuntu": "sudo apt install unpaper",
		"Arch":          "sudo pacman -S unpaper",
		"Fedora":        "sudo dnf install unpaper",
		"Alpine":        "sudo apk add unpaper",
		"macOS":         "brew install unpaper",
	},
	"pngquant": {
		"Debian/Ubuntu": "sudo apt install pngquant",
		"Arch":          "sudo pacman -S pngquant",
		"Fedora":        "sudo dnf install pngquant",
		"Alpine":        "sudo apk add pngquant",
		"macOS":         "brew install pngquant",
	},
}

var ocrmypdfCompanionDefs = []struct {
	command  string
	purpose  string
	required bool
}{
	{
		command:  "tesseract",
		purpose:  "OCR engine that ocrmypdf wraps (core dependency)",
		required: true,
	},
	{
		command:  "unpaper",
		purpose:  "used by ocrmypdf's --clean flag (page cleanup)",
		required: true,
	},
	{
		command:  "pngquant",
		purpose:  "used by ocrmypdf's --optimize flag (PNG optimization)",
		required: false,
	},
}

var tesseractLangPackagePatterns = map[string]string{
	"Debian/Ubuntu": "sudo apt install tesseract-ocr-%s",
	"Arch":          "sudo pacman -S tesseract-data-%s",
	"Fedora":        "sudo dnf install tesseract-langpack-%s",
	"Alpine":        "sudo apk add tesseract-ocr-data-%s",
	"macOS":         "brew install tesseract (bundles common languages; see brew docs for extras)",
}

var InstallHintOrder = []string{"Debian/Ubuntu", "Arch", "Fedora", "Alpine", "macOS", "pip"}

func checkOcrmypdfCompanions() []CompanionTool {
	var result []CompanionTool
	for _, d := range ocrmypdfCompanionDefs {
		_, err := exec.LookPath(d.command)
		result = append(result, CompanionTool{
			Command:      d.command,
			Purpose:      d.purpose,
			Available:    err == nil,
			Required:     d.required,
			InstallHints: companionInstallHints[d.command],
		})
	}
	return result
}

// missingTesseractLangs tries to detect which of the configured languages
// are not installed in the system tesseract. When detection fails (tesseract
// not found, --list-langs not supported, or unrecognizable output), it returns
// the full list — the caller falls back to showing all hints.
func missingTesseractLangs(configured []string) []string {
	if len(configured) == 0 {
		return nil
	}
	out, err := exec.Command("tesseract", "--list-langs").Output()
	if err != nil {
		return configured
	}
	installed := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Output lines are either header lines or bare language codes.
		// Language codes are 2-5 letter codes like "eng", "spa", "jpn".
		if len(line) >= 2 && len(line) <= 6 && !strings.Contains(line, " ") && !strings.HasPrefix(line, "List") {
			installed[line] = true
		}
	}
	var missing []string
	for _, lang := range configured {
		if !installed[lang] {
			missing = append(missing, lang)
		}
	}

	if len(missing) == len(configured) {
		return configured
	}
	return missing
}

func tesseractLangInstallHints(langs []string) []LangHint {
	if len(langs) == 0 {
		return nil
	}
	var hints []LangHint
	for _, lang := range langs {
		perLang := make(map[string]string)
		for _, system := range InstallHintOrder {
			if pattern, ok := tesseractLangPackagePatterns[system]; ok {
				perLang[system] = fmt.Sprintf(pattern, lang)
			}
		}
		// Special-case macOS: tesseract bundles common languages
		perLang["macOS"] = "brew install tesseract (bundles common languages)"
		hints = append(hints, LangHint{
			Language:     lang,
			InstallHints: perLang,
		})
	}
	return hints
}

func MissingExternalTools(cfg *Config) []ExternalTool {
	var tools []ExternalTool

	_, curlErr := exec.LookPath("curl")
	tools = append(tools, ExternalTool{
		Engine:       "curl",
		Category:     "prerequisite",
		Command:      "curl",
		Available:    curlErr == nil,
		InstallHints: engineInstallHints["curl"],
		Purpose:      "required for all model/language downloads",
	})

	checkEngine := func(engine, category, command string) {
		_, err := exec.LookPath(command)
		t := ExternalTool{
			Engine:       engine,
			Category:     category,
			Command:      command,
			Available:    err == nil,
			InstallHints: engineInstallHints[engine],
		}
		tools = append(tools, t)
	}

	if cfg.Consumer.OCR.Engine == OCR.OcrMyPdf {
		checkEngine(OCR.OcrMyPdf, "ocr", OCR.OcrMyPdf)
	}
	if cfg.Consumer.PdfOptimizer.Engine == PdfOptimizer.GS {
		checkEngine(PdfOptimizer.GS, "pdfoptimizer", PdfOptimizer.GS)
	}
	if cfg.Consumer.TextExtractor.Engine == TextExtractor.PdfToText {
		checkEngine(TextExtractor.PdfToText, "textextractor", TextExtractor.PdfToText)
	}
	if cfg.Consumer.Thumbnail.Engine == Thumbnail.Imagemagick {
		checkEngine(Thumbnail.Imagemagick, "thumbnail", Thumbnail.Imagemagick)
	}

	if cfg.Consumer.Converter.Enabled {
		_, err := exec.LookPath(cfg.Consumer.Converter.Binary)
		tools = append(tools, ExternalTool{
			Engine:       "libreoffice",
			Category:     "converter",
			Command:      cfg.Consumer.Converter.Binary,
			Available:    err == nil,
			InstallHints: engineInstallHints["libreoffice"],
		})
	}

	if cfg.Consumer.PdfOptimizer.Fallback != "" &&
		cfg.Consumer.PdfOptimizer.Fallback != cfg.Consumer.PdfOptimizer.Engine {
		_, err := exec.LookPath(cfg.Consumer.PdfOptimizer.Fallback)
		tools = append(tools, ExternalTool{
			Engine:       cfg.Consumer.PdfOptimizer.Fallback,
			Category:     "pdfoptimizer",
			Command:      cfg.Consumer.PdfOptimizer.Fallback,
			Available:    err == nil,
			InstallHints: engineInstallHints[cfg.Consumer.PdfOptimizer.Fallback],
		})
	}

	for i := range tools {
		if tools[i].Engine == OCR.OcrMyPdf {
			tools[i].Languages = cfg.Consumer.OCR.Languages
			tools[i].LangHints = tesseractLangInstallHints(missingTesseractLangs(cfg.Consumer.OCR.Languages))
			tools[i].Companions = checkOcrmypdfCompanions()
			break
		}
	}

	return tools
}

func FilterToolErrors(all []ExternalTool) []ExternalTool {
	var result []ExternalTool
	for _, t := range all {
		if t.Engine == "curl" && !t.Available {
			result = append(result, t)
			continue
		}
		if t.Category == "prerequisite" {
			continue
		}
		if !t.Available {
			result = append(result, t)
			continue
		}
		if t.Engine == OCR.OcrMyPdf {
			for _, c := range t.Companions {
				if c.Required && !c.Available {
					result = append(result, t)
					break
				}
			}
		}
	}
	return result
}

func MissingExternalToolErrors(cfg *Config) []ExternalTool {
	return FilterToolErrors(MissingExternalTools(cfg))
}
