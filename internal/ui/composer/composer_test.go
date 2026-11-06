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

package composer

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// typing returns a focused composer after typing text and pressing keys.
func typing(t *testing.T, text string, keys ...tea.KeyPressMsg) Model {
	t.Helper()
	m := New()
	m.SetWidth(40)
	m.Focus()
	for _, r := range text {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	for _, k := range keys {
		m, _ = m.Update(k)
	}
	return m
}

var (
	tab  = tea.KeyPressMsg{Code: tea.KeyTab}
	esc  = tea.KeyPressMsg{Code: tea.KeyEscape}
	down = tea.KeyPressMsg{Code: tea.KeyDown}
)

func TestShortcodeConverts(t *testing.T) {
	for text, want := range map[string]string{
		"love :heart:":     "love ❤️",
		":+1:":             "👍",
		"(:joy:)":          "(😂)",
		":HEART:":          "❤️",
		"at 10:30:":        "at 10:30:", // not after a letter or digit
		"a:heart:":         "a:heart:",
		":not_an_emoji:":   ":not_an_emoji:",
		"::":               "::",
		"one\ntwo :fire:":  "one\ntwo 🔥",
		":heart: :heart: ": "❤️ ❤️ ",
	} {
		m := New()
		m.Focus()
		for _, r := range text {
			k := tea.KeyPressMsg{Code: r, Text: string(r)}
			if r == '\n' {
				k = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
			}
			m, _ = m.Update(k)
		}
		if got := m.ta.Value(); got != want {
			t.Errorf("typing %q gives %q, want %q", text, got, want)
		}
	}
}

func TestSuggestions(t *testing.T) {
	m := typing(t, "hi :h")
	if m.Suggesting() {
		t.Fatal("suggesting after one letter")
	}
	h := m.Height()
	m = typing(t, "hi :hea")
	if !m.Suggesting() || m.Height() != h+len(m.sugg) {
		t.Fatalf("suggestions %v, height %d; want some, height %d+their count", m.sugg, m.Height(), h)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "❤️  :heart:") {
		t.Fatalf("suggestions don't show :heart: first:\n%s", v)
	}

	m = typing(t, "hi :hea", tab)
	if got := m.ta.Value(); got != "hi ❤️ " || m.Suggesting() {
		t.Fatalf("tab gives %q (suggesting %v), want %q", got, m.Suggesting(), "hi ❤️ ")
	}
	first := typing(t, ":hea").sugg[0]
	second := typing(t, ":hea").sugg[1]
	if got := typing(t, ":hea", down, tab).ta.Value(); got != second.Char+" " {
		t.Fatalf("down then tab gives %q, want %q (not %q)", got, second.Char+" ", first.Char)
	}

	m = typing(t, "hi :hea", esc)
	if m.Suggesting() || m.ta.Value() != "hi :hea" {
		t.Fatalf("esc: suggesting %v, text %q; want closed, text kept", m.Suggesting(), m.ta.Value())
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if !m.Suggesting() {
		t.Fatal("typing on after esc doesn't reopen the suggestions")
	}
}

func TestEnterPicksSuggestionInsteadOfSending(t *testing.T) {
	m, cmd := typing(t, "hi :hea").Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil || m.ta.Value() != "hi ❤️ " {
		t.Fatalf("enter: cmd %v, text %q; want no send, the emoji picked", cmd, m.ta.Value())
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if msg, ok := cmd().(SubmitMsg); !ok || msg.Text != "hi ❤️" {
		t.Fatalf("second enter sends %+v, want %q", msg, "hi ❤️")
	}
}
