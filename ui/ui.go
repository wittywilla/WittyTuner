// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// Package ui is WittyTuner's server-driven UI (module API 5; WittyMediaServer docs/SDUI-MODULES.md):
// the tuner pages every app draws, the Apple TV and Android TV apps among them, which have no web
// view for the module's web part. They're for administrators: the tuners and guide sources, one
// tuner's channels, and the first-run step. Their sources are answered by WittyTuner's service
// (package api), and every row that does something runs one of WittyTuner's own commands, on its
// own routes, which check again who is asking. Typing a playlist's address or a password is a
// browser's (Settings, Live TV & tuners).
//
// The guide, live TV and recordings stay the server's own pages.
package ui

import (
	"github.com/wittywilla/wittymodule/sdui"
	core "github.com/wittywilla/wittymodule/sdui/core"
)

// Module is WittyTuner's ID.
const Module = "wittytuner"

// Pages.
const (
	// PageTuners is the tuners, what's on this network to add, and the guide sources.
	PageTuners = Module
	// PageTuner is one tuner (?item=<its id>&name=<its name>): what can be done to it, and its channels.
	PageTuner = Module + "-tuner"
	// PageSetup is the first-run step (integration.json setup "tv").
	PageSetup = Module + "-setup"
)

// Sources, answered by the service.
const (
	SourceTuners   = Module + ".tuners"   // every tuner, opening its page
	SourceFound    = Module + ".found"    // tuners on this network, each with Add
	SourceGuides   = Module + ".guides"   // guide sources, each with Read now and Remove
	SourceTuner    = Module + ".tuner"    // one tuner: how it is, and what can be done to it
	SourceChannels = Module + ".channels" // one tuner's channels, each a switch for the guide
	SourceSetup    = Module + ".setup"    // finishing the first-run step, or putting it off
)

// Commands: WittyTuner's own routes, for administrators.
const (
	CommandAdd          = Module + ".tuner.add"
	CommandRefresh      = Module + ".tuner.refresh"
	CommandScan         = Module + ".tuner.scan"
	CommandCheck        = Module + ".tuner.check"
	CommandCheckStop    = Module + ".tuner.checkStop"
	CommandOn           = Module + ".tuner.on"
	CommandOff          = Module + ".tuner.off"
	CommandRemove       = Module + ".tuner.remove"
	CommandChannelShow  = Module + ".channel.show"
	CommandChannelHide  = Module + ".channel.hide"
	CommandGuideRefresh = Module + ".guide.refresh"
	CommandGuideRemove  = Module + ".guide.remove"
	CommandSetupDone    = Module + ".setup.done"
	CommandSetupLater   = Module + ".setup.later"
)

// Commands are WittyTuner's own commands, as sdui.json declares them. Every one is an
// administrator's route.
func Commands() []core.CommandSpec {
	routes := "/api/modules/" + Module
	return []core.CommandSpec{
		{Op: CommandAdd, Label: "Add it", Method: "POST", Path: routes + "/found/{item}",
			Description: "Live TV, administrators: adds a tuner the search found on this network (item: what the search called it), and reads its channels."},
		{Op: CommandRefresh, Label: "Read its channels again", Method: "POST", Path: routes + "/tuners/{item}/refresh",
			Description: "Live TV, administrators: reads a tuner's channel list again (item: its ID). What was chosen for each channel stays."},
		{Op: CommandScan, Label: "Scan for channels", Method: "POST", Path: routes + "/tuners/{item}/scan", Body: `{}`,
			Description: "Live TV, administrators: has a tuner look for channels (item: its ID), which can take minutes; its page says how far it has got."},
		{Op: CommandCheck, Label: "Check every channel", Method: "POST", Path: routes + "/tuners/{item}/health",
			Description: "Live TV, administrators: tunes each of a tuner's channels in turn (item: its ID), a few at a time, and keeps which ones work."},
		{Op: CommandCheckStop, Label: "Stop checking", Method: "DELETE", Path: routes + "/tuners/{item}/health",
			Description: "Live TV, administrators: stops a tuner's channel check (item: its ID)."},
		{Op: CommandOn, Label: "Turn it on", Method: "PUT", Path: routes + "/tuners/{item}", Body: `{"enabled":true}`,
			Description: "Live TV, administrators: a tuner's channels are in the guide again (item: its ID)."},
		{Op: CommandOff, Label: "Turn it off", Method: "PUT", Path: routes + "/tuners/{item}", Body: `{"enabled":false}`,
			Description: "Live TV, administrators: a tuner's channels leave the guide, and nothing is recorded from it (item: its ID)."},
		{Op: CommandRemove, Label: "Remove", Method: "DELETE", Path: routes + "/tuners/{item}",
			Description: "Live TV, administrators: removes a tuner, its channels and its own guide source (item: its ID)."},
		{Op: CommandChannelShow, Label: "Show in the guide", Method: "PUT", Path: routes + "/channels/{item}", Body: `{"enabled":true}`,
			Description: "Live TV, administrators: a channel is in the guide (item: its ID)."},
		{Op: CommandChannelHide, Label: "Hide from the guide", Method: "PUT", Path: routes + "/channels/{item}", Body: `{"enabled":false}`,
			Description: "Live TV, administrators: a channel leaves the guide (item: its ID)."},
		{Op: CommandGuideRefresh, Label: "Read it now", Method: "POST", Path: routes + "/epg/sources/{item}/refresh",
			Description: "Live TV, administrators: reads a guide source again now, in the background (item: its ID)."},
		{Op: CommandGuideRemove, Label: "Remove", Method: "DELETE", Path: routes + "/epg/sources/{item}",
			Description: "Live TV, administrators: removes a guide source (item: its ID), and what the guide kept from it."},
		{Op: CommandSetupDone, Label: "Finish setting up", Method: "POST", Path: routes + "/setup/tv", Body: `{"skip":false}`,
			Description: "Administrators: WittyTuner's first-run step is done, so the server stops showing \"Finish setting up WittyTuner\"."},
		{Op: CommandSetupLater, Label: "Do this later", Method: "POST", Path: routes + "/setup/tv", Body: `{"skip":true}`,
			Description: "Administrators: WittyTuner's first-run step is put off; everything in it is in Settings, Live TV & tuners."},
	}
}

func sources() []sdui.SourceSpec {
	tunerParam := sdui.PropSpec{Name: "tuner", Type: "ref", Label: "Tuner", Required: true, Help: "The tuner's ID, $item on its page."}
	return []sdui.SourceSpec{
		{Type: SourceTuners, Label: "Tuners", Icon: "tv", Title: "Tuners and IPTV",
			Description: "For an administrator: every tuner, playlist and stream, with how many channels it has and how it is, opening its page. Nothing for anyone else.",
			Yields:      []string{"view"}, CardStyle: "tile"},
		{Type: SourceFound, Label: "Tuners on this network", Icon: "search", Title: "Found on this network",
			Description: "For an administrator: the tuners that answer a search of this server's network and machine, each with a row that adds it, so no address has to be typed. It looks for a few seconds.",
			Yields:      []string{"view"}, CardStyle: "tile"},
		{Type: SourceGuides, Label: "Guide sources", Icon: "calendar", Title: "Guide data",
			Description: "For an administrator: each XMLTV guide source, when it was last read and how many programmes it gave, with rows to read it now or remove it.",
			Yields:      []string{"view"}, CardStyle: "tile"},
		{Type: SourceTuner, Label: "A tuner", Icon: "tv", Title: "",
			Description: "For an administrator: one tuner, how it is (channels, encrypted ones, a scan or check under way), then what can be done: read its channels again, scan, check every channel, turn it on or off, remove it.",
			Params:      []sdui.PropSpec{tunerParam}, Yields: []string{"view"}, CardStyle: "tile"},
		{Type: SourceChannels, Label: "A tuner's channels", Icon: "live", Title: "Channels",
			Description: "For an administrator: one tuner's channels by number, each a switch for whether it's in the guide, with whether it's encrypted or failing.",
			Params:      []sdui.PropSpec{tunerParam, {Name: "limit", Type: "int", Label: "At most", Default: 200, Min: sdui.Int(1), Max: sdui.Int(2000)}},
			Yields:      []string{"view"}, CardStyle: "tile", Paged: true},
		{Type: SourceSetup, Label: "Finishing the first-run step", Icon: "check", Title: "",
			Description: "For an administrator: rows to finish WittyTuner's first-run step or put it off, which tell the server to stop showing \"Finish setting up WittyTuner\". Nothing for anyone else.",
			Yields:      []string{"view"}, CardStyle: "tile"},
	}
}

func withWhen(s sdui.Section, when *sdui.Condition) sdui.Section { s.When = when; return s }

// UI is the module's SDUI.
func UI() *sdui.Module {
	m := sdui.New(Module, "Live TV tuners")
	for _, s := range sources() {
		m.AddSource(s)
	}
	m.Commands = Commands()
	admin, notAdmin := true, false
	forAdmins, forOthers := &sdui.Condition{Admin: &admin}, &sdui.Condition{Admin: &notAdmin}
	list := func(id, title string, src sdui.Source, kv ...any) sdui.Section {
		return withWhen(sdui.Sec(id, "List", sdui.Props(append([]any{"title", title, "focusable", true}, kv...)...), src), forAdmins)
	}
	page := func(id, title string, sections ...sdui.Section) sdui.Page {
		return sdui.Page{ID: id, Kind: "custom", Title: title, Icon: "tv", Scope: Module, Sections: sections}
	}
	admins := withWhen(sdui.Notice("admins", "info", "Only an administrator adds and changes tuners."), forOthers)
	browser := withWhen(sdui.Notice("browser", "info", "A playlist's address, an IPTV provider's account and a single stream are added in Settings, Live TV & tuners, in a browser."), forAdmins)

	tuners := page(PageTuners, "Live TV & tuners", admins,
		withWhen(sdui.Sec("intro", "Text", sdui.Props("text", "Tuners, IPTV playlists and streams put their channels in the guide beside the ones made from your library. Encrypted channels are shown as encrypted and never decrypted."), nil), forAdmins),
		list("tuners", "Tuners and IPTV", sdui.Src(SourceTuners), "empty", "message", "emptyText", "None yet. Add one found on this network below, or in a browser."),
		list("found", "Found on this network", sdui.Src(SourceFound), "empty", "message", "emptyText", "Nothing answered. Check it's on this server's network, and that the firewall lets wittytuner-module use private networks. Or add it by its address, in a browser."),
		list("guides", "Guide data", sdui.Src(SourceGuides), "empty", "message", "emptyText", "No guide sources. A tuner or provider that has its own guide adds it by itself."),
		browser)
	tuners.Description = "For administrators, on every app: the tuners, IPTV playlists and streams, the tuners on this network to add with a press, and the guide sources."
	m.AddPage(tuners)

	one := page(PageTuner, "{title}", admins,
		list("tuner", "", sdui.Src(SourceTuner, "tuner", "$item"), "empty", "message", "emptyText", "This tuner isn't on this server."),
		list("channels", "Channels", sdui.Src(SourceChannels, "tuner", "$item"), "empty", "message", "emptyText", "No channels yet. Read its channels again, or scan."))
	one.Description = "For administrators: one tuner, what can be done to it, and its channels, each a switch for the guide. Opened with ?item=<its id>&name=<its name>."
	m.AddPage(one)

	setup := page(PageSetup, "Set up live TV", admins,
		withWhen(sdui.Sec("intro", "Text", sdui.Props("text", "Add a TV tuner, a provider's playlist or a single stream, and its channels sit in the guide beside the ones made from your library. No channel lists or stream addresses come with the server: bring your own."), nil), forAdmins),
		list("tuners", "Added", sdui.Src(SourceTuners), "empty", "message", "emptyText", "Nothing added yet."),
		list("found", "Found on this network", sdui.Src(SourceFound), "empty", "message", "emptyText", "Nothing answered. Check it's on this server's network, and that the firewall lets wittytuner-module use private networks. Or add it by its address, in a browser."),
		browser,
		list("finish", "", sdui.Src(SourceSetup)))
	setup.Description = "WittyTuner's first-run step on every app, for administrators: the server's \"Finish setting up WittyTuner\" notice on Home opens it."
	m.AddPage(setup)
	return m
}
