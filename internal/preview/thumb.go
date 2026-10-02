// Package preview generates and caches preview resources (image thumbnails).
// Caches live on disk and in a bounded memory LRU.
package preview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/golang-lru/v2"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	_ "image/gif"
)

// DefaultThumbSize is the thumbnail edge length when not requested.
const DefaultThumbSize = 384

// ErrUnsupported indicates the file cannot be previewed as an image.
var ErrUnsupported = errors.New("unsupported image format")

// Service caches generated preview resources.
type Service struct {
	cacheDir string
	lru      *lru.Cache[string, []byte]
}

// Options configures the preview cache.
type Options struct {
	CacheDir      string
	MemoryEntries int // memory LRU capacity
}

func New(opt Options) (*Service, error) {
	if err := os.MkdirAll(filepath.Join(opt.CacheDir, "thumb"), 0o755); err != nil {
		return nil, err
	}
	n := opt.MemoryEntries
	if n <= 0 {
		n = 512
	}
	lru, err := lru.New[string, []byte](n)
	if err != nil {
		return nil, err
	}
	return &Service{cacheDir: opt.CacheDir, lru: lru}, nil
}

// Thumb generates a JPEG thumbnail for an image file, reusing the memory or
// disk cache when available. The thumbnail fits within a `size`x`size` box.
func (s *Service) Thumb(imagePath string, size int) ([]byte, error) {
	if size <= 0 {
		size = DefaultThumbSize
	}
	key, err := cacheKey(imagePath)
	if err != nil {
		return nil, err
	}
	key = fmt.Sprintf("thumb/%d/%s.jpg", size, key)

	if b, ok := s.lru.Get(key); ok {
		return b, nil
	}
	disk := filepath.Join(s.cacheDir, key)
	if b, err := os.ReadFile(disk); err == nil {
		s.lru.Add(key, b)
		return b, nil
	}

	thumb, err := renderThumb(imagePath, size)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(disk, thumb, 0o644)
	s.lru.Add(key, thumb)
	return thumb, nil
}

func renderThumb(imagePath string, size int) ([]byte, error) {
	src, err := os.Open(imagePath)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	img, _, err := image.Decode(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return nil, ErrUnsupported
	}
	scale := float64(size) / float64(max(w, h))
	if scale >= 1 {
		scale = 1 // never upscale
	}
	dw := max(1, int(float64(w)*scale))
	dh := max(1, int(float64(h)*scale))

	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// cacheKey is a content hash so edited files regenerate thumbnails.
func cacheKey(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SupportedImage reports whether an extension can be previewed as an image.
func SupportedImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp":
		return true
	}
	return false
}
