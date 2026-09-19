package tui

import (
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"rocina/internal/bus"
	"rocina/internal/session"
	"rocina/internal/tools"
)

const frameRate = time.Second / 20

type eventMsg struct {
	session string
	event   bus.Event
}

type approvalRequest struct {
	approval tools.Approval
	reply    chan bool
}

type approvalMsg struct {
	request approvalRequest
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
)

type sessionView struct {
	session  *session.Session
	chats    map[string][]Block
	expanded map[string]bool
	focus    int
	scroll   int
	ease     float64
	started  bool
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
	approval      *approvalRequest
	sessionCursor int

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

	m := Model{
		mgr:       mgr,
		input:     input,
		spinner:   sp,
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
	view := &sessionView{session: s, chats: map[string][]Block{}, expanded: map[string]bool{}}
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
		return msg
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
	case approvalMsg:
		if m.approval == nil {
			req := msg.request
			m.approval = &req
			m.overlay = overlayApproval
		}
		return m, nil
	case frameMsg:
		m.frame++
		for _, view := range m.views {
			target := 0.0
			if view.started {
				target = 1
			}
			view.ease += (target - view.ease) * 0.16
			if math.Abs(target-view.ease) < 0.005 {
				view.ease = target
			}
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

	switch msg.String() {
	case "ctrl+c":
		return true, tea.Quit
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
			contentW := l.chatW - 4
			contentH := l.chatH - 2
			_, keys := m.buildChat(view, m.focusedName(view), contentW, maxInt(1, contentH-1))
			line := msg.Y - (l.chatY + 2)
			if line >= 0 && line < len(keys) && keys[line] != "" && msg.X >= l.chatX+2 && msg.X < l.chatX+2+contentW {
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
	chief := "chief"
	if c := view.session.Orch.Bus().Chief(); c != nil {
		chief = c.Name
	}
	if err := view.session.Orch.Submit(text); err != nil {
		m.err = err
	}
	view.chats[chief] = append(view.chats[chief], Block{Kind: "user", Text: text, Done: true, At: time.Now()})
	view.started = true
	view.scroll = 0
	m.input.Reset()
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
	case "tool.result":
		finishTool(view, ev.Agent, ev.CallID, ev.Tool, ev.Text)
	case "user":
		view.chats[ev.Agent] = append(view.chats[ev.Agent], Block{Kind: "user", Text: ev.Text, Done: true, At: ev.At})
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
