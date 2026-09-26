package tui

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"rocina/internal/bus"
	"rocina/internal/config"
	"rocina/internal/llm"
	"rocina/internal/session"
	"rocina/internal/tools"
)

const frameRate = time.Second / 20

type eventMsg struct {
	session string
	event   bus.Event
}

// eventBatch carries every event that arrived since the last frame, so a burst
// from several busy agents is applied in one update and rendered once.
type eventBatch []eventMsg

type approvalRequest struct {
	approval tools.Approval
	reply    chan bool
}

type approvalMsg struct {
	request approvalRequest
}

type modelsMsg struct {
	models []string
	err    error
}

type refreshMsg struct{}
type frameMsg struct{}
type errMsg error

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlaySessions
	overlaySettings
	overlayApproval
	overlayModels
	overlayStop
)

type stopOption struct {
	kind  string
	label string
}

type sessionView struct {
	session  *session.Session
	chats    map[string][]Block
	expanded map[string]bool
	cwd      map[string]string
	spawn    map[string]float64
	focus    int
	focusPos float64
	scroll   int
	ease     float64
	started  bool
	dirty    bool
}

type Model struct {
	mgr *session.Manager

	width  int
	height int
	ready  bool
	frame  int
	err    error

	views  []*sessionView
	active int

	input   textarea.Model
	spinner spinner.Model

	eventCh   chan eventMsg
	approvals chan approvalRequest

	overlay       overlayKind
	shownOverlay  overlayKind
	overlayFrame  int
	approval      *approvalRequest
	sessionCursor int

	stopMenu      []stopOption
	stopCursor    int
	stopSubmenu   bool
	stopSubCursor int
	lastEsc       time.Time

	userDir string

	models      []string
	modelsErr   string
	modelCursor int
	modelTarget int

	settings settingsState
}

func New(mgr *session.Manager) Model {
	input := textarea.New()
	input.Placeholder = "message the chief…"
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.CharLimit = 8000
	input.SetHeight(3)
	input.SetPromptFunc(2, func(line int) string {
		if line == 0 {
			return "❯ "
		}
		return "  "
	})
	input.KeyMap.InsertNewline.SetKeys("shift+enter", "ctrl+j")
	surface := lipgloss.NewStyle().Background(lipgloss.Color(colorSurface.hex()))
	input.FocusedStyle.Base = surface
	input.FocusedStyle.CursorLine = surface
	input.FocusedStyle.Placeholder = faintStyle
	input.FocusedStyle.Text = textStyle
	input.FocusedStyle.Prompt = fgStyle(plum)
	input.FocusedStyle.LineNumber = faintStyle
	input.FocusedStyle.EndOfBuffer = surface
	input.BlurredStyle = input.FocusedStyle
	input.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	input.Cursor.TextStyle = textStyle
	input.Focus()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = labelStyle

	userDir, _ := os.Getwd()

	m := Model{
		mgr:       mgr,
		input:     input,
		spinner:   sp,
		userDir:   userDir,
		eventCh:   make(chan eventMsg, 4096),
		approvals: make(chan approvalRequest, 16),
		settings:  newSettings(mgr.Config()),
	}
	for _, s := range mgr.List() {
		m.views = append(m.views, m.attach(s))
	}
	return m
}

func (m *Model) attach(s *session.Session) *sessionView {
	view := &sessionView{session: s, chats: loadChats(s), expanded: map[string]bool{}, cwd: map[string]string{}, spawn: map[string]float64{}}
	go func(id string, events <-chan bus.Event) {
		for ev := range events {
			m.eventCh <- eventMsg{session: id, event: ev}
		}
	}(s.ID, s.Orch.Bus().Events())
	s.Orch.SetApprover(m.approver())
	return view
}

func (m Model) approver() tools.Approver {
	return func(a tools.Approval) bool {
		reply := make(chan bool, 1)
		select {
		case m.approvals <- approvalRequest{approval: a, reply: reply}:
		case <-time.After(5 * time.Second):
			return false
		}
		select {
		case allow := <-reply:
			return allow
		case <-time.After(5 * time.Minute):
			return false
		}
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.input.Focus(), m.spinner.Tick, m.waitEvent(), m.waitApproval(), frameTick())
}

func (m Model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.eventCh
		if !ok {
			return refreshMsg{}
		}
		// Drain everything already queued so a burst from several busy agents
		// is applied in one update and rendered once.
		batch := eventBatch{msg}
		for len(batch) < 512 {
			select {
			case next, ok := <-m.eventCh:
				if !ok {
					return batch
				}
				batch = append(batch, next)
			default:
				return batch
			}
		}
		return batch
	}
}

func (m Model) waitApproval() tea.Cmd {
	return func() tea.Msg {
		req, ok := <-m.approvals
		if !ok {
			return refreshMsg{}
		}
		return approvalMsg{request: req}
	}
}

func frameTick() tea.Cmd {
	return tea.Tick(frameRate, func(time.Time) tea.Msg { return frameMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.input.SetWidth(maxInt(12, msg.Width-8))
		m.settings.resize(msg.Width)
		return m, nil
	case eventMsg:
		if view := m.viewByID(msg.session); view != nil {
			m.applyEvent(view, msg.event)
		}
		return m, m.waitEvent()
	case eventBatch:
		for _, msg := range msg {
			if view := m.viewByID(msg.session); view != nil {
				m.applyEvent(view, msg.event)
			}
		}
		return m, m.waitEvent()
	case approvalMsg:
		if m.approval == nil {
			req := msg.request
			m.approval = &req
			m.overlay = overlayApproval
		}
		return m, nil
	case modelsMsg:
		m.models = msg.models
		if msg.err != nil {
			m.modelsErr = msg.err.Error()
		}
		m.syncModelCursor()
		return m, nil
	case frameMsg:
		m.frame++
		if m.shownOverlay != m.overlay {
			m.shownOverlay = m.overlay
			m.overlayFrame = 0
		} else if m.overlayFrame < popupFrames {
			m.overlayFrame++
		}
		for _, view := range m.views {
			target := 0.0
			if view.started {
				target = 1
			}
			view.ease += (target - view.ease) * 0.16
			if math.Abs(target-view.ease) < 0.005 {
				view.ease = target
			}
			for name, progress := range view.spawn {
				if progress >= 1 {
					delete(view.spawn, name)
					continue
				}
				progress += (1 - progress) * 0.18
				if 1-progress < 0.01 {
					progress = 1
				}
				view.spawn[name] = progress
			}
			view.focusPos += (float64(view.focus) - view.focusPos) * 0.45
			if math.Abs(float64(view.focus)-view.focusPos) < 0.02 {
				view.focusPos = float64(view.focus)
			}
		}
		m.settings.ease()
		if m.frame%60 == 0 {
			m.flushChats()
		}
		return m, frameTick()
	case errMsg:
		m.err = msg
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if handled, cmd := m.handleKey(msg); handled {
			return m, cmd
		}
	}

	if m.overlay == overlayNone {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.overlay == overlayNone && msg.String() == "esc" {
		now := time.Now()
		if now.Sub(m.lastEsc) < 500*time.Millisecond && m.openStopMenu() {
			m.lastEsc = time.Time{}
			return true, nil
		}
		m.lastEsc = now
	}
	if m.overlay == overlayStop {
		return m.handleStopKey(msg)
	}
	if m.overlay == overlayApproval {
		switch msg.String() {
		case "y", "enter":
			return true, m.resolveApproval(true)
		case "n", "esc", "ctrl+c":
			return true, m.resolveApproval(false)
		}
		return true, nil
	}
	if m.overlay == overlaySettings {
		return m.settings.handleKey(msg, m)
	}
	if m.overlay == overlaySessions {
		switch msg.String() {
		case "esc", "ctrl+l", "ctrl+o":
			m.overlay = overlayNone
		case "up", "k":
			if m.sessionCursor > 0 {
				m.sessionCursor--
			}
		case "down", "j":
			if m.sessionCursor < len(m.views)-1 {
				m.sessionCursor++
			}
		case "enter":
			m.switchTo(m.sessionCursor)
			m.overlay = overlayNone
		case "ctrl+n":
			m.newSession()
		case "d", "delete", "backspace":
			m.deleteSession(m.sessionCursor)
		}
		return true, nil
	}

	if m.overlay == overlayModels {
		switch msg.String() {
		case "esc", "ctrl+a":
			m.overlay = overlayNone
		case "tab", "shift+tab":
			m.modelTarget = 1 - m.modelTarget
			m.syncModelCursor()
		case "up", "k":
			if m.modelCursor > 0 {
				m.modelCursor--
			}
		case "down", "j":
			if m.modelCursor < len(m.models)-1 {
				m.modelCursor++
			}
		case "enter":
			m.selectModel()
			m.overlay = overlayNone
		}
		return true, nil
	}

	switch msg.String() {
	case "ctrl+c":
		m.flushChats()
		return true, tea.Quit
	case "ctrl+a":
		m.overlay = overlayModels
		m.modelCursor = 0
		m.models = nil
		m.modelsErr = ""
		m.modelTarget = 0
		if view := m.activeView(); view != nil {
			if agent := view.session.Orch.Bus().Get(m.focusedName(view)); agent != nil && agent.Kind == bus.KindSub {
				m.modelTarget = 1
			}
		}
		return true, m.fetchModels()
	case "ctrl+n":
		m.newSession()
		return true, nil
	case "ctrl+l":
		m.overlay = overlaySessions
		m.sessionCursor = m.active
		return true, nil
	case "ctrl+o":
		m.settings.load(m.mgr.Config())
		m.overlay = overlaySettings
		return true, nil
	case "tab":
		m.cycleAgent(1)
		return true, nil
	case "shift+tab":
		m.cycleAgent(-1)
		return true, nil
	case "shift+enter", "alt+enter":
		return true, m.insertNewline()
	case "enter":
		m.submit()
		return true, nil
	case "esc":
		m.input.Reset()
		return true, nil
	}
	return false, nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return *m, nil
	}
	if m.overlay == overlaySettings {
		return *m, m.settings.handleMouse(msg, m)
	}
	if m.overlay == overlaySessions {
		for i, r := range m.sessionRects() {
			if r.contains(msg.X, msg.Y) {
				m.sessionCursor = i
				m.switchTo(i)
				m.overlay = overlayNone
				return *m, nil
			}
		}
		return *m, nil
	}
	if m.overlay == overlayApproval {
		if r, ok := m.approvalRects(); ok {
			if r[0].contains(msg.X, msg.Y) {
				return *m, m.resolveApproval(true)
			}
			if r[1].contains(msg.X, msg.Y) {
				return *m, m.resolveApproval(false)
			}
		}
		return *m, nil
	}

	if m.overlay == overlayModels {
		for index, r := range m.modelRects() {
			if r.contains(msg.X, msg.Y) {
				m.modelCursor = index
				m.selectModel()
				m.overlay = overlayNone
				return *m, nil
			}
		}
		return *m, nil
	}

	if m.overlay == overlayStop {
		for index, r := range m.stopItemRects() {
			if !r.contains(msg.X, msg.Y) {
				continue
			}
			if m.stopSubmenu {
				subs := m.stopSubs()
				if index < len(subs) {
					m.stopSubCursor = index
					m.stopAgent(subs[index].Name)
				}
				m.closeStop()
			} else {
				m.stopCursor = index
				m.activateStop()
			}
			return *m, nil
		}
		return *m, nil
	}

	l := m.computeLayout()
	view := m.activeView()
	if view == nil {
		return *m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if view.ease > 0.5 {
			view.scroll += 3
		}
	case tea.MouseButtonWheelDown:
		if view.ease > 0.5 {
			view.scroll -= 3
			if view.scroll < 0 {
				view.scroll = 0
			}
		}
	case tea.MouseButtonLeft:
		if view.ease <= 0.5 {
			return *m, nil
		}
		if l.sideW > 0 {
			gear := rect{x: l.sideX + 9, y: l.chatY + 1, w: 10, h: 1}
			if gear.contains(msg.X, msg.Y) {
				m.settings.load(m.mgr.Config())
				m.overlay = overlaySettings
				return *m, nil
			}
		}
		for i, r := range m.agentCardRects(l) {
			if r.contains(msg.X, msg.Y) {
				view.focus = i
				view.scroll = 0
				return *m, nil
			}
		}
		if l.aboveH > 0 {
			contentW := l.chatW - 2
			if contentW < 8 {
				contentW = 8
			}
			contentH := l.chatH - 1
			_, keys := m.buildChat(view, m.focusedName(view), contentW, maxInt(1, contentH))
			line := msg.Y - (l.chatY + 1)
			if line >= 0 && line < len(keys) && keys[line] != "" && msg.X >= l.chatX && msg.X < l.chatX+contentW {
				view.expanded[keys[line]] = !view.expanded[keys[line]]
			}
		}
	}
	return *m, nil
}

func (m *Model) resolveApproval(allow bool) tea.Cmd {
	if m.approval == nil {
		return nil
	}
	select {
	case m.approval.reply <- allow:
	default:
	}
	m.approval = nil
	m.overlay = overlayNone
	return m.waitApproval()
}

func (m *Model) insertNewline() tea.Cmd {
	updated, cmd := m.input.Update(tea.KeyMsg{Type: tea.KeyCtrlJ})
	m.input = updated
	return cmd
}

func (m *Model) submit() {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return
	}
	view := m.activeView()
	if view == nil {
		return
	}
	if !view.started {
		view = m.beginSession(view)
		if view == nil {
			return
		}
	}
	target := m.focusedName(view)
	if err := view.session.Orch.SubmitTo(target, text); err != nil {
		m.err = err
	}
	view.chats[target] = append(view.chats[target], Block{Kind: "user", Text: text, Done: true, At: time.Now()})
	view.started = true
	view.scroll = 0
	m.input.Reset()
}

// beginSession turns the launcher into a working session. A pristine placeholder
// is reused; a session that already carries history gets a fresh successor so a
// message typed on the landing page never lands in the last opened session.
func (m *Model) beginSession(view *sessionView) *sessionView {
	if !hasContent(view) {
		view.started = true
		return view
	}
	s, err := m.mgr.NewSession("")
	if err != nil {
		m.err = err
		return nil
	}
	fresh := m.attach(s)
	fresh.started = true
	m.views = append(m.views, fresh)
	m.active = len(m.views) - 1
	return fresh
}

func hasContent(view *sessionView) bool {
	for _, blocks := range view.chats {
		if len(blocks) > 0 {
			return true
		}
	}
	return false
}

func (m *Model) newSession() {
	s, err := m.mgr.NewSession("")
	if err != nil {
		m.err = err
		return
	}
	view := m.attach(s)
	view.started = true
	m.views = append(m.views, view)
	m.active = len(m.views) - 1
	m.overlay = overlayNone
}

func (m *Model) deleteSession(index int) {
	if index < 0 || index >= len(m.views) {
		return
	}
	view := m.views[index]
	m.mgr.DeleteSession(view.session.ID)
	m.views = append(m.views[:index], m.views[index+1:]...)
	if len(m.views) == 0 {
		m.newSession()
		m.overlay = overlayNone
		return
	}
	if m.active > index {
		m.active--
	}
	if m.active >= len(m.views) {
		m.active = len(m.views) - 1
	}
	if m.sessionCursor >= len(m.views) {
		m.sessionCursor = len(m.views) - 1
	}
	if m.sessionCursor < 0 {
		m.sessionCursor = 0
	}
}

func (m *Model) switchTo(index int) {
	if index < 0 || index >= len(m.views) {
		return
	}
	m.active = index
	m.mgr.SetActive(m.views[index].session.ID)
	m.views[index].started = true
}

func (m Model) fetchModels() tea.Cmd {
	view := m.activeView()
	if view == nil {
		return nil
	}
	orch := view.session.Orch
	return func() tea.Msg {
		cfg := orch.Config()
		list := append([]string{}, cfg.Models...)
		if lister, ok := orch.Provider().(llm.ModelLister); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			remote, err := lister.Models(ctx)
			if err != nil && len(list) == 0 {
				return modelsMsg{err: err}
			}
			for _, model := range remote {
				if !containsString(list, model) {
					list = append(list, model)
				}
			}
		}
		sort.Strings(list)
		if len(list) == 0 {
			return modelsMsg{err: fmt.Errorf("no models available")}
		}
		return modelsMsg{models: list}
	}
}

func (m *Model) selectModel() {
	if m.modelCursor < 0 || m.modelCursor >= len(m.models) {
		return
	}
	view := m.activeView()
	if view == nil {
		return
	}
	if m.modelTarget == 1 {
		view.session.Orch.SetSubModel(m.models[m.modelCursor])
	} else {
		view.session.Orch.SetChiefModel(m.models[m.modelCursor])
	}
	_ = config.Save(config.UserConfigPath(), view.session.Orch.Config())
}

func (m Model) targetModel(view *sessionView) string {
	cfg := view.session.Orch.Config()
	if m.modelTarget == 1 {
		if cfg.SubModel != "" {
			return cfg.SubModel
		}
		return cfg.Model
	}
	return cfg.Model
}

func (m *Model) syncModelCursor() {
	view := m.activeView()
	if view == nil {
		return
	}
	current := m.targetModel(view)
	for index, name := range m.models {
		if name == current {
			m.modelCursor = index
			return
		}
	}
}

func (m *Model) openStopMenu() bool {
	view := m.activeView()
	if view == nil {
		return false
	}
	working := view.session.Orch.WorkingAgents()
	if len(working) == 0 {
		return false
	}
	hasChief := false
	subs := 0
	for _, agent := range working {
		if agent.Kind == bus.KindChief {
			hasChief = true
		} else {
			subs++
		}
	}
	menu := []stopOption{}
	if hasChief {
		menu = append(menu, stopOption{"chief", "chief"})
	}
	if subs > 0 {
		menu = append(menu, stopOption{"submenu", "Subagent…"})
		menu = append(menu, stopOption{"subs", "all subagents"})
	}
	menu = append(menu, stopOption{"all", "all agents"})
	m.stopMenu = menu
	m.stopCursor = 0
	m.stopSubmenu = false
	m.stopSubCursor = 0
	m.overlay = overlayStop
	return true
}

func (m *Model) closeStop() {
	m.overlay = overlayNone
	m.stopMenu = nil
	m.stopSubmenu = false
	m.stopCursor = 0
	m.stopSubCursor = 0
}

func (m *Model) handleStopKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if m.stopSubmenu {
		subs := m.stopSubs()
		switch msg.String() {
		case "esc", "left":
			m.stopSubmenu = false
		case "up", "k":
			if m.stopSubCursor > 0 {
				m.stopSubCursor--
			}
		case "down", "j":
			if m.stopSubCursor < len(subs)-1 {
				m.stopSubCursor++
			}
		case "tab":
			if len(subs) > 0 {
				m.stopSubCursor = (m.stopSubCursor + 1) % len(subs)
			}
		case "enter":
			if m.stopSubCursor < len(subs) {
				m.stopAgent(subs[m.stopSubCursor].Name)
			}
			m.closeStop()
		}
		return true, nil
	}
	switch msg.String() {
	case "esc":
		m.closeStop()
	case "up", "k":
		if m.stopCursor > 0 {
			m.stopCursor--
		}
	case "down", "j":
		if m.stopCursor < len(m.stopMenu)-1 {
			m.stopCursor++
		}
	case "tab":
		if len(m.stopMenu) > 0 {
			m.stopCursor = (m.stopCursor + 1) % len(m.stopMenu)
		}
	case "enter":
		m.activateStop()
	}
	return true, nil
}

func (m *Model) activateStop() {
	if m.stopCursor < 0 || m.stopCursor >= len(m.stopMenu) {
		return
	}
	view := m.activeView()
	if view == nil {
		return
	}
	orch := view.session.Orch
	switch m.stopMenu[m.stopCursor].kind {
	case "chief":
		if chief := orch.Bus().Chief(); chief != nil {
			orch.StopAgent(chief.Name)
		}
		m.closeStop()
	case "submenu":
		m.stopSubmenu = true
		m.stopSubCursor = 0
	case "subs":
		orch.StopSubs()
		m.closeStop()
	case "all":
		orch.StopAll()
		m.closeStop()
	}
}

func (m *Model) stopAgent(name string) {
	view := m.activeView()
	if view == nil {
		return
	}
	view.session.Orch.StopAgent(name)
}

func (m Model) stopSubs() []*bus.Agent {
	view := m.activeView()
	if view == nil {
		return nil
	}
	var subs []*bus.Agent
	for _, agent := range view.session.Orch.WorkingAgents() {
		if agent.Kind == bus.KindSub {
			subs = append(subs, agent)
		}
	}
	return subs
}

func (m Model) stopLines() []string {
	if m.stopSubmenu {
		var lines []string
		for index, agent := range m.stopSubs() {
			cursor := "  "
			name := textStyle.Render(agent.Name)
			if index == m.stopSubCursor {
				cursor = fgStyle(plum).Render("▍ ")
				name = fgStyle(plum).Bold(true).Render(agent.Name)
			}
			lines = append(lines, cursor+name)
		}
		if len(lines) == 0 {
			lines = append(lines, mutedStyle.Render("no subagents"))
		}
		lines = append(lines, faintStyle.Render("enter stop · esc back"))
		return lines
	}
	var lines []string
	for index, option := range m.stopMenu {
		cursor := "  "
		label := textStyle.Render(option.label)
		if index == m.stopCursor {
			cursor = fgStyle(plum).Render("▍ ")
			label = fgStyle(plum).Bold(true).Render(option.label)
		}
		lines = append(lines, cursor+label)
	}
	lines = append(lines, faintStyle.Render("tab move · enter stop · esc close"))
	return lines
}

func (m Model) stopWidth() int {
	width := m.width - 4
	if width > 46 {
		width = 46
	}
	if width < 20 {
		width = 20
	}
	return width
}

func (m Model) stopItemRects() []rect {
	l := m.computeLayout()
	count := len(m.stopMenu)
	if m.stopSubmenu {
		count = len(m.stopSubs())
	}
	rects := make([]rect, 0, count)
	width := m.stopWidth()
	for index := 0; index < count; index++ {
		rects = append(rects, rect{x: 3, y: l.stopY + 2 + index, w: width - 2, h: 1})
	}
	return rects
}

func (m Model) modelWindowStart() int {
	rows := m.overlayGeom(len(m.models)+1).h - 3
	if rows < 1 {
		rows = 1
	}
	if m.modelCursor >= rows {
		return m.modelCursor - rows + 1
	}
	return 0
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (m *Model) cycleAgent(direction int) {
	view := m.activeView()
	if view == nil {
		return
	}
	agents := view.session.Orch.Bus().List()
	if len(agents) == 0 {
		return
	}
	view.focus = (view.focus + direction + len(agents)) % len(agents)
	view.scroll = 0
}

func (m Model) activeView() *sessionView {
	if m.active < 0 || m.active >= len(m.views) {
		return nil
	}
	return m.views[m.active]
}

func (m Model) activeConfig() config.Config {
	if view := m.activeView(); view != nil {
		return view.session.Orch.Config()
	}
	return m.mgr.Config()
}

func (m Model) viewByID(id string) *sessionView {
	for _, v := range m.views {
		if v.session.ID == id {
			return v
		}
	}
	return nil
}

func (m Model) focusedName(view *sessionView) string {
	agents := view.session.Orch.Bus().List()
	if len(agents) == 0 {
		return "chief"
	}
	if view.focus >= len(agents) {
		view.focus = len(agents) - 1
	}
	if view.focus < 0 {
		view.focus = 0
	}
	return agents[view.focus].Name
}

func (m *Model) applyEvent(view *sessionView, ev bus.Event) {
	if _, ok := view.chats[ev.Agent]; !ok {
		view.chats[ev.Agent] = nil
	}
	switch ev.Type {
	case "assistant.delta":
		appendDelta(view, ev.Agent, "text", ev.Text)
	case "assistant.done":
		finishBlock(view, ev.Agent, "text")
	case "reasoning.delta":
		appendDelta(view, ev.Agent, "reasoning", ev.Text)
	case "reasoning.done":
		finishBlock(view, ev.Agent, "reasoning")
	case "tool.call":
		view.chats[ev.Agent] = append(view.chats[ev.Agent], Block{Kind: "tool", Tool: ev.Tool, CallID: ev.CallID, At: ev.At})
	case "tool.args":
		appendToolArgs(view, ev.Agent, ev.CallID, ev.Text)
	case "tool.output":
		appendToolOutput(view, ev.Agent, ev.CallID, ev.Text)
	case "tool.result":
		finishTool(view, ev.Agent, ev.CallID, ev.Tool, ev.Text)
	case "assistant.error":
		view.chats[ev.Agent] = append(view.chats[ev.Agent], Block{Kind: "error", Text: ev.Text, Done: true, At: ev.At})
	case "user":
		view.chats[ev.Agent] = append(view.chats[ev.Agent], Block{Kind: "user", Text: ev.Text, Done: true, At: ev.At})
	case "cwd":
		if ev.Text != "" {
			view.cwd[ev.Agent] = ev.Text
		}
	case "agent.added":
		// A freshly spawned subagent emerges from the chief card instead of
		// popping into the sidebar. Restored agents skip this.
		if ev.Text == string(bus.KindSub) && view.started {
			view.spawn[ev.Agent] = 0
		}
	}
	pruneBlocks(view, ev.Agent)
	view.dirty = true
}

// maxBlocksPerAgent bounds the on-screen chat for one agent. Live output from a
// busy command can append without limit, which slows every redraw; older blocks
// scroll out of view anyway.
const maxBlocksPerAgent = 400

func pruneBlocks(view *sessionView, agent string) {
	blocks := view.chats[agent]
	if len(blocks) <= maxBlocksPerAgent {
		return
	}
	view.chats[agent] = append([]Block(nil), blocks[len(blocks)-maxBlocksPerAgent:]...)
}

func (m *Model) flushChats() {
	for _, view := range m.views {
		if view.dirty {
			saveChats(view.session, view.chats)
			view.dirty = false
		}
	}
}

func appendDelta(view *sessionView, agent, kind, text string) {
	blocks := view.chats[agent]
	if len(blocks) > 0 {
		last := &blocks[len(blocks)-1]
		if last.Kind == kind && !last.Done {
			last.Text += text
			return
		}
	}
	view.chats[agent] = append(blocks, Block{Kind: kind, Text: text, At: time.Now()})
}

func finishBlock(view *sessionView, agent, kind string) {
	blocks := view.chats[agent]
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind == kind {
			blocks[i].Done = true
			return
		}
	}
}

func appendToolArgs(view *sessionView, agent, callID, text string) {
	blocks := view.chats[agent]
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind != "tool" || blocks[i].Done {
			continue
		}
		if callID == "" || blocks[i].CallID == callID {
			blocks[i].Args += text
			return
		}
	}
}

func appendToolOutput(view *sessionView, agent, callID, text string) {
	blocks := view.chats[agent]
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind != "tool" || blocks[i].Done {
			continue
		}
		if callID == "" || blocks[i].CallID == callID {
			blocks[i].Result += text
			return
		}
	}
}

func finishTool(view *sessionView, agent, callID, tool, result string) {
	blocks := view.chats[agent]
	for i := len(blocks) - 1; i >= 0; i-- {
		if blocks[i].Kind != "tool" || blocks[i].Done {
			continue
		}
		if callID == "" || blocks[i].CallID == callID {
			if blocks[i].Tool == "" {
				blocks[i].Tool = tool
			}
			blocks[i].Result = result
			blocks[i].Done = true
			return
		}
	}
}

func toolCount(view *sessionView, agent string) int {
	count := 0
	for _, b := range view.chats[agent] {
		if b.Kind == "tool" {
			count++
		}
	}
	return count
}

func msgCount(view *sessionView, agent string) int {
	count := 0
	for _, b := range view.chats[agent] {
		if b.Kind == "text" || b.Kind == "user" {
			count++
		}
	}
	return count
}

func lastPreview(view *sessionView, agent string, width int) string {
	blocks := view.chats[agent]
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		var text string
		switch b.Kind {
		case "user":
			text = "you: " + b.Text
		case "text":
			text = b.Text
		case "reasoning":
			text = "thinking…"
		case "tool":
			text = "tool: " + b.Tool
		}
		if strings.TrimSpace(text) != "" {
			return truncate(strings.ReplaceAll(text, "\n", " "), width)
		}
	}
	return "—"
}
