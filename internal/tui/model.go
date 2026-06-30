// Package tui implements the interactive two-pane terminal UI.
package tui

import (
	"context"
	"fmt"

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
	stateBatchProbing    // waiting for metadata on a multi-selection
	stateBatchConverting // running the batch queue, one file at a time
	stateNotice          // a transient blocking message (e.g. non-uniform group)
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

	// group selection: when anchor >= 0 the selection is the inclusive range
	// between anchor and cursor; anchor == -1 means "just the cursor".
	anchor int

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

	// batch resize (set when a group of >1 videos is being processed)
	batch        bool
	batchQueue   []int // indices into files, captured at confirm
	batchPos     int   // index into batchQueue of the file being converted
	batchTotal   int
	batchP       int // target shorter-edge for every file in the batch
	batchCRF     int
	batchCancel  bool // user asked to abort the whole run
	batchResults []batchResult

	// notice
	notice string
}

// batchResult records the outcome of one file in a batch.
type batchResult struct {
	name string
	out  string
	err  error
}

// New builds the initial model for dir with the given files.
func New(dir string, files []media.Video) Model {
	return Model{
		dir:        dir,
		files:      files,
		state:      stateBrowsing,
		anchor:     -1,
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
		if m.state == stateBatchProbing && m.selectionResolved() {
			return m.evalBatchSelection()
		}
		return m, nil

	case media.Progress:
		m.frac = msg.Frac
		m.speed = msg.Speed
		m.eta = msg.ETA
		return m, waitForConvMsg(m.convCh)

	case media.Done:
		if m.state == stateBatchConverting {
			return m.batchDone(msg)
		}
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
	case stateBatchConverting:
		if key == "esc" || key == "q" {
			m.batchCancel = true
			m.cancelConversion() // canceled Done stops the run
		}
		return m, nil
	case stateBatchProbing:
		if key == "esc" || key == "q" {
			m.state = stateBrowsing // abandon; selection is kept
		}
		return m, nil
	case stateNotice:
		if key == "enter" || key == "esc" || key == "q" {
			m.state = stateBrowsing
			m.notice = ""
		}
		return m, nil
	case stateDone:
		if key == "enter" || key == "esc" || key == "q" {
			m.state = stateBrowsing
			m.doneErr = nil
			m.doneNote = ""
			// Clear batch lifecycle state; indices are stale after the refresh.
			m.batch = false
			m.batchCancel = false
			m.anchor = -1
			m.batchQueue = nil
			m.batchResults = nil
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
	case "esc":
		m.anchor = -1 // collapse any group selection
		return m, nil
	case "up", "k":
		m.anchor = -1
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.probeCurrent()
	case "down", "j":
		m.anchor = -1
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
		return m, m.probeCurrent()
	case "shift+up":
		if m.anchor < 0 {
			m.anchor = m.cursor
		}
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.probeCurrent()
	case "shift+down":
		if m.anchor < 0 {
			m.anchor = m.cursor
		}
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
		return m, m.probeCurrent()
	case "enter":
		if len(m.files) == 0 {
			return m, nil
		}
		if m.selCount() > 1 {
			return m.enterBatch()
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
		if m.batch {
			m.batch = false
			m.state = stateBrowsing // keep the selection so it can be tweaked
			return m, nil
		}
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
			if m.batch {
				return m.startBatchConversion()
			}
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

// --- batch resize ---

// enterBatch begins the group-resize flow. If metadata for every selected file
// is already available it evaluates immediately; otherwise it probes the
// missing ones and waits in stateBatchProbing.
func (m Model) enterBatch() (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for _, i := range m.selectedIndices() {
		p := m.files[i].Path
		_, cached := m.probeCache[p]
		_, errored := m.probeErr[p]
		if !cached && !errored {
			cmds = append(cmds, probeCmd(p))
		}
	}
	if len(cmds) == 0 {
		return m.evalBatchSelection()
	}
	m.state = stateBatchProbing
	return m, tea.Batch(cmds...)
}

// selectionResolved reports whether every selected file has either probed
// metadata or a recorded probe error.
func (m Model) selectionResolved() bool {
	for _, i := range m.selectedIndices() {
		p := m.files[i].Path
		_, cached := m.probeCache[p]
		_, errored := m.probeErr[p]
		if !cached && !errored {
			return false
		}
	}
	return true
}

// evalBatchSelection inspects the resolved selection: it blocks with a notice if
// any probe failed or the group isn't uniform, otherwise it opens the resize
// settings in batch mode.
func (m Model) evalBatchSelection() (tea.Model, tea.Cmd) {
	idx := m.selectedIndices()

	var first *media.Info
	for _, i := range idx {
		f := m.files[i]
		if _, ok := m.probeErr[f.Path]; ok {
			m.state = stateNotice
			m.notice = "Couldn't read metadata for one or more selected videos. Narrow the selection and try again."
			return m, nil
		}
		info := m.probeCache[f.Path]
		if info == nil {
			// Shouldn't happen once resolved, but guard anyway.
			m.state = stateNotice
			m.notice = "Metadata is still loading. Try again in a moment."
			return m, nil
		}
		if first == nil {
			first = info
		} else if info.Width != first.Width || info.Height != first.Height || info.Codec != first.Codec {
			m.state = stateNotice
			m.notice = fmt.Sprintf("Selected videos aren't uniform (mixed dimensions or codec). Batch resize needs them to share the same format and size.\n\nReference: %d x %d %s.", first.Width, first.Height, first.Codec)
			return m, nil
		}
	}

	m.rungs = media.AvailableRungs(first.Width, first.Height)
	m.sizeCursor = 0
	m.qualityCursor = media.DefaultQualityIndex
	m.settingsField = fieldSize
	m.batch = true
	m.state = stateResizeSettings
	return m, nil
}

// startBatchConversion captures the chosen target and kicks off the queue.
func (m Model) startBatchConversion() (tea.Model, tea.Cmd) {
	if len(m.rungs) == 0 {
		return m, nil
	}
	m.batchQueue = m.selectedIndices()
	m.batchTotal = len(m.batchQueue)
	m.batchPos = 0
	m.batchP = m.rungs[m.sizeCursor].P
	m.batchCRF = media.Qualities[m.qualityCursor].CRF
	m.batchCancel = false
	m.batchResults = nil
	m.state = stateBatchConverting
	return m.startBatchFile()
}

// startBatchFile launches ffmpeg for the file at batchPos and installs the wait
// for its messages. It is the sole issuer of waitForConvMsg during a batch.
func (m Model) startBatchFile() (tea.Model, tea.Cmd) {
	f := m.files[m.batchQueue[m.batchPos]]
	info := m.probeCache[f.Path]
	if info == nil {
		// Defensive: skip a file whose metadata vanished.
		m.batchResults = append(m.batchResults, batchResult{name: f.Name, err: fmt.Errorf("metadata unavailable")})
		return m.advanceBatch()
	}
	out := media.OutputPath(m.dir, f.Name, m.batchP)
	scale := media.ScaleFilter(info.Width, info.Height, m.batchP)
	args := media.FFmpegArgs(f.Path, out, scale, m.batchCRF)

	ch := make(chan any, 64)
	ctx, cancel := context.WithCancel(context.Background())
	m.convCh = ch
	m.convCancel = cancel
	m.outPath = out
	m.frac = 0
	m.speed = ""
	m.eta = "--:--"

	go media.Run(ctx, args, out, info.Duration, ch)
	return m, waitForConvMsg(ch)
}

// advanceBatch moves to the next queued file, or finishes the batch. It must
// return the next wait (via startBatchFile) so exactly one wait is outstanding.
func (m Model) advanceBatch() (tea.Model, tea.Cmd) {
	m.batchPos++
	if m.batchPos < len(m.batchQueue) {
		return m.startBatchFile()
	}
	return m.finishBatch(), nil
}

// batchDone records the current file's outcome and either advances the queue or
// finishes (on user cancel). It never re-waits except through startBatchFile.
func (m Model) batchDone(msg media.Done) (tea.Model, tea.Cmd) {
	cur := m.files[m.batchQueue[m.batchPos]]
	switch {
	case msg.Err == context.Canceled:
		// Canceled file leaves no result row; stop the whole run.
		return m.finishBatch(), nil
	case msg.Err != nil:
		m.batchResults = append(m.batchResults, batchResult{name: cur.Name, err: msg.Err})
	default:
		m.batchResults = append(m.batchResults, batchResult{name: cur.Name, out: m.outPath})
	}
	if m.batchCancel {
		return m.finishBatch(), nil
	}
	return m.advanceBatch()
}

// finishBatch refreshes the file list and shows the summary.
func (m Model) finishBatch() Model {
	if files, err := media.ListVideos(m.dir); err == nil {
		m.files = files
	}
	m.state = stateDone
	return m
}

func (m Model) currentProbe() *media.Info {
	if len(m.files) == 0 {
		return nil
	}
	return m.probeCache[m.files[m.cursor].Path]
}

// selRange returns the inclusive [lo, hi] index range of the current selection.
func (m Model) selRange() (lo, hi int) {
	if m.anchor < 0 {
		return m.cursor, m.cursor
	}
	if m.anchor <= m.cursor {
		return m.anchor, m.cursor
	}
	return m.cursor, m.anchor
}

// selCount is the number of selected files.
func (m Model) selCount() int {
	lo, hi := m.selRange()
	return hi - lo + 1
}

// selectedIndices lists the selected file indices in order.
func (m Model) selectedIndices() []int {
	lo, hi := m.selRange()
	out := make([]int, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		out = append(out, i)
	}
	return out
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
