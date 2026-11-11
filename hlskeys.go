// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"bytes"
	"strings"
)

// hlsAttr is one attribute of an HLS tag.
type hlsAttr struct {
	key   string // in capitals
	value string // without its quotes
}

// parseHLSAttrs reads KEY=value,KEY="value, with commas". A value can hold words that look like
// another attribute, such as IV="METHOD=NONE", so looking for those words in the line isn't enough.
func parseHLSAttrs(s string) []hlsAttr {
	var out []hlsAttr
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t,")
		if s == "" {
			break
		}
		i := 0
		for i < len(s) && s[i] != '=' && s[i] != ',' {
			i++
		}
		key := strings.ToUpper(strings.TrimSpace(s[:i]))
		if i == len(s) || s[i] == ',' {
			out = append(out, hlsAttr{key: key})
			s = s[i:]
			continue
		}
		j := i + 1
		var value string
		if j < len(s) && s[j] == '"' {
			end := strings.IndexByte(s[j+1:], '"')
			if end < 0 {
				value, j = s[j+1:], len(s)
			} else {
				value = s[j+1 : j+1+end]
				j += end + 2
			}
		} else {
			start := j
			for j < len(s) && s[j] != ',' {
				j++
			}
			value = s[start:j]
		}
		out = append(out, hlsAttr{key: key, value: value})
		s = s[j:]
	}
	return out
}

// hlsKeyProtection says why a key line means a stream can't be played, or "". Only METHOD=NONE, said
// plainly and with nothing else claiming otherwise, means no key.
func hlsKeyProtection(line string) string {
	_, rest, _ := strings.Cut(line, ":")
	none, encrypted, drm := false, false, false
	for _, a := range parseHLSAttrs(rest) {
		switch a.key {
		case "METHOD":
			switch strings.ToUpper(a.value) {
			case "NONE":
				none = true
			case "SAMPLE-AES", "SAMPLE-AES-CTR", "SAMPLE-AES-CENC":
				encrypted, drm = true, true
			default:
				encrypted = true
			}
		case "KEYFORMAT":
			drm = true
		}
	}
	switch {
	case none && !encrypted:
		return ""
	case drm:
		return "its HLS playlist uses DRM"
	}
	return "its HLS playlist is encrypted"
}

// hlsLines goes through a playlist line by line, however long a line is: a reader with a limit stops
// at the first one over it, and what comes after would go unread.
func hlsLines(b []byte, f func(l string) bool) {
	for line := range bytes.Lines(b) {
		if !f(string(bytes.TrimSpace(line))) {
			return
		}
	}
}

// hlsProtection says why an HLS playlist cannot be played, or "".
func hlsProtection(b []byte) string {
	why := ""
	hlsLines(b, func(l string) bool {
		if strings.HasPrefix(l, "#EXT-X-KEY:") || strings.HasPrefix(l, "#EXT-X-SESSION-KEY:") {
			why = hlsKeyProtection(l)
		}
		return why == ""
	})
	return why
}

// hlsVariants finds a master playlist's streams, and what its audio and subtitle
// renditions point at: any of them could be what FFmpeg reads.
func hlsVariants(b []byte) []string {
	next := false
	var out []string
	hlsLines(b, func(l string) bool {
		switch {
		case strings.HasPrefix(l, "#EXT-X-STREAM-INF"):
			next = true
		case strings.HasPrefix(l, "#EXT-X-MEDIA:"):
			for _, a := range parseHLSAttrs(strings.TrimPrefix(l, "#EXT-X-MEDIA:")) {
				if a.key == "URI" && a.value != "" {
					out = append(out, a.value)
				}
			}
		case next && l != "" && !strings.HasPrefix(l, "#"):
			out = append(out, l)
			next = false
		}
		return true
	})
	return out
}
