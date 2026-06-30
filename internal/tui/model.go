// Package tui implements the interactive two-pane terminal UI.
package tui

import (
	"context"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/nbw/vids/internal/media"
)

type state int

const (
	stateBrowsing state = iota
	stateActionMenu
	stateResizeSettings
	stateConverting
	stateDone
)

// action menu items (v1: only Resize is enabled).
type action struct {
	key     string
	label   string
	enabled bool
}

var actions = []action{
	{"r", "Resize", true},
	{"c", "Convert format", false},
}

// resize settings fields.
const (
	fieldSize = iota
	fieldQuality
	fieldConfirm
	fieldCount
)

type probedMsg struct {
	path string
	info *media.Info
	err  error
}

// Model is the Bubble Tea model for vids.
type Model struct {
	dir   string
	files []media.Video

	width  int
	height int

	state  state
	cursor int // index into files (browsing)

	// metadata cache, keyed by path.
	probeCache map[string]*media.Info
	probeErr   map[string]string

	// action menu
	actionCursor int

	// resize settings
	rungs         []media.Rung
	sizeCursor    int
	qualityCursor int
	settingsField int

	// converting
	prog       progress.Model
	frac       float64
	speed      string
	eta        string
	convCh     chan any
	convCancel context.CancelFunc
	outPath    string

	// done
	doneErr  error
	doneNote string
}

// New builds the initial model for dir with the given files.
func New(dir string, files []media.Video) Model {
	return Model{
		dir:        dir,
		files:      files,
		state:      stateBrowsing,
		probeCache: map[string]*media.Info{},
		probeErr:   map[string]string{},
		prog:       progress.New(progress.WithDefaultGradient()),
	}
}

func (m Model) Init() tea.Cmd {
	if len(m.files) == 0 {
		return nil
	}
	return probeCmd(m.files[0].Path)
}

func probeCmd(path string) tea.Cmd {
	return func() tea.Msg {
		info, err := media.Probe(path)
		return probedMsg{path: path, info: info, err: err}
	}
}

// probeCurrent issues a probe for the highlighted file if not cached.
func (m Model) probeCurrent() tea.Cmd {
	if len(m.files) == 0 {
		return nil
	}
	p := m.files[m.cursor].Path
	if _, ok := m.probeCache[p]; ok {
		return nil
	}
	if _, ok := m.probeErr[p]; ok {
		return nil
	}
	return probeCmd(p)
}

func waitForConvMsg(ch chan any) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.prog.Width = max(10, m.rightInnerWidth()-2)
		return m, nil

	case probedMsg:
		if msg.err != nil {
			m.probeErr[msg.path] = msg.err.Error()
		} else {
			m.probeCache[msg.path] = msg.info
		}
		return m, nil

	case media.Progress:
		m.frac = msg.Frac
		m.speed = msg.Speed
		m.eta = msg.ETA
		return m, waitForConvMsg(m.convCh)

	case media.Done:
		m.state = stateDone
		if msg.Err == context.Canceled {
			m.doneErr = nil
			m.doneNote = "Canceled."
		} else if msg.Err != nil {
			m.doneErr = msg.Err
		} else {
			m.doneErr = nil
			m.doneNote = "Saved " + m.outPath
		}
		if files, err := media.ListVideos(m.dir); err == nil {
			m.files = files // refresh so any new output appears
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		m.cancelConversion()
		return m, tea.Quit
	}

	switch m.state {
	case stateBrowsing:
		return m.keyBrowsing(key)
	case stateActionMenu:
		return m.keyActionMenu(key)
	case stateResizeSettings:
		return m.keyResizeSettings(key)
	case stateConverting:
		if key == "esc" || key == "q" {
			m.cancelConversion() // convDone(canceled) will arrive
		}
		return m, nil
	case stateDone:
		if key == "enter" || key == "esc" || key == "q" {
			m.state = stateBrowsing
			m.doneErr = nil
			m.doneNote = ""
			if m.cursor >= len(m.files) {
				m.cursor = max(0, len(m.files)-1)
			}
			return m, m.probeCurrent()
		}
		return m, nil
	}
	return m, nil
}

func (m Model) keyBrowsing(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.probeCurrent()
	case "down", "j":
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
		return m, m.probeCurrent()
	case "enter":
		if len(m.files) == 0 {
			return m, nil
		}
		m.state = stateActionMenu
		m.actionCursor = 0
		return m, nil
	}
	return m, nil
}

func (m Model) keyActionMenu(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.state = stateBrowsing
		return m, nil
	case "up", "k":
		if m.actionCursor > 0 {
			m.actionCursor--
		}
		return m, nil
	case "down", "j":
		if m.actionCursor < len(actions)-1 {
			m.actionCursor++
		}
		return m, nil
	case "r":
		return m.enterResize()
	case "enter":
		if actions[m.actionCursor].enabled && actions[m.actionCursor].key == "r" {
			return m.enterResize()
		}
		return m, nil
	}
	return m, nil
}

// enterResize opens the resize settings, computing rungs from the probed size.
func (m Model) enterResize() (tea.Model, tea.Cmd) {
	info := m.currentProbe()
	if info == nil {
		return m, m.probeCurrent() // metadata not ready; stay put
	}
	m.rungs = media.AvailableRungs(info.Width, info.Height)
	m.sizeCursor = 0
	m.qualityCursor = media.DefaultQualityIndex
	m.settingsField = fieldSize
	m.state = stateResizeSettings
	return m, nil
}

func (m Model) keyResizeSettings(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.state = stateActionMenu
		return m, nil
	case "up", "k":
		if m.settingsField > 0 {
			m.settingsField--
		}
		return m, nil
	case "down", "j":
		if m.settingsField < fieldCount-1 {
			m.settingsField++
		}
		return m, nil
	case "left", "h":
		m.adjustField(-1)
		return m, nil
	case "right", "l":
		m.adjustField(1)
		return m, nil
	case "enter":
		if m.settingsField == fieldConfirm {
			return m.startConversion()
		}
		if m.settingsField == fieldSize || m.settingsField == fieldQuality {
			m.adjustField(1) // Enter cycles a value field for convenience
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) adjustField(delta int) {
	switch m.settingsField {
	case fieldSize:
		if len(m.rungs) == 0 {
			return
		}
		m.sizeCursor = wrap(m.sizeCursor+delta, len(m.rungs))
	case fieldQuality:
		m.qualityCursor = wrap(m.qualityCursor+delta, len(media.Qualities))
	}
}

func (m Model) startConversion() (tea.Model, tea.Cmd) {
	info := m.currentProbe()
	if info == nil || len(m.rungs) == 0 {
		return m, nil
	}
	r := m.rungs[m.sizeCursor]
	q := media.Qualities[m.qualityCursor]
	in := m.files[m.cursor].Path
	out := media.OutputPath(m.dir, m.files[m.cursor].Name, r.P)
	scale := media.ScaleFilter(info.Width, info.Height, r.P)
	args := media.FFmpegArgs(in, out, scale, q.CRF)

	ch := make(chan any, 64)
	ctx, cancel := context.WithCancel(context.Background())
	m.convCh = ch
	m.convCancel = cancel
	m.outPath = out
	m.frac = 0
	m.speed = ""
	m.eta = "--:--"
	m.state = stateConverting

	go media.Run(ctx, args, out, info.Duration, ch)
	return m, waitForConvMsg(ch)
}

func (m *Model) cancelConversion() {
	if m.convCancel != nil {
		m.convCancel()
	}
}

func (m Model) currentProbe() *media.Info {
	if len(m.files) == 0 {
		return nil
	}
	return m.probeCache[m.files[m.cursor].Path]
}

func wrap(i, n int) int {
	if n == 0 {
		return 0
	}
	return ((i % n) + n) % n
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
