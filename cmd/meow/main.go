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

// Command meow is the termeownal terminal client.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"termeownal/internal/app"
	"termeownal/internal/backend/fake"
	"termeownal/internal/backend/instagram"
	"termeownal/internal/config"
	"termeownal/internal/core"
	"termeownal/internal/secrets"
	"termeownal/internal/store"
)

const usage = `Usage:
  meow [--backend=instagram|fake]  open your DMs; fake shows canned threads, no network
  meow login                       log in to Instagram with cookies from a browser
  meow logout [--purge]            forget the login; --purge also deletes the message cache
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "meow:", err)
		os.Exit(1)
	}
}

func run() error {
	cmd, args := "", os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("meow", flag.ExitOnError)
	flags.Usage = func() { _, _ = fmt.Fprint(flags.Output(), usage) }
	backend := new(string)
	purge := new(bool)
	switch cmd {
	case "":
		backend = flags.String("backend", "instagram", "")
	case "login":
	case "logout":
		purge = flags.Bool("purge", false, "")
	default:
		flags.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
	_ = flags.Parse(args) // ExitOnError: it exits rather than return an error
	if flags.NArg() > 0 {
		flags.Usage()
		return fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}

	paths := config.DefaultPaths()
	cfg, err := config.Load(paths.ConfigFile)
	if err != nil {
		return err
	}
	log, logFile, err := app.OpenLog(paths.LogFile, cfg.LogLevel)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	log.Info().Str("config", paths.ConfigFile).Str("command", cmd).Str("backend", *backend).Msg("meow starting")
	ctx := context.Background()
	con := newConsole(os.Stdin, os.Stdout)
	if cmd != "" || *backend == "instagram" {
		// One meow at a time, or one could save a session over another's.
		lock, err := secrets.Lock(paths.DataDir)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Close() }()
	}
	f := secrets.New(paths.DataDir, con.passphrase)
	switch cmd {
	case "login":
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return errors.New("meow login needs a terminal: it reads secrets with echo off")
		}
		if err := login(ctx, instagram.NewLogin(log), con, f, paths.CacheFile); err != nil {
			log.Warn().Err(err).Msg("login failed")
			if hint := loginHint(err, paths.LogFile); hint != "" {
				return fmt.Errorf("%w\n%s", err, hint)
			}
			return err
		}
		return nil
	case "logout":
		if err := logout(f, paths.CacheFile, *purge); err != nil {
			return err
		}
		fmt.Println("Logged out.")
		return nil
	}

	switch *backend {
	case "fake":
		b, err := newFake(ctx)
		if err != nil {
			return err
		}
		// The fake's message IDs restart every run and its history moves
		// with the clock, so a cache on disk would mix runs.
		st, err := store.Open(ctx, store.Memory)
		if err != nil {
			b.Close()
			return err
		}
		defer func() { _ = st.Close() }()
		return app.Run(ctx, b, st, core.Session{}, nil, log)
	case "instagram":
		s, err := f.Load()
		switch {
		case errors.Is(err, secrets.ErrNoSession):
			return errors.New("not logged in; run `meow login`")
		case errors.Is(err, secrets.ErrUnreadable):
			return fmt.Errorf("%w; run `meow login` again", err)
		case err != nil:
			return err
		}
		b := instagram.New(log)
		if err := b.Restore(ctx, s); err != nil {
			b.Close()
			return fmt.Errorf("%w; run `meow login` again", err)
		}
		st, err := store.Open(ctx, paths.CacheFile)
		if err != nil {
			b.Close()
			return err
		}
		defer func() { _ = st.Close() }()
		return app.Run(ctx, b, st, s, f.Save, log)
	default:
		return fmt.Errorf("unknown backend %q", *backend)
	}
}

// newFake returns a logged-in fake backend that receives a message now and
// then.
func newFake(ctx context.Context) (core.Backend, error) {
	step, err := fake.Authenticator{}.Submit(ctx, map[string]string{"username": "meow.tester", "password": "fake"})
	if err != nil {
		return nil, err
	}
	b := fake.New(fake.Options{IncomingEvery: 15 * time.Second, EchoDelay: 300 * time.Millisecond})
	if err := b.Restore(ctx, core.Session{Auth: step.Result.Auth}); err != nil {
		return nil, err
	}
	return b, nil
}
