package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"strconv"
	"strings"
)

// assetEntry is one file in assets-manifest.json, which web/plugins/
// precompress.ts writes: the ETag of the file's bytes, and of each
// precompressed variant ("gzip" in <file>.gz, "zstd" in <file>.zst).
type assetEntry struct {
	ETag     string                  `json:"etag"`
	Variants map[string]assetVariant `json:"variants"`
}

type assetVariant struct {
	ETag string `json:"etag"`
}

var suffixes = map[string]string{"gzip": ".gz", "zstd": ".zst"}

// loadManifest reads assets-manifest.json. Without one (no build, or a
// damaged one), files are served as they are, without ETags.
func loadManifest(dist fs.FS) map[string]assetEntry {
	b, err := fs.ReadFile(dist, "assets-manifest.json")
	if err != nil {
		return nil
	}
	var m map[string]assetEntry
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// etagOf is the plugin's ETag: the first 16 bytes of the SHA-256, in hex.
func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// negotiate picks the content coding for an Accept-Encoding header (RFC
// 9110 §12.5.3): "zstd", "gzip", or "" for identity. A coding's weight is
// its q-value, else the "*" entry's, else 0; identity's is its own, else
// the "*" entry's, else 0, so a listed coding beats an unlisted identity.
// At equal weight zstd beats gzip, and both beat identity.
func negotiate(header string) string {
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
