package hooks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestDurationJSON(t *testing.T) {
	b, _ := json.Marshal(Duration(90 * time.Second))
	if string(b) != `"1m30s"` {
		t.Fatalf("marshal = %s", b)
	}
	var d Duration
	if err := json.Unmarshal([]byte(`"250ms"`), &d); err != nil || d.Std() != 250*time.Millisecond {
		t.Fatalf("unmarshal = %v, %v", d, err)
	}
	for _, bad := range []string{`"soon"`, `5`, `"-1s"`} {
		if err := json.Unmarshal([]byte(bad), &d); err == nil {
			t.Errorf("unmarshal %s should fail", bad)
		}
	}
	if err := json.Unmarshal([]byte(`"0s"`), &d); err != nil || d != 0 {
		t.Fatalf("zero must be allowed (it disables holding): %v %v", d, err)
	}
}

func TestDefaultPolicyMatchesDesign(t *testing.T) {
	e := DefaultPolicy.Defaults
	want := Effective{
		AttachPolicy: AttachTakeover, HostHeader: HostPreserve,
		HoldTimeout: Duration(30 * time.Second), ResponseTimeout: Duration(2 * time.Minute),
		IdleTimeout: Duration(60 * time.Second), MaxBodyBytes: 0,
		RateLimit: RateLimit{PerSecond: 100, Burst: 200},
	}
	if e != want {
		t.Fatalf("defaults = %+v, want %+v (DESIGN.md §8)", e, want)
	}
	if err := DefaultPolicy.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestResolve(t *testing.T) {
	got := DefaultPolicy.Resolve(Settings{
		HostHeader:  ptr(HostRewrite),
		HoldTimeout: ptr(Duration(0)),
		RateLimit:   &RateLimit{PerSecond: 5, Burst: 10},
	})
	if got.HostHeader != HostRewrite || got.HoldTimeout != 0 || got.RateLimit.PerSecond != 5 {
		t.Fatalf("overrides not applied: %+v", got)
	}
	if got.ResponseTimeout != DefaultPolicy.Defaults.ResponseTimeout || got.AttachPolicy != AttachTakeover {
		t.Fatalf("defaults not applied: %+v", got)
	}
}

// A hook stored under a higher maximum is clamped to the current one.
func TestResolveClampsToMaxima(t *testing.T) {
	p := DefaultPolicy
	p.Max.MaxBodyBytes = 1 << 20
	p.Max.RateLimit = RateLimit{PerSecond: 10, Burst: 20}
	got := p.Resolve(Settings{
		HoldTimeout:     ptr(Duration(time.Hour)),
		ResponseTimeout: ptr(Duration(time.Hour)),
		IdleTimeout:     ptr(Duration(time.Hour)),
		MaxBodyBytes:    ptr(int64(0)), // unlimited
		RateLimit:       &RateLimit{PerSecond: 50, Burst: 5},
	})
	want := Effective{
		AttachPolicy:    AttachTakeover,
		HostHeader:      HostPreserve,
		HoldTimeout:     p.Max.HoldTimeout,
		ResponseTimeout: p.Max.ResponseTimeout,
		IdleTimeout:     p.Max.IdleTimeout,
		MaxBodyBytes:    1 << 20,
		RateLimit:       RateLimit{PerSecond: 10, Burst: 5},
	}
	if got != want {
		t.Fatalf("Resolve = %+v, want %+v", got, want)
	}
	if got := p.Resolve(Settings{MaxBodyBytes: ptr(int64(5))}); got.MaxBodyBytes != 5 {
		t.Fatalf("maxBodyBytes under the maximum = %d, want 5", got.MaxBodyBytes)
	}
	if got := DefaultPolicy.Resolve(Settings{MaxBodyBytes: ptr(int64(0))}); got.MaxBodyBytes != 0 {
		t.Fatalf("maxBodyBytes without a maximum = %d, want unlimited", got.MaxBodyBytes)
	}
}

func TestValidate(t *testing.T) {
	p := DefaultPolicy
	p.Max.MaxBodyBytes = 1 << 20
	cases := map[string]Settings{
		"settings.attachPolicy":    {AttachPolicy: ptr(AttachPolicy("steal"))},
		"settings.hostHeader":      {HostHeader: ptr(HostHeader("keep"))},
		"settings.holdTimeout":     {HoldTimeout: ptr(Duration(time.Hour))},
		"settings.responseTimeout": {ResponseTimeout: ptr(Duration(0))},
		"settings.idleTimeout":     {IdleTimeout: ptr(Duration(0))},
		"settings.maxBodyBytes":    {MaxBodyBytes: ptr(int64(2 << 20))},
		"settings.rateLimit":       {RateLimit: &RateLimit{PerSecond: 0, Burst: 1}},
	}
	for field, s := range cases {
		errs := p.Validate(s)
		if len(errs) != 1 || errs[0].Field != field {
			t.Errorf("%s: errs = %v", field, errs)
		}
	}
	// "unlimited" is not allowed once the operator set a maximum
	if errs := p.Validate(Settings{MaxBodyBytes: ptr(int64(0))}); len(errs) != 1 {
		t.Errorf("unlimited body under a maximum: %v", errs)
	}
	if errs := p.Validate(Settings{HoldTimeout: ptr(Duration(0)), MaxBodyBytes: ptr(int64(1024))}); len(errs) != 0 {
		t.Errorf("valid settings rejected: %v", errs)
	}
	if !strings.Contains(p.Validate(cases["settings.holdTimeout"])[0].Message, "5m0s") {
		t.Error("the message should name the maximum")
	}
}

func TestPolicyCheck(t *testing.T) {
	p := DefaultPolicy
	p.Max.ResponseTimeout = Duration(time.Minute) // below the 2m default
	if err := p.Check(); err == nil || !strings.Contains(err.Error(), "response") {
		t.Fatalf("Check = %v", err)
	}
}
