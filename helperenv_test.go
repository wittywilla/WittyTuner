// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The helper process has only these to go on, as the server's own checks are in another process.
func TestHelperClients(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "here") }))
	defer srv.Close()
	away := HelperEnv(false)
	if res, err := away.HTTP.Get(srv.URL); err == nil {
		res.Body.Close()
		t.Error("a stream from the internet reached this machine")
	}
	home := HelperEnv(true)
	res, err := home.HTTP.Get(srv.URL)
	if err != nil {
		t.Fatalf("at home: %v", err)
	}
	res.Body.Close()
	for _, tc := range []struct {
		ip         string
		home, away bool
	}{
		{"8.8.8.8", true, true},
		{"192.168.1.5", true, false},
		{"10.0.0.1", true, false},
		{"100.64.0.1", true, false},
		{"127.0.0.1", true, false},
		{"::1", true, false},
		{"fd00::1", true, false},
		{"169.254.10.1", true, false},
		{"169.254.169.254", false, false},
		{"fd00:ec2::254", false, false},
		{"0.0.0.0", false, false},
		{"::", false, false},
		{"224.0.0.1", true, false},
		{"::ffff:127.0.0.1", true, false},
		{"64:ff9b::7f00:1", true, false},
		{"2002:7f00:1::", true, false},
		{"2606:4700::1", true, true},
	} {
		ip := net.ParseIP(tc.ip)
		if got := reachable(ip, true); got != tc.home {
			t.Errorf("%s from home: %v", tc.ip, got)
		}
		if got := reachable(ip, false); got != tc.away {
			t.Errorf("%s from the internet: %v", tc.ip, got)
		}
	}
}

func TestWhatCountsAsAPlaylistAtHome(t *testing.T) {
	for p, want := range map[string]bool{
		"/srv/list.m3u": true, "http://192.168.1.2/x.m3u": true, "http://127.0.0.1/x.m3u": true, "http://localhost/x.m3u": true, "http://nas.local/x.m3u": true,
		"http://nas.lan:8000/x.m3u": true, "http://mediabox/x.m3u": true, "http://nas.home.arpa/x.m3u": true, "http://[fd00::1]/x.m3u": true,
		"https://iptv.example.com/x.m3u": false, "http://8.8.8.8/x.m3u": false, "http://nas.local.example.com/x.m3u": false, "http://127.0.0.1.nip.io/x.m3u": false,
	} {
		if got := LocalPlaylist(p); got != want {
			t.Errorf("%s: %v", p, got)
		}
	}
}
