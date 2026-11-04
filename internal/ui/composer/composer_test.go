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
	"regexp"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// darkOnly matches the 16-colour codes that vanish on one terminal
// background: a black background (40), or light grey or white text (37, 97).
var darkOnly = regexp.MustCompile(`\x1b\[[0-9;]*\b(40|37|97)m`)

// The composer must stay readable on light and dark terminals alike.
func TestViewReadableOnAnyBackground(t *testing.T) {
	m := New()
	m.SetWidth(40)
	m.Focus()
	for _, r := range "hi" {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if v := m.View(); darkOnly.MatchString(v) {
		t.Errorf("focused view uses a colour that vanishes on light or dark terminals: %q", v)
	}
	m.Blur()
	if v := m.View(); darkOnly.MatchString(v) {
		t.Errorf("blurred view uses a colour that vanishes on light or dark terminals: %q", v)
	}
}
