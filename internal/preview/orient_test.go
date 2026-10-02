package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// makeExif builds a minimal little-endian TIFF/EXIF APP1 block that only sets
// Orientation (0x0112) to the given value.
func makeExif(orientation int) []byte {
	tiff := []byte{
		0x49, 0x49, 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00, // II, 42, IFD0 @8
		0x01, 0x00, // IFD entry count = 1
		0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, // tag 0x0112, type SHORT, count 1
		byte(orientation), 0x00, 0x00, 0x00, // value
		0x00, 0x00, 0x00, 0x00, // next IFD = 0
	}
	ex := append([]byte("Exif\x00\x00"), tiff...)
	// APP1 segment: FFE1 len(2) data
	seg := []byte{0xFF, 0xE1}
	length := uint16(2 + len(ex))
	seg = append(seg, byte(length>>8), byte(length&0xFF))
	return append(seg, ex...)
}

func fillRGBA(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 50), G: uint8(y * 70), B: 120, A: 255})
		}
	}
	return img
}

// TestExifOrientationThumb ensures a stored-4x2 / Orientation-6 JPEG renders a
// tall (2x4) thumbnail, matching the browser's EXIF-aware display.
func TestExifOrientationThumb(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "photo.jpg")

	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, fillRGBA(4, 2), nil)
	j := buf.Bytes() // FFD8 ...
	app1 := makeExif(6)
	withExif := append([]byte{0xFF, 0xD8}, app1...)
	withExif = append(withExif, j[2:]...)
	if err := os.WriteFile(p, withExif, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readOrientation(p); got != 6 {
		t.Fatalf("readOrientation = %d, want 6", got)
	}

	svc, _ := New(Options{CacheDir: dir, MemoryEntries: 8})
	thumb, err := svc.Thumb(p, 48, 80)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatal(err)
	}
	d := dec.Bounds()
	if d.Dx() >= d.Dy() {
		t.Errorf("thumbnail should be tall (2x4 -> 24x48), got %dx%d", d.Dx(), d.Dy())
	}
}

// TestRotateDropsExifOrientation: after rotating an orientation-6 image, the
// result is pixel-corrected and carries no EXIF orientation.
func TestRotateDropsExifOrientation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "photo.jpg")
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, fillRGBA(4, 2), nil)
	j := buf.Bytes()
	withExif := append([]byte{0xFF, 0xD8}, makeExif(6)...)
	withExif = append(withExif, j[2:]...)
	_ = os.WriteFile(p, withExif, 0o644)

	if err := RotateOnDisk(p, 90); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if got := readOrientation(p); got != 1 {
		t.Errorf("orientation should be cleared after rotate, got %d", got)
	}
	f, _ := os.Open(p)
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Display-correct orientation of 4x2 is 2x4; rotate 90 CW => 4x2.
	b := img.Bounds()
	if b.Dx() != 4 || b.Dy() != 2 {
		t.Errorf("expected 4x2 after rotate-on-oriented, got %dx%d", b.Dx(), b.Dy())
	}
}
