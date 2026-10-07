package port

import "testing"

func TestNotionErrorKindString(t *testing.T) {
	if got := NotionRejected.String(); got != "rejected" {
		t.Errorf("NotionRejected = %q", got)
	}
	for k, want := range map[NotionErrorKind]string{-1: "NotionErrorKind(-1)", 99: "NotionErrorKind(99)"} {
		if got := k.String(); got != want {
			t.Errorf("%d = %q, want %q", int(k), got, want)
		}
	}
}
