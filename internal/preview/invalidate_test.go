package preview

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// writeJPEG writes a JPEG of the given dimensions.
func writeJPEG(path string, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	_ = os.WriteFile(path, buf.Bytes(), 0o644)
}

func TestInvalidateRegenerates(t *testing.T) {
	dir := t.TempDir()
	svc, _ := New(Options{CacheDir: dir, MemoryEntries: 8})
	p := filepath.Join(dir, "img.jpg")
	writeJPEG(p, 200, 100)

	w1, err := svc.Thumb(p, 48, 80)
	if err != nil {
		t.Fatal(err)
	}
	// Overwrite the source image with a different aspect ratio.
	writeJPEG(p, 100, 200)
	// Without invalidation the cache would keep the old thumbnail; after
	// invalidation it must be regenerated from the new file (dimensions differ).
	if err := svc.Invalidate(p); err != nil {
		t.Fatal(err)
	}
	w2, err := svc.Thumb(p, 48, 80)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(w1, w2) {
		t.Error("thumbnail should be regenerated after Invalidate")
	}
	// The source went 200x100 -> 100x200, so thumbnails swap aspect.
	if aspectOf(w1) == aspectOf(w2) {
		t.Errorf("expected swapped aspect after invalidation, got same")
	}
}

func aspectOf(b []byte) string {
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		return "?"
	}
	d := img.Bounds()
	if d.Dx() > d.Dy() {
		return "wide"
	}
	if d.Dy() > d.Dx() {
		return "tall"
	}
	return "square"
}
