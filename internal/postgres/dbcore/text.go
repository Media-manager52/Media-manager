package dbcore

import "strings"

// TruncateText cuts text to at most limit characters on a character
// boundary and drops invalid UTF-8. PostgreSQL measures text length in
// characters and rejects invalid UTF-8, so a byte cut through æ, ø or å
// would make the write fail on every retry.
func TruncateText(text string, limit int) string {
	text = strings.ToValidUTF8(text, "")
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit])
	}
	return text
}
