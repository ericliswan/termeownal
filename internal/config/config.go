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

// Package config loads termeownal's settings file and knows where its files
// live on each platform. Nothing secret belongs here.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/rs/zerolog"
)

// Config holds the user's settings. Every field has a default, so the
// settings file is optional.
type Config struct {
	// LogLevel is the lowest level written to the log file: debug, info,
	// warn or error.
	LogLevel zerolog.Level `toml:"log_level"`
	// ForceRefreshInterval is how often to force a full reconnect, which
	// keeps long sessions from drifting. Zero disables it.
	ForceRefreshInterval time.Duration `toml:"force_refresh_interval"`
}

// minForceRefresh matches upstream's limit of one full reconnect per hour.
const minForceRefresh = time.Hour

// Default returns the settings used when the file doesn't set them.
func Default() Config {
	return Config{
		LogLevel:             zerolog.InfoLevel,
		ForceRefreshInterval: 20 * time.Hour,
	}
}

// Load reads the settings file at path on top of the defaults. A missing
// file is not an error. Unknown keys are, so typos don't go unnoticed.
func Load(path string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return Config{}, fmt.Errorf("config %s: unknown setting %s", path, strings.Join(keys, ", "))
	}
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) validate() error {
	// Trace level would let upstream log raw payloads, which hold message
	// text (AGENTS.md rule 4).
	if c.LogLevel < zerolog.DebugLevel || c.LogLevel > zerolog.ErrorLevel {
		return fmt.Errorf("log_level %q: use debug, info, warn or error", c.LogLevel)
	}
	if c.ForceRefreshInterval != 0 && c.ForceRefreshInterval < minForceRefresh {
		return fmt.Errorf("force_refresh_interval %s: use 0 to disable, or at least %s",
			c.ForceRefreshInterval, minForceRefresh)
	}
	return nil
}
