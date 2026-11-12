// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A fixed clock: 12:00 UTC, asking for 10:00 to 11:00.
var (
	catchupNow   = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	catchupStart = catchupNow.Add(-2 * time.Hour)
	catchupEnd   = catchupNow.Add(-time.Hour)
)

func archiveOf(ch Channel) Channel {
	ch.ArchiveStart, ch.ArchiveEnd = catchupStart.Unix(), catchupEnd.Unix()
	if ch.CatchupDays == 0 {
		ch.CatchupDays = 3
	}
	return ch
}

func TestArchiveURLForEveryMode(t *testing.T) {
	s, e, n := catchupStart.Unix(), catchupEnd.Unix(), catchupNow.Unix()
	for _, tc := range []struct {
		name string
		ch   Channel
		want string
	}{
		{"default, brace placeholders",
			Channel{Catchup: "default", Locator: "http://live.test/1.ts", CatchupSource: "http://archive.test/{utc}/{utcend}/{lutc}/{duration}/{offset}/{Y}{m}{d}{H}{M}{S}.ts"},
			fmt.Sprintf("http://archive.test/%d/%d/%d/3600/7200/20261111100000.ts", s, e, n)},
		{"default, dollar placeholders",
			Channel{Catchup: "default", Locator: "http://live.test/1.ts", CatchupSource: "http://archive.test/a.m3u8?s=${start}&e=${end}&n=${now}&t=${timestamp}&d=${duration}&o=${offset}&b=${(b)yyyy-MM-dd:HH-mm-ss}&x=${(e)yyyyMMddHHmmss}"},
			fmt.Sprintf("http://archive.test/a.m3u8?s=%d&e=%d&n=%d&t=%d&d=3600&o=7200&b=2026-11-11:10-00-00&x=20261111110000", s, e, n, n)},
		{"default, formats and units",
			Channel{Catchup: "default", Locator: "http://live.test/1.ts", CatchupSource: "http://archive.test/{utc:Y-m-d_H-M-S}/{utcend:YmdHM}/{lutc:H}/{duration:60}/{offset:3600}.ts"},
			"http://archive.test/2026-11-11_10-00-00/202611111100/12/60/2.ts"},
		{"default, relative to the live address",
			Channel{Catchup: "default", Locator: "http://live.test/ch/1.m3u8?token=a", CatchupSource: "/archive/1.m3u8?from={utc}"},
			fmt.Sprintf("http://live.test/archive/1.m3u8?from=%d", s)},
		{"append to an address with a query",
			Channel{Catchup: "append", Locator: "http://live.test/ch.m3u8?token=a", CatchupSource: "?utc={utc}&lutc={lutc}"},
			fmt.Sprintf("http://live.test/ch.m3u8?token=a&utc=%d&lutc=%d", s, n)},
		{"append to one without",
			Channel{Catchup: "append", Locator: "http://live.test/ch.m3u8", CatchupSource: "?utc={utc}"},
			fmt.Sprintf("http://live.test/ch.m3u8?utc=%d", s)},
		{"shift",
			Channel{Catchup: "shift", Locator: "http://live.test/ch.ts"},
			fmt.Sprintf("http://live.test/ch.ts?utc=%d&lutc=%d", s, n)},
		{"shift after a query",
			Channel{Catchup: "shift", Locator: "http://live.test/ch.ts?token=a"},
			fmt.Sprintf("http://live.test/ch.ts?token=a&utc=%d&lutc=%d", s, n)},
		{"flussonic index",
			Channel{Catchup: "flussonic", Locator: "http://fs.test/news/index.m3u8?token=a"},
			fmt.Sprintf("http://fs.test/news/archive-%d-3600.m3u8?token=a", s)},
		{"flussonic named list",
			Channel{Catchup: "flussonic", Locator: "https://fs.test/news/video.m3u8"},
			fmt.Sprintf("https://fs.test/news/video-%d-3600.m3u8", s)},
		{"flussonic mpegts",
			Channel{Catchup: "flussonic", Locator: "http://fs.test/news/mpegts?token=a"},
			fmt.Sprintf("http://fs.test/news/timeshift_abs-%d.ts?token=a", s)},
		{"flussonic with its own template",
			Channel{Catchup: "flussonic", Locator: "http://fs.test/news/index.m3u8", CatchupSource: "http://fs.test/news/index-{utc}-{duration}.m3u8"},
			fmt.Sprintf("http://fs.test/news/index-%d-3600.m3u8", s)},
		{"xc from /live/",
			Channel{Catchup: "xc", Locator: "http://xc.test:8080/live/user/pass/42.ts"},
			"http://xc.test:8080/timeshift/user/pass/60/2026-11-11:10-00/42.ts"},
		{"xc from a short address",
			Channel{Catchup: "xc", Locator: "http://xc.test/user/pass/42"},
			"http://xc.test/timeshift/user/pass/60/2026-11-11:10-00/42.ts"},
		{"xc stays HLS",
			Channel{Catchup: "xc", Locator: "http://xc.test/live/user/pass/42.m3u8"},
			"http://xc.test/timeshift/user/pass/60/2026-11-11:10-00/42.m3u8"},
		{"xc keeps an escaped login",
			Channel{Catchup: "xc", Locator: "http://xc.test/live/a%20b/p%2Fq/42.ts"},
			"http://xc.test/timeshift/a%20b/p%2Fq/60/2026-11-11:10-00/42.ts"},
	} {
		got, err := ArchiveURL(archiveOf(tc.ch), catchupNow)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
		} else if got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

// Minutes are rounded up, so the end of a programme isn't cut off.
func TestXCArchiveRoundsUp(t *testing.T) {
	ch := archiveOf(Channel{Catchup: "xc", Locator: "http://xc.test/live/u/p/1.ts"})
	ch.ArchiveEnd = ch.ArchiveStart + 3601
	got, err := ArchiveURL(ch, catchupNow)
	if err != nil || !strings.Contains(got, "/61/") {
		t.Fatalf("%v %s", err, got)
	}
}

func TestArchiveURLRefusals(t *testing.T) {
	day := int64(24 * 3600)
	n := catchupNow.Unix()
	shift := Channel{Catchup: "shift", CatchupDays: 2, Locator: "http://live.test/ch.ts"}
	with := func(start, end int64, ch Channel) Channel {
		ch.ArchiveStart, ch.ArchiveEnd = start, end
		return ch
	}
	for _, tc := range []struct {
		name string
		ch   Channel
		want string // part of the error; "" means it's allowed
	}{
		{"inside the window", with(n-2*day+60, 0, shift), ""},
		{"an hour ago, up to now", with(n-3600, 0, shift), ""},
		{"a programme still on", with(n-600, n+600, shift), ""},
		{"older than the source keeps", with(n-2*day-60, 0, shift), "keeps the last 2 days"},
		{"not shown yet", with(n+60, n+120, shift), "hasn't been shown yet"},
		{"ends before it starts", with(n-600, n-1200, shift), "ends before it starts"},
		{"longer than a day", with(n-day+60, n+day, shift), "longer than a day"},
		{"no catch-up", with(n-600, 0, Channel{Locator: "http://live.test/ch.ts"}), "no catch-up"},
		{"days but no way", with(n-600, 0, Channel{CatchupDays: 2, Locator: "http://live.test/ch.ts"}), "no catch-up"},
		{"a way but no days", with(n-600, 0, Channel{Catchup: "shift", Locator: "http://live.test/ch.ts"}), "no catch-up"},
		{"one day, said so", with(n-day-60, 0, Channel{Catchup: "shift", CatchupDays: 1, Locator: "http://live.test/ch.ts"}), "the last 1 day"},
		{"an unknown kind", with(n-600, 0, Channel{Catchup: "vod", CatchupDays: 2, Locator: "http://live.test/ch.ts"}), "isn't a kind"},
		{"default without a template", with(n-600, 0, Channel{Catchup: "default", CatchupDays: 2, Locator: "http://live.test/ch.ts"}), "no template"},
		{"an unknown placeholder", with(n-600, 0, Channel{Catchup: "default", CatchupDays: 2, CatchupSource: "http://a.test/{catchup-id}"}), "{catchup-id}"},
		{"a bad unit", with(n-600, 0, Channel{Catchup: "default", CatchupDays: 2, CatchupSource: "http://a.test/{duration:0}"}), "{duration:0}"},
		{"flussonic that isn't", with(n-600, 0, Channel{Catchup: "flussonic", CatchupDays: 2, Locator: "http://fs.test/stream"}), "Flussonic"},
		{"xc that isn't", with(n-600, 0, Channel{Catchup: "xc", CatchupDays: 2, Locator: "http://xc.test/a/b/c/d/e.ts"}), "Xtream"},
	} {
		_, err := ArchiveURL(tc.ch, catchupNow)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestParseM3UCatchup(t *testing.T) {
	const list = `#EXTM3U catchup="shift" catchup-days="3"
#EXTINF:-1 tvg-id="a",Inherits
http://live.test/a.ts
#EXTINF:-1 catchup="default" catchup-days="7" catchup-source="http://archive.test/b?s={utc}",Own template
http://live.test/b.ts
#EXTINF:-1 catchup-type="flussonic",Type and the header's days
http://fs.test/c/index.m3u8
#EXTINF:-1 catchup="append",Append without a template
http://live.test/d.ts
#EXTINF:-1 catchup="vod" catchup-days="2",A kind nothing plays
http://live.test/e.ts
#EXTINF:-1 catchup="xc" catchup-days="9999",Too many days
http://xc.test/live/u/p/1.ts
`
	const old = `#EXTM3U
#EXTINF:-1 timeshift="5",Old shift
http://live.test/f.ts
#EXTINF:-1 timeshift="4" catchup-source="http://archive.test/g/{utc}",Old with a template
http://live.test/g.ts
#EXTINF:-1,None
http://live.test/h.ts
#EXTINF:-1 catchup="shift",A way but no days
http://live.test/i.ts
`
	type want struct {
		mode   string
		days   int
		source string
	}
	for _, tc := range []struct {
		list string
		want []want
	}{
		{list, []want{{"shift", 3, ""}, {"default", 7, "http://archive.test/b?s={utc}"}, {"flussonic", 3, ""}, {"", 0, ""}, {"", 0, ""}, {"xc", maxCatchupDays, ""}}},
		{old, []want{{"shift", 5, ""}, {"default", 4, "http://archive.test/g/{utc}"}, {"", 0, ""}, {"", 0, ""}}},
	} {
		m, err := ParseM3U(strings.NewReader(tc.list))
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Entries) != len(tc.want) {
			t.Fatalf("%d entries", len(m.Entries))
		}
		for i, w := range tc.want {
			e := m.Entries[i]
			if got := (want{e.Catchup, e.CatchupDays, e.CatchupSource}); got != w {
				t.Errorf("%s: got %+v, want %+v", e.Name, got, w)
			}
		}
	}
}

// What the archive sends is held to every rule a live stream is.
func TestOpenArchive(t *testing.T) {
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("/ch.ts", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.RawQuery)
		_, _ = w.Write(bytes.Repeat(tsPacket(0x100, false), 50))
	})
	mux.HandleFunc("/archive/locked.m3u8", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n#EXTINF:4,\nseg1.ts\n#EXT-X-ENDLIST\n")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "list.m3u")
	if err := os.WriteFile(path, []byte("#EXTM3U\n#EXTINF:-1 catchup=\"shift\" catchup-days=\"1\",Ch\n"+srv.URL+"/ch.ts\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := New("iptv", Config{Options: map[string]string{"playlist": path}}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Channels(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %+v", err, list)
	}
	ch := list[0]
	start := time.Now().Add(-time.Hour).Unix()
	ch.ArchiveStart = start
	st, err := s.Open(context.Background(), ch)
	if err != nil || st.Guard == nil {
		t.Fatalf("archive: %v %+v", err, st)
	}
	st.Close()
	if len(asked) != 1 || !strings.HasPrefix(asked[0], fmt.Sprintf("utc=%d&lutc=", start)) {
		t.Fatalf("asked for %q", asked)
	}

	ch.ArchiveStart = time.Now().Add(-48 * time.Hour).Unix()
	if _, err := s.Open(context.Background(), ch); err == nil || !strings.Contains(err.Error(), "too long ago") {
		t.Fatalf("outside the window: %v", err)
	}

	locked := Channel{Name: "Locked", Locator: srv.URL + "/ch.ts", Catchup: "default", CatchupDays: 1, CatchupSource: srv.URL + "/archive/locked.m3u8", ArchiveStart: start}
	var enc *EncryptedError
	if _, err := s.Open(context.Background(), locked); !errors.As(err, &enc) {
		t.Fatalf("an encrypted archive must be refused: %v", err)
	}
	if len(asked) != 1 {
		t.Fatalf("the live address was asked for instead: %q", asked)
	}
}

// A playlist from elsewhere can't send its archive home, any more than its live streams.
func TestArchiveFromElsewhereCantLeadHome(t *testing.T) {
	withLookup(t, map[string]string{"stream.example": "93.184.216.34", "sneaky.example": "127.0.0.1"})
	s, err := New("iptv", Config{Options: map[string]string{"playlist": "https://iptv.example/list.m3u"}}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour).Unix()
	for _, src := range []string{"http://127.0.0.1:1/archive.ts", "http://sneaky.example/archive.ts", "file:///etc/passwd"} {
		ch := Channel{Name: "Ch", Locator: "http://stream.example/live.ts", Catchup: "default", CatchupDays: 1, CatchupSource: src, ArchiveStart: start}
		if _, err := s.Open(context.Background(), ch); err == nil || strings.Contains(err.Error(), "could not be reached") {
			t.Errorf("%s: %v", src, err)
		}
	}
}
