// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"testing"

	"github.com/wittywilla/wittymodule/sdui"
)

// A page of a source's rows is asked for with any offset and limit a caller sends: none may
// crash the helper, and a page holds no more than was asked for or than there is.
func FuzzPageItems(f *testing.F) {
	f.Add(-1, 10)
	f.Add(3, -1)
	f.Add(1<<62, 1<<62)
	rows := []sdui.Item{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	f.Fuzz(func(t *testing.T, offset, limit int) {
		a := pageItems(rows, offset, limit)
		if len(a.Items) > len(rows) || (limit > 0 && len(a.Items) > limit) || a.Total != len(rows) {
			t.Errorf("offset %d limit %d: %d items, total %d", offset, limit, len(a.Items), a.Total)
		}
	})
}
