package notify

import (
	"context"
	"errors"
	"log"
	"time"
)

// Worker is the daily engagement-push fan-out. It mirrors the gamification
// worker pattern (ticker + recover) and runs every 15 minutes so each user's
// 19:00–21:00 LOCAL window is caught within a quarter hour regardless of
// timezone spread.
type Worker struct {
	cfg    Config
	store  *Store
	sender *Sender // nil when push is disabled
}

// NewWorker builds the worker. With no APNs credentials the worker is
// constructed but inert (StartDailyWorker logs once and returns), so staging
// and dev run the same code path prod does, minus the sends.
func NewWorker(cfg Config, store *Store) (*Worker, error) {
	w := &Worker{cfg: cfg, store: store}
	if cfg.Enabled() {
		sender, err := NewSender(cfg)
		if err != nil {
			// A malformed .p8 is a MISCONFIGURATION, not a reason to take the
			// whole API down — log loudly and stay inert like the absent case.
			log.Printf("[notify] APNS_PRIVATE_KEY invalid (%v) — push DISABLED", err)
			return w, nil
		}
		w.sender = sender
	}
	return w, nil
}

// StartDailyWorker runs the fan-out every 15 minutes until ctx is cancelled.
func (w *Worker) StartDailyWorker(ctx context.Context) {
	if w.sender == nil {
		// One line, exactly once — the spec'd no-op for missing credentials.
		log.Println("[notify] APNS_KEY_ID/APNS_TEAM_ID/APNS_PRIVATE_KEY not all set — push worker disabled (device registration still active)")
		return
	}
	log.Printf("[notify] push worker started (topic %s, sandbox=%v)", w.cfg.BundleID, w.cfg.UseSandbox)

	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("[notify] push worker shutting down")
			return
		case t := <-ticker.C:
			runSafelyNotify("push fan-out", func() { w.runOnce(ctx, t) })
		}
	}
}

// runOnce evaluates every registered device and sends at most one push per
// device (the daily cap lives in decidePush + MarkNotified).
func (w *Worker) runOnce(ctx context.Context, now time.Time) {
	candidates, err := w.store.ListPushCandidates()
	if err != nil {
		log.Printf("[notify] fan-out: list candidates: %v", err)
		return
	}
	sent, pruned := 0, 0
	for _, c := range candidates {
		decision := decidePush(c, now)
		if !decision.send {
			continue
		}
		err := w.sender.Send(ctx, c.Token, Payload{
			Title:    decision.title,
			Body:     decision.body,
			ThreadID: decision.threadID,
		})
		if errors.Is(err, ErrTokenGone) {
			// 400/410 — the install is gone; prune so we never pay for it again.
			if derr := w.store.DeleteByToken(c.Token); derr != nil {
				log.Printf("[notify] fan-out: prune dead token: %v", derr)
			}
			pruned++
			continue
		}
		if err != nil {
			// Transient (network, 429, 5xx): leave last_notified_at alone so a
			// later tick retries within the evening window.
			log.Printf("[notify] fan-out: send to user %d failed (will retry next tick): %v", c.UserID, err)
			continue
		}
		if err := w.store.MarkNotified(c.Token, now); err != nil {
			log.Printf("[notify] fan-out: mark notified: %v", err)
		}
		sent++
		log.Printf("[notify] pushed user %d (%s)", c.UserID, decision.reason)
	}
	if sent > 0 || pruned > 0 {
		log.Printf("[notify] fan-out: sent=%d pruned=%d evaluated=%d", sent, pruned, len(candidates))
	}
}

// runSafelyNotify contains panics in the background loop (same rationale as
// gamification.runSafely — the server has no other supervisor).
func runSafelyNotify(name string, fn func()) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[notify] %s panicked (recovered): %v", name, rec)
		}
	}()
	fn()
}
