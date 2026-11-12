// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePanel is a stand-in for an Xtream Codes-style panel, with generated channels.
type fakePanel struct {
	mu         sync.Mutex
	user, pass string
	account    string // the answer to the account question
	categories string
	streams    string
	asked      []string // stream paths asked for
}

func newFakePanel(t *testing.T) (*fakePanel, *httptest.Server) {
	t.Helper()
	p := &fakePanel{
		user: "viewer one", pass: "p/ss?&",
		account:    `{"user_info":{"auth":1,"status":"Active","max_connections":"2"},"server_info":{"timezone":"UTC"}}`,
		categories: `[{"category_id":"1","category_name":"News"},{"category_id":2,"category_name":" Sport "},{"category_id":"3","category_name":null}]`,
		streams: `[
{"num":"5","name":"News One","stream_type":"live","stream_id":"101","stream_icon":"http://logo.test/1.png","epg_channel_id":"news.test","category_id":"1","tv_archive":"1","tv_archive_duration":"3"},
{"num":6,"name":"Sport","stream_type":"live","stream_id":102,"stream_icon":"file:///etc/passwd","epg_channel_id":null,"category_id":2,"tv_archive":1,"tv_archive_duration":7},
{"num":null,"name":"Radio","stream_type":"radio_streams","stream_id":103,"stream_icon":"","category_id":null,"tv_archive":0,"tv_archive_duration":"5"},
{"num":"8","name":"No archive days","stream_type":"live","stream_id":"104","category_id":"9","tv_archive":"1","tv_archive_duration":""},
{"num":"9","name":"Odd fields","stream_type":"live","stream_id":"105","category_id":[1,2],"tv_archive":"yes","tv_archive_duration":{"days":3}},
{"num":"10","name":"Nameless id","stream_type":"live","stream_id":null}
]`,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/player_api.php", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		q := r.URL.Query()
		if q.Get("username") != p.user || q.Get("password") != p.pass {
			fmt.Fprint(w, `{"user_info":{"auth":0}}`)
			return
		}
		switch q.Get("action") {
		case "":
			fmt.Fprint(w, p.account)
		case "get_live_categories":
			fmt.Fprint(w, p.categories)
		case "get_live_streams":
			fmt.Fprint(w, p.streams)
		default:
			w.WriteHeader(400)
		}
	})
	stream := func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.asked = append(p.asked, r.URL.EscapedPath())
		p.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n#EXTINF:4,\nseg1.ts\n")
			return
		}
		_, _ = w.Write(bytes.Repeat(tsPacket(0x100, false), 50))
	}
	mux.HandleFunc("/live/", stream)
	mux.HandleFunc("/timeshift/", stream)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return p, srv
}

func (p *fakePanel) set(f func(*fakePanel)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f(p)
}

func newXtream(t *testing.T, srv *httptest.Server, p *fakePanel, opts map[string]string) Source {
	t.Helper()
	o := map[string]string{"url": srv.URL + "/", "username": p.user, "password": p.pass}
	for k, v := range opts {
		o[k] = v
	}
	s, err := New("xtream", Config{Name: "Provider", Options: o}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestXtreamChannels(t *testing.T) {
	p, srv := newFakePanel(t)
	s := newXtream(t, srv, p, nil)
	list, err := s.Channels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 5 {
		t.Fatalf("%d channels: %+v", len(list), list)
	}
	live := func(id string) string {
		return srv.URL + "/live/viewer%20one/p%2Fss%3F&/" + id + ".ts"
	}
	for i, want := range []Channel{
		{ID: "101", Number: "5", Name: "News One", Group: "News", Logo: "http://logo.test/1.png", EPGID: "news.test", Locator: live("101"), Catchup: "xc", CatchupDays: 3},
		{ID: "102", Number: "6", Name: "Sport", Group: "Sport", Locator: live("102"), Catchup: "xc", CatchupDays: 7},
		{ID: "103", Number: "103", Name: "Radio", Radio: true, Locator: live("103")},
		{ID: "104", Number: "8", Name: "No archive days", Locator: live("104")},
		{ID: "105", Number: "9", Name: "Odd fields", Locator: live("105")},
	} {
		if list[i] != want {
			t.Errorf("channel %d:\n got %+v\nwant %+v", i, list[i], want)
		}
	}
	info := s.Info()
	if info.MaxStreams != 2 || info.Driver != "xtream" {
		t.Errorf("info %+v", info)
	}
	g, err := url.Parse(info.GuideURL)
	if err != nil || g.Path != "/xmltv.php" || g.Query().Get("username") != p.user || g.Query().Get("password") != p.pass || !strings.HasPrefix(info.GuideURL, srv.URL+"/") {
		t.Errorf("guide %s", info.GuideURL)
	}
	if strings.Contains(info.Detail, p.pass) {
		t.Errorf("the detail shows the password: %s", info.Detail)
	}

	hls := newXtream(t, srv, p, map[string]string{"output": "m3u8"})
	list, err = hls.Channels(context.Background())
	if err != nil || !strings.HasSuffix(list[0].Locator, "/101.m3u8") {
		t.Fatalf("%v %+v", err, list)
	}
	// The HLS gate holds: the fake panel's HLS is encrypted.
	var enc *EncryptedError
	if _, err := hls.Open(context.Background(), list[0]); !errors.As(err, &enc) {
		t.Fatalf("encrypted HLS from a panel: %v", err)
	}
}

func TestXtreamAccountProblems(t *testing.T) {
	for _, tc := range []struct {
		name    string
		account string
		status  int
		want    string
	}{
		{"auth 0", `{"user_info":{"auth":0}}`, 200, "refused that username or password"},
		{"auth \"0\"", `{"user_info":{"auth":"0","status":"Active"}}`, 200, "refused that username or password"},
		{"nothing", ``, 200, "refused that username or password"},
		{"an empty list", `[]`, 200, "refused that username or password"},
		{"no user", `{"server_info":{}}`, 200, "refused that username or password"},
		{"forbidden", ``, 403, "refused that username or password"},
		{"expired", `{"user_info":{"auth":1,"status":"Expired"}}`, 200, "has expired"},
		{"banned", `{"user_info":{"auth":"1","status":"Banned"}}`, 200, "is banned"},
		{"disabled", `{"user_info":{"auth":1,"status":"Disabled"}}`, 200, "is disabled"},
		{"not a panel", `<html>hello</html>`, 200, "didn't answer as an Xtream Codes panel"},
		{"broken", ``, 500, "answered 500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.account)
			}))
			defer srv.Close()
			var logged bytes.Buffer
			env := Env{Log: newHelperLog(&logged, "debug", "")}
			s, err := New("xtream", Config{Options: map[string]string{"url": srv.URL, "username": "the-user", "password": "secret-pass"}}, env)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.Channels(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "secret-pass") {
				t.Fatalf("the error shows the password: %v", err)
			}
			// A refusal is said where the server's owner sees it, without the login.
			refused := tc.status != 500 && tc.name != "not a panel"
			if got := logged.String(); refused != strings.Contains(got, "level=WARN msg=\"the IPTV provider refused the account\"") ||
				strings.Contains(got, "secret-pass") || strings.Contains(got, "the-user") || (refused && !strings.Contains(got, "cat=tv")) {
				t.Fatalf("logged: %s", got)
			}
		})
	}
	// A wrong password, as the fake panel answers it.
	p, srv := newFakePanel(t)
	s := newXtream(t, srv, p, map[string]string{"password": "wrong"})
	if _, err := s.Channels(context.Background()); !errors.Is(err, errXtreamRefused) {
		t.Fatalf("wrong password: %v", err)
	}
	p.set(func(p *fakePanel) { p.streams = `{"user_info":{"auth":1}}` })
	if _, err := newXtream(t, srv, p, nil).Channels(context.Background()); err == nil || !strings.Contains(err.Error(), "no channel list") {
		t.Fatalf("an object for a list: %v", err)
	}
	p.set(func(p *fakePanel) { p.streams = `[{"stream_id":1,"name":"a"},{"stream_id":2,` })
	if _, err := newXtream(t, srv, p, nil).Channels(context.Background()); err == nil || !strings.Contains(err.Error(), "cut short") {
		t.Fatalf("a list cut short: %v", err)
	}
	p.set(func(p *fakePanel) { p.streams, p.categories = `[]`, `null` })
	if _, err := newXtream(t, srv, p, nil).Channels(context.Background()); err == nil || !strings.Contains(err.Error(), "no live channels") {
		t.Fatalf("no channels: %v", err)
	}
	p.set(func(p *fakePanel) {
		p.streams, p.categories = `[{"stream_id":1,"name":"a","category_id":"1"}]`, `{"error":"x"}`
	})
	if list, err := newXtream(t, srv, p, nil).Channels(context.Background()); err != nil || len(list) != 1 || list[0].Group != "" {
		t.Fatalf("odd categories cost only the groups: %v %+v", err, list)
	}
}

func TestXtreamCatchup(t *testing.T) {
	p, srv := newFakePanel(t)
	s := newXtream(t, srv, p, nil)
	list, err := s.Channels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ch := list[0]
	start := time.Now().Add(-2 * time.Hour).Truncate(time.Minute)
	ch.ArchiveStart, ch.ArchiveEnd = start.Unix(), start.Add(20*time.Minute).Unix()
	st, err := s.Open(context.Background(), ch)
	if err != nil || st.Guard == nil {
		t.Fatalf("%v %+v", err, st)
	}
	st.Close()
	want := "/timeshift/viewer%20one/p%2Fss%3F&/20/" + start.UTC().Format("2006-01-02:15-04") + "/101.ts"
	if len(p.asked) != 1 || p.asked[0] != want {
		t.Fatalf("asked for %q, want %q", p.asked, want)
	}
	// A channel the panel keeps nothing for has no catch-up to ask for.
	radio := list[2]
	radio.ArchiveStart = start.Unix()
	if _, err := s.Open(context.Background(), radio); err == nil || !strings.Contains(err.Error(), "no catch-up") {
		t.Fatalf("no archive: %v", err)
	}
	ch.ArchiveStart, ch.ArchiveEnd = time.Now().Add(-4*24*time.Hour).Unix(), 0
	if _, err := s.Open(context.Background(), ch); err == nil || !strings.Contains(err.Error(), "the last 3 days") {
		t.Fatalf("too old: %v", err)
	}
}

// A big provider's list is read as it comes, and quickly.
func TestXtreamManyChannels(t *testing.T) {
	p, srv := newFakePanel(t)
	const n = 12000
	var b strings.Builder
	b.WriteString("[")
	for i := range n {
		if i > 0 {
			b.WriteString(",")
		}
		num := any(i + 1)
		if i%2 == 0 {
			num = fmt.Sprint(i + 1)
		}
		e, _ := json.Marshal(map[string]any{"num": num, "name": fmt.Sprintf("Channel %d", i+1), "stream_type": "live", "stream_id": 1000 + i,
			"stream_icon": fmt.Sprintf("http://logo.test/%d.png", i), "epg_channel_id": fmt.Sprintf("c%d.test", i), "category_id": fmt.Sprint(1 + i%2),
			"tv_archive": i % 2, "tv_archive_duration": "2", "added": "1700000000", "custom_sid": nil, "direct_source": ""})
		b.Write(e)
	}
	b.WriteString("]")
	p.set(func(p *fakePanel) { p.streams = b.String() })
	s := newXtream(t, srv, p, nil)
	began := time.Now()
	list, err := s.Channels(context.Background())
	took := time.Since(began)
	if err != nil || len(list) != n {
		t.Fatalf("%v, %d channels", err, len(list))
	}
	if took > 5*time.Second {
		t.Errorf("%d channels took %s", n, took)
	}
	last := list[n-1]
	if last.ID != fmt.Sprint(1000+n-1) || last.Number != fmt.Sprint(n) || last.Group != "Sport" || last.Catchup != "xc" || last.CatchupDays != 2 {
		t.Fatalf("%+v", last)
	}
}

func TestXtreamServerAddress(t *testing.T) {
	for raw, want := range map[string]string{
		"http://provider.example:8080":                  "http://provider.example:8080",
		"http://provider.example:8080/":                 "http://provider.example:8080",
		"https://provider.example/player_api.php":       "https://provider.example",
		"http://provider.example/panel/player_api.php/": "http://provider.example/panel",
		" http://provider.example/get.php ":             "http://provider.example",
		"provider.example:8080":                         "",
		"ftp://provider.example":                        "",
		"http://u:p@provider.example":                   "",
		"http://provider.example/get.php?username=u":    "",
		"http://169.254.169.254":                        "",
		"":                                              "",
	} {
		got, err := xtreamServer(raw)
		if want == "" && err == nil {
			t.Errorf("%q should be refused, got %s", raw, got)
		}
		if want != "" && (err != nil || got != want) {
			t.Errorf("%q: got %q %v, want %q", raw, got, err, want)
		}
	}
	if _, err := New("xtream", Config{Options: map[string]string{"url": "http://provider.example", "username": "u", "password": "p", "output": "mkv"}}, Env{}); err == nil {
		t.Error("an output format that isn't offered")
	}
	if _, err := New("xtream", Config{Options: map[string]string{"url": "http://provider.example", "username": "u"}}, Env{}); err == nil {
		t.Error("a password is required")
	}
}

// A panel on the internet can't lead the helper home; one the owner keeps at home can.
func TestXtreamHomeAndAway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "here") }))
	defer srv.Close()
	for place, home := range map[string]bool{
		srv.URL: true, "http://192.168.1.9:8080": true, "http://nas.local": true,
		"http://provider.example:8080": false, "provider.example:8080": false, "": false,
	} {
		env := HelperEnvFor("xtream", map[string]string{"url": place})
		res, err := env.HTTP.Get(srv.URL)
		if err == nil {
			res.Body.Close()
		}
		if (err == nil) != home {
			t.Errorf("%q: reached this machine: %v", place, err == nil)
		}
	}
	s, err := New("xtream", Config{Options: map[string]string{"url": "http://provider.example:8080", "username": "u", "password": "p"}}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	c := s.(checked)
	for loc, ok := range map[string]bool{
		"http://provider.example:8080/live/u/p/1.ts": true,
		"http://PROVIDER.example:8080/live/u/p/1.ts": true,
		"http://other.example/live/u/p/1.ts":         false,
		"http://provider.example:9090/live/u/p/1.ts": false,
		"file:///etc/passwd":                         false,
	} {
		if err := c.CheckLocator(loc); (err == nil) != ok {
			t.Errorf("%s: %v", loc, err)
		}
	}
	// What it opens is held to a playlist from elsewhere's rules, catch-up included.
	withLookup(t, map[string]string{"provider.example": "127.0.0.1"})
	ch := Channel{Name: "Ch", Locator: "http://provider.example:8080/live/u/p/1.ts", Catchup: "xc", CatchupDays: 2}
	if _, err := s.Open(context.Background(), ch); err == nil || !strings.Contains(err.Error(), "may not point at this server") {
		t.Errorf("live: %v", err)
	}
	ch.ArchiveStart = time.Now().Add(-time.Hour).Unix()
	if _, err := s.Open(context.Background(), ch); err == nil || !strings.Contains(err.Error(), "may not point at this server") {
		t.Errorf("archive: %v", err)
	}
}

func TestFlexibleNumbers(t *testing.T) {
	for raw, want := range map[string]struct {
		s string
		n int64
	}{
		`"5"`: {"5", 5}, `5`: {"5", 5}, `5.0`: {"5.0", 5}, `"5.0"`: {"5.0", 5}, `null`: {"", 0}, `""`: {"", 0},
		`"abc"`: {"abc", 0}, `true`: {"true", 0}, `[1]`: {"", 0}, `{"a":1}`: {"", 0}, `"\u0041"`: {"A", 0}, `-1`: {"-1", -1},
	} {
		var v struct {
			S flexString `json:"s"`
			N flexInt    `json:"n"`
		}
		if err := json.Unmarshal([]byte(`{"s":`+raw+`,"n":`+raw+`}`), &v); err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		if string(v.S) != want.s || int64(v.N) != want.n {
			t.Errorf("%s: got %q %d", raw, v.S, v.N)
		}
	}
}
