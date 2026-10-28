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

package config

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

const appName = "termeownal"

// Paths are where termeownal keeps its files. On macOS all three live under
// ~/Library/Application Support/termeownal; on Linux they follow the XDG
// base directories. The XDG_* variables override them on every platform.
type Paths struct {
	// ConfigFile is the optional settings file.
	ConfigFile string
	// DataDir holds the encrypted session file and the message cache.
	DataDir string
	// CacheFile is the message cache, in DataDir.
	CacheFile string
	// LogFile is the log, in the state directory.
	LogFile string
}

// DefaultPaths returns the platform paths. It creates nothing.
func DefaultPaths() Paths {
	return Paths{
		ConfigFile: filepath.Join(xdg.ConfigHome, appName, "config.toml"),
		DataDir:    filepath.Join(xdg.DataHome, appName),
		CacheFile:  filepath.Join(xdg.DataHome, appName, "cache.db"),
		LogFile:    filepath.Join(xdg.StateHome, appName, "meow.log"),
	}
}
