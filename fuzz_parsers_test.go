// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

// Playlists, header options and tuners' discovery answers come from other machines, some of
// them on the internet. Reading them must never panic, and must never let what they say slip
// past the checks: a header value on more than one line, or an address of a kind no channel may have.

func FuzzParseM3U(f *testing.F) {
	f.Add("#EXTM3U url-tvg=\"https://example.com/g.xml\"\n#EXTINF:-1 tvg-id=\"a\" tvg-logo=\"https://example.com/a.png\" group-title=\"News\",A\nhttp://example.com/a.ts\n")
	f.Add("#EXTINF:-1 http-user-agent=\"x\",B\n#EXTVLCOPT:http-referrer=https://r.example/\nhttps://example.com/b.m3u8|User-Agent=a%0d%0aX-Evil:1&Referer=b\n")
	f.Add("#EXTHTTP:{\"User-Agent\":\"a\\r\\nX: y\"}\n#KODIPROP:inputstream.adaptive.stream_headers=User-Agent=z\nrtsp://198.51.100.1/live\n")
	f.Add("#EXTINF:-1 catchup=\"shift\" catchup-days=\"7\" tvg-shift=\"+2\",C\n#KODIPROP:inputstream.adaptive.license_type=widevine\nhttp://example.com/c\n")
	f.Fuzz(func(t *testing.T, s string) {
		m, err := ParseM3U(strings.NewReader(s))
		if err != nil || m == nil {
			return
		}
		for _, e := range m.Entries {
			for _, h := range []string{e.UserAgent, e.Referrer} {
				if strings.ContainsAny(h, "\r\n\x00") || len(h) > 512 {
					t.Fatalf("a header value from a playlist kept a line break or grew too long: %q", h)
				}
			}
			if strings.ContainsAny(e.Locator, "\r\n\x00") && checkStreamURL(e.Locator, true) == nil {
				t.Fatalf("an address with a line break in it was taken: %q", e.Locator)
			}
			if e.Logo != "" && !strings.HasPrefix(e.Logo, "http://") && !strings.HasPrefix(e.Logo, "https://") {
				t.Fatalf("a logo that isn't a web address: %q", e.Logo)
			}
		}
	})
}

func FuzzM3UHeaders(f *testing.F) {
	f.Add("#EXTVLCOPT:http-user-agent=a")
	f.Add("#EXTHTTP:{\"Referer\":\"b\"}")
	f.Add("#KODIPROP:x.stream_headers=User-Agent=%0d%0a")
	f.Fuzz(func(t *testing.T, line string) {
		ua, ref := m3uHeaders(line)
		_, _ = clipHeader(ua), clipHeader(ref)
		for _, h := range []string{clipHeader(ua), clipHeader(ref)} {
			if strings.ContainsAny(h, "\r\n\x00") {
				t.Fatalf("%q", h)
			}
		}
	})
}

func FuzzStreamURL(f *testing.F) {
	for _, s := range []string{"http://127.0.0.1/", "rtsp://[::1]:554/", "udp://@239.0.0.1:1234", "http://2130706433/", "file:///etc/passwd", "http://169.254.169.254/", "concat:a|b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if checkStreamURL(s, false) != nil {
			return
		}
		low := strings.ToLower(s)
		for _, bad := range []string{"file:", "concat:", "subfile:", "data:", "pipe:", "crypto:"} {
			if strings.HasPrefix(low, bad) {
				t.Fatalf("a %s address was taken for a channel: %q", bad, s)
			}
		}
	})
}

func FuzzHDHRReply(f *testing.F) {
	ok := func(body []byte) []byte {
		b := make([]byte, 4, 4+len(body)+4)
		binary.BigEndian.PutUint16(b, hdhrDiscoverRes)
		binary.BigEndian.PutUint16(b[2:], uint16(len(body)))
		b = append(b, body...)
		return binary.LittleEndian.AppendUint32(b, crc32.ChecksumIEEE(b))
	}
	f.Add(ok([]byte{0x02, 0x04, 0x12, 0x34, 0x56, 0x78, 0x10, 0x01, 0x02}))
	f.Add(ok([]byte{0x2a, 0x10, 'h', 't', 't', 'p', ':', '/', '/', '1', '0', '.', '0', '.', '0', '.', '9', '/'}))
	f.Add(bytes.Repeat([]byte{0xff}, 16))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseHDHRReply(b)
	})
}
