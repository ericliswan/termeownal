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

// Package threadlist is the list of threads on the left of the screen.
package threadlist

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"termeownal/internal/core"
	"termeownal/internal/ui/theme"
)

// Item is one thread in the list.
type Item struct {
	Thread core.Thread
	// Preview is the newest message, already formatted, or empty.
	Preview string
}

// FilterValue implements list.Item.
func (i Item) FilterValue() string { return i.Thread.Title }

// Model is the thread list.
type Model struct {
	list list.Model
}

// New returns an empty thread list. now is used to format times.
func New(now func() time.Time) Model {
	l := list.New(nil, delegate{now: now}, 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()
	return Model{list: l}
}

// SetItems replaces the threads, keeping the cursor on the same thread.
func (m *Model) SetItems(items []Item) {
	sel, ok := m.Selected()
	litems := make([]list.Item, len(items))
	for i, it := range items {
		litems[i] = it
	}
	m.list.SetItems(litems)
	for i, it := range items {
		if ok && it.Thread.ID == sel {
			m.list.Select(i)
		}
	}
}

// Selected returns the thread under the cursor.
func (m Model) Selected() (core.ThreadID, bool) {
	it, ok := m.list.SelectedItem().(Item)
	return it.Thread.ID, ok
}

// SetSize sets the inner size of the list.
func (m *Model) SetSize(w, h int) { m.list.SetSize(w, h) }

// Update handles cursor movement.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// View renders the list.
func (m Model) View() string { return m.list.View() }

// delegate draws each thread as a title line and a preview line.
type delegate struct{ now func() time.Time }

func (delegate) Height() int                         { return 2 }
func (delegate) Spacing() int                        { return 1 }
func (delegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d delegate) Render(w io.Writer, m list.Model, index int, li list.Item) {
	it := li.(Item)
	cursor, mark := "  ", "  "
	if index == m.Index() {
		cursor = theme.Title.Render("▌ ")
	}
	title := it.Thread.Title
	if it.Thread.Unread {
		mark = theme.Title.Render("● ")
		title = theme.Bold.Render(title)
	}
	when := FormatTime(it.Thread.LastActivity, d.now())
	room := m.Width() - 4 - lipgloss.Width(when) - 1
	title = ansi.Truncate(title, max(room, 0), "…")
	gap := strings.Repeat(" ", max(room-lipgloss.Width(title), 0)+1)
	preview := theme.Dim.Render(ansi.Truncate(strings.ReplaceAll(it.Preview, "\n", " "), max(m.Width()-4, 0), "…"))
	_, _ = fmt.Fprintf(w, "%s%s%s%s%s\n%s  %s", cursor, mark, title, gap, theme.Dim.Render(when), cursor, preview)
}

// FormatTime formats t briefly, relative to now: a time today, a weekday
// this week, a date otherwise.
func FormatTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(now.Location())
	y, mo, d := t.Date()
	ny, nmo, nd := now.Date()
	switch {
	case y == ny && mo == nmo && d == nd:
		return t.Format("15:04")
	case now.Sub(t) < 6*24*time.Hour:
		return t.Format("Mon")
	case y == ny:
		return t.Format("Jan 2")
	}
	return t.Format("2006-01-02")
}
