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

package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"termeownal/internal/core"
)

// The keyring is an in-memory mock, so tests never touch the real one. It
// isn't safe for concurrent use, so no test here runs in parallel.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

var (
	session1 = core.Session{Auth: core.AuthData(`{"cookie":"value-1"}`), SeqID: 42, SeqAt: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	session2 = core.Session{Auth: core.AuthData(`{"cookie":"value-2"}`), SeqID: 43, SeqAt: time.Date(2026, 10, 29, 12, 5, 0, 0, time.UTC)}
)

func TestKeyringRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	if err := New(dir, noAsk(t)).Create(session1); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %v, want 0600", perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range [][]byte{session1.Auth, []byte(base64.StdEncoding.EncodeToString(session1.Auth))} {
		if bytes.Contains(data, plain) {
			t.Errorf("the file holds the login data unencrypted")
		}
	}

	f := New(dir, noAsk(t))
	got, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	assertSession(t, got, session1)
	if err := f.Save(session2); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(core.Session{SeqID: 44}); err == nil {
		t.Error("saved a session with no login data")
	}
	got, err = New(dir, noAsk(t)).Load()
	if err != nil {
		t.Fatal(err)
	}
	assertSession(t, got, session2)
}

func TestPassphraseRoundTrip(t *testing.T) {
	noKeyring(t)
	dir := t.TempDir()
	var asked []bool
	ask := func(pass string) func(bool) ([]byte, error) {
		return func(create bool) ([]byte, error) {
			asked = append(asked, create)
			return []byte(pass), nil
		}
	}

	f := New(dir, ask("correct horse"))
	if err := f.Create(session1); err != nil {
		t.Fatal(err)
	}
	if err := f.Save(session2); err != nil {
		t.Fatal(err)
	}
	got, err := New(dir, ask("correct horse")).Load()
	if err != nil {
		t.Fatal(err)
	}
	assertSession(t, got, session2)
	if _, err := New(dir, ask("wrong horse")).Load(); !errors.Is(err, ErrWrongPassphrase) {
		t.Errorf("Load with the wrong passphrase = %v, want ErrWrongPassphrase", err)
	}
	// Once each by Create and the two Loads; Save reuses the key.
	if want := []bool{true, false, false}; !slices.Equal(asked, want) {
		t.Errorf("asked for passphrases %v, want %v (true = new)", asked, want)
	}
}

func TestEmptyPassphraseRefused(t *testing.T) {
	noKeyring(t)
	dir := t.TempDir()
	empty := func(bool) ([]byte, error) { return nil, nil }
	if err := New(dir, empty).Create(session1); err == nil {
		t.Error("Create with an empty passphrase succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat = %v, want no file", err)
	}
}

func TestNoSession(t *testing.T) {
	if _, err := New(t.TempDir(), noAsk(t)).Load(); !errors.Is(err, ErrNoSession) {
		t.Errorf("Load = %v, want ErrNoSession", err)
	}
}

func TestUnreadable(t *testing.T) {
	edit := func(change func([]byte) []byte) func(string) error {
		return func(path string) error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, change(data), 0o600)
		}
	}
	flip := func(i int) func(string) error {
		return edit(func(b []byte) []byte {
			b[(i+len(b))%len(b)] ^= 1
			return b
		})
	}
	for name, spoil := range map[string]func(path string) error{
		"not a session file": flip(0),
		"other version":      flip(4),
		"unknown key kind":   flip(5),
		"header changed":     flip(6), // the salt: unused with a keyring key, but authenticated
		"ciphertext changed": flip(-1),
		"truncated":          edit(func(b []byte) []byte { return b[:len(b)-1] }),
		"empty":              edit(func([]byte) []byte { return nil }),
		"key gone":           func(path string) error { return keyring.Delete(service, path) },
		"bad key":            func(path string) error { return keyring.Set(service, path, "AAAA") },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := New(dir, noAsk(t)).Create(session1); err != nil {
				t.Fatal(err)
			}
			if err := spoil(filepath.Join(dir, fileName)); err != nil {
				t.Fatal(err)
			}
			if _, err := New(dir, noAsk(t)).Load(); !errors.Is(err, ErrUnreadable) {
				t.Errorf("Load = %v, want ErrUnreadable", err)
			}
		})
	}
}

func TestSaveReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	f := New(dir, noAsk(t))
	if err := f.Create(session1); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = old.Close() }()

	if err := f.Save(session2); err != nil {
		t.Fatal(err)
	}
	// Writing in place would change what the open file reads.
	if after, err := io.ReadAll(old); err != nil || !bytes.Equal(after, before) {
		t.Errorf("Save wrote into the old file instead of replacing it (err %v)", err)
	}
	assertOnlySessionFile(t, dir)
}

func TestFailedSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	// A directory in the file's place makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(dir, fileName, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := New(dir, noAsk(t)).Create(session1); err == nil {
		t.Fatal("Create over a directory succeeded")
	}
	assertOnlySessionFile(t, dir)
}

func TestDelete(t *testing.T) {
	t.Run("keyring", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, fileName)
		f := New(dir, noAsk(t))
		if err := f.Create(session1); err != nil {
			t.Fatal(err)
		}
		if err := f.Delete(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat = %v, want no file", err)
		}
		if _, err := keyring.Get(service, path); !errors.Is(err, keyring.ErrNotFound) {
			t.Errorf("keyring.Get = %v, want ErrNotFound", err)
		}
		if err := f.Delete(); err != nil {
			t.Errorf("second Delete = %v", err)
		}
		if err := f.Save(session2); err == nil {
			t.Error("Save after Delete succeeded")
		}
	})
	t.Run("passphrase", func(t *testing.T) {
		noKeyring(t)
		dir := t.TempDir()
		f := New(dir, func(bool) ([]byte, error) { return []byte("correct horse"), nil })
		if err := f.Create(session1); err != nil {
			t.Fatal(err)
		}
		if err := f.Delete(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, fileName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat = %v, want no file", err)
		}
	})
	t.Run("keyring unavailable", func(t *testing.T) {
		dir := t.TempDir()
		if err := New(dir, noAsk(t)).Create(session1); err != nil {
			t.Fatal(err)
		}
		noKeyring(t)
		if err := New(dir, noAsk(t)).Delete(); err == nil {
			t.Error("Delete succeeded with the key stuck in the keyring")
		}
		if _, err := os.Stat(filepath.Join(dir, fileName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat = %v, want no file", err)
		}
	})
}

// noAsk fails the test if a passphrase is asked for.
func noAsk(t *testing.T) func(bool) ([]byte, error) {
	return func(bool) ([]byte, error) {
		t.Error("asked for a passphrase")
		return nil, errors.New("no passphrase")
	}
}

// noKeyring makes every keyring call fail until the test ends.
func noKeyring(t *testing.T) {
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
}

func assertSession(t *testing.T, got, want core.Session) {
	t.Helper()
	if !bytes.Equal(got.Auth, want.Auth) || got.SeqID != want.SeqID || !got.SeqAt.Equal(want.SeqAt) {
		t.Errorf("session = {%s %d %v}, want {%s %d %v}", string(got.Auth), got.SeqID, got.SeqAt, string(want.Auth), want.SeqID, want.SeqAt)
	}
}

// assertOnlySessionFile fails if dir holds anything but the session file,
// such as a leftover temporary file.
func assertOnlySessionFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{fileName}) {
		t.Errorf("dir holds %v, want only %s", names, fileName)
	}
}
