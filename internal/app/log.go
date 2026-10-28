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

// Package app wires termeownal together and runs it.
package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"
)

// OpenLog opens the log file at path for appending, creating its directory,
// and returns a logger that writes JSON lines at level and above. The log
// never goes to the terminal, which belongs to the TUI. Close the returned
// file on exit.
func OpenLog(path string, level zerolog.Level) (zerolog.Logger, io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return zerolog.Nop(), nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return zerolog.Nop(), nil, fmt.Errorf("open log: %w", err)
	}
	// Debug lines can hold IDs and thread names, so tighten a file someone
	// created with wider permissions.
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return zerolog.Nop(), nil, fmt.Errorf("restrict log permissions: %w", err)
	}
	return zerolog.New(f).Level(level).With().Timestamp().Logger(), f, nil
}
