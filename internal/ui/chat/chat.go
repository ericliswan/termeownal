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

// Model shows one thread's messages.
type Model struct {
	vp      viewport.Model
	now     func() time.Time
	open    bool
	thread  core.Thread
	me      core.User
	msgs    []core.Message
	more    bool
	loading bool
}

// New returns an empty chat view. now is used to label days.
func New(now func() time.Time) Model {
	return Model{vp: viewport.New(), now: now}
}

// SetSize sets the inner size, including the header line.
func (m *Model) SetSize(w, h int) {
	m.vp.SetWidth(w)
	m.vp.SetHeight(max(h-1, 0))
	m.render(false)
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

	atBottom, before := m.vp.AtBottom(), m.vp.TotalLineCount()
	m.render(switched || atBottom)
	if prepended {
		m.vp.SetYOffset(m.vp.YOffset() + m.vp.TotalLineCount() - before)
	}
}

// Thread returns the open thread.
func (m Model) Thread() (core.ThreadID, bool) { return m.thread.ID, m.open }

// AtTop reports whether the view is scrolled to the top.
func (m Model) AtTop() bool { return m.vp.AtTop() }

// Update scrolls the view.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "g", "home":
			m.vp.GotoTop()
			return m, nil
		case "G", "end":
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
	m.vp.SetContentLines(Render(m.thread, m.me, m.msgs, m.more, m.loading, m.vp.Width(), m.now()))
	if bottom {
		m.vp.GotoBottom()
	}
}

// Render lays out msgs as lines of at most width cells: day separators,
// then groups of consecutive messages from one sender under a name and time.
func Render(thread core.Thread, me core.User, msgs []core.Message, more, loading bool, width int, now time.Time) []string {
	if width <= 2 {
		return nil
	}
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
		body := Body(*m)
		if m.Text == "" {
			body = theme.Dim.Render(body)
		}
		for _, l := range strings.Split(lipgloss.Wrap(body, width-2, ""), "\n") {
			lines = append(lines, "  "+l)
		}
		prev = m
	}
	return lines
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
