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

package emoji

import (
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for name, want := range map[string]string{"heart": "❤️", "HEART": "❤️", "+1": "👍", "thumbsup": "👍", "joy": "😂"} {
		if got, ok := Lookup(name); !ok || got != want {
			t.Errorf("Lookup(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	if got, ok := Lookup("not_an_emoji"); ok {
		t.Errorf("Lookup(not_an_emoji) = %q, want none", got)
	}
}

func TestSearch(t *testing.T) {
	got := Search("hea", 3)
	if len(got) != 3 || got[0].Name != "heart" {
		t.Fatalf("Search(hea) = %v, want heart first", got)
	}
	// Names starting with the query beat names containing it.
	got = Search("heart", 100)
	seenContains := false
	for _, e := range got {
		starts := strings.HasPrefix(e.Name, "heart")
		if starts && seenContains {
			t.Fatalf("Search(heart) put %q after a name that only contains heart: %v", e.Name, got)
		}
		seenContains = seenContains || !starts
	}
	if !seenContains {
		t.Errorf("Search(heart) = %v, want names like broken_heart too", got)
	}
	if got := Search("zzzz", 5); len(got) != 0 {
		t.Errorf("Search(zzzz) = %v, want none", got)
	}
}
