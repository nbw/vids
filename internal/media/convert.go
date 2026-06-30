package media

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// standardRungs are the candidate target resolutions, expressed as the shorter
// edge ("the p number"), the conventional meaning for both landscape and
// portrait video.
var standardRungs = []int{2160, 1440, 1080, 720, 480, 360}

// Rung is a downscale target resolution.
type Rung struct {
	P int // shorter-edge target, e.g. 720
	W int // resulting width (for display)
	H int // resulting height (for display)
}

// Label renders the rung as e.g. "720p".
func (r Rung) Label() string {
	return fmt.Sprintf("%dp", r.P)
}

// AvailableRungs returns the downscale-only rungs for a source of size w x h,
// preserving aspect ratio. Dimensions are rounded to even numbers (libx264).
func AvailableRungs(w, h int) []Rung {
	if w <= 0 || h <= 0 {
		return nil
	}
	landscape := w >= h
	short := h
	if !landscape {
		short = w
	}
	var out []Rung
	for _, p := range standardRungs {
		if p >= short { // downscale only
			continue
		}
		var nw, nh int
		if landscape {
			nh = p
			nw = even(w * p / h)
		} else {
			nw = p
			nh = even(h * p / w)
		}
		out = append(out, Rung{P: p, W: nw, H: nh})
	}
	return out
}

func even(n int) int {
	if n%2 != 0 {
		n--
	}
	if n < 2 {
		n = 2
	}
	return n
}

// Quality is an encode quality preset.
type Quality struct {
	Name string
	CRF  int
}

// Qualities are the selectable quality presets.
var Qualities = []Quality{
	{"High", 18},
	{"Medium", 23},
	{"Small", 28},
}

// DefaultQualityIndex is the preselected quality (Medium).
const DefaultQualityIndex = 1

// ScaleFilter builds the ffmpeg -vf scale expression. -2 keeps aspect ratio and
// forces the computed dimension to be even.
func ScaleFilter(srcW, srcH, p int) string {
	if srcW >= srcH { // landscape: target height = p
		return fmt.Sprintf("scale=-2:%d", p)
	}
	return fmt.Sprintf("scale=%d:-2", p) // portrait: target width = p
}

// OutputPath returns a non-colliding output path in dir for the given source
// base name and rung, e.g. test_720p.mp4, then test_720p_1.mp4, ...
func OutputPath(dir, baseName string, p int) string {
	stem := strings.TrimSuffix(baseName, filepath.Ext(baseName))
	name := fmt.Sprintf("%s_%dp.mp4", stem, p)
	full := filepath.Join(dir, name)
	for i := 1; fileExists(full); i++ {
		name = fmt.Sprintf("%s_%dp_%d.mp4", stem, p, i)
		full = filepath.Join(dir, name)
	}
	return full
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// FFmpegArgs builds the resize command. Explicit stream mapping keeps audio
// optional (0:a?) and excludes data/telemetry streams the mp4 muxer rejects.
func FFmpegArgs(in, out, scale string, crf int) []string {
	return []string{
		"-hide_banner",
		"-nostats",
		"-progress", "pipe:1",
		"-i", in,
		"-map", "0:v:0",
		"-map", "0:a?",
		"-vf", scale,
		"-c:v", "libx264",
		"-crf", strconv.Itoa(crf),
		"-preset", "medium",
		"-c:a", "copy",
		"-movflags", "+faststart",
		"-y", out,
	}
}

// --- streaming conversion ---

// Progress is emitted periodically while ffmpeg runs.
type Progress struct {
	Frac  float64
	Speed string
	ETA   string
}

// Done is emitted once when ffmpeg finishes, fails, or is canceled.
type Done struct {
	Err error
}

// Run runs ffmpeg, parsing -progress output and pushing Progress/Done values
// onto ch. On cancel (ctx) or error the partial output file is removed. ch is
// typed as `chan any` so the TUI can consume the values directly as tea.Msg.
func Run(ctx context.Context, args []string, outPath string, duration float64, ch chan any) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ch <- Done{Err: err}
		return
	}
	if err := cmd.Start(); err != nil {
		ch <- Done{Err: err}
		return
	}

	scanner := bufio.NewScanner(stdout)
	var curUS float64 // out_time in microseconds
	var speed string
	for scanner.Scan() {
		key, val, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			// NOTE: ffmpeg's out_time_ms is mislabeled microseconds; use _us.
			if v, e := strconv.ParseFloat(strings.TrimSpace(val), 64); e == nil {
				curUS = v
			}
		case "speed":
			speed = strings.TrimSpace(val)
		case "progress":
			frac := 0.0
			if duration > 0 {
				frac = (curUS / 1e6) / duration
			}
			ch <- Progress{Frac: clamp01(frac), Speed: speed, ETA: computeETA(duration, curUS/1e6, speed)}
		}
	}

	err = cmd.Wait()
	if ctx.Err() != nil {
		os.Remove(outPath) // canceled: drop the partial file
		ch <- Done{Err: context.Canceled}
		return
	}
	if err != nil {
		os.Remove(outPath) // failed: drop the partial file
		ch <- Done{Err: fmt.Errorf("%v\n%s", err, tailString(stderr.String(), 400))}
		return
	}
	ch <- Progress{Frac: 1, Speed: speed, ETA: "0:00"}
	ch <- Done{Err: nil}
}

func computeETA(duration, cur float64, speed string) string {
	sv := parseFloat(strings.TrimSuffix(strings.TrimSpace(speed), "x"))
	if sv <= 0 || duration <= 0 {
		return "--:--"
	}
	rem := (duration - cur) / sv
	if rem < 0 {
		rem = 0
	}
	return FormatClock(rem)
}

// FormatClock formats seconds as m:ss.
func FormatClock(sec float64) string {
	s := int(sec + 0.5)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}

func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
