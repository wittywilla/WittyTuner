// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import "testing"

// What Go can't read as an address, FFmpeg's C library can, and it would be this machine.
func TestNamesThatAreReallyAddresses(t *testing.T) {
	withLookup(t, nil)
	for _, h := range []string{"0", "127.1", "2130706433", "0x7f000001", "0x7f.1", "0177.0.0.1", "017700000001", "2130706433.", "0X7F.1"} {
		if err := checkOpening("rtsp://"+h+"/x", false); err == nil {
			t.Errorf("%s went through", h)
		}
		if err := checkOpening("rtsp://"+h+"/x", true); err != nil {
			t.Errorf("%s from the owner: %v", h, err)
		}
	}
	// A name with no answer here leads nowhere by itself, and may go through a proxy.
	for _, h := range []string{"unknown.example", "stream.example", "a1.b2", "0x7f.example", "1.2.3.example"} {
		if err := checkOpening("http://"+h+"/x", false); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
}
