// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// helperLog is where a helper says what its drivers did: slog's text lines on stderr, which the
// server reads back into its own log with their level, cat and session (WittyMediaServer's
// internal/modules/proc.go). The level is the one the server asked for in WITTYMODULE_LOG; with any
// WITTYMODULE_LOG_TOPICS on it prints Debug too, and the server keeps what the topic lets through.
var helperLog = sync.OnceValue(func() *slog.Logger {
	return newHelperLog(os.Stderr, os.Getenv("WITTYMODULE_LOG"), os.Getenv("WITTYMODULE_LOG_TOPICS"))
})

func newHelperLog(w io.Writer, level, topics string) *slog.Logger {
	lv := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error", "off":
		lv = slog.LevelError
	}
	if strings.TrimSpace(topics) != "" {
		lv = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv})).With("cat", "tv")
}

// A helper process has no one else to check where it connects, as the server does for itself. These
// clients check each connection as it is made, on the address it really goes to, so a name that
// changes what it points at or a redirect can't get round them.

// HelperEnv is the Env for a helper. home says the stream was named by the owner or a playlist on the
// home network, so it may lead there; anything else stays on the internet.
func HelperEnv(home bool) Env {
	d := &net.Dialer{Timeout: 15 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if !reachable(net.ParseIP(host), home) {
			return fmt.Errorf("refusing to connect to %s", host)
		}
		return nil
	}}
	tr := &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConnsPerHost: 2}
	check := func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return errors.New("too many or unsafe redirects")
		}
		return nil
	}
	return Env{HTTP: &http.Client{Timeout: 30 * time.Second, Transport: tr, CheckRedirect: check}, Stream: &http.Client{Transport: tr, CheckRedirect: check},
		Log: helperLog()}
}

// HelperEnvFor is the Env for a call about this tuner.
func HelperEnvFor(driver string, options map[string]string) Env {
	switch driver {
	case "iptv":
		return HelperEnv(LocalPlaylist(options["playlist"]))
	case "xtream":
		// A provider's panel is on the internet unless the owner put it at home.
		base, err := xtreamServer(options["url"])
		return HelperEnv(err == nil && LocalPlaylist(base))
	}
	return HelperEnv(true)
}

// Addresses a cloud host answers questions about itself on. No tuner lives there, wherever its
// address came from.
var cloudMetadata = []netip.Addr{netip.MustParseAddr("169.254.169.254"), netip.MustParseAddr("fd00:ec2::254"), netip.MustParseAddr("100.100.100.200")}

// reachable says a connection may be made to an address.
func reachable(ip net.IP, home bool) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if a.IsUnspecified() {
		return false
	}
	for _, m := range cloudMetadata {
		if a == m {
			return false
		}
	}
	return home || publicAddr(a)
}

// notInternet are the ranges that aren't the public internet. Some lead back to private networks:
// NAT64 and 6to4 addresses embed an IPv4 one.
var notInternet = func() []netip.Prefix {
	var out []netip.Prefix
	for _, c := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
		"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/96", "::ffff:0:0:0/96", "100::/64", "2001::/32", "2001:db8::/32", "64:ff9b:1::/48", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(c))
	}
	return out
}()

var (
	nat64Prefix = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour   = netip.MustParsePrefix("2002::/16")
)

func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	if a.Is6() {
		b := a.As16()
		switch {
		case nat64Prefix.Contains(a):
			return publicAddr(netip.AddrFrom4([4]byte(b[12:16])))
		case sixToFour.Contains(a):
			return publicAddr(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	for _, p := range notInternet {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// LocalPlaylist says a playlist's place is on the owner's own network: a file, an address at home or on
// a private network, or a name only a local network can resolve. A name anyone can register can point
// anywhere, so it doesn't count, whatever it resolves to now. The owner typed this address, so a
// playlist from elsewhere can't choose it.
func LocalPlaylist(place string) bool {
	if !strings.Contains(place, "://") {
		return place != ""
	}
	u, err := url.Parse(place)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if a, err := netip.ParseAddr(h); err == nil {
		a = a.Unmap()
		return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(a)
	}
	return localName(h)
}

func localName(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	if !strings.Contains(host, ".") {
		return true // "localhost" or a computer's own name
	}
	for _, suffix := range []string{".localhost", ".local", ".localdomain", ".lan", ".home", ".home.arpa", ".fritz.box", ".internal", ".intranet"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
