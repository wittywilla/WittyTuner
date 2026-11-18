// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/wittywilla/wittymodule/host"
	tuner "github.com/wittywilla/wittytuner"
	"github.com/wittywilla/wittytuner/store"
)

// Tuner is one configured source.
type Tuner struct {
	ID         int64             `json:"id"`
	Driver     string            `json:"driver"`
	Name       string            `json:"name"`
	Options    map[string]string `json:"options"`
	Enabled    bool              `json:"enabled"`
	MaxStreams int               `json:"maxStreams"`
	LastScan   *int64            `json:"lastScan"`
	LastError  string            `json:"lastError"`
	Detail     string            `json:"detail"`
	Channels   int               `json:"channels"`
	Encrypted  int               `json:"encrypted"` // channels known to be protected
	Clear      int               `json:"clear"`     // channels known not to be
	InUse      int               `json:"inUse"`
}

// Channel is a tuner's channel as the guide and the settings see it. Number
// and Name are what the viewer sees: the owner's choice, else the source's.
type Channel struct {
	ID        int64  `json:"id"`
	TunerID   int64  `json:"tunerId"`
	SourceID  string `json:"sourceId"`
	Number    string `json:"number"`
	Name      string `json:"name"`
	Group     string `json:"group,omitempty"`
	CallSign  string `json:"callsign,omitempty"`
	Logo      string `json:"-"` // the source's address, never sent to clients: see handleTunerLogo
	HD        bool   `json:"hd,omitempty"`
	Radio     bool   `json:"radio,omitempty"`
	Encrypted string `json:"encrypted,omitempty"`
	// Security is encrypted, clear or unknown (nobody can tell until it is tuned).
	Security   string `json:"security"`
	ATSC3      bool   `json:"atsc3,omitempty"`
	VideoCodec string `json:"videoCodec,omitempty"`
	AudioCodec string `json:"audioCodec,omitempty"`
	// NoRecord is why this channel may not be recorded: the device said so, or
	// the owner did from what the device's documentation says.
	NoRecord  string `json:"noRecord,omitempty"`
	Program   int    `json:"program,omitempty"`
	Enabled   bool   `json:"enabled"`
	EPGID     string `json:"epgId,omitempty"`
	NumberSet string `json:"numberSet,omitempty"`
	NameSet   string `json:"nameSet,omitempty"`
	EPGSet    string `json:"epgSet,omitempty"`
	// LogoSet is what a person chose for its logo: "" to find it by name, "none", or
	// "kind|name" in the logo service.
	LogoSet string `json:"logoSet,omitempty"`
	// CatchupDays: the source keeps this many days of what the channel showed, to be
	// watched from the start. catchup and catchupSource say how to ask for it.
	CatchupDays int `json:"catchupDays,omitempty"`
	// Health is what the last check or tune found: HealthOK, HealthFailing, or "" for not yet.
	Health    string `json:"health,omitempty"`
	HealthAt  int64  `json:"healthAt,omitempty"`
	HealthMsg string `json:"healthMsg,omitempty"`
	// MergedInto: this channel is a backup of that one, hidden from the guide and tried
	// when that one won't open. Backups counts a channel's own backups (settings only).
	MergedInto int64 `json:"mergedInto,omitempty"`
	Backups    int   `json:"backups,omitempty"`
	// What the playlist says about it: language, country, and the minutes its guide is moved by.
	Language   string `json:"language,omitempty"`
	Country    string `json:"country,omitempty"`
	GuideShift int    `json:"guideShift,omitempty"`
	// MissingSince: the source no longer lists it (Unix seconds). It stays, hidden from the guide,
	// for a while, so that a playlist that drops it for a day loses none of what was chosen for it.
	MissingSince int64 `json:"missingSince,omitempty"`
	// EPGScore is how sure a guide match is (0-100), and EPGHow what it went by: "source" (the
	// playlist or device named it), "manual", "remembered", or what an automatic match used.
	EPGScore      int    `json:"epgScore,omitempty"`
	EPGHow        string `json:"epgHow,omitempty"`
	extra         chanExtra
	catchup       string
	catchupSource string
	locator       string
	sourceName    string
	driver        string
}

// Manager owns the tuners: their settings and channels in WittyTuner's own database, the
// sources running now, scans, channel checks and the guide sources.
type Manager struct {
	DB   *store.DB
	Dir  string
	Host *host.Client // nil in tests
	Log  *slog.Logger
	// FFmpeg and FFprobe are the server's, as the host API says; "" until known.
	FFmpeg, FFprobe string
	// AllowCommands: whoever runs the machine allows the custom command driver.
	AllowCommands bool

	mu      sync.Mutex
	sources map[int64]tuner.Source
	scans   map[int64]*ScanState
	inUse   map[int64]int
	health  map[int64]*HealthState // channel checks, by tuner
	slots   map[string]*slot       // streams the server holds, by id
	nextID  int64
	epgOnce sync.Once
	// Stop ends everything at shutdown.
	stopCtx context.Context
	stop    context.CancelFunc
	bg      sync.WaitGroup
}

// ScanState is a scan under way, or the last one.
type ScanState struct {
	Running bool   `json:"running"`
	Percent int    `json:"percent"`
	Found   int    `json:"found"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Added   int    `json:"added"`
	// What the finished scan found.
	Clear     int `json:"clear"`
	Encrypted int `json:"encrypted"`
	Unknown   int `json:"unknown"`
	cancel    context.CancelFunc
}

// New makes a manager over a database. Background work starts only when something needs it.
func New(db *store.DB, dir string, h *host.Client, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{DB: db, Dir: dir, Host: h, Log: log.With("cat", "tv"), sources: map[int64]tuner.Source{}, scans: map[int64]*ScanState{},
		inUse: map[int64]int{}, health: map[int64]*HealthState{}, slots: map[string]*slot{}, stopCtx: ctx, stop: cancel}
}

// Start runs the guide refresher, if there is anything for it to do. Called when the service
// starts and whenever a tuner or a guide source is added.
func (m *Manager) Start() {
	m.startDemo()
	var n int
	_ = m.DB.QueryRow(`SELECT COUNT(*) FROM epg_sources`).Scan(&n)
	if n == 0 {
		return
	}
	m.epgOnce.Do(func() { m.background("guide refresher", m.epgLoop) })
}

// startDemo starts the demo tuners, which answer on this machine only while WittyTuner runs, so
// their guide and channels are there before anyone asks for a channel.
func (m *Manager) startDemo() {
	rows, err := m.DB.Query(`SELECT id FROM tuners WHERE driver = 'demo' AND enabled = 1`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if _, _, err := m.source(m.stopCtx, id); err != nil {
			m.setError(id, PublicError(err))
		}
	}
}

// Close stops everything the manager runs, and every stream.
func (m *Manager) Close() {
	m.stop()
	m.mu.Lock()
	for id, s := range m.slots {
		s.close()
		delete(m.slots, id)
	}
	for id, s := range m.sources {
		_ = s.Close()
		delete(m.sources, id)
	}
	m.mu.Unlock()
}

// background runs fn until it returns or the manager stops.
func (m *Manager) background(where string, fn func(ctx context.Context)) bool {
	if m.stopCtx.Err() != nil {
		return false
	}
	m.bg.Add(1)
	go func() {
		defer m.bg.Done()
		defer func() {
			if p := recover(); p != nil {
				m.Log.Error("background work failed", "work", where, "panic", fmt.Sprint(p))
			}
		}()
		fn(m.stopCtx)
	}()
	return true
}

// tunerLog is for what a tuner did, as opposed to what the guide did.
func (m *Manager) tunerLog() *slog.Logger { return m.Log.With("module", "wittytuner") }

var addressInText = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"']+`)

// PublicError is what people may be told about a tuner or stream that failed. Connection errors say
// what's on the network, and hold the address, which can hold a login. Anything else that quotes
// an address, as a program's errors can, has it taken out.
func PublicError(err error) string {
	if err == nil {
		return ""
	}
	var ue *url.Error
	var op *net.OpError
	var dns *net.DNSError
	var pe *fs.PathError
	if errors.As(err, &ue) || errors.As(err, &op) || errors.As(err, &dns) {
		return "the stream could not be reached"
	}
	if errors.As(err, &pe) {
		return "a file could not be read"
	}
	return addressInText.ReplaceAllString(err.Error(), "(address)")
}

// env is what a tuner's driver gets: clients held to where its settings say it may reach (a
// playlist from the internet doesn't get to reach the home network), and the server's FFmpeg.
func (m *Manager) env(driver string, options map[string]string) tuner.Env {
	env := tuner.HelperEnvFor(driver, options)
	env.FFmpeg, env.AllowCommands = m.FFmpeg, m.AllowCommands
	env.Log = m.tunerLog()
	env.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, name, args...)
	}
	return env
}

// newSource makes a running source, which is also what checks its settings.
func (m *Manager) newSource(_ context.Context, driver string, cfg tuner.Config) (tuner.Source, error) {
	return tuner.New(driver, cfg, m.env(driver, cfg.Options))
}

// Drivers lists the kinds of tuner.
func (m *Manager) Drivers(context.Context) ([]tuner.Driver, error) {
	return tuner.Drivers(m.env("", nil)), nil
}

// secretFields are the settings of a driver that are never shown again.
func secretFields(_ context.Context, driver string, _ map[string]string) []string {
	return tuner.SecretFields(driver)
}

// mayLeadHome: the owner named this tuner's address, or its playlist is a file or on the home
// network, going by the address the owner typed. A playlist from the internet stays on the
// internet: what FFmpeg opens for it is held to that by the server's gate.
func mayLeadHome(t *Tuner) bool {
	switch t.Driver {
	case "iptv":
		return tuner.LocalPlaylist(t.Options["playlist"])
	case "xtream":
		return tuner.LocalPlaylist(t.Options["url"])
	}
	return true
}

// now is the time as the database keeps it.
func now() int64 { return time.Now().Unix() }

const tunerCols = `t.id, t.driver, t.name, t.options, t.enabled, t.max_streams, t.last_scan, t.last_error,
	(SELECT COUNT(*) FROM tuner_channels c WHERE c.tuner_id = t.id),
	(SELECT COUNT(*) FROM tuner_channels c WHERE c.tuner_id = t.id AND (c.encrypted = 1 OR c.encrypted_seen <> '')),
	(SELECT COUNT(*) FROM tuner_channels c WHERE c.tuner_id = t.id AND c.encrypted = 0 AND c.encrypted_seen = '' AND (c.checked = 1 OR c.seen_clear = 1))`

func scanTuner(sc interface{ Scan(...any) error }) (*Tuner, error) {
	var t Tuner
	var opts string
	var enabled int
	var last sql.NullInt64
	if err := sc.Scan(&t.ID, &t.Driver, &t.Name, &opts, &enabled, &t.MaxStreams, &last, &t.LastError, &t.Channels, &t.Encrypted, &t.Clear); err != nil {
		return nil, err
	}
	t.Enabled = enabled != 0
	if last.Valid {
		t.LastScan = &last.Int64
	}
	if err := json.Unmarshal([]byte(opts), &t.Options); err != nil || t.Options == nil {
		t.Options = map[string]string{}
	}
	return &t, nil
}

// Tuners lists every tuner.
func (m *Manager) Tuners(ctx context.Context) ([]*Tuner, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT `+tunerCols+` FROM tuners t ORDER BY t.name COLLATE NOCASE, t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Tuner{}
	for rows.Next() {
		t, err := scanTuner(rows)
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		t.InUse = m.inUse[t.ID]
		if src := m.sources[t.ID]; src != nil {
			t.Detail = src.Info().Detail
		}
		m.mu.Unlock()
		out = append(out, t)
	}
	return out, rows.Err()
}

// Tuner returns one tuner.
func (m *Manager) Tuner(ctx context.Context, id int64) (*Tuner, error) {
	t, err := scanTuner(m.DB.QueryRowContext(ctx, `SELECT `+tunerCols+` FROM tuners t WHERE t.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

// Masked replaces passwords when options go to a browser.
const Masked = "••••••••"

// PublicOptions hides passwords.
func PublicOptions(t *Tuner) map[string]string {
	out := make(map[string]string, len(t.Options))
	for k, v := range t.Options {
		out[k] = v
	}
	for _, k := range secretFields(context.Background(), t.Driver, out) {
		if out[k] != "" {
			out[k] = Masked
		}
	}
	return out
}

// Create adds a tuner, checking its settings, and lists its channels. A
// channel list that cannot be read yet is not an error: the tuner is kept, with
// the reason, because the device may simply be off.
func (m *Manager) Create(ctx context.Context, t *Tuner) (*Tuner, error) {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return nil, errors.New("give the tuner a name")
	}
	if len(t.Name) > 200 {
		return nil, errors.New("the name is too long")
	}
	if _, err := m.newSource(ctx, t.Driver, tuner.Config{Name: t.Name, Options: cloneMap(t.Options)}); err != nil {
		return nil, err
	}
	opts, _ := json.Marshal(t.Options)
	res, err := m.DB.ExecContext(ctx, `INSERT INTO tuners(driver, name, options, max_streams, created_at) VALUES(?,?,?,?,?)`, t.Driver, t.Name, string(opts), max(t.MaxStreams, 0), now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	m.Log.InfoContext(ctx, "tuner added", "id", id, "driver", t.Driver, "tuner", t.Name)
	m.Start()
	if err := m.Refresh(ctx, id); err != nil {
		// Refresh warned with the reason; the device may simply be off.
		m.Log.InfoContext(ctx, "tuner added, channels not read yet", "tuner", t.Name, "err", err)
	}
	return m.Tuner(ctx, id)
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Update changes a tuner's settings. A masked password keeps the stored one.
func (m *Manager) Update(ctx context.Context, id int64, name string, options map[string]string, enabled bool, maxStreams int) (*Tuner, error) {
	t, err := m.Tuner(ctx, id)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("give the tuner a name")
	}
	if options != nil {
		for _, k := range secretFields(ctx, t.Driver, t.Options) {
			if options[k] == Masked {
				options[k] = t.Options[k]
			}
		}
	} else {
		options = t.Options
	}
	if _, err := m.newSource(ctx, t.Driver, tuner.Config{Name: name, Options: cloneMap(options)}); err != nil {
		return nil, err
	}
	opts, _ := json.Marshal(options)
	en := 0
	if enabled {
		en = 1
	}
	if _, err := m.DB.ExecContext(ctx, `UPDATE tuners SET name=?, options=?, enabled=?, max_streams=? WHERE id=?`, name, string(opts), en, max(maxStreams, 0), id); err != nil {
		return nil, err
	}
	m.forget(id)
	m.Log.InfoContext(ctx, "tuner changed", "id", id, "driver", t.Driver, "tuner", name, "enabled", enabled, "maxStreams", max(maxStreams, 0))
	return m.Tuner(ctx, id)
}

// Delete removes a tuner, its channels and its guide source.
func (m *Manager) Delete(ctx context.Context, id int64) error {
	res, err := m.DB.ExecContext(ctx, `DELETE FROM tuners WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// The database logs its own failures; a guide source left behind is only refetched.
	_, _ = m.DB.ExecContext(ctx, `DELETE FROM epg_sources WHERE tuner_id = ?`, id)
	m.forget(id)
	m.unorphanBackups(ctx)
	m.Log.InfoContext(ctx, "tuner removed", "id", id)
	return nil
}

func (m *Manager) forget(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sources[id]; s != nil {
		_ = s.Close()
		delete(m.sources, id)
	}
}

func (m *Manager) source(ctx context.Context, id int64) (tuner.Source, *Tuner, error) {
	t, err := m.Tuner(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sources[id]; s != nil {
		return s, t, nil
	}
	s, err := m.newSource(ctx, t.Driver, tuner.Config{Name: t.Name, Options: t.Options})
	if err != nil {
		return nil, t, err
	}
	m.sources[id] = s
	return s, t, nil
}

// Refresh re-reads a tuner's channels without scanning.
func (m *Manager) Refresh(ctx context.Context, id int64) error {
	s, _, err := m.source(ctx, id)
	if err != nil {
		return err
	}
	log := m.tunerLog().With("topic", "tuner", "tuner", id)
	ch, err := s.Channels(ctx)
	if err != nil {
		log.WarnContext(ctx, "could not read a tuner's channels", "err", err)
		m.setError(id, PublicError(err))
		return err
	}
	if ch != nil {
		added, err := m.saveChannels(ctx, id, ch)
		if err != nil {
			log.WarnContext(ctx, "could not save a tuner's channels", "err", err)
			return err
		}
		log.InfoContext(ctx, "tuner channels read", "channels", len(ch), "new", added)
	}
	m.setError(id, "")
	m.ensureGuide(ctx, id, s)
	// New channels may already be in the guide, or remembered.
	m.background("guide matching", func(ctx context.Context) { _, _ = m.AutoMatch(ctx) })
	return nil
}

func (m *Manager) setError(id int64, msg string) {
	if _, err := m.DB.Exec(`UPDATE tuners SET last_error = ? WHERE id = ?`, msg, id); err != nil {
		m.Log.Warn("could not save a tuner's last error", "tuner", id, "err", err)
	}
}

// ensureGuide offers a source's own guide address as a guide source.
func (m *Manager) ensureGuide(ctx context.Context, id int64, s tuner.Source) {
	info := s.Info()
	if !webGuide(info.GuideURL) { // nor can a playlist choose a file on this machine, or a network share
		return
	}
	auth := ""
	if b, ok := s.(interface{ GuideHeaders() http.Header }); ok {
		if h := b.GuideHeaders(); len(h) > 0 {
			raw, _ := json.Marshal(h)
			auth = string(raw)
		}
	}
	if _, err := m.DB.ExecContext(ctx, `INSERT INTO epg_sources(name, url, auth, tuner_id) VALUES(?,?,?,?) ON CONFLICT(url) DO UPDATE SET auth = excluded.auth, tuner_id = excluded.tuner_id`,
		"From "+info.Name, info.GuideURL, auth, id); err != nil {
		m.Log.WarnContext(ctx, "could not add a tuner's own guide", "tuner", id, "err", err)
		return
	}
	m.Log.DebugContext(ctx, "a tuner's own guide is a guide source", "tuner", id)
	m.Start()
	m.background("guide refresh", m.refreshDue)
}

// saveChannels merges a channel list: what the owner chose stays. A channel whose id changed (a
// playlist address with a new login token in it) keeps its row, and so its favourites, recordings
// and choices, when its name and group still say which it is. One that has gone from the source
// is kept, hidden, for a week before it goes, in case it comes back. It returns how many are new.
func (m *Manager) saveChannels(ctx context.Context, id int64, list []tuner.Channel) (int, error) {
	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// What is there now, by id, and by name and group for those whose id may have changed.
	type row struct {
		id      int64
		missing int64
	}
	have := map[string]row{}
	byKey := map[string][]string{}
	rows, err := tx.QueryContext(ctx, `SELECT id, source_id, name, grp, missing_since FROM tuner_channels WHERE tuner_id = ?`, id)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var r row
		var sid, name, grp string
		if err := rows.Scan(&r.id, &sid, &name, &grp, &r.missing); err != nil {
			rows.Close()
			return 0, err
		}
		have[sid] = r
		k := sameChannelKey(name, grp)
		byKey[k] = append(byKey[k], sid)
	}
	rows.Close()
	keep := make(map[string]bool, len(list))
	newKeys := map[string][]int{}
	for i, c := range list {
		if c.ID == "" || keep[c.ID] {
			continue
		}
		keep[c.ID] = true
		if _, ok := have[c.ID]; !ok {
			k := sameChannelKey(c.Name, c.Group)
			newKeys[k] = append(newKeys[k], i)
		}
	}
	// A new id takes over a row whose id has gone when, on both sides, only one channel has that
	// name in that group: no guessing between two of the same name.
	rekeyed := 0
	for k, idx := range newKeys {
		if len(idx) != 1 {
			continue
		}
		var gone []string
		for _, sid := range byKey[k] {
			if !keep[sid] {
				gone = append(gone, sid)
			}
		}
		if len(gone) != 1 {
			continue
		}
		c := list[idx[0]]
		if _, err := tx.ExecContext(ctx, `UPDATE tuner_channels SET source_id = ? WHERE id = ?`, c.ID, have[gone[0]].id); err != nil {
			return 0, err
		}
		have[c.ID] = have[gone[0]]
		delete(have, gone[0])
		rekeyed++
	}
	added := 0
	seen := make(map[string]bool, len(list))
	for _, c := range list {
		if c.ID == "" || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		if _, ok := have[c.ID]; !ok {
			added++
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO tuner_channels(tuner_id, source_id, number, name, grp, callsign, locator, logo, hd, radio, encrypted, encrypted_why, program, epg_id, checked, atsc3, video_codec, audio_codec, no_record, catchup_days, catchup, catchup_source, extra, missing_since, epg_src)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,?)
			ON CONFLICT(tuner_id, source_id) DO UPDATE SET number=excluded.number, name=excluded.name, grp=excluded.grp, callsign=excluded.callsign, locator=excluded.locator,
			logo=excluded.logo, hd=excluded.hd, radio=excluded.radio, encrypted=excluded.encrypted, encrypted_why=excluded.encrypted_why, program=excluded.program,
			epg_id=CASE WHEN excluded.epg_id = epg_src AND epg_how NOT IN ('', 'source') THEN epg_id ELSE excluded.epg_id END,
			epg_score=CASE WHEN excluded.epg_id = epg_src THEN epg_score ELSE 0 END,
			epg_how=CASE WHEN excluded.epg_id = epg_src THEN epg_how ELSE '' END,
			epg_src=excluded.epg_id,
			checked=excluded.checked, atsc3=excluded.atsc3, video_codec=excluded.video_codec, audio_codec=excluded.audio_codec, no_record=excluded.no_record,
			catchup_days=excluded.catchup_days, catchup=excluded.catchup, catchup_source=excluded.catchup_source, extra=excluded.extra, missing_since=0`,
			id, c.ID, c.Number, c.Name, c.Group, c.CallSign, c.Locator, c.Logo, boolInt(c.HD), boolInt(c.Radio), boolInt(c.Encrypted != ""), c.Encrypted, c.Program, c.EPGID,
			boolInt(c.Checked), boolInt(c.ATSC3), c.VideoCodec, c.AudioCodec, c.NoRecord, max(c.CatchupDays, 0), c.Catchup, c.CatchupSource, extraOf(c), c.EPGID)
		if err != nil {
			return 0, err
		}
	}
	// What the source no longer lists: marked missing now, and gone after a week of that.
	t := now()
	var gone []int64
	for sid, r := range have {
		if keep[sid] {
			continue
		}
		switch {
		case r.missing == 0:
			if _, err := tx.ExecContext(ctx, `UPDATE tuner_channels SET missing_since = ? WHERE id = ?`, t, r.id); err != nil {
				return 0, err
			}
		case time.Since(time.Unix(r.missing, 0)) > missingKeep:
			gone = append(gone, r.id)
		}
	}
	for _, cid := range gone {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tuner_channels WHERE id = ?`, cid); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if rekeyed > 0 {
		m.tunerLog().InfoContext(ctx, "channels kept their place though their address changed", "topic", "tuner", "tuner", id, "channels", rekeyed)
	}
	if len(gone) > 0 {
		m.unorphanBackups(ctx)
	}
	return added, nil
}

// chanExtra is what a playlist says about a channel beyond its address, kept as JSON.
type chanExtra struct {
	UserAgent  string   `json:"ua,omitempty"`
	Referrer   string   `json:"ref,omitempty"`
	Alternates []string `json:"alt,omitempty"`
	Language   string   `json:"lang,omitempty"`
	Country    string   `json:"country,omitempty"`
	GuideShift int      `json:"shift,omitempty"`
}

func extraOf(c tuner.Channel) string {
	e := chanExtra{c.UserAgent, c.Referrer, c.Alternates, c.Language, c.Country, c.GuideShift}
	if e.UserAgent == "" && e.Referrer == "" && len(e.Alternates) == 0 && e.Language == "" && e.Country == "" && e.GuideShift == 0 {
		return ""
	}
	b, _ := json.Marshal(e)
	return string(b)
}

// missingKeep is how long a channel the source stopped listing is kept, hidden, before it goes.
const missingKeep = 7 * 24 * time.Hour

// sameChannelKey says two entries of one source are the same channel when its id changed: a
// playlist address with a login token in it changes, its name and group don't.
func sameChannelKey(name, group string) string {
	return normalizeName(name) + "\x00" + strings.ToLower(strings.TrimSpace(group))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// StartScan begins a scan in the background.
func (m *Manager) StartScan(id int64, source string) error {
	ctx, cancel := context.WithTimeout(m.stopCtx, 2*time.Hour)
	s, _, err := m.source(ctx, id)
	if err != nil {
		cancel()
		return err
	}
	m.mu.Lock()
	if cur := m.scans[id]; cur != nil && cur.Running {
		m.mu.Unlock()
		cancel()
		return errors.New("that tuner is already scanning")
	}
	if m.inUse[id] > 0 {
		m.mu.Unlock()
		cancel()
		return errors.New("that tuner is in use: stop watching or recording first")
	}
	st := &ScanState{Running: true, cancel: cancel}
	m.scans[id] = st
	m.inUse[id]++
	m.mu.Unlock()
	log := m.tunerLog().With("topic", "tuner", "tuner", id)
	log.InfoContext(ctx, "channel scan started")
	task := m.task("Finding channels on a tuner")
	// ctx, the scan's own, ends with the manager too.
	started := m.background("channel scan", func(context.Context) {
		defer cancel()
		ch, err := s.Scan(ctx, tuner.ScanOptions{Source: source}, func(p tuner.Progress) {
			m.mu.Lock()
			st.Percent, st.Found, st.Message = p.Percent, p.Found, p.Message
			m.mu.Unlock()
			task(p.Percent, 100, nil)
		})
		defer func() { task(100, 100, err) }()
		added := 0
		if err == nil && ch != nil {
			added, err = m.saveChannels(context.Background(), id, ch)
		}
		_, _ = m.DB.Exec(`UPDATE tuners SET last_scan = ? WHERE id = ?`, now(), id)
		found, _ := m.Channels(context.Background(), id)
		m.mu.Lock()
		m.inUse[id]--
		st.Running, st.Added = false, added
		st.Clear, st.Encrypted, st.Unknown = 0, 0, 0
		for _, c := range found {
			switch c.Security {
			case SecurityClear:
				st.Clear++
			case SecurityEncrypted:
				st.Encrypted++
			default:
				st.Unknown++
			}
		}
		if err != nil {
			st.Error = PublicError(err)
		} else {
			st.Percent, st.Message = 100, ""
		}
		found2 := st.Found
		m.mu.Unlock()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.InfoContext(ctx, "channel scan cancelled")
			} else {
				log.WarnContext(ctx, "channel scan failed", "err", err)
			}
			m.setError(id, PublicError(err))
		} else {
			log.InfoContext(ctx, "channel scan finished", "found", found2, "added", added)
			m.setError(id, "")
			m.ensureGuide(context.Background(), id, s)
		}
	})
	if !started {
		cancel()
		m.mu.Lock()
		m.inUse[id]--
		st.Running, st.Error = false, errStopping.Error()
		m.mu.Unlock()
		task(0, 0, errStopping)
		return errStopping
	}
	return nil
}

// ScanProgress reads a scan's state.
func (m *Manager) ScanProgress(id int64) ScanState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.scans[id]; s != nil {
		return *s
	}
	return ScanState{}
}

// --------------------------------------------------------------------------
// Channels
// --------------------------------------------------------------------------

const channelCols = `c.id, c.tuner_id, c.source_id, c.number, c.name, c.grp, c.callsign, c.locator, c.logo, c.hd, c.radio, c.encrypted, c.encrypted_why, c.program, c.enabled,
	c.number_set, c.name_set, c.epg_id, c.epg_set, t.name, t.driver, c.encrypted_seen, c.checked, c.seen_clear, c.atsc3, c.video_codec, c.audio_codec, c.no_record, c.no_record_set, c.logo_set,
	c.catchup_days, c.catchup, c.catchup_source, c.health, c.health_at, c.health_msg, c.merged_into, c.extra, c.missing_since, c.epg_score, c.epg_how`

// What is known about a channel's protection.
const (
	SecurityClear     = "clear"
	SecurityEncrypted = "encrypted"
	SecurityUnknown   = "unknown"
)

func scanChannel(sc interface{ Scan(...any) error }, more ...any) (*Channel, error) {
	var c Channel
	var hd, radio, enc, enabled, checked, seenClear, atsc3, noRecordSet int
	var why, seen, noRecord string
	var merged sql.NullInt64
	var extra string
	if err := sc.Scan(append([]any{&c.ID, &c.TunerID, &c.SourceID, &c.Number, &c.Name, &c.Group, &c.CallSign, &c.locator, &c.Logo, &hd, &radio, &enc, &why, &c.Program, &enabled,
		&c.NumberSet, &c.NameSet, &c.EPGID, &c.EPGSet, &c.sourceName, &c.driver, &seen, &checked, &seenClear, &atsc3, &c.VideoCodec, &c.AudioCodec, &noRecord, &noRecordSet, &c.LogoSet,
		&c.CatchupDays, &c.catchup, &c.catchupSource, &c.Health, &c.HealthAt, &c.HealthMsg, &merged,
		&extra, &c.MissingSince, &c.EPGScore, &c.EPGHow}, more...)...); err != nil {
		return nil, err
	}
	if extra != "" {
		_ = json.Unmarshal([]byte(extra), &c.extra)
	}
	c.Language, c.Country, c.GuideShift = c.extra.Language, c.extra.Country, c.extra.GuideShift
	switch {
	case c.EPGSet != "":
		c.EPGScore, c.EPGHow = 100, "manual"
	case c.EPGID != "" && c.EPGHow == "":
		c.EPGScore, c.EPGHow = 100, "source"
	}
	c.HD, c.Radio, c.Enabled, c.ATSC3 = hd != 0, radio != 0, enabled != 0, atsc3 != 0
	c.MergedInto = merged.Int64
	switch {
	case enc != 0:
		c.Encrypted = firstNonEmpty(why, "it is marked as encrypted")
	case seen != "":
		c.Encrypted = seen
	}
	switch {
	case c.Encrypted != "":
		c.Security = SecurityEncrypted
	case checked != 0 || seenClear != 0:
		c.Security = SecurityClear
	default:
		c.Security = SecurityUnknown
	}
	c.NoRecord = noRecord
	if noRecordSet != 0 {
		c.NoRecord = firstNonEmpty(noRecord, "you marked it as not to be recorded, from what the device says")
	}
	if c.NumberSet != "" {
		c.Number = c.NumberSet
	}
	if c.NameSet != "" {
		c.Name = c.NameSet
	}
	return &c, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// GuideKey is the id that links a channel to guide data.
func (c *Channel) GuideKey() string { return firstNonEmpty(c.EPGSet, c.EPGID) }

// Channels lists a tuner's channels, for settings.
func (m *Manager) Channels(ctx context.Context, tunerID int64) ([]*Channel, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT `+channelCols+` FROM tuner_channels c JOIN tuners t ON t.id = c.tuner_id WHERE c.tuner_id = ?`, tunerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if counts, err := m.backupCounts(ctx); err == nil {
		for _, c := range out {
			c.Backups = counts[c.ID]
		}
	}
	sortChannels(out)
	return out, nil
}

func sortChannels(l []*Channel) {
	sort.SliceStable(l, func(i, j int) bool {
		a, ea := strconv.ParseFloat(l[i].Number, 64)
		b, eb := strconv.ParseFloat(l[j].Number, 64)
		switch {
		case ea == nil && eb == nil:
			return a < b
		case ea == nil:
			return true
		case eb == nil:
			return false
		}
		return l[i].Number < l[j].Number
	})
}

// GuideChannels lists every channel that can appear in the guide, across
// enabled tuners. Encrypted channels stay, so people can see why they do not play.
func (m *Manager) GuideChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT `+channelCols+` FROM tuner_channels c JOIN tuners t ON t.id = c.tuner_id WHERE c.enabled = 1 AND t.enabled = 1 AND c.merged_into IS NULL AND c.missing_since = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Channel{}
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Channel returns one channel.
func (m *Manager) Channel(ctx context.Context, id int64) (*Channel, error) {
	c, err := scanChannel(m.DB.QueryRowContext(ctx, `SELECT `+channelCols+` FROM tuner_channels c JOIN tuners t ON t.id = c.tuner_id WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// ChannelEdit is what an owner may change about a channel.
type ChannelEdit struct {
	Enabled *bool   `json:"enabled"`
	Number  *string `json:"number"`
	Name    *string `json:"name"`
	EPG     *string `json:"epg"`
	Logo    *string `json:"logo"` // see Channel.LogoSet
	// NoRecord: the device's documentation says this may not be recorded.
	NoRecord *bool `json:"noRecord"`
	// CheckAgain forgets what a stream showed, for a station that has changed.
	// A channel still encrypted is refused again the next time it is tuned.
	CheckAgain bool `json:"checkAgain"`
}

// EditChannel saves the owner's choices. They survive rescans.
func (m *Manager) EditChannel(ctx context.Context, id int64, e ChannelEdit) (*Channel, error) {
	if e.Number != nil {
		n := strings.TrimSpace(*e.Number)
		if len(n) > 12 || strings.Trim(n, "0123456789.-") != "" {
			return nil, errors.New("channel numbers look like 101 or 5.1")
		}
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET number_set = ? WHERE id = ?`, n, id); err != nil {
			return nil, err
		}
	}
	if e.Name != nil {
		if len(*e.Name) > 200 {
			return nil, errors.New("the name is too long")
		}
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET name_set = ? WHERE id = ?`, strings.TrimSpace(*e.Name), id); err != nil {
			return nil, err
		}
	}
	if e.Logo != nil {
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET logo_set = ? WHERE id = ?`, *e.Logo, id); err != nil {
			return nil, err
		}
	}
	if e.EPG != nil {
		if len(*e.EPG) > 400 {
			return nil, errors.New("that guide id is too long")
		}
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET epg_set = ? WHERE id = ?`, strings.TrimSpace(*e.EPG), id); err != nil {
			return nil, err
		}
		m.remember(ctx, id, strings.TrimSpace(*e.EPG))
	}
	if e.Enabled != nil {
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET enabled = ? WHERE id = ?`, boolInt(*e.Enabled), id); err != nil {
			return nil, err
		}
	}
	if e.NoRecord != nil {
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET no_record_set = ? WHERE id = ?`, boolInt(*e.NoRecord), id); err != nil {
			return nil, err
		}
	}
	if e.CheckAgain {
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET encrypted_seen = '', seen_clear = 0 WHERE id = ?`, id); err != nil {
			return nil, err
		}
	}
	return m.Channel(ctx, id)
}

// SetAllEnabled turns every channel of a tuner on or off, for long playlists.
func (m *Manager) SetAllEnabled(ctx context.Context, tunerID int64, on bool) error {
	_, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET enabled = ? WHERE tuner_id = ?`, boolInt(on), tunerID)
	return err
}

// --------------------------------------------------------------------------
// noteStream keeps what a stream showed, so the guide and settings are right
// from then on: why it is encrypted, or "" for clear.
func (m *Manager) noteStream(id int64, why string) {
	var err error
	if why != "" {
		_, err = m.DB.Exec(`UPDATE tuner_channels SET encrypted_seen = ?, seen_clear = 0 WHERE id = ?`, why, id)
	} else {
		_, err = m.DB.Exec(`UPDATE tuner_channels SET seen_clear = 1, encrypted_seen = '' WHERE id = ?`, id)
	}
	if err != nil {
		m.Log.Warn("could not save what a stream showed", "channel", id, "err", err)
	}
	if why != "" {
		m.Log.Info("channel marked as encrypted", "module", "wittytuner", "topic", "tuner", "channel", id, "why", why)
	}
}

// Discover looks for tuners, and leaves out those already added.
func (m *Manager) Discover(ctx context.Context) ([]tuner.Found, error) {
	found := tuner.Discover(ctx, m.env("", nil))
	tuners, _ := m.Tuners(ctx)
	out := []tuner.Found{}
	for _, f := range found {
		dup := false
		for _, t := range tuners {
			same := t.Driver == f.Driver
			for k, v := range f.Options {
				same = same && t.Options[k] == v
			}
			dup = dup || same
		}
		if !dup {
			out = append(out, f)
		}
	}
	return out, nil
}

// Describe gives a short account of a tuner, for logs.
func (t *Tuner) Describe() string { return fmt.Sprintf("%s (%s)", t.Name, t.Driver) }

var errStopping = errors.New("the service is stopping")

// task reports a long piece of work to the server's list of tasks (administrators only): a
// function to call with how far it has got, and once more with how it ended.
func (m *Manager) task(name string) func(done, total int, err error) {
	return func(done, total int, err error) {
		if m.Host == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Host.Task(ctx, name, done, total, err)
	}
}

func normalizeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSuffix(b.String(), "hd")
}

// unreachable: a connection failed, which says what's on the network.
func unreachable(err error) bool {
	var ue *url.Error
	var op *net.OpError
	var dns *net.DNSError
	return errors.As(err, &ue) || errors.As(err, &op) || errors.As(err, &dns)
}
