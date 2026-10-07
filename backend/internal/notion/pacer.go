package notion

import (
	"context"
	"sync"
	"time"
)

// MinInterval spaces calls at least Interval apart (C22: ≥ 350 ms between Notion calls).
type MinInterval struct {
	Interval time.Duration
	mu       sync.Mutex
	last     time.Time
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

func NewMinInterval(d time.Duration) *MinInterval {
	return &MinInterval{Interval: d, now: time.Now, sleep: sleepCtx}
}

func (p *MinInterval) Wait(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.last.IsZero() {
		if d := p.Interval - p.now().Sub(p.last); d > 0 {
			if err := p.sleep(ctx, d); err != nil {
				return err
			}
		}
	}
	p.last = p.now()
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
