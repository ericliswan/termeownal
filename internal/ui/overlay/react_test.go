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

package overlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPicker(t *testing.T) {
	keys := map[string]tea.KeyPressMsg{
		"enter":     {Code: tea.KeyEnter},
		"esc":       {Code: tea.KeyEscape},
		"right":     {Code: tea.KeyRight},
		"left":      {Code: tea.KeyLeft},
		"backspace": {Code: tea.KeyBackspace},
		"space":     {Code: tea.KeySpace, Text: " "},
	}
	tests := []struct {
		name   string
		keys   []string
		picked string
		done   bool
	}{
		{"first quick", []string{"enter"}, "❤️", true},
		{"moved", []string{"right", "right", "enter"}, "😮", true},
		{"stops at the ends", []string{"left", "enter"}, "❤️", true},
		{"search", []string{"t", "h", "u", "m", "b", "s", "u", "p", "enter"}, "👍", true},
		{"backspace", []string{"j", "x", "backspace", "o", "y", "enter"}, "😂", true},
		{"space ignored", []string{"space", "enter"}, "❤️", true},
		{"no match", []string{"z", "z", "z", "z", "z", "enter"}, "", false},
		{"cancel", []string{"right", "esc"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPicker("")
			var picked string
			var done bool
			for _, k := range tt.keys {
				msg, ok := keys[k]
				if !ok {
					msg = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
				}
				p, picked, done = p.Update(msg)
			}
			if picked != tt.picked || done != tt.done {
				t.Fatalf("picked %q, done %v; want %q, %v", picked, done, tt.picked, tt.done)
			}
		})
	}
}

func TestPickerMarksCurrent(t *testing.T) {
	if v := ansi.Strip(NewPicker("❤️").View()); !strings.Contains(v, "Enter removes it") {
		t.Fatalf("current reaction not marked:\n%s", v)
	}
	if v := ansi.Strip(NewPicker("😂").View()); strings.Contains(v, "Enter removes it") {
		t.Fatalf("another reaction marked as current:\n%s", v)
	}
}
