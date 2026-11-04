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

package theme

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Highlights use the terminal's own text colour: a fixed near-white vanishes
// on a light terminal.
func TestHighlightsUseTerminalForeground(t *testing.T) {
	for name, s := range map[string]lipgloss.Style{
		"Title":  Title,
		"OK":     lipgloss.NewStyle().Foreground(OK),
		"Error":  lipgloss.NewStyle().Foreground(Error),
		"Border": FocusedPane,
		"Banner": Banner,
	} {
		// 38 sets a text colour, 48 a background colour.
		if v := s.Render("x"); strings.Contains(v, "38;") || strings.Contains(v, "48;") {
			t.Errorf("%s sets a fixed text colour: %q", name, v)
		}
	}
}
