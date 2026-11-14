// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	tuner "github.com/wittywilla/wittytuner"
)

// Opening channels, within each tuner's limit. The server's live TV, recordings and catch-up ask
// for a channel here (the routes under /tv/, which only the server can reach: they're the
// service's own, not the module's HTTP API), and watch every stream themselves: the server only
// believes what its own guard sees, so nothing here can say a channel is clear. The guide, its
// channels' place in it and what a profile may watch are the server's.

// ErrNotFound: no such tuner, channel or guide source.
var ErrNotFound = errors.New("not found")

// ErrBusy: every tuner of a source is in use.
var ErrBusy = tuner.ErrBusy

// ErrNoArchive: what was asked for isn't in the source's archive.
var ErrNoArchive = errors.New("that isn't in this channel's archive")

// opened is a stream with the tuner it holds.
type opened struct {
	Stream  *tuner.Stream
	Channel *Channel
	// Home: what the stream names may lead to the home network (mayLeadHome).
	Home    bool
	release func()
	once    sync.Once
}

// Release frees the tuner, and remembers what the stream's own guard saw.
func (o *opened) Release() {
	o.once.Do(func() {
		_ = o.Stream.Close()
		o.release()
	})
}

// openChannel opens one channel, with no backups, within its tuner's limit, and keeps what that
// showed about it: whether it opened (unless it's from the archive), and why it's encrypted.
func (m *Manager) openChannel(ctx context.Context, channelID, archiveStart, archiveEnd int64) (*opened, error) {
	c, err := m.Channel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if c.Encrypted != "" {
		return nil, &tuner.EncryptedError{Channel: c.Name, Why: c.Encrypted}
	}
	if archiveStart != 0 {
		now := time.Now().Unix()
		if c.CatchupDays <= 0 || archiveStart <= 0 || archiveStart > now || archiveStart < now-int64(c.CatchupDays)*86400 || (archiveEnd != 0 && archiveEnd <= archiveStart) {
			return nil, ErrNoArchive
		}
	}
	s, t, err := m.source(ctx, c.TunerID)
	if err != nil {
		return nil, err
	}
	if !t.Enabled {
		return nil, errors.New("that tuner is turned off")
	}
	home := mayLeadHome(t)
	// FFmpeg makes its own connections for anything but HTTP, and those can't be held to the
	// internet, so a list from the internet only gets to name web addresses.
	if !home && !webGuide(c.locator) {
		return nil, errors.New("a playlist from the internet can only name web addresses. To use this channel, keep the playlist on your home network, or add it as a single stream")
	}
	limit := t.MaxStreams
	if limit == 0 {
		limit = s.Info().MaxStreams
	}
	m.mu.Lock()
	if limit > 0 && m.inUse[c.TunerID] >= limit {
		m.mu.Unlock()
		return nil, ErrBusy
	}
	m.inUse[c.TunerID]++
	m.mu.Unlock()
	release := func() {
		m.mu.Lock()
		m.inUse[c.TunerID]--
		m.mu.Unlock()
	}
	st, err := s.Open(ctx, tuner.Channel{ID: c.SourceID, Number: c.Number, Name: c.Name, Locator: c.locator, Program: c.Program,
		CatchupDays: c.CatchupDays, Catchup: c.catchup, CatchupSource: c.catchupSource, ArchiveStart: archiveStart, ArchiveEnd: archiveEnd})
	if err != nil {
		release()
		m.noteOpenFailure(ctx, c, err)
		if archiveStart == 0 {
			m.noteHealth(c.ID, err)
		}
		var enc *tuner.EncryptedError
		if errors.As(err, &enc) && enc.Why != "" {
			m.noteStream(c.ID, enc.Why)
		}
		return nil, err
	}
	if c.Health != HealthOK && archiveStart == 0 {
		m.noteHealth(c.ID, nil)
	}
	m.Log.DebugContext(ctx, "channel opened", "module", "wittytuner", "topic", "tuner", "tuner", c.TunerID, "channel", c.Name)
	return &opened{Stream: st, Channel: c, Home: home, release: release}, nil
}

// openWithBackups opens a channel, and when it won't open and has backups (the same station
// from other sources, see Merge), tries them in turn, the ones that worked last time first; if
// none opens, the channel's own error is the answer.
func (m *Manager) openWithBackups(ctx context.Context, channelID int64) (*opened, error) {
	o, err := m.openChannel(ctx, channelID, 0, 0)
	if !worthFailingOver(ctx, err) {
		return o, err
	}
	backups, berr := m.backupsOf(ctx, channelID)
	if berr != nil {
		m.Log.WarnContext(ctx, "could not list a channel's backups", "channel", channelID, "err", berr)
	}
	tried := 0
	for _, b := range backups {
		bo, berr := m.openChannel(ctx, b, 0, 0)
		if berr == nil {
			m.tunerLog().InfoContext(ctx, "a channel didn't open, so its backup is playing", "topic", "tuner", "channel", channelID, "backup", b, "why", err)
			return bo, nil
		}
		if ctx.Err() != nil {
			return nil, err
		}
		tried++
		m.tunerLog().DebugContext(ctx, "a backup channel didn't open either", "topic", "tuner", "channel", channelID, "backup", b, "err", berr)
	}
	if tried > 0 {
		m.tunerLog().WarnContext(ctx, "a channel and every backup failed to open", "topic", "tuner", "channel", channelID, "backups", tried, "err", err)
	}
	return nil, err
}

// noteOpenFailure leaves a record of a tuner that wouldn't give a channel.
// Being busy, or the caller leaving, isn't a fault; a refusal is only worth a note.
func (m *Manager) noteOpenFailure(ctx context.Context, c *Channel, err error) {
	log := m.tunerLog().With("topic", "tuner", "tuner", c.TunerID, "channel", c.Name)
	switch {
	case ctx.Err() != nil, errors.Is(err, ErrBusy):
		log.DebugContext(ctx, "channel not opened", "err", err)
	case tuner.IsEncrypted(err):
		log.InfoContext(ctx, "channel refused: encrypted", "err", err)
	default:
		log.WarnContext(ctx, "could not open a tuner channel", "err", err)
	}
}

// --------------------------------------------------------------------------
// The server's side: /tv/…
// --------------------------------------------------------------------------

// slot is a stream the server holds. It lasts until the server lets it go, or the service stops.
type slot struct {
	o      *opened
	cancel context.CancelFunc
	taken  bool // its bytes were handed over
}

func (s *slot) close() {
	s.cancel()
	s.o.Release()
}

// ServerChannel is a channel as the server's guide, live TV and recordings see it.
type ServerChannel struct {
	*Channel
	// The tuner's: its name and kind, and whether it's on.
	TunerName    string `json:"tunerName"`
	Driver       string `json:"driver"`
	TunerEnabled bool   `json:"tunerEnabled"`
	// TunerStreams is how many channels the tuner can stream at once: the owner's limit, else the
	// device's; 0 for no known limit. Recordings that would want more are marked as conflicts.
	TunerStreams int `json:"tunerStreams,omitempty"`
	// LogoURL is the source's own address for its logo, for the server to fetch and serve.
	LogoURL string `json:"logoUrl,omitempty"`
}

// openRequest is what the server asks to open.
type openRequest struct {
	Channel      int64 `json:"channel"`
	ArchiveStart int64 `json:"archiveStart,omitempty"`
	ArchiveEnd   int64 `json:"archiveEnd,omitempty"`
	// Backups: try the channel's backups when it won't open (never from the archive).
	Backups bool `json:"backups,omitempty"`
}

// openAnswer is an opened stream: raw MPEG-TS at /tv/stream/{slot}, or what FFmpeg should open
// by itself (Input), which the server holds to its own rules first.
type openAnswer struct {
	Slot    string   `json:"slot"`
	Channel int64    `json:"channel"` // the one that opened: a backup, perhaps
	Input   []string `json:"input,omitempty"`
	// Verified: an Input stream was inspected on opening and found clear. Recheck: it can change
	// while it plays, so it's worth asking again (/tv/recheck).
	Verified bool `json:"verified,omitempty"`
	Recheck  bool `json:"recheck,omitempty"`
	Home     bool `json:"home,omitempty"`
}

// ServerRoutes are the service's own routes for the server (service.Config.Routes): never a
// person's, so they aren't in integration.json, and only the server has the service's secret.
func ServerRoutes(mux *http.ServeMux) {
	h := func(f func(*Manager, http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			m, err := Get()
			if err != nil {
				writeErr(w, 503, err.Error())
				return
			}
			f(m, w, r)
		}
	}
	mux.HandleFunc("GET /tv/channels", h(handleServerChannels))
	mux.HandleFunc("POST /tv/open", h(handleServerOpen))
	mux.HandleFunc("GET /tv/stream/{slot}", h(handleServerStream))
	mux.HandleFunc("POST /tv/release", h(handleServerRelease))
	mux.HandleFunc("POST /tv/recheck", h(handleServerRecheck))
	mux.HandleFunc("POST /tv/note", h(handleServerNote))
}

// GET /tv/channels: every channel of every tuner, for the server's guide, live TV, recordings and
// sets.
func handleServerChannels(m *Manager, w http.ResponseWriter, r *http.Request) {
	list, err := m.AllChannels(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"channels": list})
}

// AllChannels is every channel of every tuner, with its tuner's name and state.
func (m *Manager) AllChannels(ctx context.Context) ([]ServerChannel, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT `+channelCols+`, t.enabled FROM tuner_channels c JOIN tuners t ON t.id = c.tuner_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServerChannel{}
	for rows.Next() {
		var on int
		c, err := scanChannel(rows, &on)
		if err != nil {
			return nil, err
		}
		out = append(out, ServerChannel{Channel: c, TunerName: c.sourceName, Driver: c.driver, TunerEnabled: on != 0, LogoURL: c.Logo})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	limits := map[int64]int{}
	for i := range out {
		id := out[i].TunerID
		if _, ok := limits[id]; !ok {
			limits[id] = m.streamLimit(ctx, id)
		}
		out[i].TunerStreams = limits[id]
	}
	if counts, err := m.backupCounts(ctx); err == nil {
		for _, c := range out {
			c.Backups = counts[c.ID]
		}
	}
	return out, nil
}

// streamLimit is how many channels a tuner can stream at once: the owner's limit, else what the
// device says; 0 for no known limit.
func (m *Manager) streamLimit(ctx context.Context, tunerID int64) int {
	s, t, err := m.source(ctx, tunerID)
	if err != nil {
		if t != nil {
			return t.MaxStreams
		}
		return 0
	}
	if t.MaxStreams > 0 {
		return t.MaxStreams
	}
	return s.Info().MaxStreams
}

// codeOf names an error the server acts on.
func codeOf(err error) (int, string) {
	var enc *tuner.EncryptedError
	switch {
	case errors.Is(err, ErrNotFound):
		return 404, "not-found"
	case errors.As(err, &enc):
		return 403, "encrypted"
	case errors.Is(err, ErrBusy):
		return 503, "busy"
	case errors.Is(err, ErrNoArchive):
		return 404, "no-archive"
	case unreachable(err):
		return 502, "unreachable"
	}
	return 400, ""
}

// POST /tv/open {channel, archiveStart, archiveEnd, backups}
func handleServerOpen(m *Manager, w http.ResponseWriter, r *http.Request) {
	var in openRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || in.Channel <= 0 {
		writeErr(w, 400, "say which channel")
		return
	}
	// The stream lasts until the server lets it go, not as long as this request.
	ctx, cancel := context.WithCancel(m.stopCtx)
	var o *opened
	var err error
	switch {
	case in.ArchiveStart != 0:
		o, err = m.openChannel(ctx, in.Channel, in.ArchiveStart, in.ArchiveEnd)
	case in.Backups:
		o, err = m.openWithBackups(ctx, in.Channel)
	default:
		o, err = m.openChannel(ctx, in.Channel, 0, 0)
	}
	if err != nil {
		cancel()
		status, code := codeOf(err)
		msg := err.Error()
		var enc *tuner.EncryptedError
		if errors.As(err, &enc) {
			msg = enc.Why
		} else if code != "busy" && code != "not-found" && code != "no-archive" {
			msg = PublicError(err)
		}
		writeJSON(w, status, map[string]string{"error": msg, "code": code})
		return
	}
	id := newSlotID()
	m.mu.Lock()
	m.slots[id] = &slot{o: o, cancel: cancel}
	m.mu.Unlock()
	ans := openAnswer{Slot: id, Channel: o.Channel.ID, Home: o.Home}
	if o.Stream.Reader == nil {
		ans.Input, ans.Verified, ans.Recheck = o.Stream.Input, o.Stream.Verified, o.Stream.Recheck != nil
	}
	writeJSON(w, 200, ans)
}

func newSlotID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) slot(id string) *slot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.slots[id]
}

// GET /tv/stream/{slot}: an opened channel's MPEG-TS, once. The server guards it for itself, so
// it isn't looked through twice. Ending the request ends the stream, and frees the tuner.
func handleServerStream(m *Manager, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("slot")
	m.mu.Lock()
	s := m.slots[id]
	if s != nil && (s.taken || s.o.Stream.Reader == nil) {
		s = nil
	}
	if s != nil {
		s.taken = true
	}
	m.mu.Unlock()
	if s == nil {
		writeErr(w, 404, "no such stream")
		return
	}
	defer m.releaseSlot(id)
	var src io.Reader = s.o.Stream.Reader
	if g := s.o.Stream.Guard; g != nil {
		src = g.Underneath()
	}
	w.Header().Set("Content-Type", "video/mp2t")
	w.WriteHeader(200)
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 188*348)
	stop := context.AfterFunc(r.Context(), func() { _ = s.o.Stream.Close() })
	defer stop()
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) releaseSlot(id string) {
	m.mu.Lock()
	s := m.slots[id]
	delete(m.slots, id)
	m.mu.Unlock()
	if s != nil {
		s.close()
	}
}

// POST /tv/release {slot}: the server is done with a stream.
func handleServerRelease(m *Manager, w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slot string `json:"slot"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in)
	m.releaseSlot(in.Slot)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// POST /tv/recheck {slot}: looks again at a stream FFmpeg is reading that can change while it
// plays. why says it's encrypted now; next is the seconds until it's worth asking again.
func handleServerRecheck(m *Manager, w http.ResponseWriter, r *http.Request) {
	var in struct {
		Slot string `json:"slot"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<10)).Decode(&in)
	s := m.slot(in.Slot)
	if s == nil || s.o.Stream.Recheck == nil {
		writeErr(w, 404, "no such stream")
		return
	}
	why, next, err := s.o.Stream.Recheck(r.Context())
	if err != nil {
		writeErr(w, 502, PublicError(err))
		return
	}
	writeJSON(w, 200, map[string]any{"why": why, "next": int64(next / time.Second)})
}

// POST /tv/note {channel, encrypted, clear}: what the server's own guard saw a stream show
// about itself, kept so the guide and settings are right from then on.
func handleServerNote(m *Manager, w http.ResponseWriter, r *http.Request) {
	var in struct {
		Channel   int64  `json:"channel"`
		Encrypted string `json:"encrypted,omitempty"`
		Clear     bool   `json:"clear,omitempty"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || in.Channel <= 0 {
		writeErr(w, 400, "say which channel")
		return
	}
	switch {
	case in.Encrypted != "":
		m.noteStream(in.Channel, clipText(in.Encrypted, 300))
	case in.Clear:
		m.noteStream(in.Channel, "")
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

var _ = strconv.Itoa
