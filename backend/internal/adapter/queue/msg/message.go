// Package msg is the transport-free half of the queue adapter: notion-sync.fifo
// messages (spec §2.12), their dedup ids, the SyncQueue port and a test Recorder.
package msg

import (
	"context"
	"strconv"
	"time"
)

type Type string

const (
	TypeArticle Type = "article"
	TypeDay     Type = "day"
	TypePurge   Type = "purge"
)

// Message is the JSON body of a queue message.
type Message struct {
	Type           Type   `json:"type"`
	InstallationID string `json:"installation_id"`
	URLHash        string `json:"url_hash,omitempty"`
	Date           string `json:"date,omitempty"`
	UserID         string `json:"user_id,omitempty"`
}

func Article(inst, urlHash string) Message {
	return Message{Type: TypeArticle, InstallationID: inst, URLHash: urlHash}
}
func Day(inst, date string) Message { return Message{Type: TypeDay, InstallationID: inst, Date: date} }
func Purge(userID, inst string) Message {
	return Message{Type: TypePurge, InstallationID: inst, UserID: userID}
}

// GroupID serialises all work for one installation (C4).
func (m Message) GroupID() string {
	if m.InstallationID != "" {
		return m.InstallationID
	}
	return "user#" + m.UserID
}

func (m Message) key() string {
	switch m.Type {
	case TypeArticle:
		return "article#" + m.InstallationID + "#" + m.URLHash
	case TypeDay:
		return "day#" + m.InstallationID + "#" + m.Date
	default:
		return "purge#" + m.UserID
	}
}

// DedupID collapses article/day messages per debounce bucket B; purge per second.
func (m Message) DedupID(now time.Time, bucket time.Duration) string {
	if m.Type == TypePurge {
		return m.key() + "#" + strconv.FormatInt(now.Unix(), 10)
	}
	b := max(int64(bucket/time.Second), 1)
	return m.key() + "#" + strconv.FormatInt(now.Unix()/b, 10)
}

// RequeueDedupID never collides with DedupID, so a worker re-enqueue inside the
// same bucket isn't swallowed by SQS's 5-minute dedup window.
func (m Message) RequeueDedupID(now time.Time) string {
	return m.key() + "#r" + strconv.FormatInt(now.UnixNano(), 10)
}

// Outgoing is a message plus its deduplication id.
type Outgoing struct {
	Message Message
	DedupID string
}

// Bucketed pairs each message with its bucket dedup id.
func Bucketed(now time.Time, bucket time.Duration, msgs ...Message) []Outgoing {
	out := make([]Outgoing, len(msgs))
	for i, m := range msgs {
		out[i] = Outgoing{Message: m, DedupID: m.DedupID(now, bucket)}
	}
	return out
}

// Sender enqueues messages.
type Sender interface {
	Send(ctx context.Context, msgs ...Outgoing) error
}
