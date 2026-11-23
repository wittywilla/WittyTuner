// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package xmltv

import (
	"strings"
	"testing"
)

// A guide comes from whoever publishes it: no file may crash the reader or its time parser.
func FuzzParse(f *testing.F) {
	f.Add(`<tv><channel id="a"><display-name>One</display-name></channel><programme start="20261123010000 +0000" stop="20261123020000 +0000" channel="a"><title>News</title></programme></tv>`)
	f.Add(`<tv><programme start="20261123020000 -0500" stop="20261123010000" channel="b"><title/></programme>`)
	f.Add(`<!DOCTYPE tv [<!ENTITY a "aaaa">]><tv>&a;</tv>`)
	f.Fuzz(func(t *testing.T, s string) {
		_ = Parse(strings.NewReader(s), func(Channel) bool { return true }, func(Programme) bool { return true })
		ParseTime(s)
	})
}
