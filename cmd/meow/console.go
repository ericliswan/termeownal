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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"termeownal/internal/core"
)

const (
	// maxAnswer bounds one answer; a curl command is a few KiB.
	maxAnswer = 1 << 20
	// pasteOn and pasteOff turn bracketed paste on and off, so the terminal
	// marks where a paste starts and ends.
	pasteOn  = "\x1b[?2004h"
	pasteOff = "\x1b[?2004l"
)

var errCancelled = errors.New("cancelled")

// console asks questions on the terminal. Answers are read in raw mode,
// not line by line: the terminal's line editor caps a line (at 1024 bytes on
// macOS), which a pasted curl command is longer than.
type console struct {
	in  *bufio.Reader
	out io.Writer
	// raw puts the terminal in raw mode and returns a function that undoes it.
	raw func() (restore func(), err error)
}

func newConsole(in *os.File, out io.Writer) *console {
	fd := int(in.Fd())
	return &console{in: bufio.NewReader(in), out: out, raw: func() (func(), error) {
		if !term.IsTerminal(fd) {
			return nil, errors.New("this needs a terminal: secrets are read from it with echo off")
		}
		old, err := term.MakeRaw(fd)
		if err != nil {
			return nil, err
		}
		return func() { _ = term.Restore(fd, old) }, nil
	}}
}

// printf writes to the terminal. A failed write has nowhere to be reported.
func (c *console) printf(format string, a ...any) { _, _ = fmt.Fprintf(c.out, format, a...) }

// ask prints label and reads an answer, echoing it unless secret.
func (c *console) ask(label string, secret bool) (string, error) {
	c.printf("%s: ", label)
	restore, err := c.raw()
	if err != nil {
		return "", err
	}
	c.printf(pasteOn)
	echo := c.out
	if secret {
		echo = nil
	}
	s, err := readAnswer(c.in, echo)
	c.printf(pasteOff)
	restore()
	c.printf("\n")
	return s, err
}

// field asks for a login field, again while a required one is empty.
func (c *console) field(f core.LoginField) (string, error) {
	for {
		s, err := c.ask(f.Label, f.Secret)
		if err != nil || f.Optional || strings.TrimSpace(s) != "" {
			return s, err
		}
	}
}

// passphrase reads the passphrase that stands in for the keyring. A new one
// is typed twice.
func (c *console) passphrase(create bool) ([]byte, error) {
	if !create {
		s, err := c.ask("Passphrase", true)
		return []byte(s), err
	}
	c.printf("There's no keyring to keep the session key in, so choose a passphrase to encrypt the session with. meow asks for it each time it starts.\n")
	for {
		s, err := c.ask("New passphrase", true)
		if err != nil {
			return nil, err
		}
		if s == "" {
			c.printf("The passphrase can't be empty.\n")
			continue
		}
		again, err := c.ask("Type it again", true)
		if err != nil {
			return nil, err
		}
		if s == again {
			return []byte(s), nil
		}
		c.printf("They don't match; try again.\n")
	}
}

// readAnswer reads one answer from a terminal in raw mode, up to Enter.
// Enter inside a bracketed paste, or after a backslash, is a newline, so a
// multi-line curl command arrives whole. Other keys with no text, such as
// arrows, are ignored. echo, if not nil, is sent what's typed.
func readAnswer(in *bufio.Reader, echo io.Writer) (string, error) {
	var buf []byte
	var pasting, afterCR bool
	show := func(s string) {
		if echo != nil {
			_, _ = io.WriteString(echo, s)
		}
	}
	for {
		b, err := in.ReadByte()
		if err != nil {
			return "", err
		}
		wasCR := afterCR
		afterCR = b == '\r'
		switch {
		case b == 0x03 || b == 0x04 && len(buf) == 0: // Ctrl+C, or Ctrl+D on nothing
			return "", errCancelled
		case b == 0x1b:
			switch seq, err := readEscape(in); {
			case err != nil:
				return "", err
			case seq == "[200~":
				pasting = true
			case seq == "[201~":
				pasting = false
			}
		case b == '\n' && wasCR: // the second half of a CRLF
		case b == '\r' || b == '\n':
			if !pasting && !bytes.HasSuffix(buf, []byte{'\\'}) {
				return string(buf), nil
			}
			buf = append(buf, '\n')
			show("\r\n")
		case b == 0x7f || b == 0x08: // Backspace
			if len(buf) > 0 {
				_, n := utf8.DecodeLastRune(buf)
				buf = buf[:len(buf)-n]
				show("\b \b")
			}
		case b < 0x20 && b != '\t':
		default:
			if len(buf) >= maxAnswer {
				return "", errors.New("that's too long")
			}
			buf = append(buf, b)
			show(string(buf[len(buf)-1:]))
		}
	}
}

// readEscape reads the rest of an escape sequence after its ESC and returns
// it. After a lone ESC it leaves the next byte to be read again.
func readEscape(in *bufio.Reader) (string, error) {
	b, err := in.ReadByte()
	if err != nil {
		return "", err
	}
	switch b {
	case '[': // CSI: parameters, then a final byte from @ to ~
		seq := []byte{b}
		for len(seq) < 32 {
			b, err := in.ReadByte()
			if err != nil {
				return "", err
			}
			seq = append(seq, b)
			if b >= 0x40 && b <= 0x7e {
				break
			}
		}
		return string(seq), nil
	case 'O': // SS3, as some terminals send arrows: one more byte
		_, err := in.ReadByte()
		return "", err
	default:
		return "", in.UnreadByte()
	}
}
