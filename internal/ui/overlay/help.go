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

// Package overlay draws boxes shown on top of the main screen.
package overlay

import (
	"strings"

	"charm.land/lipgloss/v2"

	"termeownal/internal/ui/theme"
)

var helpKeys = [][2]string{
	{"Tab / Shift+Tab", "move focus"},
	{"j k ↑ ↓", "move / scroll"},
	{"Enter", "open thread / send"},
	{"Alt+Enter", "new line in message"},
	{"g / G", "top / bottom"},
	{"PgUp", "page up; at the top, load older"},
	{"Esc", "back to the thread list"},
	{"?", "this help"},
	{"Ctrl+C", "quit"},
}

// Help is the key reference box.
func Help() string {
	var b strings.Builder
	b.WriteString(theme.Title.Render("Keys") + "\n\n")
	for _, k := range helpKeys {
		b.WriteString(theme.Bold.Render(lipgloss.PlaceHorizontal(18, lipgloss.Left, k[0])) + k[1] + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("Esc or ? to close"))
	return theme.FocusedPane.Padding(1, 2).Render(b.String())
}

// Center draws box over the middle of background, which is w×h cells.
func Center(background, box string, w, h int) string {
	x := max((w-lipgloss.Width(box))/2, 0)
	y := max((h-lipgloss.Height(box))/2, 0)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(box).X(x).Y(y).Z(1),
	).Render()
}
