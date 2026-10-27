package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
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
