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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"termeownal/internal/backend/fake"
	"termeownal/internal/core"
	"termeownal/internal/secrets"
	"termeownal/internal/store"
)

func TestLoginSavesTheSessionAndAccount(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache.db")
	// The empty username is asked again; the password isn't echoed.
	c, out := testConsole("\rmeow.tester\rs3cret\r")
	if err := login(t.Context(), fake.Authenticator{}, c, secrets.New(dir, nil), cache); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "s3cret") || !strings.Contains(out.String(), "Logged in as") {
		t.Errorf("output = %q", out.String())
	}
	s, err := secrets.New(dir, nil).Load()
	if err != nil || len(s.Auth) == 0 {
		t.Fatalf("Load = %v, %v", s, err)
	}
	st, err := store.Open(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if me, _, err := st.Snapshot(t.Context(), 1); err != nil || me.Username == "" {
		t.Errorf("cached account = %+v, %v", me, err)
	}
}

func TestFailedLoginSavesNothing(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache.db")
	c, _ := testConsole("meow.tester\r" + fake.WrongPassword + "\r")
	if err := login(t.Context(), fake.Authenticator{}, c, secrets.New(dir, nil), cache); err == nil {
		t.Fatal("login succeeded with the wrong password")
	}
	if _, err := secrets.New(dir, nil).Load(); !errors.Is(err, secrets.ErrNoSession) {
		t.Errorf("Load = %v, want ErrNoSession", err)
	}
	if _, err := os.Stat(cache); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("cache: %v, want it not to exist", err)
	}
}

func TestLoginWithoutKeyringAsksForAPassphrase(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
	dir := t.TempDir()
	c, _ := testConsole("meow.tester\rs3cret\rpass\rpass\rpass\r")
	f := secrets.New(dir, c.passphrase)
	if err := login(t.Context(), fake.Authenticator{}, c, f, filepath.Join(dir, "cache.db")); err != nil {
		t.Fatal(err)
	}
	if _, err := secrets.New(dir, c.passphrase).Load(); err != nil {
		t.Errorf("Load = %v", err)
	}
}

func TestLoginHint(t *testing.T) {
	if hint := loginHint(fmt.Errorf("load inbox: %w", core.ErrSessionExpired), "meow.log"); !strings.Contains(hint, "cookies") {
		t.Errorf("session expired: hint = %q", hint)
	}
	if hint := loginHint(errCancelled, "meow.log"); hint != "" {
		t.Errorf("cancelled: hint = %q", hint)
	}
	if hint := loginHint(errors.New("boom"), "meow.log"); !strings.Contains(hint, "meow.log") {
		t.Errorf("other: hint = %q", hint)
	}
}

func TestLogout(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache.db")
	if err := logout(secrets.New(dir, nil), cache, true); err != nil {
		t.Fatalf("logout with nothing saved: %v", err)
	}
	for _, purge := range []bool{false, true} {
		if err := secrets.New(dir, nil).Create(core.Session{Auth: core.AuthData("fake")}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{cache, cache + "-wal", cache + "-shm"} {
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := logout(secrets.New(dir, nil), cache, purge); err != nil {
			t.Fatal(err)
		}
		if _, err := secrets.New(dir, nil).Load(); !errors.Is(err, secrets.ErrNoSession) {
			t.Errorf("purge=%v: Load = %v, want ErrNoSession", purge, err)
		}
		_, err := os.Stat(cache + "-wal")
		if gone := errors.Is(err, fs.ErrNotExist); gone != purge {
			t.Errorf("purge=%v: cache deleted = %v", purge, gone)
		}
	}
}
