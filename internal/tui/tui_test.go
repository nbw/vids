package tui

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nbw/vids/internal/media"
)

// TestViewRenders ensures View doesn't panic across sizes/states (guards against
// negative lipgloss dimensions) and shows expected content.
func TestViewRenders(t *testing.T) {
	files := []media.Video{{Name: "test.MP4", Path: "/x/test.MP4", Size: 67254703}}
	m := New("/x", files)
	m.probeCache["/x/test.MP4"] = &media.Info{
		Width: 3840, Height: 2160, Codec: "h264", FPS: 29.97,
		Duration: 8.008, BitRate: 56949552, Size: 67254703,
	}

	for _, s := range [][2]int{{20, 10}, {50, 14}, {120, 40}, {200, 60}} {
		mm, _ := m.Update(tea.WindowSizeMsg{Width: s[0], Height: s[1]})
		out := mm.(Model).View()
		if out == "" {
			t.Errorf("%dx%d: empty view", s[0], s[1])
		}
		if s[0] < 50 && !strings.Contains(out, "too small") {
			t.Errorf("%dx%d: expected too-small message", s[0], s[1])
		}
		if s[0] >= 50 && !strings.Contains(out, "test.MP4") {
			t.Errorf("%dx%d: expected filename in view", s[0], s[1])
		}
	}
}

// TestSelectedRowNoWrap verifies the highlighted row fits exactly the pane's
// usable width (w-2) so the selection highlight never wraps onto a blank line.
func TestSelectedRowNoWrap(t *testing.T) {
	files := []media.Video{
		{Name: "a-very-long-filename-that-would-overflow-the-narrow-pane.MP4", Path: "/x/a.MP4", Size: 1234567},
	}
	m := New("/x", files)
	m, _ = updWin(m, 120, 40)

	leftInner := m.leftOuterWidth() - 4
	avail := leftInner - 2

	// The list body's lines must each be <= avail wide (visible runes), so the
	// styled background can't spill past the content area and wrap.
	for _, line := range strings.Split(m.viewList(leftInner), "\n") {
		if w := lipgloss.Width(line); w > avail {
			t.Errorf("list line width %d exceeds usable %d: %q", w, avail, line)
		}
	}
}

func updWin(m Model, w, h int) (Model, tea.Cmd) {
	mm, cmd := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return mm.(Model), cmd
}

// TestStateFlow walks browsing -> action menu -> resize settings -> confirm row.
func TestStateFlow(t *testing.T) {
	files := []media.Video{{Name: "test.MP4", Path: "/x/test.MP4", Size: 1}}
	m := New("/x", files)
	m.probeCache["/x/test.MP4"] = &media.Info{Width: 3840, Height: 2160, Duration: 8}
	m, _ = updWin(m, 120, 40)

	m, _ = updStr(m, "enter")
	if m.state != stateActionMenu {
		t.Fatalf("expected action menu, got %v", m.state)
	}
	m, _ = updStr(m, "r")
	if m.state != stateResizeSettings {
		t.Fatalf("expected resize settings, got %v", m.state)
	}
	if len(m.rungs) == 0 {
		t.Fatal("expected rungs computed")
	}
	if m.qualityCursor != media.DefaultQualityIndex {
		t.Errorf("default quality not preselected: %d", m.qualityCursor)
	}
	m, _ = updStr(m, "down")
	m, _ = updStr(m, "down")
	if m.settingsField != fieldConfirm {
		t.Errorf("expected confirm field, got %d", m.settingsField)
	}
	m, _ = updStr(m, "esc")
	if m.state != stateActionMenu {
		t.Errorf("esc should return to action menu, got %v", m.state)
	}
}

func updStr(m Model, s string) (Model, tea.Cmd) {
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return mm.(Model), cmd
}

func updKey(m Model, t tea.KeyType) (Model, tea.Cmd) {
	mm, cmd := m.Update(tea.KeyMsg{Type: t})
	return mm.(Model), cmd
}

// threeUniform builds a model with three same-sized, same-codec videos probed.
func threeUniform() Model {
	files := []media.Video{
		{Name: "a.MP4", Path: "/x/a.MP4", Size: 1},
		{Name: "b.MP4", Path: "/x/b.MP4", Size: 1},
		{Name: "c.MP4", Path: "/x/c.MP4", Size: 1},
	}
	m := New("/x", files)
	for _, f := range files {
		m.probeCache[f.Path] = &media.Info{Width: 3840, Height: 2160, Codec: "h264", Duration: 8}
	}
	m, _ = updWin(m, 120, 40)
	return m
}

// TestViewRendersBatchStates ensures the new batch states render without panic
// across sizes (negative lipgloss dimensions) and don't overflow.
func TestViewRendersBatchStates(t *testing.T) {
	base := threeUniform()
	base, _ = updKey(base, tea.KeyShiftDown)
	base, _ = updKey(base, tea.KeyShiftDown) // 3 selected

	states := []func() Model{
		func() Model { m := base; m.state = stateBatchProbing; return m },
		func() Model {
			m := base
			m.batch = true
			m.state = stateResizeSettings
			m.rungs = media.AvailableRungs(3840, 2160)
			return m
		},
		func() Model {
			m := base
			m.batch = true
			m.state = stateBatchConverting
			m.batchQueue = []int{0, 1, 2}
			m.batchTotal = 3
			m.batchPos = 1
			return m
		},
		func() Model { m := base; m.state = stateNotice; m.notice = "videos differ\nin dimensions"; return m },
		func() Model {
			m := base
			m.batch = true
			m.state = stateDone
			m.batchTotal = 3
			m.batchResults = []batchResult{{name: "a.MP4", out: "/x/a_720p.mp4"}, {name: "b.MP4", err: context.Canceled}}
			return m
		},
	}
	for _, mk := range states {
		for _, s := range [][2]int{{50, 14}, {120, 40}, {200, 60}} {
			m, _ := updWin(mk(), s[0], s[1])
			if out := m.View(); out == "" {
				t.Errorf("%dx%d: empty view in state %v", s[0], s[1], m.state)
			}
		}
	}
}

// TestBatchEndToEnd runs a real two-file batch through the full event loop with
// ffmpeg, asserting both outputs are produced. Skipped when ffmpeg is absent.
func TestBatchEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	dir := t.TempDir()
	for _, name := range []string{"a.mp4", "b.mp4"} {
		gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc=duration=1:size=1280x720:rate=10",
			"-pix_fmt", "yuv420p", "-c:v", "libx264", "-y", filepath.Join(dir, name))
		if out, err := gen.CombinedOutput(); err != nil {
			t.Skipf("could not generate fixture %s: %v\n%s", name, err, out)
		}
	}

	files, err := media.ListVideos(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("ListVideos: %d files, err=%v", len(files), err)
	}
	m := New(dir, files)
	for _, f := range files {
		info, perr := media.Probe(f.Path)
		if perr != nil {
			t.Fatalf("probe %s: %v", f.Name, perr)
		}
		m.probeCache[f.Path] = info
	}
	m, _ = updWin(m, 120, 40)

	// Select both, open batch settings, pick a smaller size, confirm.
	m, _ = updKey(m, tea.KeyShiftDown)
	m, _ = updStr(m, "enter")
	if m.state != stateResizeSettings || !m.batch {
		t.Fatalf("expected batch settings, got state=%v batch=%v", m.state, m.batch)
	}
	m, _ = updStr(m, "down") // Size -> Quality
	m, _ = updStr(m, "down") // -> Confirm
	m, cmd := updStr(m, "enter")
	if m.state != stateBatchConverting {
		t.Fatalf("expected batch converting, got %v", m.state)
	}

	// Pump the event loop: execute the returned cmd, feed its msg back, repeat.
	for cmd != nil && m.state == stateBatchConverting {
		tm, c := m.Update(cmd())
		m, cmd = tm.(Model), c
	}

	if m.state != stateDone {
		t.Fatalf("batch did not finish: state=%v", m.state)
	}
	var ok int
	for _, r := range m.batchResults {
		if r.err == nil {
			ok++
		}
	}
	if ok != 2 {
		t.Fatalf("expected 2 successful conversions, got %d (%+v)", ok, m.batchResults)
	}
}

// TestShiftSelectRange verifies shift+arrow builds a contiguous range that a
// plain arrow collapses.
func TestShiftSelectRange(t *testing.T) {
	m := threeUniform()
	if m.selCount() != 1 {
		t.Fatalf("initial selCount = %d, want 1", m.selCount())
	}
	m, _ = updKey(m, tea.KeyShiftDown)
	m, _ = updKey(m, tea.KeyShiftDown)
	if m.selCount() != 3 {
		t.Fatalf("after 2x shift+down selCount = %d, want 3", m.selCount())
	}
	if got := m.selectedIndices(); len(got) != 3 || got[0] != 0 || got[2] != 2 {
		t.Fatalf("selectedIndices = %v, want [0 1 2]", got)
	}
	m, _ = updKey(m, tea.KeyUp) // plain arrow collapses
	if m.selCount() != 1 {
		t.Errorf("after plain up selCount = %d, want 1", m.selCount())
	}
}

// TestBatchUniformOpensSettings verifies a uniform group routes Enter straight
// into batch resize settings.
func TestBatchUniformOpensSettings(t *testing.T) {
	m := threeUniform()
	m, _ = updKey(m, tea.KeyShiftDown)
	m, _ = updKey(m, tea.KeyShiftDown)
	m, _ = updStr(m, "enter")
	if m.state != stateResizeSettings || !m.batch {
		t.Fatalf("state=%v batch=%v, want resize settings + batch", m.state, m.batch)
	}
	if len(m.rungs) == 0 {
		t.Error("expected rungs computed from common dimensions")
	}
}

// TestBatchMixedBlocked verifies a non-uniform group is blocked with a notice.
func TestBatchMixedBlocked(t *testing.T) {
	files := []media.Video{
		{Name: "a.MP4", Path: "/x/a.MP4", Size: 1},
		{Name: "b.MP4", Path: "/x/b.MP4", Size: 1},
	}
	m := New("/x", files)
	m.probeCache["/x/a.MP4"] = &media.Info{Width: 3840, Height: 2160, Codec: "h264", Duration: 8}
	m.probeCache["/x/b.MP4"] = &media.Info{Width: 1920, Height: 1080, Codec: "h264", Duration: 8}
	m, _ = updWin(m, 120, 40)

	m, _ = updKey(m, tea.KeyShiftDown)
	m, _ = updStr(m, "enter")
	if m.state != stateNotice {
		t.Fatalf("state=%v, want notice for mixed group", m.state)
	}
}

// TestBatchDoneSummaryAndReset drives the terminal Done of a batch and verifies
// the summary plus the lifecycle reset on return.
func TestBatchDoneSummaryAndReset(t *testing.T) {
	m := threeUniform()
	// Simulate being on the last file of a two-file batch.
	m.batch = true
	m.state = stateBatchConverting
	m.batchQueue = []int{0, 1}
	m.batchTotal = 2
	m.batchPos = 1
	m.outPath = "/x/b_720p.mp4"
	m.batchResults = []batchResult{{name: "a.MP4", out: "/x/a_720p.mp4"}}

	mm, _ := m.Update(media.Done{Err: nil})
	m = mm.(Model)
	if m.state != stateDone {
		t.Fatalf("after final Done state=%v, want done", m.state)
	}
	if len(m.batchResults) != 2 {
		t.Fatalf("batchResults = %d, want 2", len(m.batchResults))
	}
	// Returning must clear batch lifecycle state.
	m, _ = updStr(m, "enter")
	if m.state != stateBrowsing || m.batch || m.anchor != -1 || m.batchQueue != nil {
		t.Errorf("after return: state=%v batch=%v anchor=%d queue=%v", m.state, m.batch, m.anchor, m.batchQueue)
	}
}

// TestBatchCancelStops verifies a user cancel mid-batch ends the run.
func TestBatchCancelStops(t *testing.T) {
	m := threeUniform()
	m.batch = true
	m.state = stateBatchConverting
	m.batchQueue = []int{0, 1, 2}
	m.batchTotal = 3
	m.batchPos = 0
	m.batchResults = []batchResult{}

	// esc requests cancel; the canceled Done then stops the batch.
	m, _ = updStr(m, "esc")
	if !m.batchCancel {
		t.Fatal("esc should set batchCancel")
	}
	cm, _ := m.Update(media.Done{Err: context.Canceled})
	m = cm.(Model)
	if m.state != stateDone {
		t.Fatalf("after canceled Done state=%v, want done", m.state)
	}
	if m.batchPos != 0 { // did not advance past the canceled file
		t.Errorf("batchPos advanced to %d on cancel", m.batchPos)
	}
}
