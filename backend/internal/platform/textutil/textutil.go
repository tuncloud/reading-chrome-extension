// Package textutil holds small string helpers.
package textutil

// Truncate cuts s to at most max runes without splitting a character.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}
