package enrichment

import "strings"

func truncate(s string, maxLength int) string {
	defaultString := "Unknown"

	if strings.ReplaceAll(s, " ", "") == "" {
		return defaultString
	}

	runes := []rune(s)
	if len(runes) <= maxLength {
		return s
	}

	trunc := string(runes[:maxLength])
	trunc = strings.TrimRight(trunc, " \t\n\r")
	return trunc
}
