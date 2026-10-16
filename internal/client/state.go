package client

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Radiergummi/tsrelay/internal/atomicfile"
)

// State remembers which hook the CLI used per relay, working directory and
// target, so URLs pasted into .env files survive restarts (DESIGN.md §15).
type State struct {
	Paths  map[string]string `json:"paths"`
	URLs   map[string]string `json:"urls,omitempty"`
	file   string
	legacy string // the file this state was read from instead; removed on the next write
}

// DefaultStatePath is $XDG_STATE_HOME/tsrelay/hooks.json, or
// ~/.local/state/tsrelay/hooks.json if XDG_STATE_HOME is not set.
func DefaultStatePath() (string, error) {
	if d := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "tsrelay", "hooks.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "tsrelay", "hooks.json"), nil
}

// DefaultRunDir is where background tunnels keep their records and logs:
// run/ next to DefaultStatePath.
func DefaultRunDir() (string, error) {
	p, err := DefaultStatePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), "run"), nil
}

// LegacyStatePath is where tsrelay kept its state before DefaultStatePath:
// <user config dir>/tsrelay/state.json.
func LegacyStatePath() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tsrelay", "state.json"), nil
}

// LoadState reads the state at path. If path does not exist yet and legacy
// is not empty, it reads legacy instead; the next write then moves the
// state to path.
func LoadState(path, legacy string) (*State, error) {
	s := &State{file: path}
	err := s.read(path)
	if errors.Is(err, fs.ErrNotExist) && legacy != "" {
		if err = s.read(legacy); err == nil {
			s.legacy = legacy
		}
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if s.Paths == nil {
		s.Paths = map[string]string{}
	}
	if s.URLs == nil {
		s.URLs = map[string]string{}
	}
	return s, nil
}

func (s *State) read(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, s)
}

func (s *State) Path(key string) string { return s.Paths[key] }

// URL is the hook URL last used for key; empty if none is known.
func (s *State) URL(key string) string { return s.URLs[key] }

// Remember records the hook used for key and saves the state.
func (s *State) Remember(key, hookPath, hookURL string) error {
	s.Paths[key] = hookPath
	s.URLs[key] = hookURL
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		return err
	}
	// The write does not merge: when two CLIs write concurrently, the last
	// rename wins and the other's entry is lost.
	if err := atomicfile.WriteFile(s.file, b, 0o600); err != nil {
		return err
	}
	if s.legacy != "" {
		_ = os.Remove(s.legacy) // migrated; a leftover copy is harmless
		s.legacy = ""
	}
	return nil
}

func StateKey(relayURL, dir string, target *url.URL) string {
	return relayURL + " " + dir + " " + target.String()
}

// Targets returns the targets with a remembered hook for relayURL and dir,
// sorted.
func (s *State) Targets(relayURL, dir string) []string {
	prefix := StateKey(relayURL, dir, &url.URL{})
	var targets []string
	for k := range s.Paths {
		// Targets never contain spaces; a remainder with one belongs to a
		// directory that merely starts with dir.
		if t, ok := strings.CutPrefix(k, prefix); ok && t != "" && !strings.Contains(t, " ") {
			targets = append(targets, t)
		}
	}
	slices.Sort(targets)
	return targets
}
