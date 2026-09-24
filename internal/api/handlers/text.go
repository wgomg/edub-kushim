package handlers

import "strings"

func stripTags(input string) string {
	var result []byte
	for i := 0; i < len(input); i++ {
		if input[i] == '<' {
			j := strings.IndexByte(input[i+1:], '>')
			if j >= 0 {
				i += j + 1
				continue
			}
		}
		result = append(result, input[i])
	}
	return string(result)
}

func stripTagsPtr(input *string) *string {
	if input == nil {
		return nil
	}
	s := stripTags(*input)
	return &s
}
