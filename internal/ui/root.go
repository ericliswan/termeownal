// termeownal — a terminal client for Instagram DMs.
// Copyright (C) 2026 ericliswan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Package ui is the Bubble Tea front end. It sees only core types and
// tea.Msgs; all backend work runs as tea.Cmds that Actions provides.
package ui

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"termeownal/internal/core"
	"termeownal/internal/ui/chat"
	"termeownal/internal/ui/composer"
	"termeownal/internal/ui/overlay"
	"termeownal/internal/ui/theme"
	"termeownal/internal/ui/threadlist"
)

// Smallest usable terminal.
const minWidth, minHeight = 60, 15

// historyPage is how many messages one History call asks for.
const historyPage = 50

// Actions runs backend calls for the UI. Each returns a tea.Cmd whose result
// comes back as the message named in its doc.
type Actions interface {
	// Snapshot loads the user and threads; returns a SnapshotMsg.
	Snapshot() tea.Cmd
	// History loads older messages; returns a HistoryMsg.
	History(thread core.ThreadID, before core.MessageID, n int) tea.Cmd
	// Send sends text; returns an ErrorMsg on failure, otherwise nil. The
	// message appears when its echo arrives as a core.MessageAdded.
	Send(thread core.ThreadID, text string) tea.Cmd
}

// SnapshotMsg is the result of Actions.Snapshot.
type SnapshotMsg struct {
	Me      core.User
	Threads []core.Thread
	Err     error
}

// HistoryMsg is the result of Actions.History.
type HistoryMsg struct {
	Thread   core.ThreadID
	Messages []core.Message
	More     bool
	Err      error
}

// ErrorMsg reports a failed action. It shows in the status bar until the
// next key press.
type ErrorMsg struct{ Err error }

type pane int

const (
	paneThreads pane = iota
	paneChat
	paneComposer
)

// threadState is a thread with its loaded messages in Thread.Messages,
// oldest first.
type threadState struct {
	core.Thread
	more    bool // older messages exist
	loading bool // a History call is running
	fetched bool // the newest page has been loaded since start
}

// Model is the top-level UI model.
type Model struct {
	act Actions
	now func() time.Time

	me      core.User
	threads map[core.ThreadID]*threadState

	list     threadlist.Model
	chat     chat.Model
	composer composer.Model
	focus    pane

	width, height int
	status        core.Status
	authErr       error
	errText       string
	help          bool
}

// New returns the UI. now is the clock used to label times.
func New(act Actions, now func() time.Time) Model {
	return Model{
		act:      act,
		now:      now,
		threads:  make(map[core.ThreadID]*threadState),
		list:     threadlist.New(now),
		chat:     chat.New(now),
		composer: composer.New(),
	}
}

// Init loads the threads.
func (m Model) Init() tea.Cmd { return m.act.Snapshot() }

// Update handles input, action results and core events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	case composer.SubmitMsg:
		if id, ok := m.chat.Thread(); ok {
			return m, m.act.Send(id, msg.Text)
		}
	case SnapshotMsg:
		if msg.Err != nil {
			return m.fail(msg.Err), nil
		}
		m.me = cleanUser(msg.Me)
		for _, t := range msg.Threads {
			m.upsert(t)
		}
		m.refresh()
	case HistoryMsg:
		st, ok := m.threads[msg.Thread]
		if !ok {
			return m, nil
		}
		st.loading = false
		if msg.Err != nil {
			m.refresh()
			return m.fail(msg.Err), nil
		}
		msgs, dropped := mergePage(st.Messages, cleanThread(core.Thread{Messages: msg.Messages}).Messages)
		st.Messages = msgs
		st.more, st.fetched = msg.More || dropped, true
		m.refresh()
	case ErrorMsg:
		return m.fail(msg.Err), nil

	case core.StatusChanged:
		m.status = msg.Status
	case core.AuthFailed:
		m.authErr = msg.Err
	case core.ThreadUpdated:
		m.upsert(msg.Thread)
		m.refresh()
	case core.ThreadRead:
		if st, ok := m.threads[msg.Thread]; ok {
			st.Unread = false
			m.refresh()
		}
	case core.MessageAdded:
		m.addMessage(msg.Message)
		m.refresh()
	}
	return m, nil
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	m.errText = ""
	if m.help {
		if s == "esc" || s == "?" {
			m.help = false
		}
		return m, nil
	}
	_, open := m.chat.Thread()
	switch {
	case s == "tab" || s == "shift+tab":
		order := []pane{paneThreads, paneChat}
		if open {
			order = append(order, paneComposer)
		}
		step := 1
		if s == "shift+tab" {
			step = len(order) - 1
		}
		return m, m.setFocus(order[(slices.Index(order, m.focus)+step)%len(order)])
	case s == "esc":
		return m, m.setFocus(paneThreads)
	case s == "?" && m.focus != paneComposer:
		m.help = true
		return m, nil
	}

	var cmd tea.Cmd
	switch m.focus {
	case paneThreads:
		if s == "enter" {
			if id, ok := m.list.Selected(); ok {
				return m, tea.Batch(m.open(id), m.setFocus(paneComposer))
			}
			return m, nil
		}
		m.list, cmd = m.list.Update(k)
	case paneChat:
		if s == "pgup" && m.chat.AtTop() {
			if id, ok := m.chat.Thread(); ok {
				return m, m.loadOlder(id)
			}
		}
		m.chat, cmd = m.chat.Update(k)
	case paneComposer:
		m.composer, cmd = m.composer.Update(k)
	}
	return m, cmd
}

func (m *Model) setFocus(p pane) tea.Cmd {
	m.focus = p
	if p == paneComposer {
		return m.composer.Focus()
	}
	m.composer.Blur()
	return nil
}

// open shows a thread, loading its newest messages the first time.
func (m *Model) open(id core.ThreadID) tea.Cmd {
	st := m.threads[id]
	var cmd tea.Cmd
	if !st.fetched && !st.loading {
		st.loading = true
		cmd = m.act.History(id, "", historyPage)
	}
	m.chat.Show(st.Thread, m.me, st.Messages, st.more, st.loading)
	return cmd
}

func (m *Model) loadOlder(id core.ThreadID) tea.Cmd {
	st := m.threads[id]
	if !st.more || st.loading || len(st.Messages) == 0 {
		return nil
	}
	st.loading = true
	m.refresh()
	return m.act.History(id, st.Messages[0].ID, historyPage)
}

func (m Model) fail(err error) Model {
	if errors.Is(err, core.ErrRateLimited) {
		m.errText = "Instagram is rate-limiting this account; wait a while before retrying."
	} else {
		m.errText = clean(err.Error())
	}
	return m
}

// upsert stores a thread's details, merging any messages it carries.
func (m *Model) upsert(t core.Thread) {
	t = cleanThread(t)
	st, ok := m.threads[t.ID]
	if !ok {
		m.threads[t.ID] = &threadState{Thread: t, more: true}
		return
	}
	msgs, dropped := mergePage(st.Messages, t.Messages)
	st.Thread = t
	st.Messages = msgs
	st.more = st.more || dropped
}

func (m *Model) addMessage(msg core.Message) {
	msg = cleanMessage(msg)
	st, ok := m.threads[msg.Thread]
	if !ok {
		// Backends send ThreadUpdated first; this only guards against one
		// that doesn't.
		st = &threadState{Thread: core.Thread{ID: msg.Thread}, more: true}
		m.threads[msg.Thread] = st
	}
	st.Messages = merge(st.Messages, []core.Message{msg})
	if msg.At.After(st.LastActivity) {
		st.LastActivity = msg.At
	}
	// shortcut: opening a thread doesn't mark it read yet; that lands with
	// the focus-aware mark-read item.
	if open, ok := m.chat.Thread(); msg.Sender != m.me.ID && (!ok || open != msg.Thread) {
		st.Unread = true
	}
}

// refresh redraws the thread list and the open thread from m.threads.
func (m *Model) refresh() {
	states := make([]*threadState, 0, len(m.threads))
	for _, st := range m.threads {
		states = append(states, st)
	}
	slices.SortFunc(states, func(a, b *threadState) int {
		return cmp.Or(b.LastActivity.Compare(a.LastActivity), cmp.Compare(a.ID, b.ID))
	})
	items := make([]threadlist.Item, len(states))
	for i, st := range states {
		items[i] = threadlist.Item{Thread: st.Thread, Preview: m.preview(st.Messages)}
	}
	m.list.SetItems(items)
	if id, ok := m.chat.Thread(); ok {
		st := m.threads[id]
		m.chat.Show(st.Thread, m.me, st.Messages, st.more, st.loading)
	}
}

func (m Model) preview(msgs []core.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if !chat.Visible(msgs[i]) {
			continue
		}
		if msgs[i].Sender == m.me.ID {
			return "You: " + chat.Body(msgs[i])
		}
		return chat.Body(msgs[i])
	}
	return ""
}

// merge adds msgs to have, replacing ones with the same ID, and keeps the
// result sorted by time. Applying the same messages twice changes nothing.
func merge(have, msgs []core.Message) []core.Message {
	if len(msgs) == 0 {
		return have
	}
	out := slices.Clone(have)
	for _, msg := range msgs {
		if i := slices.IndexFunc(out, func(o core.Message) bool { return o.ID == msg.ID }); i >= 0 {
			out[i] = msg
		} else {
			out = append(out, msg)
		}
	}
	slices.SortStableFunc(out, func(a, b core.Message) int { return a.At.Compare(b.At) })
	return out
}

// mergePage merges page, consecutive messages from the server, into have.
// If page doesn't reach back to a message in have, messages may be missing
// between them, so the sent ones in have older than page are dropped, as the
// store does; paging back loads them again. It reports whether any were
// dropped.
func mergePage(have, page []core.Message) ([]core.Message, bool) {
	if len(page) == 0 {
		return have, false
	}
	oldest := slices.MinFunc(page, func(a, b core.Message) int { return a.At.Compare(b.At) })
	if slices.ContainsFunc(have, func(m core.Message) bool { return m.ID == oldest.ID }) {
		return merge(have, page), false
	}
	// Compare as the store does, to the millisecond, so both drop the same ones.
	kept := slices.DeleteFunc(slices.Clone(have), func(m core.Message) bool {
		return m.State == core.Sent &&
			cmp.Or(cmp.Compare(m.At.UnixMilli(), oldest.At.UnixMilli()), cmp.Compare(m.ID, oldest.ID)) < 0
	})
	return merge(kept, page), len(kept) < len(have)
}

// layout sizes the components to the terminal. Each pane has a 1-cell
// border; the bottom line is the status bar.
func (m *Model) layout() {
	if m.width < minWidth || m.height < minHeight {
		return
	}
	lw, rw, h := m.listWidth(), m.width-m.listWidth(), m.height-1
	m.list.SetSize(lw-2, h-2)
	m.chat.SetSize(rw-2, h-m.composerHeight()-2)
	m.composer.SetWidth(rw - 2)
}

func (m Model) listWidth() int      { return min(max(m.width/3, 24), 40) }
func (m Model) composerHeight() int { return m.composer.Height() + 2 }

// View draws the screen.
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "meow"
	return v
}

func (m Model) render() string {
	if m.width < minWidth || m.height < minHeight {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			"Terminal too small: need 60×15")
	}
	lw, rw, h := m.listWidth(), m.width-m.listWidth(), m.height-1
	style := func(p pane) lipgloss.Style {
		if m.focus == p {
			return theme.FocusedPane
		}
		return theme.Pane
	}
	right := lipgloss.JoinVertical(lipgloss.Left,
		style(paneChat).Width(rw).Height(h-m.composerHeight()).Render(m.chat.View()),
		style(paneComposer).Width(rw).Height(m.composerHeight()).Render(m.composer.View()),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, style(paneThreads).Width(lw).Height(h).Render(m.list.View()), right)
	if m.help {
		body = overlay.Center(body, overlay.Help(), m.width, h)
	}
	return body + "\n" + m.statusBar()
}

func (m Model) statusBar() string {
	if m.authErr != nil {
		return theme.Banner.Width(m.width).Render(ansi.Truncate(" "+authMessage(m.authErr), m.width, "…"))
	}
	left := map[core.Status]string{
		core.Connecting:   theme.Dim.Render("○ connecting…"),
		core.Connected:    lipgloss.NewStyle().Foreground(theme.OK).Render("● connected"),
		core.Reconnecting: lipgloss.NewStyle().Foreground(theme.Warn).Render("◌ reconnecting…"),
	}[m.status]
	if m.me.Username != "" {
		left += theme.Dim.Render(" · @" + m.me.Username)
	}
	if m.errText != "" {
		left += "  " + lipgloss.NewStyle().Foreground(theme.Error).Render("! "+m.errText)
	}
	right := theme.Dim.Render("? help · ctrl+c quit")
	left = ansi.Truncate(left, max(m.width-lipgloss.Width(right)-1, 0), "…")
	gap := strings.Repeat(" ", max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1))
	return left + gap + right
}

// authMessage is the banner text for an AuthFailed error.
func authMessage(err error) string {
	switch {
	case errors.Is(err, core.ErrVerificationRequired):
		return "Instagram wants to verify this login. Complete the check in the app or website, then run `meow login`."
	case errors.Is(err, core.ErrConsentRequired):
		return "Accept Instagram's pending terms, then retry."
	case errors.Is(err, core.ErrAccountSuspended):
		return "This account is suspended."
	default:
		return "Session expired. Run `meow login`."
	}
}

// clean strips control characters, which could inject terminal escape
// sequences, from text that came from the network. Newlines stay; tabs
// become spaces.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// cleanLine is clean for one-line fields such as names.
func cleanLine(s string) string { return strings.ReplaceAll(clean(s), "\n", " ") }

func cleanUser(u core.User) core.User {
	u.Username, u.Name = cleanLine(u.Username), cleanLine(u.Name)
	return u
}

func cleanMessage(msg core.Message) core.Message {
	msg.Text, msg.ContentType = clean(msg.Text), cleanLine(msg.ContentType)
	return msg
}

func cleanThread(t core.Thread) core.Thread {
	t.Title = cleanLine(t.Title)
	t.Members = slices.Clone(t.Members)
	for i := range t.Members {
		t.Members[i] = cleanUser(t.Members[i])
	}
	t.Messages = slices.Clone(t.Messages)
	for i := range t.Messages {
		t.Messages[i] = cleanMessage(t.Messages[i])
	}
	return t
}
