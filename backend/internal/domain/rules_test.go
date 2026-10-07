package domain

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIsCompleted(t *testing.T) {
	p := SyncPolicy{CompleteProgress: 0.9, CompleteTimeRatio: 0.3, WordsPerMinute: 230}
	cases := []struct {
		a    Article
		want bool
	}{
		{Article{MaxProgress: 0.95, WordCount: 1000, TotalSeconds: 100}, true}, // 100 ≥ 0.3 × 260.9
		{Article{MaxProgress: 0.95, WordCount: 1000, TotalSeconds: 50}, false}, // skimmed
		{Article{MaxProgress: 0.95, WordCount: 0, TotalSeconds: 5}, true},      // unknown length
		{Article{MaxProgress: 0.5, WordCount: 0, TotalSeconds: 999}, false},
		{Article{MaxProgress: 0.1, Completed: true}, true}, // sticky
	}
	for i, c := range cases {
		if got := p.IsCompleted(c.a); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}

func TestURLHash(t *testing.T) {
	h := URLHash("https://medium.com/p/8f3c2a1b9d4e")
	if len(h) != 32 || h != URLHash("https://medium.com/p/8f3c2a1b9d4e") || h == URLHash("https://medium.com/p/other") {
		t.Fatalf("hash %q", h)
	}
}

func TestTimestampIsUTCSeconds(t *testing.T) {
	ts := Timestamp(time.Date(2026, 10, 6, 10, 1, 5, 999, time.FixedZone("ICT", 7*3600)))
	if ts != "2026-10-06T03:01:05Z" {
		t.Fatalf("got %s", ts)
	}
}

func TestSameIDIgnoresDashesAndCase(t *testing.T) {
	if !SameID("1F2E-ab", "1f2eAB") || SameID("1f2e", "1f2f") {
		t.Fatal("SameID")
	}
}

func TestRoundMinutes(t *testing.T) {
	for secs, want := range map[int64]int64{0: 0, 29: 0, 30: 1, 89: 1, 90: 2, 150: 3} {
		if got := RoundMinutes(secs); got != want {
			t.Errorf("RoundMinutes(%d) = %d, want %d", secs, got, want)
		}
	}
}

func TestSetupReason(t *testing.T) {
	if r := SetupReason(fmt.Errorf("wrap: %w", &SetupError{Reason: "property_missing:Progress"})); r != "property_missing:Progress" {
		t.Fatalf("got %q", r)
	}
	if r := SetupReason(errors.New("boom")); r != "notion_error" {
		t.Fatalf("got %q", r)
	}
}

func TestInstallationDatabases(t *testing.T) {
	var i Installation
	if i.HasDatabases() {
		t.Fatal("empty installation has no databases")
	}
	i.ApplyDatabases(DBConfig{LibraryDSID: "l", DailyDSID: "d", Props: map[string]string{"title": "t"}})
	if !i.HasDatabases() {
		t.Fatal("databases not applied")
	}
}
