// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wittywilla/wittytuner/mpegts"
)

// demoFFmpeg is an FFmpeg for the demo tuner's tests: WMS_FFMPEG, else one on the PATH.
func demoFFmpeg(t *testing.T) string {
	t.Helper()
	if f := os.Getenv("WMS_FFMPEG"); f != "" {
		return f
	}
	f, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no FFmpeg: set WMS_FFMPEG")
	}
	return f
}

func newDemo(t *testing.T, opts map[string]string) Source {
	t.Helper()
	SetDemoFFmpeg(demoFFmpeg(t), nil)
	s, err := New("demo", Config{Name: "Demo tuner", Options: opts}, HelperEnv(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestDemoTunerEndToEnd(t *testing.T) {
	s := newDemo(t, map[string]string{"port": "47691", "tuners": "1"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The lineup, through the network tuner's own code: guide ids filled in, the protected one marked.
	list, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(demoPatterns) {
		t.Fatalf("%d channels", len(list))
	}
	var news, protected Channel
	for _, c := range list {
		if c.EPGID != DemoGuideID(c.Number) {
			t.Errorf("%s has guide id %q", c.Name, c.EPGID)
		}
		switch c.Number {
		case "1":
			news = c
		case "9":
			protected = c
		}
	}
	if protected.Encrypted == "" {
		t.Error("the protected demo channel isn't marked")
	}
	if info := s.Info(); info.Driver != "demo" || info.MaxStreams != 1 || !strings.HasSuffix(info.GuideURL, "/guide.xml") {
		t.Errorf("info %+v", info)
	}

	// Tuning: MPEG-TS through the guard, with picture and sound.
	st, err := s.Open(ctx, news)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 188*2000)
	n, err := io.ReadAtLeast(st.Reader, buf, 188*500)
	if err != nil || buf[0] != 0x47 {
		t.Fatalf("read %d: %v", n, err)
	}
	// One tuner, in use: the next channel is busy, as a real box says.
	if _, err := s.Open(ctx, list[1]); !errors.Is(err, ErrBusy) {
		t.Errorf("a second channel on one tuner: %v", err)
	}
	st.Close()

	// The protected one is refused, with the same words as every encrypted channel.
	if _, err := s.Open(ctx, protected); !IsEncrypted(err) {
		t.Errorf("protected: %v", err)
	}

	// Its guide.
	res, err := http.Get(s.Info().GuideURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `channel="demo1.wittytuner"`) || !strings.Contains(string(body), "<title>") {
		t.Errorf("guide: %.300s", body)
	}
	_ = mpegts.ErrScrambled
}

func TestDemoTunerScanAndDiscovery(t *testing.T) {
	s := newDemo(t, map[string]string{"port": "47692"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	old := scanPoll
	scanPoll = 200 * time.Millisecond
	defer func() { scanPoll = old }()
	var last Progress
	list, err := s.Scan(ctx, ScanOptions{}, func(p Progress) { last = p })
	if err != nil || len(list) != len(demoPatterns) {
		t.Fatalf("scan: %v %d", err, len(list))
	}
	_ = last

	// Discovery on this machine finds it as the demo tuner, on its own port. Another test server
	// may hold the discovery port, and then there's nothing to test.
	d := demoDevs["47692"]
	if d == nil || d.udp == nil {
		t.Skip("the discovery port is in use here")
	}
	found := discoverNetworkTuners(ctx, HelperEnv(true))
	ok := false
	for _, f := range found {
		if f.Driver == "demo" && f.Options["port"] == "47692" {
			ok = true
		}
	}
	if !ok {
		t.Errorf("discovery found %+v", found)
	}
}

func TestDemoTunerSlowAndDropping(t *testing.T) {
	s := newDemo(t, map[string]string{"port": "47693", "slowTune": "2"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list, err := s.Channels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	began := time.Now()
	st, err := s.Open(ctx, list[0])
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := make([]byte, 188)
	if _, err := io.ReadFull(st.Reader, b); err != nil {
		t.Fatal(err)
	}
	if time.Since(began) < 2*time.Second {
		t.Errorf("tuned in %s with slow tuning of 2s", time.Since(began))
	}
}

func TestDemoSettingsChecked(t *testing.T) {
	for _, o := range []map[string]string{{"tuners": "0"}, {"tuners": "99"}, {"slowTune": "-1"}, {"dropEvery": "x"}, {"clips": "/no/such/folder"}, {"port": "80"}} {
		if _, err := demoDevice(o); err == nil {
			t.Errorf("%v was accepted", o)
		}
	}
	// A port another program holds is a clear message.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	if _, err := demoDevice(map[string]string{"port": port}); err == nil || !strings.Contains(err.Error(), "another program") {
		t.Errorf("busy port: %v", err)
	}
}
