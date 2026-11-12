// SPDX-License-Identifier: AGPL-3.0-or-later
//
// WittyTuner - Live TV and recording: network tuners, IPTV playlists, capture cards and guide data.
// Copyright (C) 2026 WittyWilla and contributors
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

// Command wittytuner-module is WittyTuner as a helper process: the only way a server reaches
// it. A process answers one question and leaves, so nothing is left running while no one is
// watching (the server's own SDUI questions are the exception: serve.go). A server watches
// every stream for encryption itself, and holds what this asks FFmpeg to open to its own
// rules. See WittyModule for the protocol.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittytuner"
)

func main() {
	m, err := wittymodule.ParseManifest(wittytuner.ManifestJSON)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) != 2 || os.Args[1] != wittymodule.HelperFlag {
		fmt.Fprintf(os.Stderr, "%s %s is a helper that WittyMediaServer starts by itself; there is nothing to do by hand.\n", m.Name, m.Version)
		os.Exit(2)
	}
	// Leaving when the server closes the pipe is what cancels whatever is under way.
	if err := serve(os.Stdin, os.Stdout, wittymodule.Hello{Module: m.ID, Version: m.Version, API: m.API}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// envFor is what a server told this process about itself, with clients chosen by the call: a
// playlist from the internet doesn't get to reach the home network. A helper starts with almost no
// environment of its own.
func envFor(c *wittytuner.HelperCall) wittytuner.Env {
	env := wittytuner.HelperEnvFor(c.Driver, c.Options)
	env.FFmpeg, env.AllowCommands = c.FFmpeg, c.AllowCommands
	env.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, name, args...)
	}
	return env
}

func handle(method string, raw json.RawMessage) (any, func(io.Writer) error, error) {
	var c wittytuner.HelperCall
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, nil, &wittymodule.CallError{Code: "bad-call", Message: "the call is unreadable"}
	}
	ctx := context.Background()
	env := envFor(&c)
	switch method {
	case "discover":
		return nil, func(w io.Writer) error {
			return send(w, wittytuner.HelperDoc{Found: wittytuner.Discover(ctx, env), Done: true})
		}, nil
	case "drivers":
		return nil, func(w io.Writer) error {
			return send(w, wittytuner.HelperDoc{Drivers: wittytuner.Drivers(env), Done: true})
		}, nil
	}
	src, err := wittytuner.New(c.Driver, wittytuner.Config{Name: c.Name, Options: c.Options}, env)
	if err != nil {
		return nil, nil, coded(err)
	}
	switch method {
	case "validate":
		return nil, func(w io.Writer) error {
			info := src.Info()
			d := wittytuner.HelperDoc{Info: &info, Done: true}
			if g, ok := src.(interface{ GuideHeaders() http.Header }); ok {
				d.GuideHeaders = g.GuideHeaders()
			}
			return send(w, d)
		}, nil
	case "channels":
		return nil, func(w io.Writer) error {
			list, err := src.Channels(ctx)
			return finish(w, src, list, err)
		}, nil
	case "scan":
		return nil, func(w io.Writer) error {
			list, err := src.Scan(ctx, wittytuner.ScanOptions{Source: c.Source}, func(p wittytuner.Progress) {
				_ = send(w, wittytuner.HelperDoc{Progress: &p})
			})
			return finish(w, src, list, err)
		}, nil
	case "recheck":
		if c.Channel == nil {
			return nil, nil, &wittymodule.CallError{Code: "bad-call", Message: "no channel was given"}
		}
		return nil, func(w io.Writer) error {
			st, err := src.Open(ctx, *c.Channel)
			if err != nil {
				var enc *wittytuner.EncryptedError
				if errors.As(err, &enc) {
					return send(w, wittytuner.HelperDoc{Why: enc.Why, Done: true})
				}
				return finish(w, src, nil, err)
			}
			defer st.Close()
			if st.Recheck == nil {
				return send(w, wittytuner.HelperDoc{Done: true})
			}
			why, next, err := st.Recheck(ctx)
			if err != nil {
				return finish(w, src, nil, err)
			}
			return send(w, wittytuner.HelperDoc{Why: why, Next: int64(next / time.Second), Done: true})
		}, nil
	case "open":
		if c.Channel == nil {
			return nil, nil, &wittymodule.CallError{Code: "bad-call", Message: "no channel was given"}
		}
		st, err := src.Open(ctx, *c.Channel)
		if err != nil {
			return nil, nil, coded(err)
		}
		if st.Reader == nil {
			return wittytuner.HelperOpen{Input: st.Input, Verified: st.Verified, Recheck: st.Recheck != nil}, func(io.Writer) error { return nil }, nil
		}
		// The server guards the stream for itself, so it isn't looked through twice.
		r := io.Reader(st.Reader)
		if st.Guard != nil {
			r = st.Guard.Underneath()
		}
		return nil, func(w io.Writer) error {
			defer st.Close()
			_, err := io.Copy(w, r)
			return err
		}, nil
	}
	return nil, nil, &wittymodule.CallError{Code: "unknown-method", Message: fmt.Sprintf("this version has no %q", method)}
}

func send(w io.Writer, d wittytuner.HelperDoc) error { return json.NewEncoder(w).Encode(d) }

func finish(w io.Writer, src wittytuner.Source, list []wittytuner.Channel, err error) error {
	if err != nil {
		var ce *wittymodule.CallError
		errors.As(coded(err), &ce)
		return send(w, wittytuner.HelperDoc{Error: ce.Message, Code: ce.Code, Done: true})
	}
	info := src.Info()
	return send(w, wittytuner.HelperDoc{Channels: list, Info: &info, Done: true})
}

// coded names the errors the server acts on.
func coded(err error) error {
	code, msg := "", err.Error()
	var enc *wittytuner.EncryptedError
	var ue *url.Error
	var op *net.OpError
	var dns *net.DNSError
	switch {
	case errors.As(err, &enc):
		code, msg = "encrypted", enc.Why
	case errors.As(err, &ue), errors.As(err, &op), errors.As(err, &dns):
		code = "unreachable" // what it says holds what's on the network, which isn't for a page
	case errors.Is(err, wittytuner.ErrBusy):
		code = "busy"
	case errors.Is(err, fs.ErrPermission):
		code = "denied"
	}
	return &wittymodule.CallError{Code: code, Message: msg}
}
