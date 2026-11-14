// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"os"

	"github.com/wittywilla/wittymodule"
)

// Lifecycle answers the server's lifecycle calls (integration.json). Taken off without keeping
// its data, WittyTuner's folder goes: its tuners, channels and guide sources. The server's own
// guide, recordings and sets stay the server's.
func Lifecycle(ctx context.Context, call string, args wittymodule.LifecycleArgs) error {
	if call == wittymodule.CallUninstall && !args.KeepData {
		appMu.Lock()
		m := shared
		shared = nil
		appMu.Unlock()
		dir := os.Getenv(DataEnv)
		if m != nil {
			m.Close()
			m.DB.Close()
			dir = m.Dir
		}
		if dir != "" {
			return os.RemoveAll(dir)
		}
	}
	return nil
}
