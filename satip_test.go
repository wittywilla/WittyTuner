// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"context"
	"strings"
	"testing"
)

// A channel list from a SAT>IP server names its tunings; it can't send FFmpeg somewhere else.
func TestSatipTuningStaysOnItsServer(t *testing.T) {
	s, err := New("satip", Config{Name: "S", Options: map[string]string{"server": "192.0.2.60:554"}}, Env{FFmpeg: "no-such-program"})
	if err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{"rtsp://192.168.1.1/?src=1&freq=1", "rtsp://user:pw@192.168.1.1:554/x", "rtsp://[::1]/x"} {
		_, err := s.Open(context.Background(), Channel{Name: "C", Locator: loc})
		if err == nil || !strings.Contains(err.Error(), "its server") {
			t.Errorf("%s: %v", loc, err)
		}
	}
	for _, loc := range []string{"rtsp://192.0.2.60/?src=1", "rtsp://192.0.2.60:554/?src=1", "RTSP://192.0.2.60/?src=1"} {
		_, err := s.Open(context.Background(), Channel{Name: "C", Locator: loc})
		if err != nil && strings.Contains(err.Error(), "its server") {
			t.Errorf("%s: %v", loc, err)
		}
	}
}
