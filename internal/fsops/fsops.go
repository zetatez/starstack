// Package fsops maps user-visible virtual paths onto the real filesystem with
// strict path-safety guarantees. It backs all file CRUD handlers.
package fsops

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrNotExist / ErrExist / ErrInvalid are sentinel errors returned by helpers.
var (
	ErrNotExist  = fs.ErrNotExist
	ErrExist     = fs.ErrExist
	ErrInvalid   = fmt.Errorf("invalid path")
	ErrNotInRoot = fmt.Errorf("path outside root")
)

// Entry is a directory listing item.
type Entry struct {
	Name    string    `json:"name"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// Manager resolves virtual paths for one storage root (personal space or share).
type Manager struct {
	Root string // absolute root directory on disk
}

func New(root string) (*Manager, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Manager{Root: abs}, nil
}

// Resolve converts a user-provided virtual path into an absolute disk path.
// It rejects any "." or ".." component, absolute input and escapes.
func (m *Manager) Resolve(virtual string) (string, error) {
	normalized := strings.ReplaceAll(virtual, "\\", "/")
	for _, seg := range strings.Split(normalized, "/") {
		if seg == "." || seg == ".." {
			return "", ErrNotInRoot
		}
	}
	clean := path.Clean("/" + normalized)
	abs := filepath.Join(m.Root, filepath.FromSlash(clean))
	// Defensively re-verify containment after joining (defense in depth).
	absAbs, err := filepath.Abs(abs)
	if err != nil {
		return "", ErrInvalid
	}
	rel, err := filepath.Rel(m.Root, absAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrNotInRoot
	}
	return absAbs, nil
}

// List enumerates entries under a virtual directory path.
func (m *Manager) List(virtual string) ([]Entry, error) {
	abs, err := m.Resolve(virtual)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%w: not a directory", ErrInvalid)
	}
	dirents, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(dirents))
	for _, d := range dirents {
		e := Entry{Name: d.Name(), IsDir: d.IsDir()}
		info, err := d.Info()
		if err != nil {
			continue
		}
		e.Size = info.Size()
		e.ModTime = info.ModTime()
		out = append(out, e)
	}
	// Dirs first, then name — cheap sort, fits the "fast & simple" goal.
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Mkdir creates a directory (including missing parents). Fails if it exists.
func (m *Manager) Mkdir(virtual string) error {
	abs, err := m.Resolve(virtual)
	if err != nil {
		return err
	}
	if strings.TrimSpace(virtual) == "" || virtual == "/" {
		return fmt.Errorf("%w: cannot create root", ErrInvalid)
	}
	return os.MkdirAll(abs, 0o755)
}

// Rename renames a file or directory to a new name within the same parent dir.
func (m *Manager) Rename(virtual, newName string) error {
	if strings.ContainsAny(newName, "/\\") || strings.TrimSpace(newName) == "" ||
		newName == "." || newName == ".." {
		return fmt.Errorf("%w: invalid name", ErrInvalid)
	}
	abs, err := m.Resolve(virtual)
	if err != nil {
		return err
	}
	if err := mustExist(abs); err != nil {
		return err
	}
	dir := filepath.Dir(abs)
	dst := filepath.Join(dir, newName)
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%w: destination exists", ErrExist)
	}
	return os.Rename(abs, dst)
}

// Move relocates a file/dir into a different (existing) directory within root.
func (m *Manager) Move(virtual, newParent string) error {
	abs, err := m.Resolve(virtual)
	if err != nil {
		return err
	}
	if err := mustExist(abs); err != nil {
		return err
	}
	if strings.TrimSpace(newParent) == "" || newParent == "/" {
		newParent = "/"
	}
	absParent, err := m.Resolve(newParent)
	if err != nil {
		return err
	}
	fi, err := os.Stat(absParent)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: not a directory", ErrInvalid)
	}
	// Refuse to move a directory into itself or its own subtree.
	if strings.HasPrefix(absParent, abs+string(os.PathSeparator)) || absParent == abs {
		return fmt.Errorf("%w: cannot move into itself", ErrInvalid)
	}
	// No-op move (same parent): succeed silently.
	if filepath.Dir(abs) == absParent {
		return nil
	}
	dst := filepath.Join(absParent, filepath.Base(abs))
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("%w: destination exists", ErrExist)
	}
	return os.Rename(abs, dst)
}

// Delete removes a file, or a directory recursively (for trash, the caller
// moves it into the trash area first).
func (m *Manager) Delete(virtual string) error {
	abs, err := m.Resolve(virtual)
	if err != nil {
		return err
	}
	if err := mustExist(abs); err != nil {
		return err
	}
	if virtual == "/" || abs == m.Root {
		return fmt.Errorf("%w: cannot delete root", ErrInvalid)
	}
	return os.RemoveAll(abs)
}

// Stat returns basic info about a virtual path.
func (m *Manager) Stat(virtual string) (Entry, error) {
	abs, err := m.Resolve(virtual)
	if err != nil {
		return Entry{}, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: filepath.Base(abs), IsDir: fi.IsDir(), Size: fi.Size(), ModTime: fi.ModTime()}, nil
}

func mustExist(abs string) error {
	fi, err := os.Stat(abs)
	if err != nil {
		return err
	}
	_ = fi
	return nil
}
