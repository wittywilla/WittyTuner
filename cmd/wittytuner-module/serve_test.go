// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package main

import (
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittytuner"
)

var testHello = wittymodule.Hello{Module: "wittytuner", Version: "0.0.0", API: 4}

// helperFor runs serve on pipes and gives the server's end of them.
func helperFor(t *testing.T) (*wittymodule.Conn, io.Closer, <-chan error) {
	t.Helper()
	toHelper, fromServer := io.Pipe()
	fromHelper, toServer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := serve(toHelper, toServer, testHello)
		_ = toServer.Close()
		done <- err
	}()
	c := wittymodule.NewConn(fromHelper, fromServer)
	h, err := c.ReadHello()
	if err != nil {
		t.Fatal(err)
	}
	if *h != testHello {
		t.Fatalf("hello %+v", *h)
	}
	return c, fromServer, done
}

func ended(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("the helper didn't leave")
		return nil
	}
}

// The server's SDUI questions come on the helper it keeps, again and again: each is answered,
// and the process stays until the server closes the pipe.
func TestTheServersQuestionsKeepTheHelper(t *testing.T) {
	c, closer, done := helperFor(t)
	for i := 0; i < 3; i++ {
		_, err := c.Call("sdui.image", map[string]string{"name": "x.png"}, nil)
		var ce *wittymodule.CallError
		if !errors.As(err, &ce) || ce.Code != "no-picture" {
			t.Fatalf("picture %d: %v, want the helper saying it has none", i, err)
		}
	}
	var ok map[string]any
	if _, err := c.Call("settings.changed", map[string]any{"values": map[string]any{}}, &ok); err != nil {
		t.Fatalf("settings.changed: %v", err)
	}
	var ce *wittymodule.CallError
	if _, err := c.Call("sdui.items", map[string]any{"source": map[string]any{"type": "wittytuner.x"}}, nil); !errors.As(err, &ce) || ce.Code != "no-source" {
		t.Fatalf("sdui.items: %v", err)
	}
	// A tuner's question doesn't belong here, and is refused rather than half answered.
	if _, err := c.Call("drivers", wittytuner.HelperCall{}, nil); !errors.As(err, &ce) || ce.Code != "unknown-method" {
		t.Fatalf("drivers: %v", err)
	}
	_ = closer.Close()
	if err := ended(t, done); err != nil {
		t.Fatal(err)
	}
}

// A tuner's question is still a stream, in a process of its own that leaves when it ends.
func TestATunersQuestionIsStillAStream(t *testing.T) {
	c, closer, done := helperFor(t)
	defer closer.Close()
	r, err := c.Stream("drivers", wittytuner.HelperCall{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc wittytuner.HelperDoc
	b, _ := io.ReadAll(r)
	if err := json.Unmarshal(b, &doc); err != nil || !doc.Done || len(doc.Drivers) == 0 {
		t.Fatalf("%v %s", err, b)
	}
	if err := ended(t, done); err != nil {
		t.Fatal(err)
	}
}
