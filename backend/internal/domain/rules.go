package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"time"
)

// URLHash is hex(SHA-256(canonical_url))[:32] (spec §2.8 step 1).
func URLHash(canonicalURL string) string {
	sum := sha256.Sum256([]byte(canonicalURL))
	return hex.EncodeToString(sum[:])[:32]
}

// Timestamp formats t as RFC 3339 UTC with second precision.
func Timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// SameID reports whether two external ids are equal, ignoring dashes and case
// (Notion accepts and returns both forms).
func SameID(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "-", "")) }
	return norm(a) == norm(b)
}

// RoundMinutes converts active seconds to whole minutes, half away from zero (§3.3).
func RoundMinutes(secs int64) int64 { return int64(math.Round(float64(secs) / 60)) }

// SyncPolicy holds the 🔶 thresholds the Notion sync applies (spec §2.13, §7).
type SyncPolicy struct {
	MinCreateSeconds  int
	CompleteProgress  float64
	CompleteTimeRatio float64
	WordsPerMinute    int
}

// IsCompleted implements spec §2.13 step 2; once true it stays true.
func (p SyncPolicy) IsCompleted(a Article) bool {
	if a.Completed {
		return true
	}
	if a.MaxProgress < p.CompleteProgress {
		return false
	}
	if a.WordCount == 0 {
		return true
	}
	est := float64(a.WordCount) / float64(p.WordsPerMinute) * 60
	return float64(a.TotalSeconds) >= p.CompleteTimeRatio*est
}
