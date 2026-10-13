package ui

import (
	"testing"

	"github.com/Radiergummi/tsrelay/internal/api"
	"github.com/Radiergummi/tsrelay/internal/hooks"
)

// A large rate limit must not switch to exponent notation: the script's
// ratelimit parser only accepts plain decimals.
func TestFormValuesRateLimit(t *testing.T) {
	for _, tc := range []struct {
		rate hooks.RateLimit
		want string
	}{
		{hooks.RateLimit{PerSecond: 1e6, Burst: 200}, "1000000/200"},
		{hooks.RateLimit{PerSecond: 2.5, Burst: 10}, "2.5/10"},
	} {
		h := &api.Hook{Settings: hooks.Settings{RateLimit: &tc.rate}}
		if got := formValues(h)["settings.rateLimit"]; got != tc.want {
			t.Errorf("formValues(%+v) = %q, want %q", tc.rate, got, tc.want)
		}
	}
}
