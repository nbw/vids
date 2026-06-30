package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableRungs(t *testing.T) {
	// 4K landscape: short edge 2160, downscale-only rungs below it.
	got := AvailableRungs(3840, 2160)
	want := []int{1440, 1080, 720, 480, 360}
	if len(got) != len(want) {
		t.Fatalf("got %d rungs, want %d: %+v", len(got), len(want), got)
	}
	for i, r := range got {
		if r.P != want[i] {
			t.Errorf("rung %d: got %dp, want %dp", i, r.P, want[i])
		}
		if r.W%2 != 0 || r.H%2 != 0 {
			t.Errorf("rung %dp not even: %dx%d", r.P, r.W, r.H)
		}
	}
	if got[2].P == 720 && (got[2].W != 1280 || got[2].H != 720) {
		t.Errorf("720p got %dx%d, want 1280x720", got[2].W, got[2].H)
	}
}

func TestScaleFilter(t *testing.T) {
	if f := ScaleFilter(3840, 2160, 720); f != "scale=-2:720" {
		t.Errorf("landscape: got %q", f)
	}
	if f := ScaleFilter(2160, 3840, 720); f != "scale=720:-2" {
		t.Errorf("portrait: got %q", f)
	}
}

func TestOutputPathCollision(t *testing.T) {
	dir := t.TempDir()
	first := OutputPath(dir, "test.MP4", 720)
	if base := filepath.Base(first); base != "test_720p.mp4" {
		t.Errorf("got %q", base)
	}
	os.WriteFile(first, []byte("x"), 0o644)
	second := OutputPath(dir, "test.MP4", 720)
	if base := filepath.Base(second); base != "test_720p_1.mp4" {
		t.Errorf("collision got %q", base)
	}
}

// TestRealConversion exercises the actual ffmpeg pipeline against testdata if
// present. Skipped when the sample isn't available (e.g. in CI).
func TestRealConversion(t *testing.T) {
	src := findSample()
	if src == "" {
		t.Skip("no sample video present; skipping real conversion")
	}
	info, err := Probe(src)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	out := OutputPath(t.TempDir(), filepath.Base(src), 360)
	scale := ScaleFilter(info.Width, info.Height, 360)
	args := FFmpegArgs(src, out, scale, 28)

	ch := make(chan any, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go Run(ctx, args, out, info.Duration, ch)

	var sawProgress bool
	var done Done
loop:
	for msg := range ch {
		switch v := msg.(type) {
		case Progress:
			if v.Frac > 0 {
				sawProgress = true
			}
		case Done:
			done = v
			break loop
		}
	}
	if done.Err != nil {
		t.Fatalf("conversion failed: %v", done.Err)
	}
	if !sawProgress {
		t.Error("never saw progress > 0")
	}

	outInfo, err := Probe(out)
	if err != nil {
		t.Fatalf("probe output: %v", err)
	}
	if outInfo.Height != 360 {
		t.Errorf("output height = %d, want 360", outInfo.Height)
	}
}

// findSample looks for a sample video to use in the real-conversion test.
func findSample() string {
	for _, p := range []string{
		filepath.Join("..", "..", "test.MP4"),
		filepath.Join("..", "..", "testdata", "test.MP4"),
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
