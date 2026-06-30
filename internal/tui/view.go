package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/nbw/vids/internal/media"
)

func (m Model) rightInnerWidth() int {
	rightOuter := m.width - m.leftOuterWidth()
	return max(10, rightOuter-4) // borders(2) + padding(2)
}

func (m Model) leftOuterWidth() int {
	w := m.width / 3
	if w < 26 {
		w = 26
	}
	if w > m.width-20 {
		w = max(20, m.width-20)
	}
	return w
}

func (m Model) View() string {
	if m.width < 50 || m.height < 14 {
		return "Terminal too small — please enlarge the window (min 50x14)."
	}

	leftOuter := m.leftOuterWidth()
	rightOuter := m.width - leftOuter
	innerH := m.height - 3 // footer line + top/bottom border
	leftInner := leftOuter - 4
	rightInner := rightOuter - 4

	left := pane(leftInner, innerH, m.state == stateBrowsing).Render(m.viewList(leftInner))
	right := pane(rightInner, innerH, m.state != stateBrowsing).Render(m.viewRight(rightInner))

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return body + "\n" + footerStyle.Render(m.footer())
}

// viewList renders the left pane. w is the pane's Width; the usable text width
// is w-2 because the pane has 1 column of padding on each side. Every row is
// built to exactly that width so the selection highlight never wraps.
func (m Model) viewList(w int) string {
	avail := max(1, w-2)

	var b strings.Builder
	b.WriteString(titleStyle.Render("vids") + dimStyle.Render("  "+shorten(m.dir, avail-6)) + "\n\n")
	if len(m.files) == 0 {
		b.WriteString(mutedStyle.Render("No videos found here."))
		return b.String()
	}
	lo, hi := m.selRange()
	for i, f := range m.files {
		text := fmt.Sprintf("%s  %s", f.Name, humanSize(f.Size))
		text = shorten(text, avail-2) // leave 2 columns for the gutter
		switch {
		case i == m.cursor:
			row := padRight("> "+text, avail)
			b.WriteString(selectedStyle.Render(row) + "\n")
		case m.anchor >= 0 && i >= lo && i <= hi:
			b.WriteString(markedStyle.Render("▌ "+text) + "\n")
		default:
			b.WriteString("  " + text + "\n")
		}
	}
	return b.String()
}

func (m Model) viewRight(w int) string {
	var b strings.Builder

	if m.isBatchContext() {
		b.WriteString(m.viewBatchHeader(w))
	} else if len(m.files) > 0 {
		// Metadata header for the highlighted file.
		f := m.files[m.cursor]
		b.WriteString(titleStyle.Render(shorten(f.Name, w-2)) + "\n")
		if info := m.currentProbe(); info != nil {
			b.WriteString(metaLine("Dimensions", fmt.Sprintf("%d x %d", info.Width, info.Height)))
			b.WriteString(metaLine("Codec", info.Codec))
			b.WriteString(metaLine("Duration", media.FormatClock(info.Duration)))
			b.WriteString(metaLine("FPS", fmt.Sprintf("%.2f", info.FPS)))
			b.WriteString(metaLine("Bitrate", humanBitrate(info.BitRate)))
			b.WriteString(metaLine("Size", humanSize(info.Size)))
		} else if e, ok := m.probeErr[f.Path]; ok {
			b.WriteString(badStyle.Render("metadata unavailable") + "\n")
			b.WriteString(dimStyle.Render(shorten(e, w-2)) + "\n")
		} else {
			b.WriteString(mutedStyle.Render("reading metadata…") + "\n")
		}
	}

	b.WriteString("\n")

	switch m.state {
	case stateActionMenu:
		b.WriteString(m.viewActionMenu())
	case stateResizeSettings:
		b.WriteString(m.viewResizeSettings())
	case stateConverting:
		b.WriteString(m.viewConverting())
	case stateBatchProbing:
		b.WriteString(mutedStyle.Render("Reading metadata…"))
	case stateBatchConverting:
		b.WriteString(m.viewBatchConverting())
	case stateNotice:
		b.WriteString(m.viewNotice(w))
	case stateDone:
		b.WriteString(m.viewDone(w))
	default:
		b.WriteString(dimStyle.Render("Press Enter to choose an action."))
	}
	return b.String()
}

// isBatchContext reports whether the right pane should show the group header
// (count + shared traits) instead of the single highlighted file's metadata.
func (m Model) isBatchContext() bool {
	switch m.state {
	case stateBatchProbing, stateBatchConverting, stateNotice:
		return true
	case stateResizeSettings, stateDone:
		return m.batch
	}
	return false
}

// viewBatchHeader shows the selection count and the traits shared by the group.
func (m Model) viewBatchHeader(w int) string {
	var b strings.Builder
	idx := m.selectedIndices()
	b.WriteString(titleStyle.Render(fmt.Sprintf("%d videos selected", len(idx))) + "\n")
	if len(idx) > 0 {
		if info := m.probeCache[m.files[idx[0]].Path]; info != nil {
			b.WriteString(metaLine("Dimensions", fmt.Sprintf("%d x %d", info.Width, info.Height)))
			b.WriteString(metaLine("Codec", info.Codec))
		}
	}
	return b.String()
}

func (m Model) viewActionMenu() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Action") + "\n")
	for i, a := range actions {
		cursor := "  "
		if i == m.actionCursor {
			cursor = "> "
		}
		label := fmt.Sprintf("%s [%s]", a.label, strings.ToUpper(a.key))
		switch {
		case !a.enabled:
			label = disabledStyle.Render(a.label) + dimStyle.Render(" (coming soon)")
		case i == m.actionCursor:
			label = fieldFocused.Render(label)
		}
		b.WriteString(cursor + label + "\n")
	}
	return b.String()
}

func (m Model) viewResizeSettings() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Resize") + "\n\n")

	sizeVal := "no smaller preset available"
	if len(m.rungs) > 0 {
		r := m.rungs[m.sizeCursor]
		sizeVal = fmt.Sprintf("%s  (%d x %d)", r.Label(), r.W, r.H)
	}
	b.WriteString(settingRow("Size", sizeVal, m.settingsField == fieldSize))

	q := media.Qualities[m.qualityCursor]
	qval := fmt.Sprintf("%s  (CRF %d)", q.Name, q.CRF)
	if m.qualityCursor == media.DefaultQualityIndex {
		qval += dimStyle.Render("  default")
	}
	b.WriteString(settingRow("Quality", qval, m.settingsField == fieldQuality))

	if len(m.rungs) > 0 {
		if m.batch {
			b.WriteString(settingRow("Output", fmt.Sprintf("%d files, suffixed _%dp.mp4", m.selCount(), m.rungs[m.sizeCursor].P), false))
		} else {
			out := media.OutputPath(m.dir, m.files[m.cursor].Name, m.rungs[m.sizeCursor].P)
			b.WriteString(settingRow("Output", filepath.Base(out), false))
		}
	}

	label := "Confirm & Convert"
	if m.batch {
		label = fmt.Sprintf("Confirm & Convert %d", m.selCount())
	}
	b.WriteString("\n")
	confirm := "  " + label
	switch {
	case len(m.rungs) == 0:
		confirm = disabledStyle.Render("  " + label)
	case m.settingsField == fieldConfirm:
		confirm = fieldFocused.Render("> " + label)
	}
	b.WriteString(confirm + "\n")
	return b.String()
}

func (m Model) viewBatchConverting() string {
	var b strings.Builder
	name := ""
	if m.batchPos < len(m.batchQueue) {
		name = m.files[m.batchQueue[m.batchPos]].Name
	}
	b.WriteString(labelStyle.Render(fmt.Sprintf("Converting %d/%d", m.batchPos+1, m.batchTotal)) + "\n")
	b.WriteString(dimStyle.Render(name) + "\n\n")
	b.WriteString(m.prog.ViewAs(m.frac) + "\n\n")
	b.WriteString(metaLine("Progress", fmt.Sprintf("%.0f%%", m.frac*100)))
	if m.speed != "" {
		b.WriteString(metaLine("Speed", m.speed))
	}
	b.WriteString(metaLine("ETA", m.eta))
	return b.String()
}

func (m Model) viewNotice(w int) string {
	var b strings.Builder
	b.WriteString(badStyle.Render("Can't batch this selection") + "\n\n")
	for _, line := range strings.Split(m.notice, "\n") {
		b.WriteString(dimStyle.Render(shorten(line, w-2)) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Enter to return."))
	return b.String()
}

func (m Model) viewConverting() string {
	var b strings.Builder
	b.WriteString(labelStyle.Render("Converting…") + "\n\n")
	b.WriteString(m.prog.ViewAs(m.frac) + "\n\n")
	b.WriteString(metaLine("Progress", fmt.Sprintf("%.0f%%", m.frac*100)))
	if m.speed != "" {
		b.WriteString(metaLine("Speed", m.speed))
	}
	b.WriteString(metaLine("ETA", m.eta))
	return b.String()
}

func (m Model) viewDone(w int) string {
	if m.batch {
		return m.viewBatchDone(w)
	}
	var b strings.Builder
	if m.doneErr != nil {
		b.WriteString(badStyle.Render("Conversion failed") + "\n")
		b.WriteString(dimStyle.Render(shorten(m.doneErr.Error(), (w-2)*3)) + "\n")
	} else {
		b.WriteString(goodStyle.Render("✓ Done") + "\n")
		b.WriteString(m.doneNote + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Enter to return."))
	return b.String()
}

func (m Model) viewBatchDone(w int) string {
	var b strings.Builder
	var ok int
	var failed []batchResult
	for _, r := range m.batchResults {
		if r.err != nil {
			failed = append(failed, r)
		} else {
			ok++
		}
	}

	header := fmt.Sprintf("✓ %d done", ok)
	if len(failed) > 0 {
		header += fmt.Sprintf(", %d failed", len(failed))
	}
	b.WriteString(goodStyle.Render(header) + "\n")
	if m.batchCancel {
		b.WriteString(dimStyle.Render(fmt.Sprintf("Canceled after %d of %d.", len(m.batchResults), m.batchTotal)) + "\n")
	}

	// List failures, bounded so the summary can't overflow the pane.
	const maxList = 6
	for i, r := range failed {
		if i == maxList {
			b.WriteString(dimStyle.Render(fmt.Sprintf("…and %d more.", len(failed)-maxList)) + "\n")
			break
		}
		b.WriteString(badStyle.Render(shorten(r.name, w-2)) + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("Enter to return."))
	return b.String()
}

func (m Model) footer() string {
	switch m.state {
	case stateBrowsing:
		if m.selCount() > 1 {
			return fmt.Sprintf("shift+↑/↓ select   enter resize %d   esc clear   q quit", m.selCount())
		}
		return "↑/↓ navigate   shift+↑/↓ select   enter select   q quit"
	case stateActionMenu:
		return "↑/↓ move   enter/R choose   esc back"
	case stateResizeSettings:
		return "↑/↓ field   ←/→ change   enter confirm   esc back"
	case stateConverting:
		return "esc cancel"
	case stateBatchProbing:
		return "reading metadata…   esc cancel"
	case stateBatchConverting:
		return "esc cancel batch"
	case stateNotice:
		return "enter return"
	case stateDone:
		return "enter return"
	}
	return ""
}

func metaLine(label, val string) string {
	return labelStyle.Render(fmt.Sprintf("%-11s", label)) + " " + val + "\n"
}

func settingRow(label, val string, focused bool) string {
	cursor := "  "
	l := labelStyle.Render(fmt.Sprintf("%-9s", label))
	if focused {
		cursor = "> "
		l = fieldFocused.Render(fmt.Sprintf("%-9s", label))
	}
	return cursor + l + " " + val + "\n"
}

// ---------- display helpers ----------

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func humanBitrate(bps int64) string {
	if bps <= 0 {
		return "unknown"
	}
	if bps >= 1_000_000 {
		return fmt.Sprintf("%.1f Mbit/s", float64(bps)/1_000_000)
	}
	return fmt.Sprintf("%.0f kbit/s", float64(bps)/1000)
}

func shorten(s string, w int) string {
	if w <= 1 {
		return ""
	}
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func padRight(s string, w int) string {
	r := []rune(s)
	if len(r) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(r))
}
