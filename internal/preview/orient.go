package preview

import (
	"image"
	"os"

	"github.com/rwcarlsen/goexif/exif"
)

// orientationValues per EXIF spec (tag 0x0112).
const (
	orientNormal  = 1
	orientMirrorH = 2
	orientRot180  = 3
	orientFlipV   = 4
	orientRot270  = 8 // rotate 90 CCW to display upright
)

// orientExif returns the image normalized to its display orientation so that
// thumbnails and rotations match what the browser shows for an <img> (which
// honors EXIF Orientation). If the file carries no EXIF orientation, img is
// returned unchanged.
func orientExif(path string, img image.Image) image.Image {
	o := readOrientation(path)
	if o <= 1 {
		return img
	}
	return applyOrientation(img, o)
}

func readOrientation(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()
	x, err := exif.Decode(f)
	if err != nil {
		return 1
	}
	tag, err := x.Get(exif.Orientation)
	if err != nil {
		return 1
	}
	v, err := tag.Int(0)
	if err != nil {
		return 1
	}
	return v
}

// applyOrientation transforms src by the EXIF orientation so the resulting
// pixel layout displays upright.
func applyOrientation(src image.Image, o int) *image.RGBA {
	switch o {
	case orientMirrorH:
		return mirror(src, true, false)
	case orientRot180:
		return rotateImage(src, 180)
	case orientFlipV:
		return mirror(src, false, true)
	case 5: // mirror-h then rotate 270 CW (transpose)
		return rotateImage(mirror(src, true, false), 90)
	case 6: // rotate 90 CW
		return rotateImage(src, 90)
	case 7: // mirror-v then rotate 90 CW (transverse)
		return rotateImage(mirror(src, false, true), 270)
	case orientRot270: // rotate 90 CCW
		return rotateImage(src, 270)
	default:
		// Unknown orientations map to "do nothing".
		d := src.Bounds()
		out := image.NewRGBA(d)
		for y := d.Min.Y; y < d.Max.Y; y++ {
			for x := d.Min.X; x < d.Max.X; x++ {
				out.Set(x, y, src.At(x, y))
			}
		}
		return out
	}
}

// mirror returns a copy of src reflected across the horizontal and/or
// vertical axis.
func mirror(src image.Image, flipH, flipV bool) *image.RGBA {
	b := src.Bounds()
	out := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			nx, ny := x, y
			if flipH {
				nx = b.Max.X - 1 - (x - b.Min.X)
			}
			if flipV {
				ny = b.Max.Y - 1 - (y - b.Min.Y)
			}
			out.Set(nx, ny, src.At(x, y))
		}
	}
	return out
}
