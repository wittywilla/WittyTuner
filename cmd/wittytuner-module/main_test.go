// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wittywilla/wittytuner"
)

// tsPacket is one clear MPEG-TS packet.
func tsPacket() []byte {
	p := bytes.Repeat([]byte{0x47}, 188)
	p[1], p[2], p[3] = 0x01, 0x00, 0x10
	return p
}

// A channel's catch-up fields cross the process line both ways: in the channel list a
// helper sends, and back in the "open" the server asks for, where they reach the driver.
func TestCatchupCrossesTheProcessLine(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.RawQuery)
		mu.Unlock()
		_, _ = w.Write(bytes.Repeat(tsPacket(), 40))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "list.m3u")
	list := fmt.Sprintf("#EXTM3U\n#EXTINF:-1 catchup=\"shift\" catchup-days=\"2\" catchup-source=\"?x={utc}\",Ch\n%s/ch.ts\n", srv.URL)
	if err := os.WriteFile(path, []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	call := wittytuner.HelperCall{Driver: "iptv", Options: map[string]string{"playlist": path}}

	// The list, as the server reads it.
	raw, _ := json.Marshal(call)
	_, write, err := handle("channels", raw)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := write(&out); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"catchup":"shift"`, `"catchupDays":2`, `"catchupSource":"?x={utc}"`} {
		if !strings.Contains(out.String(), field) {
			t.Errorf("the channel list has no %s: %s", field, out.String())
		}
	}
	var doc wittytuner.HelperDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || len(doc.Channels) != 1 {
		t.Fatalf("%v %s", err, out.String())
	}

	// The open, as the server asks for it.
	ch := doc.Channels[0]
	start := time.Now().Add(-time.Hour).Unix()
	ch.ArchiveStart, ch.ArchiveEnd = start, start+1800
	call.Channel = &ch
	raw, _ = json.Marshal(call)
	if !strings.Contains(string(raw), fmt.Sprintf(`"archiveStart":%d`, start)) || !strings.Contains(string(raw), fmt.Sprintf(`"archiveEnd":%d`, start+1800)) {
		t.Fatalf("the call carries no archive: %s", raw)
	}
	_, write, err = handle("open", raw)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := write(&out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 40*188 {
		t.Fatalf("%d bytes", out.Len())
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || !strings.HasPrefix(asked[0], fmt.Sprintf("utc=%d&lutc=", start)) {
		t.Fatalf("the driver opened %q, not the archive", asked)
	}
}
