// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package match

import (
	"fmt"
	"testing"
	"time"
)

var guide = []Entry{
	{ID: "bbcone.uk", Name: "BBC One", Aliases: []string{"1", "BBC1"}},
	{ID: "bbctwo.uk", Name: "BBC Two", Aliases: []string{"2"}},
	{ID: "channel4.uk", Name: "Channel 4", Aliases: []string{"4"}},
	{ID: "channel5.uk", Name: "Channel 5", Aliases: []string{"5"}},
	{ID: "skysportsmainevent.uk", Name: "Sky Sports Main Event"},
	{ID: "wxyz.us", Name: "WXYZ-DT", Aliases: []string{"7.1 WXYZ", "WXYZ"}},
	{ID: "demo.news", Name: "Demo News"},
	{ID: "perviy.ru", Name: "Первый канал"},
}

func TestBest(t *testing.T) {
	x := New(guide)
	for _, tc := range []struct {
		ch      Channel
		want    string
		how     string
		minimum int
	}{
		{Channel{Name: "Anything", GuideID: "bbcone.uk"}, "bbcone.uk", "id", 100},
		{Channel{Name: "Anything", GuideID: "BBCOne.uk@HD"}, "bbcone.uk", "id", 95},
		{Channel{Name: "UK: BBC One HD"}, "bbcone.uk", "name", 90},
		{Channel{Name: "[UK] BBC Two FHD"}, "bbctwo.uk", "name", 90},
		{Channel{Name: "Sky Sport Main Event"}, "skysportsmainevent.uk", "similar", Sure},
		{Channel{Name: "ABC 7", CallSign: "WXYZ"}, "wxyz.us", "callsign", 85},
		{Channel{Name: "Первый канал HD"}, "perviy.ru", "name", 90},
	} {
		got := x.Best(tc.ch, 3)
		if len(got) == 0 || got[0].ID != tc.want || got[0].Score < tc.minimum || got[0].How != tc.how {
			t.Errorf("%+v: got %+v, want %s by %s at least %d", tc.ch, got, tc.want, tc.how, tc.minimum)
		}
	}
	// Numbers must agree: Channel 4 isn't a sure match for Channel 5.
	got := x.Best(Channel{Name: "Channel 5 HD"}, 3)
	if r, ok := Pick(got); !ok || r.ID != "channel5.uk" {
		t.Errorf("Channel 5: %+v", got)
	}
	for _, r := range got {
		if r.ID == "channel4.uk" && r.Score >= Sure {
			t.Errorf("Channel 4 scored %d for Channel 5", r.Score)
		}
	}
	// Nothing alike: no sure match.
	if r, ok := Pick(x.Best(Channel{Name: "Completely Different"}, 3)); ok {
		t.Errorf("matched %+v", r)
	}
}

func TestPickNeedsAClearWinner(t *testing.T) {
	if _, ok := Pick([]Result{{ID: "a", Score: 90}, {ID: "b", Score: 88}}); ok {
		t.Error("two equal answers were picked between")
	}
	if r, ok := Pick([]Result{{ID: "a", Score: 90}, {ID: "b", Score: 60}}); !ok || r.ID != "a" {
		t.Error("a clear answer wasn't picked")
	}
}

func TestSearch(t *testing.T) {
	x := New(guide)
	got := x.Search("sky main", 5)
	if len(got) != 1 || got[0].ID != "skysportsmainevent.uk" {
		t.Errorf("search: %+v", got)
	}
	if got := x.Search("bbc1", 5); len(got) != 1 || got[0].ID != "bbcone.uk" {
		t.Errorf("alias search: %+v", got)
	}
}

// A big guide and a big playlist must match in well under a second's work per thousand.
func TestScale(t *testing.T) {
	var big []Entry
	for i := range 40000 {
		big = append(big, Entry{ID: fmt.Sprintf("ch%d.test", i), Name: fmt.Sprintf("Channel Network %d %s", i, []string{"News", "Sport", "Movies", "Kids"}[i%4])})
	}
	x := New(big)
	start := time.Now()
	for i := range 3000 {
		x.Best(Channel{Name: fmt.Sprintf("US: Channel Network %d %s HD", i*7, []string{"News", "Sport", "Movies", "Kids"}[(i*7)%4])}, 3)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Errorf("3000 channels against 40000 took %s", d)
	}
}
