package preview

import (
	"bytes"
	"os/exec"
	"strconv"
)

// ffmpegRunner generates thumbnails far faster than pure-Go decoding for large
// images (SIMD + native scaling), especially PNG/huge sources. It is already
// bundled in the runtime image; the pure-Go path remains the fallback when
// ffmpeg is unavailable or fails.
type ffmpegRunner struct {
	path string
	sem  chan struct{} // limit concurrent processes
}

func newFFmpeg(maxConcurrent int) *ffmpegRunner {
	r := &ffmpegRunner{sem: make(chan struct{}, maxConcurrent)}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		r.path = p
	}
	return r
}

// fastThumbBytes runs ffmpeg to produce a JPEG thumbnail of src, fitting within
// `size`x`size`. Returns ok=false when ffmpeg is missing or the run failed.
func (r *ffmpegRunner) fastThumbBytes(src string, size, quality int) ([]byte, bool) {
	if r.path == "" {
		return nil, false
	}
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	// map JPEG quality (0-100) to ffmpeg -q:v (2=best .. 31=worst)
	qv := 2 + (100-quality)/12
	if qv < 2 {
		qv = 2
	}
	if qv > 31 {
		qv = 31
	}
	cmd := exec.Command(r.path,
		"-hide_banner", "-loglevel", "error",
		"-i", src,
		"-vf", "scale="+strconv.Itoa(size)+":"+strconv.Itoa(size)+":force_original_aspect_ratio=decrease",
		"-frames:v", "1",
		"-q:v", strconv.Itoa(qv),
		"-f", "image2", "-", // stream to stdout
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil || out.Len() == 0 {
		return nil, false
	}
	b := out.Bytes()
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, false // not a valid JPEG
	}
	return b, true
}
