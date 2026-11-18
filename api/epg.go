// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wittywilla/wittymodule/host"
	"github.com/wittywilla/wittymodule/service"
	tuner "github.com/wittywilla/wittytuner"
)

// Guide sources: XMLTV addresses (or files on the server) the owner added, and those a tuner or
// its playlist names, fetched on a schedule. What they list is the server's: each fetch is handed
// to it as it arrives (the host API's POST /tv/guide/{source}), and it keeps the programmes of
// the channels it has, matching a channel with no guide id by its name. The guide itself, and
// what broadcasts say about themselves, stay the server's.

// EPGSource is a place guide data comes from.
type EPGSource struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	LastFetch  *int64 `json:"lastFetch"`
	LastError  string `json:"lastError"`
	Programmes int    `json:"programmes"`
	TunerID    *int64 `json:"tunerId,omitempty"`
	// From what the server kept: how many of the guide's channels it has, how far ahead its
	// programmes go (Unix seconds), and a warning when its data is old or running out.
	Channels int    `json:"channels"`
	Until    *int64 `json:"until,omitempty"`
	Warning  string `json:"warning,omitempty"`
}

const (
	epgRefresh = 12 * time.Hour
	// guideMaxTime is how long one source may take, from asking to the server having read it.
	guideMaxTime = 10 * time.Minute
	// guideIdle gives up on a source that stops sending.
	guideIdle = time.Minute
)

// Sources lists guide sources.
func (m *Manager) Sources(ctx context.Context) ([]EPGSource, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT id, name, url, last_fetch, last_error, programmes, tuner_id FROM epg_sources ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EPGSource{}
	for rows.Next() {
		var s EPGSource
		var last, tuner sql.NullInt64
		if err := rows.Scan(&s.ID, &s.Name, &s.URL, &last, &s.LastError, &s.Programmes, &tuner); err != nil {
			return nil, err
		}
		if last.Valid {
			s.LastFetch = &last.Int64
		}
		if tuner.Valid {
			s.TunerID = &tuner.Int64
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func checkEPGPlace(place string) error {
	if strings.Contains(place, "://") {
		u, err := url.Parse(place)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("guide data comes from an http(s) address or a file on the server")
		}
		return nil
	}
	if st, err := os.Stat(place); err != nil || !st.Mode().IsRegular() { // a pipe would hold the refresh for ever
		return errors.New("that guide file was not found on the server")
	}
	return nil
}

// webGuide: a guide address a tuner or a playlist may name, as opposed to what the owner types.
func webGuide(place string) bool {
	u, err := url.Parse(place)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// AddSource adds a guide source and fetches it.
func (m *Manager) AddSource(ctx context.Context, name, place string) (*EPGSource, error) {
	place = strings.TrimSpace(place)
	name = strings.TrimSpace(name)
	if err := checkEPGPlace(place); err != nil {
		return nil, err
	}
	if name == "" {
		name = place
	}
	if len(name) > 200 || len(place) > 2000 {
		return nil, errors.New("that is too long")
	}
	res, err := m.DB.ExecContext(ctx, `INSERT INTO epg_sources(name, url) VALUES(?, ?)`, name, place)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("that guide source is already there")
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	m.Log.InfoContext(ctx, "guide source added", "source", id, "name", name, "url", place)
	m.Start()
	m.background("guide refresh", func(ctx context.Context) {
		if err := m.FetchSource(ctx, id); err != nil && ctx.Err() == nil {
			m.Log.Warn("guide refresh failed", "name", name, "err", err)
		}
	})
	return &EPGSource{ID: id, Name: name, URL: place}, nil
}

// DeleteSource removes a source.
func (m *Manager) DeleteSource(ctx context.Context, id int64) error {
	res, err := m.DB.ExecContext(ctx, `DELETE FROM epg_sources WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if m.Host != nil {
		// What the server kept from it goes too.
		if err := m.Host.Do(ctx, host.On{}, "DELETE", "/tv/guide/"+strconv.FormatInt(id, 10), nil, nil); err != nil {
			m.Log.WarnContext(ctx, "the server kept a removed guide source's channels", "source", id, "err", err)
		}
	}
	m.Log.InfoContext(ctx, "guide source removed", "source", id)
	return nil
}

func (m *Manager) epgLoop(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		m.refreshDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// refreshDue fetches sources not fetched for a while.
func (m *Manager) refreshDue(ctx context.Context) {
	list, err := m.Sources(ctx)
	if err != nil {
		return
	}
	for _, s := range list {
		if s.LastFetch != nil && time.Since(time.Unix(*s.LastFetch, 0)) < epgRefresh {
			continue
		}
		if err := m.FetchSource(ctx, s.ID); err != nil {
			if ctx.Err() == nil {
				m.Log.Warn("guide refresh failed", "name", s.Name, "err", err)
			}
		}
	}
}

func (m *Manager) openGuide(ctx context.Context, place, auth string, client *http.Client) (io.ReadCloser, error) {
	if !strings.Contains(place, "://") {
		return os.Open(place)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", place, nil)
	if err != nil {
		return nil, err
	}
	if auth != "" {
		var h http.Header
		if json.Unmarshal([]byte(auth), &h) == nil {
			for k, v := range h {
				req.Header[k] = v
			}
		}
	}
	req.Header.Set("User-Agent", "WittyMediaServer")
	res, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, ue.Err // the address, which can hold a login, stays out of what's said
		}
		return nil, err
	}
	if res.StatusCode != 200 {
		res.Body.Close()
		return nil, fmt.Errorf("the guide source answered %s", res.Status)
	}
	return res.Body, nil
}

// FetchSource reads one source now, storing only programmes of channels on
// this server.
func (m *Manager) FetchSource(ctx context.Context, id int64) (err error) {
	task := m.task("Updating the TV guide")
	defer func() { task(1, 1, err) }()
	var place, auth string
	var tunerID sql.NullInt64
	if err := m.DB.QueryRowContext(ctx, `SELECT url, auth, tuner_id FROM epg_sources WHERE id = ?`, id).Scan(&place, &auth, &tunerID); err != nil {
		return ErrNotFound
	}
	client := guideClient(true)
	err = checkEPGPlace(place)
	if tunerID.Valid {
		// A guide a tuner or its playlist named, rather than the owner: from the internet, it
		// stays on the internet.
		client = guideClient(false)
		if t, terr := m.Tuner(ctx, tunerID.Int64); terr == nil && mayLeadHome(t) {
			client = guideClient(true)
		}
		if !webGuide(place) {
			err = errors.New("a playlist's guide has to be a web address")
		}
	}
	if err != nil {
		_, _ = m.DB.ExecContext(ctx, `UPDATE epg_sources SET last_error = ?, last_fetch = ? WHERE id = ?`, PublicError(err), now(), id)
		return err
	}
	began := time.Now()
	n, err := m.fetch(ctx, id, place, auth, client)
	msg := ""
	if err != nil {
		msg = PublicError(err)
	} else {
		m.Log.InfoContext(ctx, "guide fetched", "source", id, "programmes", n, "took", time.Since(began).Round(time.Millisecond).String())
		// What the guide now has may match channels that had none.
		if _, merr := m.AutoMatch(ctx); merr != nil {
			m.Log.DebugContext(ctx, "channels could not be matched to the guide", "err", merr)
		}
	}
	_, _ = m.DB.ExecContext(ctx, `UPDATE epg_sources SET last_error = ?, last_fetch = ?, programmes = ? WHERE id = ?`, msg, now(), n, id)
	return err
}

// guideClient reaches a guide source: anywhere but a cloud host's own addresses for one at
// home, and only the internet for one from the internet.
func guideClient(home bool) *http.Client {
	c := tuner.HelperEnv(home).Stream
	if c == nil {
		c = &http.Client{}
	}
	return c
}

// idleReader gives up on a source that stops sending.
type idleReader struct {
	r     io.Reader
	timer *time.Timer
	idle  time.Duration
}

func (i *idleReader) Read(p []byte) (int, error) {
	i.timer.Reset(i.idle)
	return i.r.Read(p)
}

// guideAnswer is what the server says it took from a guide.
type guideAnswer struct {
	Programmes int `json:"programmes"`
	// Matched: channels with no guide id of their own that the server matched by name, to the
	// guide's id for them.
	Matched map[string]string `json:"matched,omitempty"`
}

// fetch reads one source and hands it to the server as it arrives, which keeps the programmes of
// the channels it has. Channels the server matched by name take that guide id, unless the owner
// chose one.
func (m *Manager) fetch(ctx context.Context, id int64, place, auth string, client *http.Client) (int, error) {
	if m.Host == nil {
		return 0, errors.New("the server isn't there to keep the guide")
	}
	ctx, cancel := context.WithTimeout(ctx, guideMaxTime)
	defer cancel()
	r, err := m.openGuide(ctx, place, auth, client)
	if err != nil {
		return 0, err
	}
	defer r.Close()
	stalled := time.AfterFunc(guideIdle, cancel)
	defer stalled.Stop()
	ans, err := m.sendGuide(ctx, id, &idleReader{r: r, timer: stalled, idle: guideIdle})
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return 0, fmt.Errorf("the guide could not be read: %w", err)
	}
	for cid, key := range ans.Matched {
		n, err := strconv.ParseInt(cid, 10, 64)
		if err != nil {
			continue
		}
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET epg_id = ?, epg_how = 'name', epg_score = 90 WHERE id = ? AND epg_id = '' AND epg_set = ''`, key, n); err != nil {
			return ans.Programmes, err
		}
	}
	return ans.Programmes, nil
}

// sendGuide posts a guide to the host API. It is made by hand rather than with host.Client,
// whose calls are held to two minutes: a big guide takes longer to read.
func (m *Manager) sendGuide(ctx context.Context, id int64, body io.Reader) (guideAnswer, error) {
	var ans guideAnswer
	p := hostParams()
	if p.Host == "" {
		return ans, host.ErrNoHost
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.Host+host.Base+"/tv/guide/"+strconv.FormatInt(id, 10), body)
	if err != nil {
		return ans, err
	}
	req.Header.Set(host.HeaderToken, p.HostToken)
	req.Header.Set("Content-Type", "application/xml")
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return ans, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return ans, errors.New(e.Error)
	}
	return ans, json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&ans)
}

// hostParams is where the host API is and its secret, as the server said at the start.
var hostParams = service.Host

// GuideIDs lists the guide's channel ids the server has, for mapping channels by hand.
func (m *Manager) GuideIDs(ctx context.Context) ([]map[string]string, error) {
	out := []map[string]string{}
	if m.Host == nil {
		return out, nil
	}
	err := m.Host.Do(ctx, host.On{}, "GET", "/tv/guide/ids", nil, &out)
	return out, err
}

var _ = sql.ErrNoRows
