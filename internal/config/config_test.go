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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/rs/zerolog"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Errorf("got %+v, want defaults %+v", cfg, Default())
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Config
	}{
		{"empty file", "", Default()},
		{
			"all settings",
			"log_level = \"debug\"\nforce_refresh_interval = \"6h30m\"\n",
			Config{LogLevel: zerolog.DebugLevel, ForceRefreshInterval: 6*time.Hour + 30*time.Minute},
		},
		{
			"one setting keeps other defaults",
			"log_level = \"warn\"\n",
			Config{LogLevel: zerolog.WarnLevel, ForceRefreshInterval: Default().ForceRefreshInterval},
		},
		{
			"refresh disabled",
			"force_refresh_interval = \"0s\"\n",
			Config{LogLevel: zerolog.InfoLevel},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(writeConfig(t, tt.content))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"unknown key", "log_levl = \"debug\"\n", "unknown setting log_levl"},
		{"unknown table key", "[ui]\ntheme = \"dark\"\n", "ui.theme"},
		{"bad level", "log_level = \"loud\"\n", "log_level"},
		{"trace level", "log_level = \"trace\"\n", "use debug, info, warn or error"},
		{"disabled level", "log_level = \"disabled\"\n", "use debug, info, warn or error"},
		{"refresh too often", "force_refresh_interval = \"10m\"\n", "at least 1h0m0s"},
		{"negative refresh", "force_refresh_interval = \"-1h\"\n", "force_refresh_interval"},
		{"bad duration", "force_refresh_interval = \"soon\"\n", "force_refresh_interval"},
		{"malformed toml", "log_level = \n", "read config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)
			_, err := Load(path)
			if err == nil {
				t.Fatal("got nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), path) {
				t.Errorf("error %q should mention %q and the path", err, tt.wantErr)
			}
		})
	}
}

func TestDefaultPathsFollowXDG(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(base, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(base, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	xdg.Reload()
	t.Cleanup(xdg.Reload) // t.Setenv restores the variables first

	want := Paths{
		ConfigFile: filepath.Join(base, "config", "termeownal", "config.toml"),
		DataDir:    filepath.Join(base, "data", "termeownal"),
		CacheFile:  filepath.Join(base, "data", "termeownal", "cache.db"),
		LogFile:    filepath.Join(base, "state", "termeownal", "meow.log"),
	}
	if got := DefaultPaths(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("DefaultPaths created %d entries", len(entries))
	}
}
