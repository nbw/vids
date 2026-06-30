// Package media wraps the ffmpeg/ffprobe binaries: listing video files,
// probing their metadata, and running resize conversions.
package media

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// videoExts is the set of extensions treated as video files (case-insensitive).
var videoExts = map[string]bool{
	".mp4": true, ".mov": true, ".mkv": true, ".webm": true, ".avi": true,
	".m4v": true, ".flv": true, ".wmv": true, ".mpg": true, ".mpeg": true,
	".3gp": true, ".ts": true, ".m2ts": true, ".mts": true,
}

// Video is a video file on disk.
type Video struct {
	Name string // base name, e.g. "test.MP4"
	Path string // absolute path
	Size int64  // bytes
}

// ListVideos returns the video files in dir, flat (no recursion), sorted by name.
func ListVideos(dir string) ([]Video, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Video
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !videoExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Video{
			Name: e.Name(),
			Path: filepath.Join(dir, e.Name()),
			Size: info.Size(),
		})
	}
	// os.ReadDir already returns entries sorted by filename.
	return out, nil
}

// Info holds the metadata displayed and used for conversion.
type Info struct {
	Width    int
	Height   int
	Codec    string
	FPS      float64
	Duration float64 // seconds
	BitRate  int64   // bits/sec, 0 if unknown
	Size     int64   // bytes
	Format   string
}

// raw ffprobe JSON shape. Numeric fields like duration/bit_rate are quoted
// strings in ffprobe output, so they are parsed as strings here.
type ffprobeJSON struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		Size       string `json:"size"`
		BitRate    string `json:"bit_rate"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// Probe runs ffprobe on path and parses the result, selecting the first video
// stream (not assuming index 0) for dimensions/codec/fps.
func Probe(path string) (*Info, error) {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	)
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var raw ffprobeJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing ffprobe output: %w", err)
	}

	info := &Info{Format: raw.Format.FormatName}

	var vstreamDur, vstreamBitrate string
	found := false
	for _, s := range raw.Streams {
		if s.CodecType == "video" && s.Width > 0 && s.Height > 0 {
			info.Width = s.Width
			info.Height = s.Height
			info.Codec = s.CodecName
			info.FPS = parseFraction(s.RFrameRate)
			vstreamDur = s.Duration
			vstreamBitrate = s.BitRate
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("no video stream found")
	}

	// Duration: prefer stream, fall back to format. Needed for progress %.
	info.Duration = parseFloat(vstreamDur)
	if info.Duration == 0 {
		info.Duration = parseFloat(raw.Format.Duration)
	}

	// Bit rate: prefer stream, fall back to format.
	info.BitRate = parseInt(vstreamBitrate)
	if info.BitRate == 0 {
		info.BitRate = parseInt(raw.Format.BitRate)
	}

	info.Size = parseInt(raw.Format.Size)
	return info, nil
}

// parseFraction evaluates an ffprobe rational like "30000/1001" -> 29.97.
func parseFraction(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}
	n := parseFloat(num)
	d := parseFloat(den)
	if d == 0 {
		return 0
	}
	return n / d
}

func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

func parseInt(s string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
