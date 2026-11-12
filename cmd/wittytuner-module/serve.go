// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittymodule/sdui"
)

// A tuner's question is a process of its own, answered as a stream (ServeStream), so
// nothing stays running while no one watches. A server asks a module's SDUI questions
// (module API 4) differently: on the one helper it keeps for the module, which it expects
// to ask again. A process that left after answering would look to the server like a
// helper that crashed on the next question, and enough of those rest the module, live TV
// with it. So a process whose first question is one of those stays, and answers every
// question as an ordinary helper does (Serve), until the server closes the pipe or stops
// it for being idle.
//
// WittyTuner's sdui.json declares no sources and no settings, so the server has nothing to
// ask but a picture, which anyone signed in can name in an address. It has none.

// serverAnswers hears settings.changed. The helper reads no settings, so nothing is kept.
var serverAnswers = sdui.Answers{}

// serverCall: a question asked on the helper the server keeps, not one of a tuner's.
func serverCall(method string) bool {
	switch method {
	case sdui.MethodItems, sdui.MethodImage, sdui.MethodSettingsChanged:
		return true
	}
	return false
}

func answerServer(method string, params json.RawMessage) (any, []byte, error) {
	if res, blob, err, ok := serverAnswers.Handle(method, params); ok {
		return res, blob, err
	}
	switch method {
	case sdui.MethodItems:
		return nil, nil, &wittymodule.CallError{Code: "no-source", Message: "WittyTuner answers no data sources"}
	case sdui.MethodImage:
		return nil, nil, &wittymodule.CallError{Code: "no-picture", Message: "WittyTuner has no pictures of its own"}
	}
	return nil, nil, &wittymodule.CallError{Code: "unknown-method", Message: "a tuner's question is asked in a process of its own"}
}

// serve says hello, reads the first question, and answers it the way it is asked.
func serve(in io.Reader, out io.Writer, hello wittymodule.Hello) error {
	b, err := json.Marshal(hello)
	if err != nil {
		return err
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return err
	}
	r := bufio.NewReaderSize(in, wittymodule.MaxLine)
	line, err := r.ReadSlice('\n')
	switch {
	case errors.Is(err, bufio.ErrBufferFull):
		return errors.New("a message is too long")
	case errors.Is(err, io.EOF) && len(line) == 0:
		return nil // the server closed the pipe without asking
	case err != nil && !errors.Is(err, io.EOF):
		return err
	}
	first := bytes.Clone(line)
	var req struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(first, &req) // one that can't be read is refused by the protocol below
	// Both loops say hello themselves; it has been said, so theirs is left out.
	rest, o := io.MultiReader(bytes.NewReader(first), r), &afterHello{w: out}
	if serverCall(req.Method) {
		return wittymodule.Serve(rest, o, hello, answerServer)
	}
	return wittymodule.ServeStream(rest, o, hello, handle)
}

// afterHello passes on everything after the first line.
type afterHello struct {
	w    io.Writer
	past bool
}

func (a *afterHello) Write(p []byte) (int, error) {
	if a.past {
		return a.w.Write(p)
	}
	i := bytes.IndexByte(p, '\n')
	if i < 0 {
		return len(p), nil
	}
	a.past = true
	if _, err := a.w.Write(p[i+1:]); err != nil {
		return 0, err
	}
	return len(p), nil
}
