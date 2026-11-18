// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"testing"
	"time"

	tuner "github.com/wittywilla/wittytuner"
)

// A playlist's addresses carry a login token that changes: a refresh must keep each channel's row
// (and so its favourites, recordings and choices), and a channel the playlist drops for a while
// must come back as it was.
func TestRefreshKeepsChannels(t *testing.T) {
	h := newHarness(t)
	ctx := t.Context()
	res, err := h.m.DB.ExecContext(ctx, `INSERT INTO tuners(driver, name, options, created_at) VALUES('iptv', 'P', '{}', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := res.LastInsertId()
	list := func(token string, names ...string) []tuner.Channel {
		var out []tuner.Channel
		for i, n := range names {
			out = append(out, tuner.Channel{ID: n + token, Number: string(rune('1' + i)), Name: n, Group: "News", Locator: "http://p.test/" + n + "?t=" + token,
				UserAgent: "UA/1", Alternates: []string{"http://b.test/" + n}})
		}
		return out
	}
	if _, err := h.m.saveChannels(ctx, tid, list("a", "One", "Two", "Three")); err != nil {
		t.Fatal(err)
	}
	before, _ := h.m.Channels(ctx, tid)
	ids := map[string]int64{}
	for _, c := range before {
		ids[c.Name] = c.ID
	}
	two := ids["Two"]
	if _, err := h.m.EditChannel(ctx, two, ChannelEdit{Name: ptr("Two (mine)")}); err != nil {
		t.Fatal(err)
	}
	// New tokens, and Three gone for now.
	added, err := h.m.saveChannels(ctx, tid, list("b", "One", "Two"))
	if err != nil || added != 0 {
		t.Fatalf("added %d: %v", added, err)
	}
	after, _ := h.m.Channels(ctx, tid)
	for _, c := range after {
		if c.SourceID == "Oneb" && c.ID != ids["One"] || c.SourceID == "Twob" && (c.ID != two || c.Name != "Two (mine)") {
			t.Errorf("channel %s lost its place: %+v", c.SourceID, c)
		}
		if c.extra.UserAgent != "UA/1" || len(c.extra.Alternates) != 1 {
			t.Errorf("%s lost what the playlist says about it: %+v", c.Name, c.extra)
		}
		if c.Name == "Three" && c.MissingSince == 0 {
			t.Error("Three isn't marked missing")
		}
	}
	guide, _ := h.m.GuideChannels(ctx)
	for _, c := range guide {
		if c.Name == "Three" {
			t.Error("a missing channel is in the guide")
		}
	}
	// It comes back, with a new token, and is the same channel again.
	if _, err := h.m.saveChannels(ctx, tid, list("c", "One", "Two", "Three")); err != nil {
		t.Fatal(err)
	}
	back, _ := h.m.Channel(ctx, ids["Three"])
	if back == nil || back.MissingSince != 0 {
		t.Fatalf("Three didn't come back as itself: %+v", back)
	}
	// Gone for over a week: then it goes.
	if _, err := h.m.saveChannels(ctx, tid, list("d", "One", "Two")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.DB.ExecContext(ctx, `UPDATE tuner_channels SET missing_since = ? WHERE id = ?`, time.Now().Add(-8*24*time.Hour).Unix(), ids["Three"]); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.saveChannels(ctx, tid, list("e", "One", "Two")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Channel(ctx, ids["Three"]); err != ErrNotFound {
		t.Errorf("a channel missing for a week stays: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
