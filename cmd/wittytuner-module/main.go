// SPDX-License-Identifier: AGPL-3.0-or-later
//
// WittyTuner - Live TV and recording: network tuners, IPTV playlists, capture cards and guide data.
// Copyright (C) 2026 WittyWilla and contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

// Command wittytuner-module is WittyTuner as a service (module API 5), which the server starts by
// itself: its own routes (Settings, Live TV & tuners, the first-run step), its SDUI pages' sources,
// the guide sources it fetches on a schedule, and the channels the server's live TV, recordings
// and catch-up open through it. A server watches every stream for encryption itself.
package main

import (
	"net/http"

	"github.com/wittywilla/wittymodule/sdui"
	"github.com/wittywilla/wittymodule/service"
	"github.com/wittywilla/wittytuner"
	"github.com/wittywilla/wittytuner/api"
)

func main() {
	service.Main(service.Config{
		Manifest:  wittytuner.ManifestJSON,
		Busy:      api.Busy,
		OnStop:    api.Stop,
		UI:        sdui.Answers{Items: api.SDUIItems},
		HTTP:      api.Routes,
		Routes:    func(mux *http.ServeMux) { api.ServerRoutes(mux); api.Warm() },
		Lifecycle: api.Lifecycle,
	})
}
