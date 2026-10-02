package trash

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shiyi/starstack/internal/auth"
	"github.com/shiyi/starstack/internal/fsops"
	"github.com/shiyi/starstack/internal/store"
)

func TestTrashRestoreLifecycle(t *testing.T) {
	base := t.TempDir()
	st, err := store.Open(base + "/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	svc, err := New(st, base)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := fsops.New(filepath.Join(base, "users"))
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("pw")
	uid, _ := st.CreateUser("u", hash, false)

	if err := mgr.Mkdir("/docs"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mgr.Root, "docs", "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.Trash(uid, mgr, "me", "/docs"); err != nil {
		t.Fatalf("trash: %v", err)
	}
	// Source should be gone.
	if _, err := os.Stat(filepath.Join(mgr.Root, "docs")); !os.IsNotExist(err) {
		t.Errorf("source should be moved to trash")
	}
	entries, err := svc.List(uid)
	if err != nil || len(entries) != 1 {
		t.Fatalf("trash list: %+v err=%v", entries, err)
	}
	e := entries[0]
	if e.OrigPath != "me:/docs" || !e.IsDir || e.Size != 2 {
		t.Errorf("trash entry wrong: %+v", e)
	}

	// Restore.
	if err := svc.Restore(uid, e.ID, func(scope string) (*fsops.Manager, error) { return mgr, nil }); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mgr.Root, "docs", "a.txt")); err != nil {
		t.Errorf("restored file missing: %v", err)
	}
	if lenMust(svc.List(uid)) != 0 {
		t.Errorf("trash should be empty after restore")
	}
}

func lenMust(es []store.TrashEntry, err error) int {
	if err != nil {
		return -1
	}
	return len(es)
}
