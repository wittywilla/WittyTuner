// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/sdui"
	"github.com/wittywilla/wittymodule/service"
	"github.com/wittywilla/wittytuner/store"
	"github.com/wittywilla/wittytuner/ui"
)

// tsPacket is one clear MPEG-TS packet.
func tsPacket() []byte {
	p := bytes.Repeat([]byte{0xff}, 188)
	p[0], p[1], p[2], p[3] = 0x47, 0x01, 0x00, 0x10
	return p
}

// fakeHost is the server's host API, as much of it as WittyTuner uses.
type fakeHost struct {
	mu      sync.Mutex
	guide   string // the last guide the module handed over
	dropped []string
	setup   []string
}

func (f *fakeHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Witty-Host", "1")
	if r.Header.Get(host.HeaderToken) != "secret" {
		w.WriteHeader(403)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, host.Base)
	switch {
	case p == "/tools":
		_ = json.NewEncoder(w).Encode(host.Tools{})
	case p == "/hello":
		_ = json.NewEncoder(w).Encode(host.Hello{Module: "wittytuner"})
	case strings.HasPrefix(p, "/tasks/"):
		w.WriteHeader(200)
	case p == "/tv/guide/ids":
		_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "one.tv", "name": "One"}})
	case strings.HasPrefix(p, "/tv/guide/") && r.Method == "POST":
		b, _ := io.ReadAll(r.Body)
		f.guide = string(b)
		_ = json.NewEncoder(w).Encode(map[string]any{"programmes": 7, "matched": map[string]string{"1": "one.tv"}})
	case strings.HasPrefix(p, "/tv/guide/") && r.Method == "DELETE":
		f.dropped = append(f.dropped, strings.TrimPrefix(p, "/tv/guide/"))
	case strings.HasPrefix(p, "/setup/"):
		f.setup = append(f.setup, strings.TrimPrefix(p, "/setup/")+" by "+r.Header.Get(host.HeaderOnUser))
	default:
		w.WriteHeader(404)
	}
}

// harness is the module's routes, its routes for the server, a host and a stream to tune.
type harness struct {
	t      *testing.T
	m      *Manager
	routes *httptest.Server
	server *httptest.Server
	host   *fakeHost
	hs     *httptest.Server
	stream *httptest.Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, err := store.OpenFile(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	fh := &fakeHost{}
	hs := httptest.NewServer(fh)
	t.Cleanup(hs.Close)
	m := New(db, t.TempDir(), host.NewAt(hs.URL, "secret", nil), nil)
	t.Cleanup(func() { m.Close(); db.Close() })
	Use(m)
	t.Cleanup(func() { Use(nil) })
	own := http.NewServeMux()
	Routes(own)
	rs := httptest.NewServer(own)
	t.Cleanup(rs.Close)
	srv := http.NewServeMux()
	ServerRoutes(srv)
	ss := httptest.NewServer(srv)
	t.Cleanup(ss.Close)
	st := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp2t")
		for range 400 {
			if _, err := w.Write(tsPacket()); err != nil {
				return
			}
		}
	}))
	t.Cleanup(st.Close)
	return &harness{t: t, m: m, routes: rs, server: ss, host: fh, hs: hs, stream: st}
}

// call makes a request of the module's routes as an administrator (or as someone else).
func (h *harness) call(method, path string, body any, admin bool, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.routes.URL+path, rd)
	req.Header.Set(service.HeaderUser, "1")
	if admin {
		req.Header.Set(service.HeaderAdmin, "1")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func (h *harness) serverCall(method, path string, body any, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.server.URL+path, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

// A tuner is added, listed, its channel opened for the server and handed over as MPEG-TS, and
// let go: the tuner's limit is free again.
func TestTunerToStream(t *testing.T) {
	h := newHarness(t)
	var tu Tuner
	if st := h.call("POST", "/tuners", map[string]any{"driver": "stream", "name": "Test", "maxStreams": 1, "options": map[string]string{"url": h.stream.URL + "/one.ts", "number": "5"}}, true, &tu); st != 200 || tu.ID == 0 {
		t.Fatalf("adding a tuner: %d %+v", st, tu)
	}
	if st := h.call("GET", "/tuners", nil, false, nil); st != 403 {
		t.Fatalf("someone who isn't an administrator listed the tuners: %d", st)
	}
	var list []Tuner
	h.call("GET", "/tuners", nil, true, &list)
	if len(list) != 1 || list[0].Channels != 1 {
		t.Fatalf("tuners: %+v", list)
	}
	var all struct {
		Channels []struct {
			ID           int64  `json:"id"`
			Number       string `json:"number"`
			TunerName    string `json:"tunerName"`
			Driver       string `json:"driver"`
			TunerEnabled bool   `json:"tunerEnabled"`
		} `json:"channels"`
	}
	h.serverCall("GET", "/tv/channels", nil, &all)
	if len(all.Channels) != 1 || all.Channels[0].Number != "5" || all.Channels[0].TunerName != "Test" || all.Channels[0].Driver != "stream" || !all.Channels[0].TunerEnabled {
		t.Fatalf("the server's channels: %+v", all)
	}
	id := all.Channels[0].ID
	var o openAnswer
	if st := h.serverCall("POST", "/tv/open", openRequest{Channel: id}, &o); st != 200 || o.Slot == "" || o.Channel != id {
		t.Fatalf("open: %d %+v", st, o)
	}
	// The tuner allows one stream: another is refused as busy until this one goes.
	var refusal struct{ Error, Code string }
	if st := h.serverCall("POST", "/tv/open", openRequest{Channel: id}, &refusal); st != 503 || refusal.Code != "busy" {
		t.Fatalf("a second stream: %d %+v", st, refusal)
	}
	if o.Input == nil {
		res, err := http.Get(h.server.URL + "/tv/stream/" + o.Slot)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 188*10))
		res.Body.Close()
		if len(b) < 188 || b[0] != 0x47 {
			t.Fatalf("the stream: %d bytes", len(b))
		}
		// Handed over once only.
		if res, _ := http.Get(h.server.URL + "/tv/stream/" + o.Slot); res.StatusCode != 404 {
			t.Fatalf("a stream handed over twice: %d", res.StatusCode)
		}
	}
	h.serverCall("POST", "/tv/release", map[string]string{"slot": o.Slot}, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		var again openAnswer
		if st := h.serverCall("POST", "/tv/open", openRequest{Channel: id}, &again); st == 200 {
			h.serverCall("POST", "/tv/release", map[string]string{"slot": again.Slot}, nil)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the tuner wasn't free again after the stream was let go")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// What the server's guard saw is kept.
	h.serverCall("POST", "/tv/note", map[string]any{"channel": id, "encrypted": "the signal itself is scrambled"}, nil)
	c, err := h.m.Channel(t.Context(), id)
	if err != nil || c.Security != SecurityEncrypted {
		t.Fatalf("after the guard said it's scrambled: %+v %v", c, err)
	}
	var ref struct{ Code string }
	if st := h.serverCall("POST", "/tv/open", openRequest{Channel: id}, &ref); st != 403 || ref.Code != "encrypted" {
		t.Fatalf("an encrypted channel opened: %d %+v", st, ref)
	}
}

// A guide source is read and handed to the server, which keeps it; a channel it matched by name
// takes the guide's id, and removing the source has the server drop what it kept.
func TestGuideGoesToTheServer(t *testing.T) {
	h := newHarness(t)
	var tu Tuner
	h.call("POST", "/tuners", map[string]any{"driver": "stream", "name": "One", "options": map[string]string{"url": h.stream.URL + "/one.ts"}}, true, &tu)
	guide := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<tv><channel id="one.tv"><display-name>One</display-name></channel></tv>`)
	}))
	defer guide.Close()
	hostParams = func() service.StartParams { return service.StartParams{Host: h.hs.URL, HostToken: "secret"} }
	defer func() { hostParams = service.Host }()
	src, err := h.m.AddSource(t.Context(), "Listings", guide.URL+"/guide.xml")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.m.FetchSource(t.Context(), src.ID); err != nil {
		t.Fatal(err)
	}
	h.host.mu.Lock()
	got := h.host.guide
	h.host.mu.Unlock()
	if !strings.Contains(got, `channel id="one.tv"`) {
		t.Fatalf("the server was handed %q", got)
	}
	list, _ := h.m.Sources(t.Context())
	if len(list) != 1 || list[0].Programmes != 7 || list[0].LastError != "" {
		t.Fatalf("sources: %+v", list)
	}
	chans, _ := h.m.Channels(t.Context(), tu.ID)
	if len(chans) != 1 || chans[0].EPGID != "one.tv" {
		t.Fatalf("the channel the server matched: %+v", chans)
	}
	var ids []map[string]string
	if st := h.call("GET", "/epg/ids", nil, true, &ids); st != 200 || len(ids) != 1 {
		t.Fatalf("guide ids: %d %v", st, ids)
	}
	if err := h.m.DeleteSource(t.Context(), src.ID); err != nil {
		t.Fatal(err)
	}
	if len(h.host.dropped) != 1 || h.host.dropped[0] != strconv.FormatInt(src.ID, 10) {
		t.Fatalf("the server was told to drop %v", h.host.dropped)
	}
}

// The SDUI pages' sources are an administrator's, and their rows run the module's own commands.
func TestSDUISources(t *testing.T) {
	h := newHarness(t)
	var tu Tuner
	h.call("POST", "/tuners", map[string]any{"driver": "stream", "name": "Den", "options": map[string]string{"url": h.stream.URL + "/den.ts"}}, true, &tu)
	ask := func(src sdui.Source, admin bool) []sdui.Item {
		a, err := SDUIItems(sdui.ItemsRequest{Source: src, Limit: 50, Viewer: sdui.Viewer{User: 1, Admin: admin}})
		if err != nil {
			t.Fatal(err)
		}
		return a.Items
	}
	if got := ask(sdui.Src(ui.SourceTuners), false); len(got) != 0 {
		t.Fatalf("someone who isn't an administrator saw tuners: %+v", got)
	}
	got := ask(sdui.Src(ui.SourceTuners), true)
	if len(got) != 1 || got[0].Title != "Den" || got[0].Action == nil || !strings.HasPrefix(got[0].Action.To, "/page/"+ui.PageTuner+"?") {
		t.Fatalf("tuners: %+v", got)
	}
	one := ask(sdui.Src(ui.SourceTuner, "tuner", strconv.FormatInt(tu.ID, 10)), true)
	ops := map[string]bool{}
	for _, it := range one {
		if it.Action != nil {
			ops[it.Action.Op] = true
		}
	}
	for _, op := range []string{ui.CommandRefresh, ui.CommandCheck, ui.CommandOff, ui.CommandRemove} {
		if !ops[op] {
			t.Errorf("a tuner's rows have no %s: %+v", op, one)
		}
	}
	chans := ask(sdui.Src(ui.SourceChannels, "tuner", strconv.FormatInt(tu.ID, 10)), true)
	if len(chans) != 1 || chans[0].Control == nil || chans[0].Action.Op != ui.CommandChannelHide {
		t.Fatalf("channels: %+v", chans)
	}
	fin := ask(sdui.Src(ui.SourceSetup), true)
	if len(fin) != 2 || fin[0].Action.Op != ui.CommandSetupDone || fin[1].Action.Op != ui.CommandSetupLater {
		t.Fatalf("setup: %+v", fin)
	}
}

// Finishing the first-run step tells the server, on the administrator's behalf.
func TestSetupStepTellsTheServer(t *testing.T) {
	h := newHarness(t)
	if st := h.call("POST", "/setup/tv", map[string]bool{"skip": false}, true, nil); st != 200 {
		t.Fatalf("setup: %d", st)
	}
	if st := h.call("POST", "/setup/tv", map[string]bool{"skip": false}, false, nil); st != 403 {
		t.Fatalf("setup by someone who isn't an administrator: %d", st)
	}
	if len(h.host.setup) != 1 || h.host.setup[0] != "tv by 1" {
		t.Fatalf("the server was told %v", h.host.setup)
	}
}

// Only a file named at the server's own machine may be a playlist or a guide.
func TestFilesOnlyFromTheMachine(t *testing.T) {
	h := newHarness(t)
	var e struct{ Error string }
	if st := h.call("POST", "/epg/sources", map[string]string{"url": "/etc/passwd"}, true, &e); st != 400 || !strings.Contains(e.Error, "own desktop") {
		t.Fatalf("a file from elsewhere: %d %+v", st, e)
	}
}
