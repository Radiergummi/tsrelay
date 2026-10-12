package client

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
)

// State remembers which hook path the CLI used per relay, working directory
// and target, so URLs pasted into .env files survive restarts.
type State struct {
	Paths map[string]string `json:"paths"`
	file  string
}

// DefaultStatePath is <user config dir>/tsrelay/state.json.
func DefaultStatePath() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tsrelay", "state.json"), nil
}

func LoadState(path string) (*State, error) {
	s := &State{Paths: map[string]string{}, file: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, err
	}
	if s.Paths == nil {
		s.Paths = map[string]string{}
	}
	return s, nil
}

func (s *State) Path(key string) string { return s.Paths[key] }

func (s *State) SetPath(key, hookPath string) error {
	s.Paths[key] = hookPath
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o700); err != nil {
		return err
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.file)
}

func StateKey(relayURL, dir string, target *url.URL) string {
	return relayURL + " " + dir + " " + target.String()
}
