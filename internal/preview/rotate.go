package preview

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnsupportedFormat is returned when a file isn't a raster image we can
// re-encode for rotation.
var ErrUnsupportedFormat = errors.New("unsupported image format for rotation")

// RotateOnDisk rotates the image file at `path` by the given angle
// (±90 or 180) and atomically replaces the original with the result.
func RotateOnDisk(path string, angle int) error {
	angle = normalizeAngle(angle)
	if angle == 0 {
		return fmt.Errorf("%w: angle must be ±90 or 180", ErrUnsupportedFormat)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	img, format, err := image.Decode(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupportedFormat, err)
	}
	rotated := rotateImage(img, angle)

	var buf bytes.Buffer
	if err := encodeFormat(&buf, rotated, format); err != nil {
		return err
	}

	// Atomic replace: write a temp file next to the original, then rename over it.
	tmp := path + ".rot.tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func normalizeAngle(a int) int {
	for a < 0 {
		a += 360
	}
	switch a % 360 {
	case 90, 270:
		return 90
	case 180, -180:
		return 180
	default:
		return 0
	}
}

// rotateImage rotates src preserving its aspect.
func rotateImage(src image.Image, angle int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if angle == 90 {
		dw, dh = h, w
	}
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for oy := b.Min.Y; oy < b.Max.Y; oy++ {
		for ox := b.Min.X; ox < b.Max.X; ox++ {
			ix, iy := ox-b.Min.X, oy-b.Min.Y
			c := src.At(ox, oy)
			var nx, ny int
			switch angle {
			case 90: // clockwise
				nx, ny = h-1-iy, ix
			case 180:
				nx, ny = w-1-ix, h-1-iy
			default: // should not happen for a file with normalized angle
				nx, ny = iy, w-1-ix
			}
			out.Set(nx, ny, c)
		}
	}
	return out
}

func encodeFormat(buf *bytes.Buffer, img image.Image, format string) error {
	switch strings.TrimSpace(strings.ToLower(format)) {
	case "jpeg", "jpg":
		return jpeg.Encode(buf, img, &jpeg.Options{Quality: 90})
	case "png":
		return png.Encode(buf, img)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, format)
	}
}

// CanRotate reports whether the file extension is a raster format we can rotate.
func CanRotate(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}
