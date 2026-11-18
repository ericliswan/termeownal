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
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// notifyTimeout bounds the OS command that shows a notification.
const notifyTimeout = 5 * time.Second

// maxNotify caps the length of a notification's title and body, in runes.
const maxNotify = 200

// Notify shows a desktop notification if notify is on, leaving out body
// unless notify_preview is on. Failures are only logged: a notification is a
// hint.
func (a actions) Notify(title, body string) tea.Cmd {
	if !a.notify {
		return nil
	}
	if !a.notifyPreview {
		body = ""
	}
	seq, argv := notification(os.Getenv, runtime.GOOS, oneLine(title), oneLine(body))
	switch {
	case seq != "":
		return tea.Raw(seq)
	case argv == nil:
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, notifyTimeout)
		defer cancel()
		if err := exec.CommandContext(ctx, argv[0], argv[1:]...).Run(); err != nil {
			a.log.Debug().Err(err).Msg("notification not shown")
		}
		return nil
	}
}

// notification returns the escape sequence that shows a notification in the
// terminal getenv describes, if it has one; otherwise the command that shows
// one on goos, if there is one.
func notification(getenv func(string) string, goos, title, body string) (seq string, argv []string) {
	text := title
	if body != "" {
		text += ": " + body
	}
	if getenv("TMUX") == "" { // tmux drops these unless set to pass them through
		switch {
		case getenv("KITTY_WINDOW_ID") != "":
			return ansi.DesktopNotification(text), nil
		case slices.Contains([]string{"iTerm.app", "WezTerm", "ghostty"}, getenv("TERM_PROGRAM")):
			return ansi.Notify(text), nil
		}
	}
	switch goos {
	case "darwin":
		// Passed as arguments, so the text needs no AppleScript quoting.
		return "", []string{"osascript",
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
			"-e", "end run", title, body}
	case "linux":
		return "", []string{"notify-send", "--app-name=meow", "--", title, body}
	}
	return "", nil
}

// oneLine makes s safe to put in an escape sequence or a notification: one
// line, no control characters, at most maxNotify runes.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if r := []rune(s); len(r) > maxNotify {
		s = string(r[:maxNotify-1]) + "…"
	}
	return s
}
