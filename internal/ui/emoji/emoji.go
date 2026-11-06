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

// Package emoji maps shortcodes such as "heart" (typed as :heart:) to emoji,
// using the names GitHub and Discord use.
package emoji

//go:generate sh -c "curl -sSfL https://raw.githubusercontent.com/github/gemoji/master/db/emoji.json | go run gen.go"

import (
	"slices"
	"strings"
)

// Emoji is one shortcode name, without colons, and the emoji it stands for.
// An emoji with several names has one entry per name.
type Emoji struct {
	Name string
	Char string
}

var byName = func() map[string]string {
	m := make(map[string]string, len(table))
	for _, e := range table {
		m[e.Name] = e.Char
	}
	return m
}()

// Lookup returns the emoji named name, ignoring case.
func Lookup(name string) (string, bool) {
	c, ok := byName[strings.ToLower(name)]
	return c, ok
}

// Search returns up to n emoji whose names contain query, ignoring case:
// names that start with it first, then shorter names first.
func Search(query string, n int) []Emoji {
	query = strings.ToLower(query)
	var starts, contains []Emoji
	for _, e := range table {
		switch {
		case strings.HasPrefix(e.Name, query):
			starts = append(starts, e)
		case strings.Contains(e.Name, query):
			contains = append(contains, e)
		}
	}
	shorter := func(a, b Emoji) int { return len(a.Name) - len(b.Name) }
	slices.SortStableFunc(starts, shorter)
	slices.SortStableFunc(contains, shorter)
	all := append(starts, contains...)
	return all[:min(n, len(all))]
}
