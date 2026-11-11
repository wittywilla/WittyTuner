// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A device on the network names where to look. It may only name itself.
func TestDiscoveryOnlyBelievesTheSender(t *testing.T) {
	from := &net.UDPAddr{IP: net.ParseIP("192.168.1.60"), Port: 1900}
	for _, tc := range []struct {
		loc  string
		from net.Addr
		ok   bool
	}{
		{"http://192.168.1.60:8875/desc.xml", from, true},
		{"http://192.168.1.60/desc.xml", from, true},
		{"http://[fe80::1]/d", &net.UDPAddr{IP: net.ParseIP("fe80::1")}, true},
		{"http://169.254.10.1/d", &net.UDPAddr{IP: net.ParseIP("169.254.10.1")}, true},
		{"http://192.168.1.61/desc.xml", from, false},
		{"http://127.0.0.1:8096/anything", from, false},
		{"http://169.254.169.254/latest/meta-data/", from, false},
		{"http://169.254.169.254/latest/meta-data/", &net.UDPAddr{IP: net.ParseIP("169.254.169.254")}, false},
		{"http://8.8.8.8/x", &net.UDPAddr{IP: net.ParseIP("8.8.8.8")}, false},
		{"http://media.example/x", from, false},
		{"https://192.168.1.60/desc.xml", from, false},
		{"ftp://192.168.1.60/desc.xml", from, false},
		{"http://user:pw@192.168.1.60/desc.xml", from, false},
		{"http://[::ffff:192.168.1.60]/desc.xml", from, true},
	} {
		if _, ok := discoveredAt(tc.loc, tc.from); ok != tc.ok {
			t.Errorf("%s from %v: %v, want %v", tc.loc, tc.from, ok, tc.ok)
		}
	}
}

// A device that answers with a redirect doesn't get the server to ask somewhere else.
func TestDiscoveryFollowsNoRedirect(t *testing.T) {
	var asked atomic.Int64
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		fmt.Fprint(w, `{"DeviceID":"1","FriendlyName":"Elsewhere"}`)
	}))
	defer elsewhere.Close()
	device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/secret", http.StatusFound)
	}))
	defer device.Close()
	ctx := context.Background()
	if name, _ := satipDescribe(ctx, Env{}, device.URL+"/desc.xml"); name != "" {
		t.Errorf("%q", name)
	}
	if d, err := discoverDevice(ctx, Env{}, strings.TrimPrefix(device.URL, "http://")); err == nil && d.DeviceID != "" {
		t.Errorf("%+v", d)
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("the other address was asked %d times", n)
	}
}
