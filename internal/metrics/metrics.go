// Package metrics keeps the relay's counters and gauges and writes them in
// the Prometheus text exposition format, without a client library.
package metrics

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Registry holds metric families and writes them in registration order.
type Registry struct {
	mu       sync.Mutex
	families []*family
}

type family struct {
	name, help, kind, label string
	counts                  map[string]float64        // counters, by label value
	read                    func() map[string]float64 // gauges, by label value
}

// Counter is a family of monotonically increasing values, one per label value.
type Counter struct {
	r *Registry
	f *family
}

func NewRegistry() *Registry { return &Registry{} }

// Counter declares a counter family with one label, or none if label is "".
func (r *Registry) Counter(name, help, label string) *Counter {
	f := &family{name: name, help: help, kind: "counter", label: label, counts: map[string]float64{}}
	r.add(f)
	return &Counter{r: r, f: f}
}

// GaugeFunc declares a gauge family whose values read returns at scrape
// time, keyed by label value (the key is "" when label is "").
func (r *Registry) GaugeFunc(name, help, label string, read func() map[string]float64) {
	r.add(&family{name: name, help: help, kind: "gauge", label: label, read: read})
}

func (r *Registry) add(f *family) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.families = append(r.families, f)
}

// Add increases the counter for labelValue by v, which must not be negative.
func (c *Counter) Add(labelValue string, v float64) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	c.f.counts[labelValue] += v
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

// WriteTo writes every family in the text exposition format, version 0.0.4.
func (r *Registry) WriteTo(w io.Writer) (int64, error) {
	type snapshot struct {
		f      *family
		values map[string]float64
	}
	r.mu.Lock()
	snaps := make([]snapshot, len(r.families))
	for i, f := range r.families {
		snaps[i] = snapshot{f: f, values: maps.Clone(f.counts)}
	}
	r.mu.Unlock()

	var b strings.Builder
	for _, s := range snaps {
		values := s.values
		if s.f.read != nil {
			values = s.f.read() // outside the lock: gauges read other components
		}
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", s.f.name, helpEscaper.Replace(s.f.help), s.f.name, s.f.kind)
		for _, lv := range slices.Sorted(maps.Keys(values)) {
			b.WriteString(s.f.name)
			if s.f.label != "" {
				fmt.Fprintf(&b, `{%s="%s"}`, s.f.label, labelEscaper.Replace(lv))
			}
			b.WriteString(" " + strconv.FormatFloat(values[lv], 'g', -1, 64) + "\n")
		}
	}
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

// ServeHTTP serves the metrics to a Prometheus scraper.
func (r *Registry) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = r.WriteTo(w)
}
