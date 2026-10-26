package msg

import (
	"context"
	"sync"
)

// Recorder is an in-memory Sender for tests. Set Err to make Send fail.
type Recorder struct {
	mu   sync.Mutex
	sent []Outgoing
	Err  error
}

func (r *Recorder) Send(_ context.Context, msgs ...Outgoing) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.sent = append(r.sent, msgs...)
	return nil
}

// Messages returns the bodies sent so far.
func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Message, len(r.sent))
	for i, o := range r.sent {
		out[i] = o.Message
	}
	return out
}

// Drain returns everything sent so far and clears the recorder.
func (r *Recorder) Drain() []Outgoing {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.sent
	r.sent = nil
	return out
}
