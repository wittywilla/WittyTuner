// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/wittywilla/wittymodule/sdui"
	core "github.com/wittywilla/wittymodule/sdui/core"
	"github.com/wittywilla/wittymodule/service"
	tuner "github.com/wittywilla/wittytuner"
	"github.com/wittywilla/wittytuner/ui"
)

// The sources behind WittyTuner's SDUI pages (sdui.json: wittytuner, wittytuner-tuner,
// wittytuner-setup), which every app draws, the Apple TV and Android TV apps among them. They are
// an administrator's: anyone else gets nothing. Every row that does something runs one of
// WittyTuner's own commands, on its own routes, which check again who is asking.

// SDUIItems answers the server's sdui.items call for WittyTuner's sources.
func SDUIItems(req sdui.ItemsRequest) (sdui.ItemsAnswer, error) {
	if req.Viewer.User <= 0 || !req.Viewer.Admin {
		return sdui.ItemsAnswer{}, nil
	}
	m, err := Get()
	if err != nil {
		return sdui.ItemsAnswer{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	rows, err := m.items(ctx, req)
	if err != nil {
		return sdui.ItemsAnswer{}, err
	}
	return pageItems(rows, req.Offset, req.Limit), nil
}

func srcStr(s sdui.Source, name string) string {
	switch v := s[name].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}

func pageItems(rows []sdui.Item, offset, limit int) sdui.ItemsAnswer {
	total := len(rows)
	if offset > total {
		offset = total
	}
	rows = rows[offset:]
	more := false
	if limit > 0 && len(rows) > limit {
		rows, more = rows[:limit], true
	}
	if rows == nil {
		rows = []sdui.Item{}
	}
	return sdui.ItemsAnswer{Items: rows, More: more, Total: total}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func command(op string, id int64) *sdui.Action {
	return &sdui.Action{Type: core.ActCommand, Op: op, Item: strconv.FormatInt(id, 10)}
}

func (m *Manager) items(ctx context.Context, req sdui.ItemsRequest) ([]sdui.Item, error) {
	var rows []sdui.Item
	switch req.Source.Type() {
	case ui.SourceTuners:
		list, err := m.Tuners(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range list {
			q := url.Values{"item": {strconv.FormatInt(t.ID, 10)}, "name": {t.Name}}
			sub := plural(t.Channels, "channel", "channels")
			if t.Encrypted > 0 {
				sub += ", " + strconv.Itoa(t.Encrypted) + " encrypted"
			}
			if t.LastError != "" {
				sub += ". " + sentence(t.LastError)
			}
			trailing := "On"
			if !t.Enabled {
				trailing = "Off"
			}
			rows = append(rows, sdui.Item{ID: "tuner-" + strconv.FormatInt(t.ID, 10), Kind: "view", Title: t.Name, Subtitle: sub, Trailing: trailing, Icon: "tv",
				Action: &sdui.Action{Type: core.ActNavigate, To: "/page/" + ui.PageTuner + "?" + q.Encode()}})
		}
	case ui.SourceFound:
		list := m.found(ctx)
		for _, f := range list {
			it := sdui.Item{ID: "found-" + foundKey(f), Kind: "view", Title: f.Name, Subtitle: f.Detail, Icon: "tv", Trailing: "Add",
				Action: &sdui.Action{Type: core.ActCommand, Op: ui.CommandAdd, Item: foundKey(f)}}
			rows = append(rows, it)
		}
	case ui.SourceGuides:
		list, err := m.Sources(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range list {
			sub := "Not read yet"
			if s.LastFetch != nil {
				sub = "Read " + time.Unix(*s.LastFetch, 0).Format("2 Jan 15:04") + ", " + plural(s.Programmes, "programme", "programmes")
			}
			if s.LastError != "" {
				sub += ". " + sentence(s.LastError)
			}
			rows = append(rows,
				sdui.Item{ID: "guide-" + strconv.FormatInt(s.ID, 10), Kind: "view", Title: s.Name, Subtitle: sub, Icon: "calendar", Trailing: "Read now",
					Action: command(ui.CommandGuideRefresh, s.ID)})
			if s.TunerID == nil { // a tuner's own goes with the tuner
				rows = append(rows, sdui.Item{ID: "guide-remove-" + strconv.FormatInt(s.ID, 10), Kind: "view", Title: "Remove " + s.Name, Icon: "warning",
					Action: &sdui.Action{Type: core.ActCommand, Op: ui.CommandGuideRemove, Item: strconv.FormatInt(s.ID, 10), Confirm: "Remove the guide source " + s.Name + "?"}})
			}
		}
	case ui.SourceTuner:
		id, _ := strconv.ParseInt(srcStr(req.Source, "tuner"), 10, 64)
		t, err := m.Tuner(ctx, id)
		if err != nil {
			return nil, nil
		}
		rows = m.tunerRows(t)
	case ui.SourceChannels:
		id, _ := strconv.ParseInt(srcStr(req.Source, "tuner"), 10, 64)
		list, err := m.Channels(ctx, id)
		if err != nil {
			return nil, nil
		}
		for _, c := range list {
			sub := ""
			switch {
			case c.Security == SecurityEncrypted:
				sub = tuner.EncryptedLabel
			case c.MergedInto != 0:
				sub = "A backup of another channel"
			case c.Health == HealthFailing:
				sub = "Didn't play when last tried"
				if c.HealthMsg != "" {
					sub += ": " + c.HealthMsg
				}
			case c.Radio:
				sub = "Radio"
			}
			op := ui.CommandChannelShow
			if c.Enabled {
				op = ui.CommandChannelHide
			}
			rows = append(rows, sdui.Item{ID: "ch-" + strconv.FormatInt(c.ID, 10), Kind: "view", Title: c.Number + "  " + c.Name, Subtitle: sub, Icon: "live",
				Control: &sdui.Control{Type: core.ControlSwitch, Value: c.Enabled}, Action: command(op, c.ID)})
		}
	case ui.SourceSetup:
		rows = []sdui.Item{
			{ID: "done", Kind: "view", Title: "Finish setting up", Subtitle: "Tuners can be added and changed at any time in Settings.", Icon: "check",
				Action: &sdui.Action{Type: core.ActCommand, Op: ui.CommandSetupDone}},
			{ID: "later", Kind: "view", Title: "Do this later", Subtitle: "It stays on Home until then.", Icon: "history",
				Action: &sdui.Action{Type: core.ActCommand, Op: ui.CommandSetupLater}},
		}
	}
	return rows, nil
}

// tunerRows are one tuner: how it is, then what can be done to it.
func (m *Manager) tunerRows(t *Tuner) []sdui.Item {
	sub := plural(t.Channels, "channel", "channels")
	if t.Encrypted > 0 {
		sub += ", " + strconv.Itoa(t.Encrypted) + " encrypted"
	}
	if t.LastError != "" {
		sub += ". " + sentence(t.LastError)
	}
	state := "On"
	if !t.Enabled {
		state = "Off"
	}
	rows := []sdui.Item{{ID: "state", Kind: "view", Title: t.Name, Subtitle: sub, Trailing: state, Icon: "tv"}}
	if sc := m.ScanProgress(t.ID); sc.Running {
		rows = append(rows, sdui.Item{ID: "scan", Kind: "view", Title: "Scanning for channels", Subtitle: plural(sc.Found, "channel", "channels") + " found so far", Trailing: strconv.Itoa(sc.Percent) + "%", Icon: "search"})
	} else {
		rows = append(rows, sdui.Item{ID: "refresh", Kind: "view", Title: "Read its channels again", Subtitle: "What was chosen for each channel stays.", Icon: "history", Action: command(ui.CommandRefresh, t.ID)})
		if d := m.driverOf(t.Driver); d != nil && d.Scans {
			rows = append(rows, sdui.Item{ID: "scan", Kind: "view", Title: "Scan for channels", Subtitle: "This can take several minutes, and uses the tuner while it runs.", Icon: "search", Action: command(ui.CommandScan, t.ID)})
		}
	}
	if h := m.Health(t.ID); h != nil && h.Running {
		rows = append(rows, sdui.Item{ID: "check", Kind: "view", Title: "Checking every channel", Subtitle: fmt.Sprintf("%d of %d: %d working, %d failing", h.Done, h.Total, h.OK, h.Failing), Icon: "history",
			Trailing: "Stop", Action: command(ui.CommandCheckStop, t.ID)})
	} else {
		sub := "Tunes each channel in turn, a few at a time, and keeps which ones work."
		if h != nil && h.Done > 0 {
			sub = fmt.Sprintf("Last time: %d working, %d failing.", h.OK, h.Failing)
		}
		rows = append(rows, sdui.Item{ID: "check", Kind: "view", Title: "Check every channel", Subtitle: sub, Icon: "check", Action: command(ui.CommandCheck, t.ID)})
	}
	op := ui.CommandOff
	if !t.Enabled {
		op = ui.CommandOn
	}
	rows = append(rows,
		sdui.Item{ID: "enabled", Kind: "view", Title: "In the guide", Subtitle: "Off: its channels leave the guide, and nothing is recorded from it.", Icon: "tv",
			Control: &sdui.Control{Type: core.ControlSwitch, Value: t.Enabled}, Action: command(op, t.ID)},
		sdui.Item{ID: "remove", Kind: "view", Title: "Remove", Subtitle: "Its channels and its own guide source go too.", Icon: "warning",
			Action: &sdui.Action{Type: core.ActCommand, Op: ui.CommandRemove, Item: strconv.FormatInt(t.ID, 10), Confirm: "Remove " + t.Name + " and its channels?"}})
	return rows
}

func (m *Manager) driverOf(id string) *tuner.Driver {
	list, _ := m.Drivers(context.Background())
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// --------------------------------------------------------------------------
// What a search found, to add with a press
// --------------------------------------------------------------------------

// foundLife is how long what a search found is kept, so a page that's open doesn't search again
// every time it's drawn.
const foundLife = 30 * time.Second

var (
	foundMu   sync.Mutex
	foundList []tuner.Found
	foundAt   time.Time
)

// foundKey names a tuner a search found, so a row can add it without carrying its settings.
func foundKey(f tuner.Found) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s\x00%s", f.Driver, f.Name)
	for _, k := range sortedKeys(f.Options) {
		fmt.Fprintf(h, "\x00%s=%s", k, f.Options[k])
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// found is what a search of this network finds, not counting what's added already.
func (m *Manager) found(ctx context.Context) []tuner.Found {
	foundMu.Lock()
	defer foundMu.Unlock()
	if time.Since(foundAt) < foundLife {
		return foundList
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	list, err := m.Discover(ctx)
	if err != nil {
		return nil
	}
	foundList, foundAt = list, time.Now()
	return list
}

// POST /found/{key}: adds a tuner a search found (the SDUI pages' Add).
func handleAddFound(m *Manager, w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var pick *tuner.Found
	for _, f := range m.found(r.Context()) {
		if foundKey(f) == key {
			pick = &f
			break
		}
	}
	if pick == nil {
		writeErr(w, 404, "That tuner isn't answering any more. Search again.")
		return
	}
	got, err := m.Create(r.Context(), &Tuner{Driver: pick.Driver, Name: pick.Name, Options: pick.Options, Enabled: true})
	if err != nil {
		tunerError(w, err)
		return
	}
	foundMu.Lock()
	foundAt = time.Time{} // it's added now
	foundMu.Unlock()
	got.Options = PublicOptions(got)
	writeJSON(w, 200, got)
}

// Busy says a stream, scan or channel check is going on, so the server leaves the service running.
func Busy() bool {
	appMu.Lock()
	m := shared
	appMu.Unlock()
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.slots) > 0 {
		return true
	}
	for _, s := range m.scans {
		if s.Running {
			return true
		}
	}
	for _, h := range m.health {
		if h.Running {
			return true
		}
	}
	return false
}

// Warm opens the database and starts the guide refresher as the service starts, so guide
// sources are read on their schedule whenever the server has the service running.
func Warm() {
	go func() {
		// The server says where its host API is just after the service starts listening.
		for range 60 {
			if service.Host().Host != "" {
				_, _ = Get()
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
	}()
}
