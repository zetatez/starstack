package preview

import (
	"image"
	"image/color"
	"testing"
)

func fillImg(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	n := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(n * 40), G: uint8((n * 60) % 256), B: uint8((n * 90) % 256), A: 255})
			n++
		}
	}
	return img
}

func assertSameImage(t *testing.T, a, b image.Image, msg string) {
	t.Helper()
	ba, bb := a.Bounds(), b.Bounds()
	if ba.Dx() != bb.Dx() || ba.Dy() != bb.Dy() {
		t.Fatalf("%s: dims differ %dx%d vs %dx%d", msg, ba.Dx(), ba.Dy(), bb.Dx(), bb.Dy())
	}
	for y := ba.Min.Y; y < ba.Max.Y; y++ {
		for x := ba.Min.X; x < ba.Max.X; x++ {
			if a.At(x, y) != b.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) differs %v vs %v", msg, x, y, a.At(x, y), b.At(x, y))
			}
		}
	}
}

func TestRotateRoundTrip(t *testing.T) {
	src := fillImg(3, 2)
	// 90 then 270 restores original orientation.
	is := rotateImage(src, 90)
	wantDims := is.Bounds()
	if wantDims.Dx() != 2 || wantDims.Dy() != 3 {
		t.Fatalf("90 dims should be 2x3, got %dx%d", wantDims.Dx(), wantDims.Dy())
	}
	back := rotateImage(is, 270)
	assertSameImage(t, back, src, "90+270")
	// 90 four times = identity.
	cur := src
	for i := 0; i < 4; i++ {
		cur = rotateImage(cur, 90)
	}
	assertSameImage(t, cur, src, "90x4")
	// 90 twice = 180.
	twice := rotateImage(rotateImage(src, 90), 90)
	once180 := rotateImage(src, 180)
	assertSameImage(t, twice, once180, "90+90==180")
}

func TestNormalizeAngle(t *testing.T) {
	cases := map[int]int{90: 90, -90: 270, 180: 180, -180: 180, 270: 270, 450: 90, 0: 0, 360: 0}
	for in, want := range cases {
		if got := normalizeAngle(in); got != want {
			t.Errorf("normalizeAngle(%d) = %d, want %d", in, got, want)
		}
	}
}
