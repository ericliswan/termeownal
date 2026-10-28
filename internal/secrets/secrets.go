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

// Package secrets keeps the login session in session.enc in the data
// directory, encrypted with XChaCha20-Poly1305. The key is random and kept
// in the OS keyring; where there's no keyring, it's derived from a
// passphrase with Argon2id.
//
// The encryption protects copies of the file, such as backups. It doesn't
// protect against programs running as the same user, which can read the
// keyring: on macOS the key's item trusts /usr/bin/security, which any of
// them can run.
//
// Methods take no context because file and keyring calls can't be
// cancelled.
//
// shortcut: on Linux a locked keyring waits for its unlock prompt with no
// timeout, so meow hangs where no prompt can show (e.g. over SSH); add a
// timeout if that comes up.
package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"termeownal/internal/core"
)

const (
	fileName = "session.enc"
	// service names the keyring entry. Its account is the file's path, so
	// each data directory has its own key.
	service = "termeownal"

	version              = 1
	kindKeyring    uint8 = 1 // the key is in the keyring
	kindPassphrase uint8 = 2 // the key is derived from a passphrase and the salt

	// Argon2id with RFC 9106's second recommended settings: 3 passes over
	// 64 MiB with 4 lanes.
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
)

var magic = [4]byte{'M', 'E', 'O', 'W'}

// header starts the file. It isn't encrypted but it is authenticated: it's
// the additional data of the ciphertext that follows it.
type header struct {
	Magic   [4]byte
	Version uint8
	Kind    uint8
	Salt    [16]byte // zero unless Kind is kindPassphrase
	Nonce   [chacha20poly1305.NonceSizeX]byte
}

// payload is the plaintext: JSON with its own field names, so the file
// doesn't change when core.Session does.
type payload struct {
	Auth  []byte    `json:"auth"`
	SeqID int64     `json:"seq_id"`
	SeqAt time.Time `json:"seq_at"`
}

var (
	// ErrNoSession means no session is saved.
	ErrNoSession = errors.New("secrets: no saved session")
	// ErrUnreadable means the saved session can't be decrypted: the file is
	// damaged or its key is gone from the keyring. Logging in again
	// replaces it.
	ErrUnreadable = errors.New("secrets: saved session unreadable")
	// ErrWrongPassphrase means the passphrase doesn't decrypt the saved
	// session, or the file is damaged: the two can't be told apart.
	ErrWrongPassphrase = errors.New("secrets: wrong passphrase")
)

// File is the session file in one data directory. It isn't safe for
// concurrent use.
type File struct {
	path string
	ask  func(create bool) ([]byte, error)

	// kind, salt and key are set by Load or Create and used by Save.
	kind uint8
	salt [16]byte
	key  []byte
}

// New returns the session file in dir. ask reads a passphrase with echo
// off; it's only called when there's no keyring. With create set it's for a
// new passphrase, so it should have the user type it twice.
func New(dir string, ask func(create bool) ([]byte, error)) *File {
	return &File{path: filepath.Join(dir, fileName), ask: ask}
}

// Load reads and decrypts the saved session. It returns ErrNoSession if
// there's none, and ErrUnreadable or ErrWrongPassphrase if it can't be
// decrypted.
func (f *File) Load() (core.Session, error) {
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return core.Session{}, ErrNoSession
	}
	if err != nil {
		return core.Session{}, fmt.Errorf("read session: %w", err)
	}
	var h header
	n, err := binary.Decode(data, binary.BigEndian, &h)
	if err != nil || h.Magic != magic || h.Version != version {
		return core.Session{}, fmt.Errorf("%w: not a version %d session file", ErrUnreadable, version)
	}
	var key []byte
	switch h.Kind {
	case kindKeyring:
		key, err = keyringKey(f.path)
	case kindPassphrase:
		key, err = f.passphraseKey(false, h.Salt)
	default:
		err = fmt.Errorf("%w: unknown key kind %d", ErrUnreadable, h.Kind)
	}
	if err != nil {
		return core.Session{}, err
	}
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return core.Session{}, err
	}
	plain, err := aead.Open(nil, h.Nonce[:], data[n:], data[:n])
	if err != nil && h.Kind == kindPassphrase {
		return core.Session{}, ErrWrongPassphrase
	}
	if err != nil {
		return core.Session{}, fmt.Errorf("%w: decryption failed", ErrUnreadable)
	}
	var p payload
	if err := json.Unmarshal(plain, &p); err != nil {
		// Not wrapped: err can quote the plaintext.
		return core.Session{}, fmt.Errorf("%w: bad contents", ErrUnreadable)
	}
	f.kind, f.salt, f.key = h.Kind, h.Salt, key
	return core.Session{Auth: p.Auth, SeqID: p.SeqID, SeqAt: p.SeqAt}, nil
}

// Create saves s under a new key, replacing any saved session. The key goes
// in the keyring or, if that fails, comes from a new passphrase.
func (f *File) Create(s core.Session) error {
	key := make([]byte, chacha20poly1305.KeySize)
	rand.Read(key)
	kind, salt := kindKeyring, [16]byte{}
	if err := keyring.Set(service, f.path, base64.StdEncoding.EncodeToString(key)); err != nil {
		kind = kindPassphrase
		rand.Read(salt[:])
		if key, err = f.passphraseKey(true, salt); err != nil {
			return err
		}
	}
	f.kind, f.salt, f.key = kind, salt, key
	return f.Save(s)
}

// Save replaces the saved session with s, encrypted with the key from the
// last Load or Create.
func (f *File) Save(s core.Session) error {
	if f.key == nil {
		return errors.New("secrets: Save before Load or Create")
	}
	if len(s.Auth) == 0 {
		return errors.New("secrets: no login data to save")
	}
	plain, err := json.Marshal(payload{Auth: s.Auth, SeqID: s.SeqID, SeqAt: s.SeqAt})
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	h := header{Magic: magic, Version: version, Kind: f.kind, Salt: f.salt}
	rand.Read(h.Nonce[:])
	ad, err := binary.Append(nil, binary.BigEndian, h)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	aead, err := chacha20poly1305.NewX(f.key)
	if err != nil {
		return err
	}
	sealed := aead.Seal(nil, h.Nonce[:], plain, ad)
	return writeFile(f.path, append(ad, sealed...))
}

// Delete removes the saved session and its key. Deleting what isn't there
// isn't an error.
func (f *File) Delete() error {
	f.key = nil
	var h header
	data, _ := os.ReadFile(f.path)
	_, _ = binary.Decode(data, binary.BigEndian, &h) // on failure h.Kind stays 0
	var errs []error
	if err := os.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("delete session: %w", err))
	}
	// Without a keyring this fails, which only matters if the key was there.
	if err := keyring.Delete(service, f.path); err != nil && !errors.Is(err, keyring.ErrNotFound) && h.Kind == kindKeyring {
		errs = append(errs, fmt.Errorf("delete key from keyring: %w", err))
	}
	return errors.Join(errs...)
}

// keyringKey reads the key Create put in the keyring.
func keyringKey(path string) ([]byte, error) {
	enc, err := keyring.Get(service, path)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, fmt.Errorf("%w: its key isn't in the keyring", ErrUnreadable)
	}
	if err != nil {
		return nil, fmt.Errorf("read key from keyring: %w", err)
	}
	key, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("%w: bad key in the keyring", ErrUnreadable)
	}
	return key, nil
}

// passphraseKey derives a key from a passphrase the user types.
func (f *File) passphraseKey(create bool, salt [16]byte) ([]byte, error) {
	pass, err := f.ask(create)
	if err != nil {
		return nil, fmt.Errorf("read passphrase: %w", err)
	}
	defer clear(pass)
	if create && len(pass) == 0 {
		return nil, errors.New("secrets: empty passphrase")
	}
	return argon2.IDKey(pass, salt[:], argonTime, argonMemory, argonThreads, chacha20poly1305.KeySize), nil
}

// writeFile replaces path with data through a temporary file, so a crash
// leaves the old file or the new one, never a mix. The rename isn't synced,
// so a power cut can undo the last save; the one before is still whole.
func writeFile(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, fileName+".*.tmp") // mode 0600
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
