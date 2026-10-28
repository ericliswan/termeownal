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

// Package theme holds the colours and styles shared by the UI.
package theme

import "charm.land/lipgloss/v2"

// Colours. Bubble Tea downgrades them to what the terminal supports and
// drops them entirely under NO_COLOR, so nothing may depend on colour alone.
var (
	Accent = lipgloss.Color("#C084FC")
	Subtle = lipgloss.Color("#6B7280")
	Error  = lipgloss.Color("#F87171")
	OK     = lipgloss.Color("#4ADE80")
	Warn   = lipgloss.Color("#FBBF24")
)

// Styles shared across components.
var (
	Bold  = lipgloss.NewStyle().Bold(true)
	Dim   = lipgloss.NewStyle().Foreground(Subtle)
	Title = lipgloss.NewStyle().Bold(true).Foreground(Accent)

	// Pane draws a component's border; FocusedPane marks the one with focus.
	Pane        = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(Subtle)
	FocusedPane = Pane.BorderForeground(Accent).BorderStyle(lipgloss.ThickBorder())

	Banner = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).Background(Error)
)
