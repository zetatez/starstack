package fsops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestMgr(t *testing.T) *Manager {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	m, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestResolveRejectsEscapes(t *testing.T) {
	m := newTestMgr(t)
	cases := []string{
		"../secret",
		"/../../etc/passwd",
		"a/../../b",
		"..",
		"/..",
		"..\\..\\windows",
	}
	for _, c := range cases {
		if _, err := m.Resolve(c); err == nil {
			t.Errorf("Resolve(%q) should fail, got nil", c)
		}
	}
}

func TestResolveStaysInRoot(t *testing.T) {
	m := newTestMgr(t)
	abs, err := m.Resolve("/docs/sub")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(abs, m.Root) {
		t.Errorf("resolved %q outside root %q", abs, m.Root)
	}
	rel, _ := filepath.Rel(m.Root, abs)
	if strings.HasPrefix(rel, "..") {
		t.Errorf("rel path escapes: %q", rel)
	}
}

func TestListMkdirRenameMoveDelete(t *testing.T) {
	m := newTestMgr(t)

	if err := m.Mkdir("/docs"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := filepath.Join(m.Root, "docs", "a.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := m.List("/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "docs" {
		t.Errorf("list: %+v", entries)
	}

	if err := m.Rename("/docs/a.txt", "b.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "docs", "b.txt")); err != nil {
		t.Errorf("renamed file missing: %v", err)
	}
	// Same name duplicate must fail.
	if err := m.Rename("/docs/b.txt", "b.txt"); err == nil {
		t.Error("rename to same name should fail")
	}

	if err := m.Mkdir("/dest"); err != nil {
		t.Fatal(err)
	}
	if err := m.Move("/docs/b.txt", "/dest"); err != nil {
		t.Fatalf("move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "dest", "b.txt")); err != nil {
		t.Errorf("moved file missing: %v", err)
	}
	// Cannot move a dir into itself.
	if err := m.Mkdir("/docs/sub"); err != nil {
		t.Fatal(err)
	}
	if err := m.Move("/docs", "/docs/sub"); err == nil {
		t.Error("moving dir into itself should fail")
	}

	if err := m.Delete("/dest"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "dest")); !os.IsNotExist(err) {
		t.Errorf("delete did not remove dir: %v", err)
	}
	if err := m.Delete("/"); err == nil {
		t.Error("deleting root should fail")
	}
}

func TestCopy(t *testing.T) {
	m := newTestMgr(t)
	_ = m.Mkdir("/docs")
	_ = os.WriteFile(filepath.Join(m.Root, "docs", "a.txt"), []byte("hi"), 0o644)
	_ = m.Mkdir("/src")
	_ = os.WriteFile(filepath.Join(m.Root, "src", "b.txt"), []byte("b"), 0o644)

	if err := m.Copy("/src", "/docs"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(m.Root, "docs", "src", "b.txt")); err != nil || string(b) != "b" {
		t.Fatalf("copied nested file: %v %q", err, b)
	}
	// Collision with existing name must fail.
	if err := m.Copy("/src", "/docs"); err == nil {
		t.Error("copying onto existing destination should fail")
	}
	// Copying a dir into itself must fail.
	if err := m.Copy("/docs", "/docs/src"); err == nil {
		t.Error("copying into itself should fail")
	}
}

func TestRenameRejectsSeparators(t *testing.T) {
	m := newTestMgr(t)
	_ = m.Mkdir("/docs")
	for _, bad := range []string{"a/b", "a\\b", "..", ".", ""} {
		if err := m.Rename("/docs", bad); err == nil {
			t.Errorf("rename to %q should fail", bad)
		}
	}
}
