// Command wittytuner-module is WittyTuner as a helper process: the only way a server reaches
// it. A process answers one question and leaves, so nothing is left running while no one is
// watching. A server watches every stream for encryption itself, and holds what this asks
// FFmpeg to open to its own rules. See WittyModule for the protocol.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
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
	if err := wittymodule.ServeStream(os.Stdin, os.Stdout, wittymodule.Hello{Module: m.ID, Version: m.Version, API: m.API}, handle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// envFor is what a server told this process about itself. A helper starts with almost no
// environment of its own.
func envFor(c *wittytuner.HelperCall) wittytuner.Env {
	return wittytuner.Env{HTTP: &http.Client{Timeout: 30 * time.Second}, Stream: &http.Client{}, FFmpeg: c.FFmpeg, AllowCommands: c.AllowCommands,
		Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, name, args...)
		}}
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
	switch {
	case errors.As(err, &enc):
		code, msg = "encrypted", enc.Why
	case errors.Is(err, wittytuner.ErrBusy):
		code = "busy"
	case errors.Is(err, fs.ErrPermission):
		code = "denied"
	}
	return &wittymodule.CallError{Code: code, Message: msg}
}
