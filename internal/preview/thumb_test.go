package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makePNG writes a small colored PNG and returns its path.
func makePNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestThumbGeneratesAndCaches(t *testing.T) {
	p := makePNG(t)
	dir := t.TempDir()
	svc, err := New(Options{CacheDir: dir, MemoryEntries: 8})
	if err != nil {
		t.Fatal(err)
	}

	b1, err := svc.Thumb(p, 64, 82)
	if err != nil {
		t.Fatalf("thumb: %v", err)
	}
	if b1[0] != 0xff || b1[1] != 0xd8 {
		t.Errorf("not a JPEG: %x", b1[:2])
	}
	// Second call should hit cache and return identical bytes.
	b2, err := svc.Thumb(p, 64, 82)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Errorf("cached thumbnail differs")
	}
	// Different size should produce a separate (valid) thumbnail.
	b3, err := svc.Thumb(p, 128, 82)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(b1, b3) {
		t.Errorf("thumbnails of different sizes should differ")
	}
	// Lower quality output should be smaller than high quality for a large image.
	small, err := svc.Thumb(p, 96, 40)
	if err != nil {
		t.Fatal(err)
	}
	big, err := svc.Thumb(p, 96, 95)
	if err != nil {
		t.Fatal(err)
	}
	if len(small) >= len(big) {
		t.Errorf("lower-quality thumbnail should be smaller: low=%d high=%d", len(small), len(big))
	}
}

func TestSupportedImage(t *testing.T) {
	for _, f := range []string{"a.jpg", "b.JPEG", "c.png", "d.webp", "e.gif"} {
		if !SupportedImage(f) {
			t.Errorf("%s should be supported", f)
		}
	}
	if SupportedImage("notes.txt") {
		t.Error("txt should not be an image")
	}
}
