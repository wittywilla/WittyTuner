// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wittywilla/wittytuner/match"
)

// Matching channels to the guide. The server keeps the guide's channels (each with its names, icon
// and how far ahead it has programmes); WittyTuner matches its channels to them. After every guide
// fetch and channel refresh, channels the owner hasn't matched by hand are matched by themselves when
// one answer is sure (match.Pick). The rest are listed for review, each with its likeliest answers.
// A match made by hand is remembered by the channel's name, so a channel added again, or a playlist
// added back, keeps it.

// guideEntry is one guide channel as the server lists it.
type guideEntry struct {
	match.Entry
	Source int64
	Until  int64
}

// guideIndex reads the guide's channels from the server.
func (m *Manager) guideIndex(ctx context.Context) (*match.Index, []guideEntry, error) {
	raw, err := m.GuideIDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	list := make([]guideEntry, 0, len(raw))
	entries := make([]match.Entry, 0, len(raw))
	for _, r := range raw {
		e := guideEntry{Entry: match.Entry{ID: r["id"], Name: r["name"]}}
		if a := r["aliases"]; a != "" {
			e.Aliases = strings.Split(a, "\t")
		}
		e.Source, _ = strconv.ParseInt(r["source"], 10, 64)
		e.Until, _ = strconv.ParseInt(r["until"], 10, 64)
		list = append(list, e)
		entries = append(entries, e.Entry)
	}
	return match.New(entries), list, nil
}

// matchRow is a channel as matching sees it.
type matchRow struct {
	id                                   int64
	name, sourceName, number, callsign   string
	epgID, epgSet, epgHow, epgSrc, tuner string
	score                                int
	group                                string
}

func (m *Manager) matchRows(ctx context.Context) ([]matchRow, error) {
	rows, err := m.DB.QueryContext(ctx, `SELECT c.id, COALESCE(NULLIF(c.name_set, ''), c.name), c.name, COALESCE(NULLIF(c.number_set, ''), c.number), c.callsign,
		c.epg_id, c.epg_set, c.epg_how, c.epg_src, c.epg_score, c.grp, t.name
		FROM tuner_channels c JOIN tuners t ON t.id = c.tuner_id
		WHERE c.enabled = 1 AND t.enabled = 1 AND c.merged_into IS NULL AND c.missing_since = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []matchRow
	for rows.Next() {
		var r matchRow
		if err := rows.Scan(&r.id, &r.name, &r.sourceName, &r.number, &r.callsign, &r.epgID, &r.epgSet, &r.epgHow, &r.epgSrc, &r.score, &r.group, &r.tuner); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (r matchRow) channel() match.Channel {
	return match.Channel{Name: r.name, Number: r.number, CallSign: r.callsign, GuideID: r.epgSrc}
}

// memoryKey is how a hand-made match is remembered: by the channel's name as its source gives it.
func memoryKey(name string) string { return match.Key(name) }

// AutoMatch matches every channel that has no hand-made match and no guide entry: from a match
// remembered for its name, else from the guide when one answer is sure. It says how many changed.
func (m *Manager) AutoMatch(ctx context.Context) (int, error) {
	x, _, err := m.guideIndex(ctx)
	if err != nil || x.Len() == 0 {
		return 0, err
	}
	list, err := m.matchRows(ctx)
	if err != nil {
		return 0, err
	}
	memory := map[string]string{}
	if rows, err := m.DB.QueryContext(ctx, `SELECT name_key, epg_id FROM epg_memory`); err == nil {
		for rows.Next() {
			var k, id string
			if rows.Scan(&k, &id) == nil {
				memory[k] = id
			}
		}
		rows.Close()
	}
	changed := 0
	for _, r := range list {
		if r.epgSet != "" {
			continue
		}
		if id := memory[memoryKey(r.sourceName)]; id != "" && x.Has(id) {
			if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET epg_set = ? WHERE id = ?`, id, r.id); err != nil {
				return changed, err
			}
			changed++
			continue
		}
		if r.epgID != "" && x.Has(r.epgID) {
			if r.epgHow == "" {
				_, _ = m.DB.ExecContext(ctx, `UPDATE tuner_channels SET epg_how = 'source', epg_score = 100 WHERE id = ?`, r.id)
			}
			continue
		}
		best, ok := match.Pick(x.Best(r.channel(), 3))
		if !ok || best.ID == r.epgID {
			continue
		}
		if _, err := m.DB.ExecContext(ctx, `UPDATE tuner_channels SET epg_id = ?, epg_score = ?, epg_how = ? WHERE id = ? AND epg_set = ''`, best.ID, best.Score, best.How, r.id); err != nil {
			return changed, err
		}
		changed++
	}
	if changed > 0 {
		m.tunerLog().InfoContext(ctx, "channels matched to the guide", "topic", "tuner", "channels", changed)
	}
	return changed, nil
}

// remember keeps (or, for "", forgets) a match made by hand, under the channel's name.
func (m *Manager) remember(ctx context.Context, channelID int64, epg string) {
	var name string
	if err := m.DB.QueryRowContext(ctx, `SELECT name FROM tuner_channels WHERE id = ?`, channelID).Scan(&name); err != nil {
		return
	}
	k := memoryKey(name)
	if k == "" {
		return
	}
	if epg == "" {
		_, _ = m.DB.ExecContext(ctx, `DELETE FROM epg_memory WHERE name_key = ?`, k)
		return
	}
	_, _ = m.DB.ExecContext(ctx, `INSERT INTO epg_memory(name_key, epg_id, at) VALUES(?,?,?) ON CONFLICT(name_key) DO UPDATE SET epg_id = excluded.epg_id, at = excluded.at`, k, epg, now())
}

// ReviewChannel is a channel on the review screen: what it's matched to now, how sure that is, and
// the likeliest guide entries for it.
type ReviewChannel struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Number      string         `json:"number"`
	Group       string         `json:"group,omitempty"`
	Tuner       string         `json:"tuner"`
	GuideID     string         `json:"guideId,omitempty"`
	GuideName   string         `json:"guideName,omitempty"`
	Score       int            `json:"score"`
	How         string         `json:"how,omitempty"`
	State       string         `json:"state"` // matched | unsure | none
	Suggestions []match.Result `json:"suggestions,omitempty"`
}

// Review is the review screen's data.
type Review struct {
	Channels      []ReviewChannel `json:"channels"`
	Total         int             `json:"total"`
	Matched       int             `json:"matched"`
	Unsure        int             `json:"unsure"`
	None          int             `json:"none"`
	GuideChannels int             `json:"guideChannels"`
}

// Review lists channels with their matches: every one when all is set, else only those with no
// guide or an unsure match.
func (m *Manager) Review(ctx context.Context, all bool, limit int) (*Review, error) {
	x, _, err := m.guideIndex(ctx)
	if err != nil {
		return nil, err
	}
	list, err := m.matchRows(ctx)
	if err != nil {
		return nil, err
	}
	out := &Review{Channels: []ReviewChannel{}, GuideChannels: x.Len(), Total: len(list)}
	for _, r := range list {
		key := firstNonEmpty(r.epgSet, r.epgID)
		c := ReviewChannel{ID: r.id, Name: r.name, Number: r.number, Group: r.group, Tuner: r.tuner, GuideID: key, How: r.epgHow, Score: r.score}
		if r.epgSet != "" {
			c.How, c.Score = "manual", 100
		} else if key != "" && (c.How == "" || c.How == "source") {
			c.How, c.Score = "source", 100
		}
		e, inGuide := x.Entry(key)
		switch {
		case key == "" || !inGuide:
			c.State, c.Score = "none", 0
			out.None++
		case c.Score < match.Sure:
			c.State = "unsure"
			out.Unsure++
		default:
			c.State = "matched"
			out.Matched++
		}
		c.GuideName = e.Name
		if !all && c.State == "matched" {
			continue
		}
		if len(out.Channels) >= limit {
			continue
		}
		if c.State != "matched" {
			c.Suggestions = x.Best(r.channel(), 3)
		}
		out.Channels = append(out.Channels, c)
	}
	return out, nil
}

// staleAfter is when a guide source's data is called old: it's fetched twice a day.
const staleAfter = 2 * 24 * time.Hour

// guideFreshness adds to each source how far ahead it has programmes, and a warning when its data
// is old or about to run out.
func (m *Manager) guideFreshness(ctx context.Context, list []EPGSource) {
	_, entries, err := m.guideIndex(ctx)
	if err != nil {
		return
	}
	until := map[int64]int64{}
	count := map[int64]int{}
	for _, e := range entries {
		count[e.Source]++
		until[e.Source] = max(until[e.Source], e.Until)
	}
	t := time.Now()
	for i := range list {
		s := &list[i]
		s.Channels = count[s.ID]
		if u := until[s.ID]; u > 0 {
			s.Until = &u
		}
		switch {
		case s.LastFetch != nil && t.Sub(time.Unix(*s.LastFetch, 0)) > staleAfter && s.LastError != "":
			s.Warning = "This guide hasn't been read for over two days, and the last try failed."
		case s.LastFetch != nil && s.Until != nil && time.Unix(*s.Until, 0).Before(t.Add(12*time.Hour)):
			s.Warning = "This guide's programmes run out within 12 hours. It may have stopped being updated."
		}
	}
}

// --------------------------------------------------------------------------
// Routes
// --------------------------------------------------------------------------

// GET /epg/review?all=1: channels and their guide matches, for the review screen.
func handleReview(m *Manager, w http.ResponseWriter, r *http.Request) {
	rv, err := m.Review(r.Context(), r.URL.Query().Get("all") == "1", 5000)
	if err != nil {
		writeErr(w, 502, PublicError(err))
		return
	}
	writeJSON(w, 200, rv)
}

// POST /epg/automatch: match what can be matched now.
func handleAutoMatch(m *Manager, w http.ResponseWriter, r *http.Request) {
	n, err := m.AutoMatch(r.Context())
	if err != nil {
		writeErr(w, 502, PublicError(err))
		return
	}
	writeJSON(w, 200, map[string]int{"matched": n})
}

// GET /epg/search?q=: guide channels whose names hold every word.
func handleGuideSearch(m *Manager, w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 100 {
		writeErr(w, 400, "that search is too long")
		return
	}
	x, _, err := m.guideIndex(r.Context())
	if err != nil {
		writeErr(w, 502, PublicError(err))
		return
	}
	writeJSON(w, 200, x.Search(q, 30))
}
