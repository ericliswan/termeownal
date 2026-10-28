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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func TestOpenLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "termeownal", "meow.log")

	for _, msg := range []string{"first run", "second run"} {
		log, closer, err := OpenLog(path, zerolog.InfoLevel)
		if err != nil {
			t.Fatal(err)
		}
		log.Debug().Msg("hidden")
		log.Info().Msg(msg)
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines (appended, debug filtered), got %d:\n%s", len(lines), data)
	}
	for i, want := range []string{"first run", "second run"} {
		var entry struct{ Level, Message, Time string }
		if err := json.Unmarshal([]byte(lines[i]), &entry); err != nil {
			t.Fatalf("line %d isn't JSON: %v", i, err)
		}
		if entry.Level != "info" || entry.Message != want || entry.Time == "" {
			t.Errorf("line %d = %+v, want info %q with a time", i, entry, want)
		}
	}

	assertMode(t, path, 0o600)
	assertMode(t, filepath.Dir(path), 0o700)
}

func TestOpenLogTightensExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meow.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, closer, err := OpenLog(path, zerolog.InfoLevel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	assertMode(t, path, 0o600)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}
