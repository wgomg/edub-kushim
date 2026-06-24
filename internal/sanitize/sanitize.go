package sanitize

import "strings"

func StripTags(input string) string {
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

func StripTagsPtr(input *string) *string {
	if input == nil {
		return nil
	}
	s := StripTags(*input)
	return &s
}
