package inspect

import (
	"strconv"
	"time"
)

// Millis is a measured duration that reads and writes JSON as milliseconds,
// a number with microsecond precision: local TTFBs are routinely under a
// millisecond, which whole milliseconds would show as 0.
type Millis time.Duration

func (m Millis) Std() time.Duration { return time.Duration(m) }

func (m Millis) MarshalJSON() ([]byte, error) {
	us := time.Duration(m).Round(time.Microsecond).Microseconds()
	return strconv.AppendFloat(nil, float64(us)/1000, 'f', -1, 64), nil
}

func (m *Millis) UnmarshalJSON(b []byte) error {
	ms, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}
	*m = Millis(time.Duration(ms*1000+0.5) * time.Microsecond)
	return nil
}
