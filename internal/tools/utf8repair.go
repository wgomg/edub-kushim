package tools

import (
	"unicode/utf16"
	"unicode/utf8"
)

// repairUTF8 returns s with invalid UTF-8 repaired: a CESU-8/WTF-8 surrogate pair, a high surrogate 3-byte
// sequence immediately followed by a low one, is recombined into the proper 4-byte supplementary character,
// and every remaining invalid sequence is replaced with one U+FFFD — a lone surrogate consumes its 3 bytes,
// any other malformed byte advances one. Valid input is returned unchanged. It reports how many pairs were
// recombined and how many sequences were replaced.
func repairUTF8(s string) (string, int, int) {
	if utf8.ValidString(s) {
		return s, 0, 0
	}

	pairs, replaced := 0, 0
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		j := i
		for j < len(s) && s[j] < 0x80 {
			j++
		}
		buf = append(buf, s[i:j]...)
		i = j
		if i == len(s) {
			break
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size != 1 {
			buf = append(buf, s[i:i+size]...)
			i += size
			continue
		}

		hi, ok := surrogateAt(s, i)
		if ok && hi >= 0xd800 && hi <= 0xdbff {
			if lo, pairOk := surrogateAt(s, i+3); pairOk && lo >= 0xdc00 && lo <= 0xdfff {
				buf = utf8.AppendRune(buf, utf16.DecodeRune(hi, lo))
				i += 6
				pairs++
				continue
			}
		}

		if ok {
			buf = utf8.AppendRune(buf, utf8.RuneError)
			i += 3
			replaced++
			continue
		}

		buf = utf8.AppendRune(buf, utf8.RuneError)
		i++
		replaced++
	}

	return string(buf), pairs, replaced
}

// surrogateAt reports the UTF-16 code unit encoded at s[i] when it is a 3-byte ED-led sequence with two
// continuation bytes. A well-formed ED 80..9F xx sequence is valid UTF-8 and never reaches the invalid
// branch, so a confirmed shape here always names a surrogate code unit.
func surrogateAt(s string, i int) (rune, bool) {
	if i+3 > len(s) || s[i] != 0xed {
		return 0, false
	}
	b1, b2 := s[i+1], s[i+2]
	if b1 < 0x80 || b1 > 0xbf || b2 < 0x80 || b2 > 0xbf {
		return 0, false
	}
	return rune(0xd000 | (rune(b1&0x3f) << 6) | rune(b2&0x3f)), true
}
