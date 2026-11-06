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

// Package chat is the message view of the open thread.
package chat

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"termeownal/internal/core"
	"termeownal/internal/ui/theme"
)

// groupGap is how long a pause starts a new group of messages from the same
// sender.
const groupGap = 10 * time.Minute

// reactionLog is a history entry that records a reaction, not a chat line.
const reactionLog = "REACTION_LOG_XMAT"

// Model shows one thread's messages. While focused, one message is
// selected, for keys that act on a message.
type Model struct {
	vp      viewport.Model
	now     func() time.Time
	open    bool
	thread  core.Thread
	me      core.User
	msgs    []core.Message
	more    bool
	loading bool

	focused  bool
	selected core.MessageID
	// spans holds each shown message's first line and the line after its
	// last, in the rendered content.
	spans map[core.MessageID][2]int
}

// New returns an empty chat view. now is used to label days.
func New(now func() time.Time) Model {
	return Model{vp: viewport.New(), now: now}
}

// SetSize sets the inner size, including the header line. A view at the
// bottom stays there.
func (m *Model) SetSize(w, h int) {
	bottom := m.vp.AtBottom()
	m.vp.SetWidth(w)
	m.vp.SetHeight(max(h-1, 0))
	m.render(bottom)
}

// Show displays msgs (oldest first) of thread. more says whether older
// messages exist and loading whether they are being fetched. Showing a new
// thread scrolls to the bottom; otherwise the view stays where the user left
// it, following new messages only when already at the bottom.
func (m *Model) Show(thread core.Thread, me core.User, msgs []core.Message, more, loading bool) {
	switched := !m.open || m.thread.ID != thread.ID
	prepended := !switched && len(m.msgs) > 0 && len(msgs) > 0 &&
		m.msgs[0].ID != msgs[0].ID && m.msgs[len(m.msgs)-1].ID == msgs[len(msgs)-1].ID
	m.open, m.thread, m.me, m.msgs, m.more, m.loading = true, thread, me, msgs, more, loading
	if switched {
		m.selected = ""
	}
	// The selected message can go, e.g. a local copy replaced by its echo.
	reselect := m.focused && m.selectedIndex() < 0
	if reselect {
		m.selectLast()
	}

	atBottom, before := m.vp.AtBottom(), m.vp.TotalLineCount()
	m.render(switched || atBottom)
	if prepended {
		m.vp.SetYOffset(m.vp.YOffset() + m.vp.TotalLineCount() - before)
	}
	if reselect {
		m.scrollToSelected()
	}
}

// SetFocused shows or hides the selection. Focusing selects the newest
// message.
func (m *Model) SetFocused(focused bool) {
	if focused == m.focused {
		return
	}
	m.focused, m.selected = focused, ""
	if focused {
		m.selectLast()
	}
	m.render(false)
	m.scrollToSelected()
}

// Selected returns the selected message.
func (m Model) Selected() (core.Message, bool) {
	if i := m.selectedIndex(); i >= 0 {
		return m.msgs[i], true
	}
	return core.Message{}, false
}

func (m Model) selectedIndex() int {
	if m.selected == "" {
		return -1
	}
	return slices.IndexFunc(m.msgs, func(o core.Message) bool { return o.ID == m.selected && Visible(o) })
}

func (m *Model) selectLast() {
	m.selected = ""
	for i := len(m.msgs) - 1; i >= 0; i-- {
		if Visible(m.msgs[i]) {
			m.selected = m.msgs[i].ID
			return
		}
	}
}

// move selects the next visible message in direction step (1 or -1), or
// with all set the last one that way.
func (m *Model) move(step int, all bool) {
	i := m.selectedIndex()
	if i < 0 {
		return
	}
	for j := i + step; j >= 0 && j < len(m.msgs); j += step {
		if Visible(m.msgs[j]) {
			m.selected = m.msgs[j].ID
			if !all {
				break
			}
		}
	}
	m.render(false)
	m.scrollToSelected()
}

// scrollToSelected scrolls as little as needed to show the selected
// message, its top if it's taller than the view.
func (m *Model) scrollToSelected() {
	span, ok := m.spans[m.selected]
	if !ok {
		return
	}
	top, h := m.vp.YOffset(), m.vp.Height()
	switch {
	case span[0] < top || span[1]-span[0] > h:
		m.vp.SetYOffset(span[0])
	case span[1] > top+h:
		m.vp.SetYOffset(span[1] - h)
	}
}

// Thread returns the open thread.
func (m Model) Thread() (core.ThreadID, bool) { return m.thread.ID, m.open }

// AtTop reports whether the view is scrolled to the top.
func (m Model) AtTop() bool { return m.vp.AtTop() }

// Update moves the selection and scrolls the view.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "j", "down":
			m.move(1, false)
			return m, nil
		case "k", "up":
			m.move(-1, false)
			return m, nil
		case "g", "home":
			m.move(-1, true)
			m.vp.GotoTop()
			return m, nil
		case "G", "end":
			m.move(1, true)
			m.vp.GotoBottom()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// View renders the header and the messages.
func (m Model) View() string {
	w, h := m.vp.Width(), m.vp.Height()+1
	if !m.open {
		return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, theme.Dim.Render("Select a thread and press Enter"))
	}
	header := theme.Title.Render(m.thread.Title)
	if m.thread.IsGroup {
		header += theme.Dim.Render(" · " + names(m.thread.Members))
	}
	return ansi.Truncate(header, w, "…") + "\n" + m.vp.View()
}

func (m *Model) render(bottom bool) {
	if !m.open {
		return
	}
	var selected core.MessageID
	if m.focused {
		selected = m.selected
	}
	var lines []string
	lines, m.spans = Render(m.thread, m.me, m.msgs, selected, m.more, m.loading, m.vp.Width(), m.now())
	m.vp.SetContentLines(lines)
	if bottom {
		m.vp.GotoBottom()
	}
}

// Render lays out msgs as lines of at most width cells: day separators,
// then groups of consecutive messages from one sender under a name and time,
// and "Seen" under the user's message if it's the newest and has been read.
// A reply has its quote above it; the selected message is marked. It also
// returns each message's span of lines: its first and the one after its last.
func Render(thread core.Thread, me core.User, msgs []core.Message, selected core.MessageID, more, loading bool, width int, now time.Time) ([]string, map[core.MessageID][2]int) {
	if width <= 2 {
		return nil, nil
	}
	spans := make(map[core.MessageID][2]int, len(msgs))
	var lines []string
	switch {
	case loading:
		lines = append(lines, theme.Dim.Render("Loading older messages…"))
	case more:
		lines = append(lines, theme.Dim.Render("PgUp at the top loads older messages"))
	default:
		lines = append(lines, theme.Dim.Render("Beginning of conversation"))
	}

	senders := map[core.UserID]string{me.ID: "You"}
	for _, u := range thread.Members {
		senders[u.ID] = firstName(u)
	}
	seen := seenLine(thread, me, msgs)
	var prev *core.Message
	for i := range msgs {
		m := &msgs[i]
		if !Visible(*m) {
			continue
		}
		at := m.At.In(now.Location())
		newDay := prev == nil || !sameDay(prev.At.In(now.Location()), at)
		if newDay {
			lines = append(lines, "", daySeparator(at, now, width))
		}
		if newDay || prev.Sender != m.Sender || m.At.Sub(prev.At) >= groupGap {
			name, ok := senders[m.Sender]
			if !ok {
				name = "Unknown"
			}
			style := theme.Title
			if m.Sender == me.ID {
				style = theme.Bold
			}
			lines = append(lines, "", style.Render(name)+" "+theme.Dim.Render(at.Format("15:04")))
		}
		indent := "  "
		if m.ID == selected {
			indent = theme.Title.Render("▌") + " "
		}
		start := len(lines)
		if m.ReplyTo.ID != "" {
			q := "↪ " + QuoteText(thread, me, msgs, m.ReplyTo)
			lines = append(lines, indent+theme.Dim.Render(ansi.Truncate(q, width-2, "…")))
		}
		body := Body(*m)
		if m.Text == "" {
			body = theme.Dim.Render(body)
		}
		for _, l := range strings.Split(lipgloss.Wrap(body, width-2, ""), "\n") {
			lines = append(lines, indent+l)
		}
		switch m.State {
		case core.Sending:
			lines = append(lines, indent+theme.Dim.Render("sending…"))
		case core.SendFailed:
			lines = append(lines, indent+theme.Bold.Render("✗ not sent · r to retry"))
		}
		spans[m.ID] = [2]int{start, len(lines)}
		prev = m
	}
	if seen != "" {
		lines = append(lines, "  "+theme.Dim.Render(ansi.Truncate(seen, width-2, "…")))
	}
	return lines, spans
}

// QuoteText is a quoted message on one line: the sender's name, then its
// text or content type. A quote without either, which the service sends
// for some replies, takes them from the message in msgs if it's loaded.
func QuoteText(thread core.Thread, me core.User, msgs []core.Message, q core.Quote) string {
	body := q.Text
	if body == "" && q.ContentType != "" {
		body = "[" + q.ContentType + "]"
	}
	if body == "" {
		if i := slices.IndexFunc(msgs, func(o core.Message) bool { return o.ID == q.ID }); i >= 0 {
			q.Sender, body = msgs[i].Sender, Body(msgs[i])
		}
	}
	if body == "" {
		body = "a message"
	}
	body = strings.Join(strings.Fields(body), " ")
	switch q.Sender {
	case 0:
		return body
	case me.ID:
		return "You: " + body
	}
	for _, u := range thread.Members {
		if u.ID == q.Sender {
			return firstName(u) + ": " + body
		}
	}
	return body
}

// seenLine says who has read the newest message, if the user sent it: "Seen"
// in a 1:1, otherwise "Seen by" and names. It's empty when no one has, or the
// newest message is someone else's, whose reply shows they read on.
func seenLine(thread core.Thread, me core.User, msgs []core.Message) string {
	i := len(msgs) - 1
	for i >= 0 && !Visible(msgs[i]) {
		i--
	}
	if i < 0 || msgs[i].Sender != me.ID || msgs[i].State != core.Sent {
		return ""
	}
	// Watermarks are in milliseconds; message times may be finer.
	at := msgs[i].At.UnixMilli()
	var readers []core.User
	for _, u := range thread.Members {
		if upTo, ok := thread.ReadUpTo[u.ID]; ok && upTo.UnixMilli() >= at {
			readers = append(readers, u)
		}
	}
	switch {
	case len(readers) == 0:
		return ""
	case !thread.IsGroup:
		return "Seen"
	case len(readers) == len(thread.Members):
		return "Seen by everyone"
	default:
		return "Seen by " + names(readers)
	}
}

// Visible reports whether m is a chat line; reaction log entries aren't.
func Visible(m core.Message) bool { return m.ContentType != reactionLog }

// Body is the text shown for a message: its text, or its content type for
// messages without text.
func Body(m core.Message) string {
	if m.Text != "" {
		return m.Text
	}
	return "[" + m.ContentType + "]"
}

func daySeparator(t, now time.Time, width int) string {
	label := t.Format("Monday, 2 January")
	switch {
	case sameDay(t, now):
		label = "Today"
	case sameDay(t, now.AddDate(0, 0, -1)):
		label = "Yesterday"
	case t.Year() != now.Year():
		label = t.Format("Monday, 2 January 2006")
	}
	return theme.Dim.Render(lipgloss.PlaceHorizontal(width, lipgloss.Center, "── "+label+" ──"))
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func firstName(u core.User) string {
	if f := strings.Fields(u.Name); len(f) > 0 {
		return f[0]
	}
	return u.Username
}

func names(users []core.User) string {
	n := make([]string, len(users))
	for i, u := range users {
		n[i] = firstName(u)
	}
	return strings.Join(n, ", ")
}
