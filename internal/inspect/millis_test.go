package inspect

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMillisJSON(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		json string
	}{
		{412 * time.Microsecond, "0.412"},
		{2 * time.Second, "2000"},
		{0, "0"},
		{42 * time.Millisecond, "42"},
		{1500 * time.Microsecond, "1.5"},
		{412*time.Microsecond + 400, "0.412"}, // rounded to microseconds
	} {
		b, err := json.Marshal(Millis(tc.d))
		if err != nil || string(b) != tc.json {
			t.Errorf("Marshal(%v) = %s, %v; want %s", tc.d, b, err, tc.json)
		}
		var back Millis
		if err := json.Unmarshal(b, &back); err != nil {
			t.Errorf("Unmarshal(%s): %v", b, err)
		} else if want := tc.d.Round(time.Microsecond); back.Std() != want {
			t.Errorf("Unmarshal(%s) = %v, want %v", b, back.Std(), want)
		}
	}
	var m Millis
	if err := json.Unmarshal([]byte(`"2s"`), &m); err == nil {
		t.Error(`a Go duration string was accepted`)
	}
}
