// Package trash moves deleted files into a per-user trash area so they can be
// restored or permanently purged. Moving is cross-device safe (falls back to
// copy+delete when the trash lives on a different mount than the source).
package trash

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/shiyi/starstack/internal/fsops"
	"github.com/shiyi/starstack/internal/store"
)

// Service manages the trash area underneath the data directory.
type Service struct {
	store    *store.Store
	trashDir string
}

func New(st *store.Store, dataDir string) (*Service, error) {
	dir := filepath.Join(dataDir, "trash")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Service{store: st, trashDir: dir}, nil
}

// Trash moves a virtual path (resolved via mgr) into the acting user's trash.
// It records a row so the item can be restored later.
func (s *Service) Trash(userID int64, mgr *fsops.Manager, scope, virtual string) error {
	abs, err := mgr.Resolve(virtual)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(abs); err != nil {
		return err
	} else if !(scope == "me" || scope == "share") {
		_ = fi
	}
	base := filepath.Base(abs)
	if virtual == "/" || abs == mgr.Root {
		return fmt.Errorf("%w: cannot trash root", fsops.ErrInvalid)
	}

	// Layout: trash/<userID>/<unix>/<name>. The unix dir avoids name collisions.
	dest := filepath.Join(s.trashDir, fmt.Sprintf("%d", userID),
		fmt.Sprintf("%d", time.Now().UnixNano()), base)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	size, err := sizeOf(abs)
	if err != nil {
		return err
	}
	entry := &store.TrashEntry{
		OrigPath: virtual, TrashPath: dest, IsDir: isDir(abs), Size: size,
	}

	// Set a flag so we only insert the DB row after the move succeeds.
	entry.OrigPath = scopeVirtual(scope, virtual)
	if err := moveAcross(abs, dest); err != nil {
		return err
	}
	entry.TrashPath = dest
	return s.store.AddTrash(userID, entry)
}

// List returns trash entries for a user.
func (s *Service) List(userID int64) ([]store.TrashEntry, error) {
	return s.store.ListTrash(userID)
}

// Restore moves an entry back to its original location (if free) and removes
// the DB row.
func (s *Service) Restore(userID, id int64, mgr func(scope string) (*fsops.Manager, error)) error {
	e, err := s.store.GetTrash(userID, id)
	if err != nil {
		return err
	}
	scope, virt := unscopeVirtual(e.OrigPath)
	mg, err := mgr(scope)
	if err != nil {
		return err
	}
	abs, err := mg.Resolve(virt)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("destination already exists")
	}
	if err := moveAcross(e.TrashPath, abs); err != nil {
		return err
	}
	return s.store.DeleteTrashRow(id)
}

// Permanently removes a single trash entry (both disk file and DB row).
func (s *Service) Purge(userID, id int64) error {
	e, err := s.store.GetTrash(userID, id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(e.TrashPath); err != nil {
		return err
	}
	// Prune now-empty parent dirs.
	pruneEmptyParent(filepath.Dir(e.TrashPath))
	return s.store.DeleteTrashRow(id)
}

// Empty deletes every trash entry for the user.
func (s *Service) Empty(userID int64) (int, error) {
	entries, err := s.store.ListTrash(userID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		_ = os.RemoveAll(e.TrashPath)
		_ = s.store.DeleteTrashRow(e.ID)
		n++
	}
	if err := os.RemoveAll(filepath.Join(s.trashDir, fmt.Sprintf("%d", userID))); err != nil {
		return n, err
	}
	return n, nil
}

// ---- helpers ----

func scopeVirtual(scope, virtual string) string {
	return scope + ":" + virtual
}

func unscopeVirtual(s string) (scope, virtual string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return "me", "/"
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// sizeOf returns the recursive size of a file or directory tree.
func sizeOf(p string) (int64, error) {
	var total int64
	err := filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// moveAcross renames when possible; on cross-device link it copies then deletes.
func moveAcross(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		// Some other failure (e.g. not exist).
		return err
	}
	if err := copyRecursive(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

func copyRecursive(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return copyFile(src, dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyRecursive(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	cerr2 := out.Close()
	if cerr != nil {
		return cerr
	}
	return cerr2
}

// pruneEmptyParent removes empty directories up to the trash root.
func pruneEmptyParent(dir string) {
	for dir != "" && dir != "." {
		if err := os.Remove(dir); err != nil {
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
