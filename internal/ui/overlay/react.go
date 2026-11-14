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

	tea "charm.land/bubbletea/v2"

	"termeownal/internal/ui/emoji"
	"termeownal/internal/ui/theme"
)

// quick are the reactions offered before the user searches.
var quick = []emoji.Emoji{
	{Name: "heart", Char: "❤️"},
	{Name: "joy", Char: "😂"},
	{Name: "open_mouth", Char: "😮"},
	{Name: "cry", Char: "😢"},
	{Name: "rage", Char: "😡"},
	{Name: "+1", Char: "👍"},
}

// Picker chooses a reaction: a quick one, or one found by typing part of
// its shortcode name.
type Picker struct {
	query string
	sel   int
	// current is the user's reaction now; picking it again removes it.
	current string
}

// NewPicker returns a picker for a message the user reacted to with
// current, or "" for none.
func NewPicker(current string) Picker { return Picker{current: current} }

func (p Picker) options() []emoji.Emoji {
	if p.query == "" {
		return quick
	}
	return emoji.Search(p.query, len(quick))
}

// Update handles a key. It returns the emoji picked, or "", and whether the
// picker is done: picked or cancelled with Esc.
func (p Picker) Update(k tea.KeyPressMsg) (Picker, string, bool) {
	opts := p.options()
	switch k.String() {
	case "esc":
		return p, "", true
	case "enter", "tab":
		if len(opts) == 0 {
			return p, "", false
		}
		return p, opts[p.sel].Char, true
	case "left", "up":
		p.sel = max(p.sel-1, 0)
	case "right", "down":
		p.sel = min(p.sel+1, max(len(opts)-1, 0))
	case "backspace":
		if p.query != "" {
			p.query, p.sel = p.query[:len(p.query)-1], 0
		}
	default:
		if nameText(k.Text) {
			p.query, p.sel = p.query+strings.ToLower(k.Text), 0
		}
	}
	return p, "", false
}

// nameText reports whether s can be part of a shortcode name; these are
// ASCII, so backspace can drop the query's last byte.
func nameText(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_+-", r)
		if !ok {
			return false
		}
	}
	return true
}

// View draws the picker as a box.
func (p Picker) View() string {
	var b strings.Builder
	b.WriteString(theme.Title.Render("React") + "\n\n")
	opts := p.options()
	if len(opts) == 0 {
		b.WriteString(theme.Dim.Render("no match") + "\n\n")
	} else {
		for i, e := range opts {
			if i == p.sel {
				b.WriteString(theme.Banner.Render(" " + e.Char + " "))
			} else {
				b.WriteString(" " + e.Char + " ")
			}
		}
		name := ":" + opts[p.sel].Name + ":"
		if opts[p.sel].Char == p.current {
			name += " (yours; Enter removes it)"
		}
		b.WriteString("\n" + theme.Dim.Render(name) + "\n\n")
	}
	if p.query == "" {
		b.WriteString(theme.Dim.Render("type to search") + "\n")
	} else {
		b.WriteString("search: " + p.query + "\n")
	}
	b.WriteString(theme.Dim.Render("←→ choose · Enter react · Esc cancel"))
	return theme.FocusedPane.Padding(1, 2).Render(b.String())
}
