// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// Package api is WittyTuner's own HTTP API (module API 5): the routes WittyMediaServer passes to it
// at /api/modules/wittytuner/... once it has checked who may call them (integration.json), the
// sources of its SDUI pages, its lifecycle, and the service's own routes the server opens channels
// through (stream.go). It keeps the tuners, their channels and the guide sources in WittyTuner's own
// database (package store), and asks the server, through the host API, only what only the server
// has: its FFmpeg, where to keep a guide, and that a first-run step is done.
//
// What stays the server's: the guide and its programmes, recordings, favourites and sets, live
// TV's streams and their HLS, and the guard every stream passes through. The server never takes
// this module's word that a channel is clear.
package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/service"
	tuner "github.com/wittywilla/wittytuner"
	"github.com/wittywilla/wittytuner/store"
)

// DataEnv is where the server says WittyTuner's own folder is.
const DataEnv = "WITTYMODULE_DATA"

// HeaderMachine is "1" on a request from the server's own machine.
const HeaderMachine = "X-Witty-Machine"

var (
	appMu  sync.Mutex
	shared *Manager
)

// Get opens WittyTuner's database and reaches the server the first time something needs them.
func Get() (*Manager, error) {
	appMu.Lock()
	defer appMu.Unlock()
	if shared != nil {
		return shared, nil
	}
	dir := os.Getenv(DataEnv)
	db, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	h, err := host.New()
	if err != nil {
		db.Close()
		return nil, err
	}
	m := New(db, dir, h, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	m.learnServer(context.Background())
	m.Start()
	shared = m
	return shared, nil
}

// Use makes m the Manager the routes use, for tests.
func Use(m *Manager) {
	appMu.Lock()
	defer appMu.Unlock()
	shared = m
}

// Stop ends what the service runs, as the server goes.
func Stop() {
	appMu.Lock()
	m := shared
	appMu.Unlock()
	if m != nil {
		m.Close()
	}
}

// learnServer asks the server for its FFmpeg, and whether whoever runs the machine allows custom
// commands (WMS_TUNER_COMMANDS=1, the host API's machine "tunerCommands").
func (m *Manager) learnServer(ctx context.Context) {
	if m.Host == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if t, err := m.Host.Tools(ctx); err == nil {
		m.FFmpeg, m.FFprobe = t.FFmpeg, t.FFprobe
	}
	if h, err := m.Host.Hello(ctx); err == nil {
		m.AllowCommands = h.Machine["tunerCommands"]
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func pathInt(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return n, err == nil && n > 0
}

// Routes are the module's routes, as integration.json lists them. The server has checked who is
// asking before it passes a request on; each is checked again here.
func Routes(mux *http.ServeMux) {
	type handler func(*Manager, http.ResponseWriter, *http.Request)
	with := func(admin bool, f handler) http.Handler {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m, err := Get()
			if err != nil {
				writeErr(w, 503, "WittyTuner couldn't open its data: "+err.Error())
				return
			}
			f(m, w, r)
		})
		if admin {
			return service.Admins(h)
		}
		return service.People(h)
	}
	ad := func(f handler) http.Handler { return with(true, f) }
	mux.Handle("GET /encrypted-help", with(false, func(_ *Manager, w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, EncryptedHelp()) }))
	mux.Handle("GET /drivers", ad(handleDrivers))
	mux.Handle("GET /discover", ad(handleDiscover))
	mux.Handle("GET /tuners", ad(handleTuners))
	mux.Handle("POST /tuners", ad(handleCreateTuner))
	mux.Handle("POST /tuners/check", ad(handleCheckTuner))
	mux.Handle("PUT /tuners/{id}", ad(handleUpdateTuner))
	mux.Handle("DELETE /tuners/{id}", ad(handleDeleteTuner))
	mux.Handle("POST /tuners/{id}/refresh", ad(handleRefreshTuner))
	mux.Handle("POST /tuners/{id}/scan", ad(handleScanTuner))
	mux.Handle("GET /tuners/{id}/scan", ad(handleScanProgress))
	mux.Handle("POST /tuners/{id}/test", ad(handleTestTuner))
	mux.Handle("GET /tuners/{id}/channels", ad(handleTunerChannels))
	mux.Handle("POST /tuners/{id}/channels/enable", ad(handleEnableAllChannels))
	mux.Handle("POST /tuners/{id}/health", ad(handleStartHealth))
	mux.Handle("GET /tuners/{id}/health", ad(handleChannelHealth))
	mux.Handle("DELETE /tuners/{id}/health", ad(handleStopHealth))
	mux.Handle("PUT /channels/{id}", ad(handleEditChannel))
	mux.Handle("POST /channels/bulk", ad(handleBulkChannels))
	mux.Handle("GET /duplicates", ad(handleDuplicates))
	mux.Handle("GET /epg/sources", ad(handleEPGSources))
	mux.Handle("POST /epg/sources", ad(handleAddEPGSource))
	mux.Handle("DELETE /epg/sources/{id}", ad(handleDeleteEPGSource))
	mux.Handle("POST /epg/sources/{id}/refresh", ad(handleRefreshEPGSource))
	mux.Handle("GET /epg/ids", ad(handleEPGIDs))
	mux.Handle("POST /found/{key}", ad(handleAddFound))
	mux.Handle("POST /setup/tv", ad(handleSetupTV))
}

// EncryptedHelpURL is where the apps open the encrypted-channels page's content.
const EncryptedHelpURL = "/api/modules/wittytuner/encrypted-help"

func tunerError(w http.ResponseWriter, err error) {
	var enc *tuner.EncryptedError
	switch {
	case errors.Is(err, ErrNotFound):
		writeErr(w, 404, "not found")
	case errors.As(err, &enc):
		writeErr(w, 403, err.Error())
	case errors.Is(err, ErrBusy):
		writeErr(w, 503, err.Error())
	default:
		writeErr(w, 400, PublicError(err))
	}
}

// checkPlace bounds where a playlist or a guide file may be: an address, or a file on the server
// that someone at the server's own machine named. A file named from anywhere else could be any
// file the module can read.
func checkPlace(r *http.Request, place string) error {
	place = strings.TrimSpace(place)
	if place == "" || strings.Contains(place, "://") {
		return nil
	}
	if r.Header.Get(HeaderMachine) != "1" {
		return errors.New("A file on the server is added on the server's own desktop. From here, give its web address.")
	}
	st, err := os.Stat(place)
	if err != nil || !st.Mode().IsRegular() {
		return errors.New("that file was not found on the server")
	}
	return nil
}

func handleDrivers(m *Manager, w http.ResponseWriter, r *http.Request) {
	list, err := m.Drivers(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func handleDiscover(m *Manager, w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	found, err := m.Discover(ctx)
	if errors.Is(err, context.DeadlineExceeded) || (err == nil && ctx.Err() != nil && len(found) == 0) {
		writeErr(w, 504, "The search took too long and was stopped. Some devices don't answer a search: add yours by hand with its address.")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, found)
}

func handleTuners(m *Manager, w http.ResponseWriter, r *http.Request) {
	list, err := m.Tuners(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for _, t := range list {
		t.Options = PublicOptions(t)
	}
	writeJSON(w, 200, list)
}

func handleCreateTuner(m *Manager, w http.ResponseWriter, r *http.Request) {
	var t Tuner
	if err := readJSON(r, &t); err != nil {
		writeErr(w, 400, "that tuner could not be read")
		return
	}
	if err := checkPlace(r, t.Options["playlist"]); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	got, err := m.Create(r.Context(), &t)
	if err != nil {
		tunerError(w, err)
		return
	}
	got.Options = PublicOptions(got)
	writeJSON(w, 200, got)
}

// POST /tuners/check: what a source says before it's saved, for the guided setup. Nothing is
// kept. A source that doesn't check out is an answer, not a failure, so it's 200.
func handleCheckTuner(m *Manager, w http.ResponseWriter, r *http.Request) {
	var t Tuner
	if err := readJSON(r, &t); err != nil {
		writeErr(w, 400, "those details could not be read")
		return
	}
	if err := checkPlace(r, t.Options["playlist"]); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "problem": Problem{Kind: "settings", Message: err.Error()}})
		return
	}
	// Checking a saved tuner's changed details: a password it was shown masked keeps the stored one.
	if t.ID > 0 {
		cur, err := m.Tuner(r.Context(), t.ID)
		if err != nil {
			tunerError(w, err)
			return
		}
		for k, v := range t.Options {
			if v == Masked {
				t.Options[k] = cur.Options[k]
			}
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	res, err := m.Check(ctx, t.Driver, t.Name, t.Options)
	if err != nil {
		if PublicError(err) != err.Error() {
			m.Log.InfoContext(r.Context(), "a tuner being set up didn't check out", "driver", t.Driver, "err", err)
		}
		writeJSON(w, 200, map[string]any{"ok": false, "problem": CheckProblem(t.Driver, err)})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "result": res})
}

// POST /tuners/{id}/test: tunes one channel for a few seconds and says what arrived. An
// encrypted one is refused, as it is everywhere.
func handleTestTuner(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var b struct {
		Channel int64 `json:"channel"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if _, err := m.Tuner(r.Context(), id); err != nil {
		tunerError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	res, err := m.TestChannel(ctx, id, b.Channel)
	if err != nil {
		tunerError(w, err)
		return
	}
	if res.Problem != nil && res.Problem.Kind == "encrypted" {
		res.Problem.HelpURL = EncryptedHelpURL
	}
	writeJSON(w, 200, res)
}

func handleUpdateTuner(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var b struct {
		Name       string            `json:"name"`
		Options    map[string]string `json:"options"`
		Enabled    *bool             `json:"enabled"`
		MaxStreams int               `json:"maxStreams"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	cur, err := m.Tuner(r.Context(), id)
	if err != nil {
		tunerError(w, err)
		return
	}
	if p := b.Options["playlist"]; p != cur.Options["playlist"] {
		if err := checkPlace(r, p); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
	}
	en := cur.Enabled
	if b.Enabled != nil {
		en = *b.Enabled
	}
	got, err := m.Update(r.Context(), id, firstNonEmpty(b.Name, cur.Name), b.Options, en, b.MaxStreams)
	if err != nil {
		tunerError(w, err)
		return
	}
	got.Options = PublicOptions(got)
	writeJSON(w, 200, got)
}

func handleDeleteTuner(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if err := m.Delete(r.Context(), id); err != nil {
		tunerError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleRefreshTuner(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if err := m.Refresh(r.Context(), id); err != nil {
		tunerError(w, err)
		return
	}
	t, err := m.Tuner(r.Context(), id)
	if err != nil {
		tunerError(w, err)
		return
	}
	t.Options = PublicOptions(t)
	writeJSON(w, 200, t)
}

func handleScanTuner(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var b struct {
		Source string `json:"source"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := m.StartScan(id, b.Source); err != nil {
		tunerError(w, err)
		return
	}
	writeJSON(w, 200, m.ScanProgress(id))
}

func handleScanProgress(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	writeJSON(w, 200, m.ScanProgress(id))
}

// ChannelView is a channel for its settings page: as it's stored, with what it wears. The
// picture is the server's to serve (logoUrl, /api/tv/tun-<id>/logo), and so is the logo service
// a brand names.
type ChannelView struct {
	*Channel
	LogoURL   string `json:"logoUrl,omitempty"`
	Brand     *Brand `json:"brand,omitempty"`
	CanRecord bool   `json:"canRecord"`
}

// Brand is a logo by its kind and name, in the server's logo service.
type Brand struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func viewChannel(c *Channel) ChannelView {
	v := ChannelView{Channel: c, CanRecord: c.NoRecord == "" && c.Security != SecurityEncrypted}
	switch kind, name, ok := strings.Cut(c.LogoSet, "|"); {
	case c.LogoSet == "none":
	case ok:
		v.Brand = &Brand{kind, name}
	default:
		v.LogoURL = channelLogoURL(c)
	}
	return v
}

// channelLogoURL is where the apps get a channel's own logo, from the server: the source's address
// stays here and on the server, as it may hold a login. Its hash is there so that a browser
// notices when a playlist changes the logo.
func channelLogoURL(c *Channel) string {
	u, err := url.Parse(strings.TrimSpace(c.Logo))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ""
	}
	h := sha1.Sum([]byte(u.String()))
	return "/api/tv/tun-" + strconv.FormatInt(c.ID, 10) + "/logo?v=" + hex.EncodeToString(h[:6])
}

func handleTunerChannels(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	list, err := m.Channels(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]ChannelView, 0, len(list))
	for _, c := range list {
		out = append(out, viewChannel(c))
	}
	writeJSON(w, 200, out)
}

func handleEnableAllChannels(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := m.SetAllEnabled(r.Context(), id, b.Enabled); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// logoKinds are the logo service's kinds, as the server names them.
var logoKinds = map[string]bool{"studio": true, "network": true, "console": true, "gamestudio": true, "platform": true}

// checkLogoChoice bounds what a person can set as a channel's logo: nothing, none, or the name
// of a logo in the server's logo service.
func checkLogoChoice(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" {
		return v, nil
	}
	kind, name, ok := strings.Cut(v, "|")
	name = strings.TrimSpace(name)
	if !ok || !logoKinds[kind] || name == "" || len(name) > 120 || strings.ContainsAny(name, "\r\n|") {
		return "", errors.New("say none, or which logo: kind|name")
	}
	return kind + "|" + name, nil
}

func handleEditChannel(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var e ChannelEdit
	if err := readJSON(r, &e); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if e.Logo != nil {
		v, err := checkLogoChoice(*e.Logo)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		e.Logo = &v
	}
	c, err := m.EditChannel(r.Context(), id, e)
	if err != nil {
		tunerError(w, err)
		return
	}
	writeJSON(w, 200, viewChannel(c))
}

// POST /channels/bulk {"ids":[…],"action":…,"start":"100","into":42}
func handleBulkChannels(m *Manager, w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
		Start  string  `json:"start"`
		Into   int64   `json:"into"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if len(body.IDs) == 0 || len(body.IDs) > MaxBulk {
		writeErr(w, 400, "choose between 1 and 20000 channels")
		return
	}
	ctx := r.Context()
	var err error
	switch body.Action {
	case "enable", "disable":
		err = m.SetEnabled(ctx, body.IDs, body.Action == "enable")
	case "renumber":
		err = m.Renumber(ctx, body.IDs, body.Start)
	case "merge":
		err = m.Merge(ctx, body.Into, body.IDs)
	case "unmerge":
		err = m.Unmerge(ctx, body.IDs)
	case "check":
		// A few channels, checked now; a whole tuner goes through /health.
		if len(body.IDs) > 50 {
			writeErr(w, 400, "check up to 50 channels at once, or check the whole tuner")
			return
		}
		results := map[int64]string{}
		for _, id := range body.IDs {
			if cerr := m.CheckChannel(ctx, id); cerr != nil {
				results[id] = PublicError(cerr)
			} else {
				results[id] = ""
			}
			if ctx.Err() != nil {
				break
			}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "results": results})
		return
	default:
		writeErr(w, 400, "action is enable, disable, renumber, merge, unmerge or check")
		return
	}
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeErr(w, 404, "channel not found")
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// GET /duplicates: channels across every tuner that look like the same station.
func handleDuplicates(m *Manager, w http.ResponseWriter, r *http.Request) {
	groups, err := m.Duplicates(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if groups == nil {
		groups = []DupGroup{}
	}
	writeJSON(w, 200, map[string]any{"groups": groups})
}

func healthTuner(m *Manager, w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 404, "tuner not found")
		return 0, false
	}
	if _, err := m.Tuner(r.Context(), id); err != nil {
		writeErr(w, 404, "tuner not found")
		return 0, false
	}
	return id, true
}

// POST /tuners/{id}/health: check every enabled channel of a tuner, in the background.
func handleStartHealth(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := healthTuner(m, w, r)
	if !ok {
		return
	}
	if err := m.StartHealth(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, m.Health(id))
}

// GET /tuners/{id}/health: how that check is going.
func handleChannelHealth(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := healthTuner(m, w, r)
	if !ok {
		return
	}
	st := m.Health(id)
	if st == nil {
		st = &HealthState{}
	}
	writeJSON(w, 200, st)
}

// DELETE /tuners/{id}/health: stop it.
func handleStopHealth(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := healthTuner(m, w, r)
	if !ok {
		return
	}
	m.StopHealth(id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleEPGSources(m *Manager, w http.ResponseWriter, r *http.Request) {
	list, err := m.Sources(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func handleAddEPGSource(m *Manager, w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := checkPlace(r, b.URL); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	src, err := m.AddSource(r.Context(), b.Name, b.URL)
	if err != nil {
		tunerError(w, err)
		return
	}
	writeJSON(w, 200, src)
}

func handleDeleteEPGSource(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if err := m.DeleteSource(r.Context(), id); err != nil {
		tunerError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleRefreshEPGSource(m *Manager, w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	m.background("guide refresh", func(ctx context.Context) {
		if err := m.FetchSource(ctx, id); err != nil && ctx.Err() == nil {
			m.Log.Warn("guide refresh failed", "source", id, "err", err)
		}
	})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func handleEPGIDs(m *Manager, w http.ResponseWriter, r *http.Request) {
	list, err := m.GuideIDs(r.Context())
	if err != nil {
		writeErr(w, 502, "the server's guide couldn't be read")
		return
	}
	writeJSON(w, 200, list)
}

// POST /setup/tv {skip}: WittyTuner's first-run step (integration.json setup "tv") is done, or
// put off. The server, told on the administrator's behalf, stops showing "Finish setting up
// WittyTuner" on Home.
func handleSetupTV(m *Manager, w http.ResponseWriter, r *http.Request) {
	id := service.Who(r)
	var in struct {
		Skip bool `json:"skip"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, "A step's end is {skip}.")
		return
	}
	if m.Host == nil {
		writeErr(w, 503, "The server isn't there to tell.")
		return
	}
	if err := m.Host.Do(r.Context(), host.On{User: id.User}, "POST", "/setup/tv", in, nil); err != nil {
		writeErr(w, 502, "The server didn't take it: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "skipped": in.Skip})
}
