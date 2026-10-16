package sbom

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRawIsCycloneDX(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(Raw(), &doc); err != nil {
		t.Fatalf("the embedded SBOM is not JSON: %v", err)
	}
	if doc["bomFormat"] != "CycloneDX" {
		t.Errorf("bomFormat = %v, want CycloneDX", doc["bomFormat"])
	}
}

// served is the document GET /licenses serves, the only one with text IDs.
func served(t *testing.T) Document {
	t.Helper()
	var doc Document
	if err := json.Unmarshal(ProjectedJSON(), &doc); err != nil {
		t.Fatalf("ProjectedJSON: %v", err)
	}
	if len(doc.Components) == 0 {
		t.Fatal("the licenses document has no components; run 'make sbom'")
	}
	return doc
}

// A half-regenerated pair of artifacts would reference texts the pool no
// longer holds, and the license dialog would be empty.
func TestEveryComponentHasItsTexts(t *testing.T) {
	var missing []string
	for _, c := range served(t).Components {
		if c.Ecosystem == "other" {
			continue
		}
		if _, ok := Text(c.TextID); !ok {
			missing = append(missing, c.Name+" (license "+c.TextID+")")
		}
		if _, ok := Text(c.NoticeID); c.NoticeID != "" && !ok {
			missing = append(missing, c.Name+" (notice "+c.NoticeID+")")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d components without their texts; run 'make sbom':\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// Apache-2.0 §4(d) makes passing on a NOTICE mandatory; prometheus's
// client ships one.
func TestNoticesAreHarvested(t *testing.T) {
	for _, c := range served(t).Components {
		if c.Name == "github.com/prometheus/client_golang" {
			if c.NoticeID == "" {
				t.Fatal("github.com/prometheus/client_golang ships a NOTICE but has no noticeId")
			}
			return
		}
	}
	t.Skip("github.com/prometheus/client_golang is no longer a dependency")
}

func TestTextRejectsUnknownIDs(t *testing.T) {
	if _, ok := Text("00000000000000000000000000000000"); ok {
		t.Error("an unknown ID resolved to a text")
	}
}

func TestNoticesCoverEveryComponent(t *testing.T) {
	notices := string(Notices())
	for _, c := range served(t).Components {
		if !strings.Contains(notices, c.Name+" "+c.Version) {
			t.Errorf("%s %s is not in the notices", c.Name, c.Version)
		}
	}
}

func TestNoticesStartWithTsrelaysNotice(t *testing.T) {
	notice, err := os.ReadFile("../../NOTICE")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(Notices()), strings.TrimRight(string(notice), "\n")) {
		t.Error("the notices do not start with NOTICE; run 'make sbom'")
	}
}

// Reads one entry straight from the pool, so a broken lookup cannot supply
// its own fixture.
func TestAttachTextsSetsIDsFromThePool(t *testing.T) {
	key, want := "", ComponentTexts{}
	for k, entry := range texts.Components {
		if key == "" || entry.Notice != "" {
			key, want = k, entry
		}
		if entry.Notice != "" {
			break
		}
	}
	if key == "" {
		t.Fatal("the text pool is empty; run 'make sbom'")
	}
	ecosystem, rest, _ := strings.Cut(key, ":")
	at := strings.LastIndexByte(rest, '@')
	components := []Component{{Name: rest[:at], Version: rest[at+1:], Ecosystem: ecosystem}}
	attachTexts(components)
	if components[0].TextID != want.License || components[0].NoticeID != want.Notice {
		t.Errorf("%s: got %q/%q, want %q/%q", key, components[0].TextID, components[0].NoticeID, want.License, want.Notice)
	}
}
