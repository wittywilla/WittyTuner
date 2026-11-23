// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeHDHR is an HDHomeRun network tuner in this process, as the maker documents one
// (info.hdhomerun.com/info/http_api and /info/discovery_api): UDP discovery, discover.json,
// lineup.json, lineup_status.json and lineup.post on its API port, and MPEG-TS at
// /auto/v<channel> on a second port, as 80 and 5004 are on a real device. Its channels are
// ATSC 1.0 and 3.0, one marked DRM. D:\build-tools\hdhomerun-081 has the same as a program.
type fakeHDHR struct {
	t       *testing.T
	api     *httptest.Server
	stream  *httptest.Server
	udp     net.PacketConn
	id      uint32
	tuners  int
	host    string // how its URLs name it: an address, or hdhr-<id>.local
	legacy  bool
	mu      sync.Mutex
	inUse   int
	opened  atomic.Int64 // streams asked for
	scanned bool
	polls   int
}

func newFakeHDHR(t *testing.T, tuners int, byName bool) *fakeHDHR {
	t.Helper()
	f := &fakeHDHR{t: t, id: 0x1053C0CA, tuners: tuners}
	f.stream = httptest.NewServer(http.HandlerFunc(f.serveStream))
	f.api = httptest.NewServer(http.HandlerFunc(f.serveAPI))
	t.Cleanup(f.api.Close)
	t.Cleanup(f.stream.Close)
	f.host = "127.0.0.1"
	if byName {
		f.host = fmt.Sprintf("hdhr-%08x.local", f.id)
	}
	return f
}

func port(s *httptest.Server) string {
	_, p, _ := net.SplitHostPort(s.Listener.Addr().String())
	return p
}

// address is what someone types to add it, or what discovery stores: host:port of its API.
func (f *fakeHDHR) address() string { return f.api.Listener.Addr().String() }

func (f *fakeHDHR) baseURL() string { return "http://" + net.JoinHostPort(f.host, port(f.api)) }

func (f *fakeHDHR) lineup() []map[string]any {
	u := func(n string) string { return "http://" + net.JoinHostPort(f.host, port(f.stream)) + "/auto/v" + n }
	return []map[string]any{
		{"GuideNumber": "2.1", "GuideName": "WEMU-HD", "VideoCodec": "MPEG2", "AudioCodec": "AC3", "HD": 1, "URL": u("2.1")},
		{"GuideNumber": "4.1", "GuideName": "KTST", "VideoCodec": "H264", "AudioCodec": "AC3", "HD": 1, "Tags": "favorite", "Favorite": 1, "URL": u("4.1")},
		{"GuideNumber": "102.1", "GuideName": "WEMU 3.0", "VideoCodec": "HEVC", "AudioCodec": "AC4", "HD": 1, "URL": u("102.1")},
		{"GuideNumber": "104.1", "GuideName": "KTST 3.0", "VideoCodec": "HEVC", "AudioCodec": "AC4", "HD": 1, "DRM": 1, "Tags": "drm", "URL": u("104.1")},
	}
}

func (f *fakeHDHR) serveAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/discover.json":
		d := map[string]any{"FriendlyName": "HDHomeRun CONNECT 4K", "ModelNumber": "HDHR5-4K", "FirmwareName": "hdhomerun5_atsc3", "FirmwareVersion": "20251023",
			"DeviceID": fmt.Sprintf("%08X", f.id), "DeviceAuth": "emuAuthStr0123456789abcd", "BaseURL": f.baseURL(), "LineupURL": f.baseURL() + "/lineup.json", "TunerCount": f.tuners}
		if f.legacy {
			d = map[string]any{"FriendlyName": "HDHomeRun", "ModelNumber": "HDHR3-US", "DeviceID": fmt.Sprintf("%08X", f.id), "Legacy": 1, "TunerCount": 2}
		}
		_ = json.NewEncoder(w).Encode(d)
	case "/lineup.json":
		_ = json.NewEncoder(w).Encode(f.lineup())
	case "/lineup.post":
		if r.Method != "POST" || r.URL.Query().Get("scan") != "start" || r.URL.Query().Get("source") != "Antenna" {
			http.Error(w, "bad request", 400)
			return
		}
		f.scanned, f.polls = true, 0
	case "/lineup_status.json":
		f.polls++
		if f.scanned && f.polls < 3 {
			_ = json.NewEncoder(w).Encode(map[string]any{"ScanInProgress": 1, "Progress": 40 * f.polls, "Found": f.polls})
			return
		}
		// Done, as a device says it: no Progress at all.
		_ = json.NewEncoder(w).Encode(map[string]any{"ScanInProgress": 0, "ScanPossible": 1, "Source": "Antenna", "SourceList": []string{"Antenna"}})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeHDHR) serveStream(w http.ResponseWriter, r *http.Request) {
	f.opened.Add(1)
	ch, ok := strings.CutPrefix(r.URL.Path, "/auto/v")
	known := false
	var drm bool
	for _, l := range f.lineup() {
		if l["GuideNumber"] == ch {
			known, drm = true, l["DRM"] != nil
		}
	}
	if !ok || !known {
		w.Header().Set("X-HDHomeRun-Error", "801 Unknown Channel")
		http.Error(w, "", 404)
		return
	}
	if drm {
		w.Header().Set("X-HDHomeRun-Error", "811 Content Protection Required")
		http.Error(w, "", 503)
		return
	}
	f.mu.Lock()
	if f.inUse >= f.tuners {
		f.mu.Unlock()
		w.Header().Set("X-HDHomeRun-Error", "805 All Tuners In Use")
		http.Error(w, "", 503)
		return
	}
	f.inUse++
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.inUse--; f.mu.Unlock() }()
	w.Header().Set("Content-Type", "video/mp2t")
	pkt := make([]byte, 188)
	pkt[0], pkt[1], pkt[2], pkt[3] = 0x47, 0x01, 0x00, 0x10 // PID 0x100, clear, payload only
	for i := 0; ; i++ {
		pkt[3] = 0x10 | byte(i&0x0f)
		if _, err := w.Write(pkt); err != nil {
			return
		}
		if i%500 == 0 {
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
}

// listenUDP answers discovery on 127.0.0.1, and points WittyTuner's search there.
func (f *fakeHDHR) listenUDP() {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		f.t.Fatal(err)
	}
	f.udp = pc
	f.t.Cleanup(func() { pc.Close() })
	oldPort, oldTargets, oldNames, oldListen := hdhrDiscoverPort, hdhrDiscoverTargets, hdhrNames, discoverListen
	discoverListen = "127.0.0.1:0"
	hdhrDiscoverPort = pc.LocalAddr().(*net.UDPAddr).Port
	hdhrDiscoverTargets = func() []net.IP { return []net.IP{net.IPv4(127, 0, 0, 1)} }
	hdhrNames = nil
	f.t.Cleanup(func() {
		hdhrDiscoverPort, hdhrDiscoverTargets, hdhrNames, discoverListen = oldPort, oldTargets, oldNames, oldListen
	})
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			b := buf[:n]
			// A request as libhdhomerun sends one: type 2, its length, tags, a little-endian CRC.
			if n < 8 || binary.BigEndian.Uint16(b) != hdhrDiscoverReq || int(binary.BigEndian.Uint16(b[2:]))+8 != n ||
				binary.LittleEndian.Uint32(b[n-4:]) != crc32.ChecksumIEEE(b[:n-4]) {
				continue
			}
			tag := func(t byte, v []byte) []byte { return append([]byte{t, byte(len(v))}, v...) }
			var p []byte
			p = append(p, tag(hdhrTagType, binary.BigEndian.AppendUint32(nil, hdhrTypeTuner))...)
			p = append(p, tag(hdhrTagID, binary.BigEndian.AppendUint32(nil, f.id))...)
			p = append(p, tag(hdhrTagTuners, []byte{byte(f.tuners)})...)
			p = append(p, tag(0x2B, []byte("emuAuthStr0123456789abcd"))...) // device auth
			// The base URL as the device gives it: with its port, so the search asks the right one.
			p = append(p, tag(hdhrTagBaseURL, []byte("http://"+f.address()))...)
			p = append(p, tag(0x27, []byte("http://"+f.address()+"/lineup.json"))...) // lineup URL
			_, _ = pc.WriteTo(hdhrPacket(hdhrDiscoverRes, p), from)
		}
	}()
}

func addFake(t *testing.T, address string) Source {
	t.Helper()
	s, err := New("network", Config{Name: "Box", Options: map[string]string{"address": address}}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readSome(t *testing.T, st *Stream) {
	t.Helper()
	if n, err := io.CopyN(io.Discard, st.Reader, 188*1000); err != nil || n != 188*1000 {
		t.Fatalf("read %d: %v", n, err)
	}
}

// Found by the maker's search, it's added by the address the search found.
func TestHDHomeRunFoundBySearch(t *testing.T) {
	f := newFakeHDHR(t, 4, false)
	f.listenUDP()
	found := discoverNetworkTuners(context.Background(), Env{})
	if len(found) != 1 || found[0].Driver != "network" || found[0].Options["address"] != f.address() || !strings.Contains(found[0].Detail, "HDHR5-4K, 4 tuners") {
		t.Fatalf("%+v", found)
	}
	s := addFake(t, found[0].Options["address"])
	ch, err := s.Channels(context.Background())
	if err != nil || len(ch) != 4 {
		t.Fatalf("%v %+v", err, ch)
	}
}

// The maker's documents show a device naming itself hdhr-<id>.local: its lineup's addresses are
// then by name, while setup added it by the address the search found. That's the same device, and
// its channels play, from the address it was added by.
func TestHDHomeRunLineupByName(t *testing.T) {
	f := newFakeHDHR(t, 2, true)
	s := addFake(t, f.address())
	ctx := context.Background()
	ch, err := s.Channels(ctx)
	if err != nil || len(ch) != 4 || !strings.Contains(ch[0].Locator, ".local") {
		t.Fatalf("%v %+v", err, ch)
	}
	st, err := s.Open(ctx, ch[0])
	if err != nil {
		t.Fatalf("a lineup naming the device itself must play: %v", err)
	}
	readSome(t, st)
	st.Close()
	// Some other name is still not the device.
	if _, err := s.Open(ctx, Channel{Name: "X", Number: "2.1", Locator: "http://elsewhere.local:" + port(f.stream) + "/auto/v2.1"}); err == nil || !strings.Contains(err.Error(), "another address") {
		t.Fatalf("%v", err)
	}
	if err := s.(checked).CheckLocator(ch[0].Locator); err != nil {
		t.Fatalf("the server's check of a stored address: %v", err)
	}
}

// Added by a name (as hdhomerun.local can be), its lineup by address: the same.
func TestHDHomeRunAddedByName(t *testing.T) {
	f := newFakeHDHR(t, 2, false)
	s := addFake(t, "localhost:"+port(f.api))
	ctx := context.Background()
	ch, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st, err := s.Open(ctx, ch[0])
	if err != nil {
		t.Fatal(err)
	}
	readSome(t, st)
	st.Close()
}

// Two viewers at once on a two-tuner device; a third is told every tuner is in use, which the
// device says with 503 and 805; and a tuner let go is free again.
func TestHDHomeRunTunersInUse(t *testing.T) {
	f := newFakeHDHR(t, 2, false)
	s := addFake(t, f.address())
	ctx := context.Background()
	ch, _ := s.Channels(ctx)
	if s.Info().MaxStreams != 2 {
		t.Fatalf("%+v", s.Info())
	}
	a, err := s.Open(ctx, ch[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Open(ctx, ch[1])
	if err != nil {
		t.Fatal(err)
	}
	readSome(t, a)
	readSome(t, b)
	if _, err := s.Open(ctx, ch[2]); !errors.Is(err, ErrBusy) {
		t.Fatalf("a third viewer: %v", err)
	}
	a.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := s.Open(ctx, ch[2])
		if err == nil {
			readSome(t, c)
			c.Close()
			break
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("a tuner let go: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	b.Close()
}

// The ATSC 3.0 channel the device marks DRM is refused with the words for it, and never asked for.
func TestHDHomeRunDRMChannel(t *testing.T) {
	f := newFakeHDHR(t, 2, false)
	s := addFake(t, f.address())
	ch, _ := s.Channels(context.Background())
	var drm, atsc3 Channel
	for _, c := range ch {
		switch c.Number {
		case "104.1":
			drm = c
		case "102.1":
			atsc3 = c
		}
	}
	if drm.Encrypted == "" || !drm.ATSC3 || atsc3.Encrypted != "" || !atsc3.ATSC3 {
		t.Fatalf("%+v %+v", drm, atsc3)
	}
	_, err := s.Open(context.Background(), drm)
	if !IsEncrypted(err) || !strings.Contains(err.Error(), EncryptedLabel) {
		t.Fatalf("%v", err)
	}
	if n := f.opened.Load(); n != 0 {
		t.Fatalf("the device was asked for a protected channel %d times", n)
	}
	// Asked anyway (an old list without the mark), the device's 811 says the same.
	drm.Encrypted = ""
	if _, err := s.Open(context.Background(), drm); !IsEncrypted(err) {
		t.Fatalf("%v", err)
	}
}

// A scan ends at 100%: a device that's done says nothing of progress.
func TestHDHomeRunScanEnds(t *testing.T) {
	f := newFakeHDHR(t, 2, false)
	scanPoll = time.Millisecond
	s := addFake(t, f.address())
	var last Progress
	got, err := s.Scan(context.Background(), ScanOptions{}, func(p Progress) { last = p })
	if err != nil || len(got) != 4 || last.Percent != 100 || last.Found != 4 {
		t.Fatalf("%v %d %+v", err, len(got), last)
	}
}

// An older model without HTTP streaming says so, instead of seeming not to be a tuner at all.
func TestHDHomeRunLegacy(t *testing.T) {
	f := newFakeHDHR(t, 2, false)
	f.legacy = true
	_, err := addFake(t, f.address()).Channels(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HDHR3-US") || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("%v", err)
	}
}
