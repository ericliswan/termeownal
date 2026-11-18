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

package app

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestNotification(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			if i := slices.Index(kv, k); i >= 0 {
				return kv[i+1]
			}
			return ""
		}
	}
	tests := []struct {
		name string
		env  func(string) string
		goos string
		seq  string
		argv []string
	}{
		{"iTerm2", env("TERM_PROGRAM", "iTerm.app"), "darwin", "\x1b]9;Ann: hi\x07", nil},
		{"kitty", env("KITTY_WINDOW_ID", "1"), "linux", "\x1b]99;;Ann: hi\x07", nil},
		{"in tmux", env("TERM_PROGRAM", "ghostty", "TMUX", "/tmp/tmux"), "linux", "",
			[]string{"notify-send", "--app-name=meow", "--", "Ann", "hi"}},
		{"Terminal.app", env("TERM_PROGRAM", "Apple_Terminal"), "darwin", "",
			[]string{"osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", "Ann", "hi"}},
		{"elsewhere", env(), "windows", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seq, argv := notification(tt.env, tt.goos, "Ann", "hi")
			if seq != tt.seq || !slices.Equal(argv, tt.argv) {
				t.Errorf("got %q, %q; want %q, %q", seq, argv, tt.seq, tt.argv)
			}
		})
	}
}

func TestNotifyIsSafeAndFollowsTheSetting(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	raw := func(a actions, title, body string) string {
		t.Helper()
		msg, ok := a.Notify(title, body)().(tea.RawMsg)
		if !ok {
			t.Fatal("no escape sequence")
		}
		return msg.Msg.(string)
	}
	if got := raw(actions{}, "Ann", "secret"); strings.Contains(got, "secret") {
		t.Errorf("notify_preview off, sent %q", got)
	}
	got := raw(actions{notifyPreview: true}, "Ann\x07", "hi\x1b]9;x\x07"+strings.Repeat("a", 300))
	if strings.Count(got, "\x1b") != 1 || strings.Count(got, "\x07") != 1 || len([]rune(got)) > 2*maxNotify+10 {
		t.Errorf("unsafe sequence %q", got)
	}
}
