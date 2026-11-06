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

// Package composer is the text box for writing messages.
package composer

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"termeownal/internal/ui/theme"
)

// SubmitMsg carries the text the user sent.
type SubmitMsg struct{ Text string }

// Model wraps a textarea where Enter sends and Alt+Enter starts a new line.
// A reply shows what it replies to above the text.
type Model struct {
	ta    textarea.Model
	width int
	reply string
}

// New returns an empty, unfocused composer.
func New() Model {
	ta := textarea.New()
	ta.Placeholder = "Message…"
	ta.ShowLineNumbers = false
	ta.Prompt = "> "
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "shift+enter"))
	ta.SetHeight(3)
	// A steady cursor: a blinking one redraws the screen twice a second.
	st := ta.Styles()
	st.Cursor.Blink = false
	// The defaults assume a dark terminal (a black line behind the text, light
	// grey prompt), so typing is invisible on a light one. Use the terminal's
	// own colours, and a grey that reads on both for the rest.
	st.Cursor.Color = nil
	for _, s := range []*textarea.StyleState{&st.Focused, &st.Blurred} {
		s.CursorLine = lipgloss.NewStyle()
		s.EndOfBuffer = lipgloss.NewStyle()
		s.Prompt = theme.Dim
	}
	st.Blurred.Text = theme.Dim
	st.Blurred.CursorLine = theme.Dim
	ta.SetStyles(st)
	return Model{ta: ta}
}

// SetWidth sets the inner width.
func (m *Model) SetWidth(w int) { m.width = w; m.ta.SetWidth(w) }

// SetReply shows quote, one line, as the message replied to; "" shows none.
// It changes Height.
func (m *Model) SetReply(quote string) { m.reply = quote }

// Height is the inner height.
func (m Model) Height() int {
	if m.reply != "" {
		return m.ta.Height() + 1
	}
	return m.ta.Height()
}

// Focus gives the composer keyboard focus.
func (m *Model) Focus() tea.Cmd { return m.ta.Focus() }

// Blur removes keyboard focus.
func (m *Model) Blur() { m.ta.Blur() }

// Update edits the text. Enter with non-blank text clears the box and
// returns a SubmitMsg.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "enter" {
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return m, nil
		}
		m.ta.Reset()
		return m, func() tea.Msg { return SubmitMsg{Text: text} }
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// View renders the reply line, if any, and the text box.
func (m Model) View() string {
	if m.reply == "" {
		return m.ta.View()
	}
	line := ansi.Truncate("↪ Replying to "+m.reply, max(m.width-lipgloss.Width(" · esc cancels"), 0), "…")
	return theme.Dim.Render(line+" · esc cancels") + "\n" + m.ta.View()
}
