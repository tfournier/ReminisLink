package auth

import (
	"strconv"
	"unicode/utf8"
)

// Bounds of the values that come from the network and reach the log, so a
// client cannot fill the SD card with its requests (docs/specs/07-securite.md
// §3).
const (
	// MaxLoggedPath is the longest request path written to the log.
	MaxLoggedPath = 512
	// MaxLoggedMethod is the longest request method written to the log.
	MaxLoggedMethod = 16
)

// LogString returns s cut to at most max bytes (on a rune boundary), with
// a marker giving the number of bytes left out.
func LogString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...[" + strconv.Itoa(len(s)-cut) + " bytes truncated]"
}

// LogClient returns the X-RL-Client value to log: the id when it is a
// valid client id (a UUID), "" otherwise. A raw header is never logged.
func LogClient(id string) string {
	if ValidID(id) {
		return id
	}
	return ""
}
