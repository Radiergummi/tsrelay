package negotiate

import (
	"strconv"
	"strings"
)

// Encoding picks the content coding for an Accept-Encoding header (RFC
// 9110 §12.5.3): "zstd", "gzip", or "" for identity. A coding's weight is
// its q-value, else the "*" entry's, else 0; identity's is its own, else
// the "*" entry's, else 0, so a listed coding beats an unlisted identity.
// At equal weight zstd beats gzip, and both beat identity.
func Encoding(header string) string {
	weights := map[string]float64{}
	for part := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		q := 1.0
		if k, v, ok := strings.Cut(strings.TrimSpace(params), "="); ok && strings.TrimSpace(k) == "q" {
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				f = 0
			}
			q = f
		}
		weights[name] = q
	}
	weight := func(coding string) float64 {
		if q, ok := weights[coding]; ok {
			return q
		}
		return weights["*"] // 0 when absent
	}
	best, bestQ := "", weight("identity")
	for _, coding := range []string{"gzip", "zstd"} {
		if q := weight(coding); q > 0 && q >= bestQ {
			best, bestQ = coding, q
		}
	}
	return best
}
