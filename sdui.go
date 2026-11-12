// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import "github.com/wittywilla/wittymodule/sdui"

// ModuleID is this module's ID, the same as module.json's.
const ModuleID = "wittytuner"

// UI is WittyTuner's part of every app (module API 4, WittyMediaServer docs/SDUI-MODULES.md),
// written to sdui.json beside module.json. It is only the theme scope, on purpose:
//
//   - The live TV pages, the guide, recordings and the tuner settings are the server's own,
//     so a page or menu entry here would only show them twice.
//   - The helper reads no settings of its own: everything about a tuner comes with each
//     call, from the server's Channels & tuners settings.
//   - It answers no data source. A tuner isn't any kind of item a card shows, discovery
//     takes seconds and keeps nothing, and the tuners it finds are for the admin adding
//     one in the server's settings, not for every profile's pages.
//
// The scope still names the module for the theme editor, so an admin can colour anything
// that later carries it.
func UI() *sdui.Module {
	return sdui.New(ModuleID, "Live TV tuners")
}
