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

package main

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

// testConsole answers from input, with no terminal.
func testConsole(input string) (*console, *strings.Builder) {
	out := new(strings.Builder)
	return &console{
		in:  bufio.NewReader(strings.NewReader(input)),
		out: out,
		raw: func() (func(), error) { return func() {}, nil },
	}, out
}

func TestReadAnswer(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"line":            {"abc\r", "abc"},
		"newline":         {"abc\n", "abc"},
		"paste":           {"\x1b[200~curl x \\\r\n  -b 'a=b'\r\n\x1b[201~\r", "curl x \\\n  -b 'a=b'\n"},
		"paste, no Enter": {"\x1b[200~a\rb\x1b[201~c\r", "a\nbc"},
		"continuation":    {"curl x \\\r  -b 'a=b'\r", "curl x \\\n  -b 'a=b'"},
		"backspace":       {"abd\x7fc\r", "abc"},
		"backspace rune":  {"aé\x7f\x08\x7fb\r", "b"},
		"arrows":          {"a\x1b[Db\x1bOAc\x1b[1;5Cd\r", "abcd"},
		"lone ESC":        {"a\x1b\r", "a"},
		"other controls":  {"a\x01\tb\x04\r", "a\tb"},
		"next answer":     {"a\rb\r", "a"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := readAnswer(bufio.NewReader(strings.NewReader(tc.in)), nil)
			if err != nil || got != tc.want {
				t.Errorf("readAnswer(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
	for name, in := range map[string]string{"Ctrl+C": "ab\x03", "Ctrl+D": "\x04", "in paste": "\x1b[200~a\x03"} {
		if _, err := readAnswer(bufio.NewReader(strings.NewReader(in)), nil); !errors.Is(err, errCancelled) {
			t.Errorf("%s: err = %v, want errCancelled", name, err)
		}
	}
	if _, err := readAnswer(bufio.NewReader(strings.NewReader("abc")), nil); err != io.EOF {
		t.Errorf("unfinished: err = %v, want EOF", err)
	}
	if _, err := readAnswer(bufio.NewReader(strings.NewReader(strings.Repeat("a", maxAnswer+1)+"\r")), nil); err == nil {
		t.Error("an answer over maxAnswer was accepted")
	}
}

func TestAskEchoesOnlyWhatIsntSecret(t *testing.T) {
	c, out := testConsole("mée\x7f\rhidden\r")
	if got, err := c.ask("Name", false); err != nil || got != "mé" {
		t.Fatalf("ask = %q, %v", got, err)
	}
	if got, err := c.ask("Secret", true); err != nil || got != "hidden" {
		t.Fatalf("ask = %q, %v", got, err)
	}
	if want := "Name: " + pasteOn + "mée\b \b" + pasteOff + "\nSecret: " + pasteOn + pasteOff + "\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestPassphraseIsTypedTwiceWhenNew(t *testing.T) {
	c, out := testConsole("one\rtwo\r\rpass\rpass\r")
	got, err := c.passphrase(true)
	if err != nil || string(got) != "pass" {
		t.Fatalf("passphrase = %q, %v", got, err)
	}
	if !strings.Contains(out.String(), "don't match") || !strings.Contains(out.String(), "can't be empty") {
		t.Errorf("output = %q", out.String())
	}
	c, _ = testConsole("old\r")
	if got, err := c.passphrase(false); err != nil || string(got) != "old" {
		t.Errorf("passphrase = %q, %v", got, err)
	}
}
