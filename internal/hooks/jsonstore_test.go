package hooks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func openStore(t *testing.T, dir string) *JSONStore {
	t.Helper()
	s, err := OpenJSON(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestJSONStoreCRUDAndReload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := openStore(t, dir)
	h := Hook{ID: "h_1", Owner: 42, OwnerLogin: "m@x", Slug: "m", Path: "a", Channel: "default", CreatedAt: time.Unix(1, 0).UTC()}
	if err := s.PutHook(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSlug(ctx, "m", 42); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetHook(ctx, "h_1"); err != nil || got != h {
		t.Fatalf("GetHook = %+v, %v", got, err)
	}
	if _, err := s.GetHook(ctx, "h_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetHook(missing) err = %v, want ErrNotFound", err)
	}
	s.Close()

	s2 := openStore(t, dir)
	list, err := s2.ListHooks(ctx)
	if err != nil || len(list) != 1 || list[0] != h {
		t.Fatalf("after reload ListHooks = %+v, %v", list, err)
	}
	if owner, ok, _ := s2.SlugOwner(ctx, "m"); !ok || owner != 42 {
		t.Fatalf("SlugOwner = %v, %v", owner, ok)
	}
	if err := s2.DeleteHook(ctx, "h_1"); err != nil {
		t.Fatal(err)
	}
	if err := s2.DeleteHook(ctx, "h_1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestJSONStoreSlugBinding(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, t.TempDir())
	if err := s.BindSlug(ctx, "alice", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSlug(ctx, "alice", 1); err != nil {
		t.Fatalf("rebinding to the same user: %v", err)
	}
	if err := s.BindSlug(ctx, "alice", 2); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("binding to another user: err = %v, want ErrSlugTaken", err)
	}
}

func TestJSONStoreLocksDirectory(t *testing.T) {
	dir := t.TempDir()
	openStore(t, dir)
	if s, err := OpenJSON(dir); err == nil {
		s.Close()
		t.Fatal("second OpenJSON on a locked directory should fail")
	}
}

func TestJSONStoreRejectsBadFiles(t *testing.T) {
	for name, content := range map[string]string{
		"corrupt":       "{not json",
		"wrong version": `{"version": 99, "hooks": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "hooks.json"), []byte(content), 0o600)
			if s, err := OpenJSON(dir); err == nil {
				s.Close()
				t.Fatal("expected an error")
			}
		})
	}
}

func TestJSONStoreFileHygiene(t *testing.T) {
	dir := t.TempDir()
	s := openStore(t, dir)
	s.PutHook(context.Background(), Hook{ID: "h_1"})
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "hooks.json" && e.Name() != "lock" {
			t.Errorf("unexpected file %s left in state dir", e.Name())
		}
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, "hooks.json"))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("hooks.json mode = %v, want 0600", fi.Mode().Perm())
		}
	}
}
