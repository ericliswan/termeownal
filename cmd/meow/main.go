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
	"time"

	"termeownal/internal/app"
	"termeownal/internal/backend/fake"
	"termeownal/internal/config"
	"termeownal/internal/core"
	"termeownal/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "meow:", err)
		os.Exit(1)
	}
}

func run() error {
	backend := flag.String("backend", "instagram", "chat backend: instagram or fake (canned threads, no network)")
	flag.Parse()

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

	log.Info().Str("config", paths.ConfigFile).Str("backend", *backend).Msg("meow starting")
	ctx := context.Background()
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
		return errors.New("the Instagram backend isn't implemented yet; try --backend=fake")
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
