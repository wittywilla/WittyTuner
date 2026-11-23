// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package xmltv

import (
	"bytes"
	"compress/gzip"
	"testing"
)

// A guide comes from the internet, often gzipped. Reading one, whatever it holds, must not panic.
func FuzzParse(f *testing.F) {
	plain := []byte(`<?xml version="1.0"?><tv><channel id="a"><display-name>A</display-name><icon src="https://example.com/a.png"/></channel>` +
		`<programme start="20261123180000 +0100" stop="20261123190000 +0100" channel="a"><title>News</title><episode-num system="xmltv_ns">1.2.</episode-num></programme></tv>`)
	f.Add(plain)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(plain)
	_ = w.Close()
	f.Add(gz.Bytes())
	f.Add([]byte(`<!DOCTYPE tv [<!ENTITY a "aaaaaaaa">]><tv><programme start="x" channel="&a;"/></tv>`))
	f.Add([]byte{0x1f, 0x8b, 0, 0})
	f.Fuzz(func(t *testing.T, b []byte) {
		n := 0
		_ = Parse(bytes.NewReader(b), func(Channel) bool { n++; return n < 10000 }, func(Programme) bool { n++; return n < 10000 })
	})
}

func FuzzParseTime(f *testing.F) {
	for _, s := range []string{"20261123180000 +0100", "20261123180000", "2026112318", "", "99999999999999 -9999"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) { _, _ = ParseTime(s) })
}
