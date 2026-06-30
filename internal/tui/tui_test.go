package tui

import (
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
