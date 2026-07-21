package questions

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/lsat-prep/backend/internal/models"
)

// TestFreeQuotaRemaining covers the context read: absent → unlimited (-1).
func TestFreeQuotaRemaining(t *testing.T) {
	// Absent value → unlimited.
	rAbsent := httptest.NewRequest("POST", "/x", nil)
	if got := freeQuotaRemaining(rAbsent); got != -1 {
		t.Errorf("absent remaining = %d, want -1 (unlimited)", got)
	}
	// Present value is returned verbatim.
	for _, want := range []int{-1, 0, 1, 3} {
		r := httptest.NewRequest("POST", "/x", nil)
		r = r.WithContext(context.WithValue(r.Context(), models.FreeQuotaRemainingKey, want))
		if got := freeQuotaRemaining(r); got != want {
			t.Errorf("remaining = %d, want %d", got, want)
		}
	}
}

// TestClampToFreeQuota is the money-critical guard: a free user must never be
// served more than their remaining allowance, while an entitled/unlimited caller
// (remaining < 0) is never reduced.
func TestClampToFreeQuota(t *testing.T) {
	cases := []struct {
		name      string
		remaining int
		setCtx    bool
		requested int
		want      int
	}{
		{"unlimited passes large through", -1, true, 50, 50},
		{"unlimited passes zero through", -1, true, 0, 0},
		{"no ctx treated as unlimited", 0, false, 6, 6},
		{"free caps request above allowance", 3, true, 6, 3},
		{"free keeps request below allowance", 3, true, 2, 2},
		{"free equal to allowance", 3, true, 3, 3},
		{"free zero request → full allowance", 3, true, 0, 3},
		{"free one remaining caps hard", 1, true, 50, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/x", nil)
			if c.setCtx {
				r = r.WithContext(context.WithValue(r.Context(), models.FreeQuotaRemainingKey, c.remaining))
			}
			if got := clampToFreeQuota(r, c.requested); got != c.want {
				t.Errorf("clampToFreeQuota(remaining=%d,set=%v, requested=%d) = %d, want %d",
					c.remaining, c.setCtx, c.requested, got, c.want)
			}
		})
	}
}
