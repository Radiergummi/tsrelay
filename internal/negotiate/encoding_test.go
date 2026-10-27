package negotiate

import "testing"

func TestEncoding(t *testing.T) {
	for accept, want := range map[string]string{
		"":                          "",
		"identity":                  "",
		"gzip":                      "gzip",
		"zstd":                      "zstd",
		"gzip, zstd":                "zstd",
		"zstd;q=0.5, gzip":          "gzip",
		"zstd;q=0, gzip;q=0":        "",
		"*":                         "zstd",
		"*;q=0":                     "",
		"br, deflate":               "",
		"GZIP":                      "gzip",
		"gzip;q=0.5, identity;q=1":  "",
		"gzip;q=1, identity;q=0.5":  "gzip",
		"gzip;q=nonsense, zstd;q=0": "",
	} {
		if got := Encoding(accept); got != want {
			t.Errorf("Encoding(%q) = %q, want %q", accept, got, want)
		}
	}
}
