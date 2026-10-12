package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Radiergummi/tsrelay/internal/atomicfile"
	"tailscale.com/tailcfg"
)

const jsonStoreVersion = 1

type fileData struct {
	Version int                       `json:"version"`
	Hooks   []Hook                    `json:"hooks"`
	Slugs   map[string]tailcfg.UserID `json:"slugs"`
}

// JSONStore keeps all hooks in one JSON file, rewritten atomically on every
// change. It holds an exclusive lock on its directory while open.
type JSONStore struct {
	mu    sync.Mutex
	path  string
	lock  *os.File
	hooks map[ID]Hook
	slugs map[string]tailcfg.UserID
}

// OpenJSON opens the store in dir, creating dir if needed.
func OpenJSON(dir string) (*JSONStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	lf, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(lf); err != nil {
		lf.Close()
		return nil, fmt.Errorf("state directory %s is in use by another tsrelay: %w", dir, err)
	}
	s := &JSONStore{
		path:  filepath.Join(dir, "hooks.json"),
		lock:  lf,
		hooks: map[ID]Hook{},
		slugs: map[string]tailcfg.UserID{},
	}
	b, err := os.ReadFile(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return s, nil
	case err != nil:
		lf.Close()
		return nil, err
	}
	var d fileData
	if err := json.Unmarshal(b, &d); err != nil {
		lf.Close()
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if d.Version != jsonStoreVersion {
		lf.Close()
		return nil, fmt.Errorf("%s has version %d, this tsrelay reads version %d", s.path, d.Version, jsonStoreVersion)
	}
	for _, h := range d.Hooks {
		s.hooks[h.ID] = h
	}
	maps.Copy(s.slugs, d.Slugs)
	return s, nil
}

// Close releases the directory lock.
func (s *JSONStore) Close() error { return s.lock.Close() }

func (s *JSONStore) ListHooks(context.Context) ([]Hook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Collect(maps.Values(s.hooks)), nil
}

func (s *JSONStore) GetHook(_ context.Context, id ID) (Hook, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.hooks[id]
	if !ok {
		return Hook{}, ErrNotFound
	}
	return h, nil
}

func (s *JSONStore) PutHook(_ context.Context, h Hook) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.hooks[h.ID]
	s.hooks[h.ID] = h
	if err := s.save(); err != nil {
		if existed {
			s.hooks[h.ID] = prev
		} else {
			delete(s.hooks, h.ID)
		}
		return err
	}
	return nil
}

func (s *JSONStore) DeleteHook(_ context.Context, id ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.hooks[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.hooks, id)
	if err := s.save(); err != nil {
		s.hooks[id] = prev
		return err
	}
	return nil
}

func (s *JSONStore) BindSlug(_ context.Context, slug string, user tailcfg.UserID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner, ok := s.slugs[slug]; ok {
		if owner != user {
			return ErrSlugTaken
		}
		return nil
	}
	s.slugs[slug] = user
	if err := s.save(); err != nil {
		delete(s.slugs, slug)
		return err
	}
	return nil
}

func (s *JSONStore) SlugOwner(_ context.Context, slug string) (tailcfg.UserID, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.slugs[slug]
	return owner, ok, nil
}

// save writes the file; the caller holds s.mu.
func (s *JSONStore) save() error {
	d := fileData{
		Version: jsonStoreVersion,
		Hooks: slices.SortedFunc(maps.Values(s.hooks), func(a, b Hook) int {
			return strings.Compare(string(a.ID), string(b.ID))
		}),
		Slugs: s.slugs,
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.path, b, 0o600)
}
