package consume

import (
	"fmt"
	"strings"

	"github.com/wgomg/edub-kushim/internal/config"
)

func printToolBlock(missing []config.ExternalTool) {
	fmt.Println("Cannot consume — the following required tools are not installed:")
	for _, t := range missing {
		if t.Engine == "curl" {
			fmt.Printf("  Prerequisite \"curl\" — %s\n", t.Purpose)
		} else if len(t.Companions) > 0 {
			fmt.Printf("  OCR engine \"%s\" (binary not found in PATH)\n", t.Engine)
		} else {
			fmt.Printf("  %s engine \"%s\" (binary not found in PATH)\n", t.Category, t.Engine)
		}
		for _, system := range config.InstallHintOrder {
			if cmd, ok := t.InstallHints[system]; ok {
				fmt.Printf("    %-16s %s\n", system+":", cmd)
			}
		}
		fmt.Println()
		for _, c := range t.Companions {
			if c.Required && !c.Available {
				fmt.Printf("  Companion \"%s\" — %s\n", c.Command, c.Purpose)
				for _, system := range config.InstallHintOrder {
					if cmd, ok := c.InstallHints[system]; ok {
						fmt.Printf("    %-16s %s\n", system+":", cmd)
					}
				}
				fmt.Println()
			}
		}
	}
	fmt.Println("Install the missing tools, or switch to a built-in engine via `kushim setup` or the Settings page.")
}

func printOcrmypdfAdvisory(cfg *config.Config, allTools []config.ExternalTool) {
	if cfg.Consumer.OCR.Engine != config.OCR.OcrMyPdf {
		return
	}
	if len(cfg.Consumer.OCR.Languages) == 0 {
		return
	}

	var ocrTool *config.ExternalTool
	for i, t := range allTools {
		if t.Engine == config.OCR.OcrMyPdf {
			ocrTool = &allTools[i]
			break
		}
	}
	if ocrTool == nil {
		return
	}

	fmt.Printf("\nNote: ocrmypdf uses the system tesseract, which reads its own tessdata directory.\n")
	fmt.Printf("Make sure the tesseract language packs for your configured languages are installed:\n\n")
	fmt.Printf("  Configured languages: %s\n", strings.Join(cfg.Consumer.OCR.Languages, ", "))
	if len(ocrTool.LangHints) > 0 {
		combined := make(map[string][]string)
		for _, lh := range ocrTool.LangHints {
			for _, system := range config.InstallHintOrder {
				if cmd, ok := lh.InstallHints[system]; ok {
					combined[system] = append(combined[system], cmd)
				}
			}
		}
		for _, system := range config.InstallHintOrder {
			cmds, ok := combined[system]
			if !ok {
				continue
			}
			seen := map[string]bool{}
			var unique []string
			for _, c := range cmds {
				if !seen[c] {
					seen[c] = true
					unique = append(unique, c)
				}
			}
			fmt.Printf("    %-16s %s\n", system+":", strings.Join(unique, "  "))
		}
	}
	fmt.Println()

	for _, c := range ocrTool.Companions {
		if !c.Required && !c.Available {
			fmt.Printf("Optional companion \"%s\" — %s\n", c.Command, c.Purpose)
			fmt.Printf("  ocrmypdf will skip this feature. Install it for best results:\n")
			for _, system := range config.InstallHintOrder {
				if cmd, ok := c.InstallHints[system]; ok {
					fmt.Printf("    %-16s %s\n", system+":", cmd)
				}
			}
			fmt.Println()
		}
	}
}
