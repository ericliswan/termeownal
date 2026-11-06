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
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"termeownal/internal/ui/emoji"
	"termeownal/internal/ui/theme"
)

// SubmitMsg carries the text the user sent.
type SubmitMsg struct{ Text string }

// maxSuggestions is how many emoji a shortcode suggests at most.
const maxSuggestions = 5

// Model wraps a textarea where Enter sends and Alt+Enter starts a new line.
// A reply shows what it replies to above the text. Typing a shortcode such
// as :hea suggests emoji above the text; :heart: becomes the emoji.
type Model struct {
	ta    textarea.Model
	width int
	reply string
	// sugg are the emoji matching query, the shortcode being typed without
	// its colon; pick is the highlighted one.
	sugg  []emoji.Emoji
	pick  int
	query string
	// closed is a query whose suggestions Esc closed.
	closed string
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
	h := m.ta.Height() + len(m.sugg)
	if m.reply != "" {
		h++
	}
	return h
}

// Suggesting reports whether emoji suggestions are showing. Tab, Enter, ↑, ↓
// and Esc then act on them.
func (m Model) Suggesting() bool { return len(m.sugg) > 0 }

// Focus gives the composer keyboard focus.
func (m *Model) Focus() tea.Cmd { return m.ta.Focus() }

// Blur removes keyboard focus and closes the suggestions.
func (m *Model) Blur() { m.ta.Blur(); m.sugg, m.query = nil, "" }

// Update edits the text. Enter with non-blank text clears the box and
// returns a SubmitMsg, unless it picks a suggestion.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	k, isKey := msg.(tea.KeyPressMsg)
	if isKey && m.Suggesting() {
		switch k.String() {
		case "up":
			m.pick = (m.pick + len(m.sugg) - 1) % len(m.sugg)
			return m, nil
		case "down":
			m.pick = (m.pick + 1) % len(m.sugg)
			return m, nil
		case "tab", "enter":
			m.replace(len([]rune(m.query))+1, m.sugg[m.pick].Char+" ")
			m.suggest()
			return m, nil
		case "esc":
			m.closed, m.sugg = m.query, nil
			return m, nil
		}
	}
	if isKey && k.String() == "enter" {
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return m, nil
		}
		m.ta.Reset()
		return m, func() tea.Msg { return SubmitMsg{Text: text} }
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	if isKey && k.Text == ":" {
		m.convert()
	}
	m.suggest()
	return m, cmd
}

// shortcode returns the shortcode being typed: the letters after a colon
// that ends at the cursor, or at the colon before it if closing is set. The
// colon has to start a word, so "10:30" isn't one.
func (m Model) shortcode(closing bool) (string, bool) {
	line := []rune(strings.Split(m.ta.Value(), "\n")[m.ta.Line()])
	before := line[:min(m.ta.Column(), len(line))]
	if closing {
		if len(before) == 0 || before[len(before)-1] != ':' {
			return "", false
		}
		before = before[:len(before)-1]
	}
	i := len(before) - 1
	for i >= 0 && isNameRune(before[i]) {
		i--
	}
	if i < 0 || before[i] != ':' || i+1 == len(before) {
		return "", false
	}
	if i > 0 && (unicode.IsLetter(before[i-1]) || unicode.IsDigit(before[i-1]) || before[i-1] == ':') {
		return "", false
	}
	return string(before[i+1:]), true
}

func isNameRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '+' || r == '-'
}

// convert turns a just-closed :name: into its emoji.
func (m *Model) convert() {
	name, ok := m.shortcode(true)
	if !ok {
		return
	}
	if c, ok := emoji.Lookup(name); ok {
		m.replace(len([]rune(name))+2, c)
	}
}

// suggest updates the suggestions for the shortcode at the cursor, which
// needs two letters.
func (m *Model) suggest() {
	q, ok := m.shortcode(false)
	if !ok || len(q) < 2 {
		m.sugg, m.query, m.closed = nil, "", ""
		return
	}
	if q != m.query {
		m.pick = 0
	}
	m.query = q
	if q == m.closed {
		m.sugg = nil
		return
	}
	m.sugg = emoji.Search(q, maxSuggestions)
}

// replace swaps the n characters before the cursor for s.
func (m *Model) replace(n int, s string) {
	for range n {
		m.ta, _ = m.ta.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	m.ta.InsertString(s)
}

// View renders the emoji suggestions and the reply line, if any, and the
// text box.
func (m Model) View() string {
	var b strings.Builder
	for i, e := range m.sugg {
		mark := "  "
		if i == m.pick {
			mark = theme.Title.Render("▌") + " "
		}
		b.WriteString(ansi.Truncate(mark+e.Char+"  "+theme.Dim.Render(":"+e.Name+":"), m.width, "…") + "\n")
	}
	if m.reply != "" {
		line := ansi.Truncate("↪ Replying to "+m.reply, max(m.width-lipgloss.Width(" · esc cancels"), 0), "…")
		b.WriteString(theme.Dim.Render(line+" · esc cancels") + "\n")
	}
	return b.String() + m.ta.View()
}
