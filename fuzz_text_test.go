// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"strings"
	"testing"
)

// The playlists, lists of channels and device answers WittyTuner reads come from providers,
// devices on the network and other programs: none may crash it, and what a playlist says may
// only ever point a logo at the web.
func FuzzPlaylistAndTunerText(f *testing.F) {
	f.Add("#EXTM3U url-tvg=\"http://g/x.xml\"\n#EXTINF:-1 tvg-id=\"a\" tvg-logo=\"file:///etc/passwd\" group-title=\"News\",One\nhttp://h/1.ts\n#EXTINF:0,Two\n#EXTVLCOPT:http-user-agent=x\nhttp://h/2.m3u8\n")
	f.Add("#EXTINF:-1 tvg-logo=\"https://l/a.png\" ch-number=\"5.1\",\n\n#KODIPROP:inputstream.adaptive.license_key=x\nrtp://1.2.3.4:5000\n")
	f.Add("dvb-t:474000000:8000000:0:::17")
	f.Add("ARD;ARD:11836:hC34M2S0:S19.2E:27500:101=2:102=deu:104:0:28106:1:1101:0")
	f.Add("[DVB-T]\nDELIVERY_SYSTEM = DVBT\nFREQUENCY = 474000000\nBANDWIDTH_HZ = 8000000\n")
	f.Add("250-1 ARD;ARD:11836:H:S19.2E:27500:101:102:104:0:28106:1:1101:0")
	f.Add("[dshow @ 0] \"USB Video\" (video)\n[dshow @ 0]   Alternative name \"@device_pnp\"\n[dshow @ 0] \"Mic\" (audio)\n")
	f.Fuzz(func(t *testing.T, s string) {
		if m, err := ParseM3U(strings.NewReader(s)); err == nil {
			for _, e := range m.Entries {
				if l := strings.ToLower(e.Logo); l != "" && !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
					t.Errorf("a playlist's logo that isn't a web address was kept: %q", e.Logo)
				}
			}
		}
		parseAttrs(s)
		parseHLSAttrs(s)
		if tu, prog, err := ParseLocator(s); err == nil {
			// What a locator reads back as is written back the same, and reads back the same again.
			if tu2, prog2, err := ParseLocator(tu.Locator(prog)); err != nil || tu2 != tu || prog2 != prog {
				t.Errorf("locator %q: read %+v %d, written %q, read again %+v %d %v", s, tu, prog, tu.Locator(prog), tu2, prog2, err)
			}
		}
		for _, sys := range []string{"dvb-t", "dvb-s", "dvb-c", "atsc"} {
			ParseChannelsConf(sys, s)
			_, _ = parseTransponders(sys, s)
		}
		parseSVDRPChannel(s, "127.0.0.1", "6419")
		ParseDShowDevices(s)
		ParseAVFDevices(s)
		parseHDHRReply([]byte(s))
	})
}
