// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// Xtream Codes-style panels: an IPTV provider's account, reached through the player API many
// providers run. The channel list comes as JSON rather than a playlist, which carries what a
// playlist often leaves out: categories, guide ids and how far back each channel's archive goes.
// Streams are opened as an IPTV playlist's are, under the same rules.

func init() {
	register(Driver{
		ID: "xtream", Name: "IPTV provider account (Xtream Codes API)", Status: "mock",
		Description: "Channels, guide and catch-up from an IPTV provider's account on an Xtream Codes-style panel. Bring your own account: none is included. Tried against a stand-in panel only.",
		Fields: []Field{
			{Key: "url", Label: "Server address", Kind: "url", Required: true, Help: "The provider's address, such as http://provider.example:8080."},
			{Key: "username", Label: "User name", Kind: "text", Required: true},
			{Key: "password", Label: "Password", Kind: "password", Required: true},
			{Key: "output", Label: "Stream format", Kind: "select", Default: "ts", Options: []string{"ts", "m3u8"}, Help: "MPEG-TS suits most providers; some only send HLS."},
			{Key: "userAgent", Label: "User agent", Kind: "text", Help: "Optional. A few providers ask for a particular one."},
		},
		build: func(c Config, e Env) (Source, error) {
			base, err := xtreamServer(c.Options["url"])
			if err != nil {
				return nil, err
			}
			ext := c.Options["output"]
			if ext != "ts" && ext != "m3u8" {
				return nil, errors.New("the stream format is ts or m3u8")
			}
			return &xtream{cfg: c, env: e, base: base, user: c.Options["username"], pass: c.Options["password"], ext: ext, local: LocalPlaylist(base)}, nil
		},
	})
}

// xtreamServer is a panel's address as the owner typed it, without what people often paste
// along with it: a trailing slash or the API's own file name.
func xtreamServer(raw string) (string, error) {
	addr := strings.TrimRight(strings.TrimSpace(raw), "/")
	for _, file := range []string{"/player_api.php", "/get.php", "/xmltv.php"} {
		addr = strings.TrimSuffix(addr, file)
	}
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" {
		return "", errors.New("the server address is an http:// or https:// address, such as http://provider.example:8080")
	}
	base := u.Scheme + "://" + u.Host + strings.TrimSuffix(u.EscapedPath(), "/")
	// The owner typed it, so it may be at home; never anywhere no stream may come from.
	if err := checkStreamURL(base, true); err != nil {
		return "", err
	}
	return base, nil
}

type xtream struct {
	cfg              Config
	env              Env
	base, user, pass string
	ext              string
	local            bool // the panel is on the owner's own network

	mu         sync.Mutex
	maxStreams int
}

func (x *xtream) Info() Info {
	x.mu.Lock()
	defer x.mu.Unlock()
	q := url.Values{"username": {x.user}, "password": {x.pass}}
	return Info{Driver: "xtream", Name: x.cfg.Name, Detail: x.base, MaxStreams: x.maxStreams, GuideURL: x.base + "/xmltv.php?" + q.Encode()}
}

// client is for the panel's API: one on the internet doesn't get to send it home.
func (x *xtream) client() *http.Client {
	if x.local {
		return x.env.http()
	}
	return noWayHome(x.env.http())
}

var errXtreamRefused = errors.New("the provider refused that username or password")

// api asks the panel one question. Its answer can hold the login, so nothing it says, and
// no address, goes into an error.
func (x *xtream) api(ctx context.Context, action string) (*http.Response, error) {
	q := url.Values{"username": {x.user}, "password": {x.pass}}
	if action != "" {
		q.Set("action", action)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", x.base+"/player_api.php?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("that server address can't be used")
	}
	if ua := x.cfg.Options["userAgent"]; ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	res, err := x.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("the provider could not be reached: %w", withoutAddress(err))
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		res.Body.Close()
		x.refused(ctx, "refused")
		return nil, errXtreamRefused
	case res.StatusCode != http.StatusOK:
		res.Body.Close()
		return nil, fmt.Errorf("the provider answered %s", res.Status)
	}
	return res, nil
}

const (
	maxXtreamAccount = 1 << 20
	// maxXtreamList is far more than the biggest channel list, which is read as it comes.
	maxXtreamList = 256 << 20
)

// refused says the provider turned the account away, so the owner sees why channels stopped. Only
// the tuner's name and why: never the login, nor the panel's address, which can carry it.
func (x *xtream) refused(ctx context.Context, why string) {
	x.env.logger().WarnContext(ctx, "the IPTV provider refused the account", "topic", "tuner", "tuner", x.cfg.Name, "why", why)
}

// account checks the login, and learns how many streams it may have at once.
func (x *xtream) account(ctx context.Context) error {
	res, err := x.api(ctx, "")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxXtreamAccount))
	if err != nil {
		return fmt.Errorf("the provider could not be reached: %w", withoutAddress(err))
	}
	// Panels answer a wrong login in several ways: with auth 0, with nothing, or with an
	// empty list.
	switch string(bytes.TrimSpace(body)) {
	case "", "[]", "{}", "null":
		x.refused(ctx, "refused")
		return errXtreamRefused
	}
	var acct struct {
		UserInfo *struct {
			Auth           flexInt    `json:"auth"`
			Status         flexString `json:"status"`
			MaxConnections flexInt    `json:"max_connections"`
		} `json:"user_info"`
	}
	if err := json.Unmarshal(body, &acct); err != nil {
		return errors.New("that address didn't answer as an Xtream Codes panel")
	}
	u := acct.UserInfo
	if u == nil || u.Auth == 0 {
		x.refused(ctx, "refused")
		return errXtreamRefused
	}
	switch st := strings.ToLower(string(u.Status)); st {
	case "expired":
		x.refused(ctx, st)
		return errors.New("the provider says this account has expired")
	case "banned":
		x.refused(ctx, st)
		return errors.New("the provider says this account is banned")
	case "disabled":
		x.refused(ctx, st)
		return errors.New("the provider says this account is disabled")
	}
	x.mu.Lock()
	x.maxStreams = max(int(u.MaxConnections), 0)
	x.mu.Unlock()
	return nil
}

type xtreamCategory struct {
	ID   flexString `json:"category_id"`
	Name flexString `json:"category_name"`
}

type xtreamStream struct {
	Num        flexString `json:"num"`
	Name       flexString `json:"name"`
	Type       flexString `json:"stream_type"`
	ID         flexString `json:"stream_id"`
	Icon       flexString `json:"stream_icon"`
	EPGID      flexString `json:"epg_channel_id"`
	Category   flexString `json:"category_id"`
	Archive    flexInt    `json:"tv_archive"`
	ArchiveFor flexInt    `json:"tv_archive_duration"`
}

func (x *xtream) categories(ctx context.Context) (map[string]string, error) {
	res, err := x.api(ctx, "get_live_categories")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var list []xtreamCategory
	// Category names only label channels, so a panel that sends something odd instead of a
	// list still gives its channels, without groups.
	if json.NewDecoder(io.LimitReader(res.Body, maxXtreamAccount*8)).Decode(&list) != nil {
		return nil, nil
	}
	out := make(map[string]string, len(list))
	for _, c := range list {
		if c.ID != "" {
			out[string(c.ID)] = strings.TrimSpace(string(c.Name))
		}
	}
	return out, nil
}

// streams reads the channel list one channel at a time, as it arrives: a big provider lists
// tens of thousands, and the whole answer is never held at once.
func (x *xtream) streams(ctx context.Context, each func(xtreamStream)) error {
	res, err := x.api(ctx, "get_live_streams")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	dec := json.NewDecoder(io.LimitReader(res.Body, maxXtreamList))
	tok, err := dec.Token()
	if err != nil {
		return errors.New("the provider sent no channel list")
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return errors.New("the provider sent no channel list")
	}
	for dec.More() {
		var s xtreamStream
		if err := dec.Decode(&s); err != nil {
			return errors.New("the provider's channel list was cut short or can't be read")
		}
		each(s)
	}
	if _, err := dec.Token(); err != nil {
		return errors.New("the provider's channel list was cut short or can't be read")
	}
	return nil
}

func (x *xtream) liveURL(id string) string {
	return x.base + "/live/" + url.PathEscape(x.user) + "/" + url.PathEscape(x.pass) + "/" + url.PathEscape(id) + "." + x.ext
}

func (x *xtream) Channels(ctx context.Context) ([]Channel, error) {
	if err := x.account(ctx); err != nil {
		return nil, err
	}
	groups, err := x.categories(ctx)
	if err != nil {
		return nil, err
	}
	var out []Channel
	err = x.streams(ctx, func(s xtreamStream) {
		id := strings.TrimSpace(string(s.ID))
		if id == "" {
			return
		}
		ch := Channel{
			ID:      id,
			Number:  firstNonEmpty(strings.TrimSpace(string(s.Num)), id),
			Name:    firstNonEmpty(strings.TrimSpace(string(s.Name)), id),
			Group:   groups[string(s.Category)],
			Logo:    webLogo(string(s.Icon)),
			EPGID:   strings.TrimSpace(string(s.EPGID)),
			Radio:   string(s.Type) == "radio_streams",
			Locator: x.liveURL(id),
		}
		if s.Archive == 1 && s.ArchiveFor > 0 {
			ch.Catchup, ch.CatchupDays = catchupXC, min(int(s.ArchiveFor), maxCatchupDays)
		}
		out = append(out, ch)
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("the provider lists no live channels for this account")
	}
	return out, nil
}

func (x *xtream) Scan(ctx context.Context, _ ScanOptions, p func(Progress)) ([]Channel, error) {
	return noScan(x, ctx, p)
}

func (x *xtream) Close() error { return nil }

// Open plays a channel as an IPTV playlist's channel is played, catch-up included.
func (x *xtream) Open(ctx context.Context, ch Channel) (*Stream, error) {
	return openAddress(ctx, x.env, ch, x.local, x.cfg.Options["userAgent"])
}

// flexString is a JSON value panels send as a string one day and a number the next, or null.
// Anything else is read as nothing, so one odd channel doesn't lose the list.
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0, b[0] == 'n', b[0] == '{', b[0] == '[':
		*f = ""
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flexString(s)
	default:
		*f = flexString(b) // a number or true/false, as written
	}
	return nil
}

// flexInt is a number panels send as a number or as a string holding one: 5, "5", "5.0".
// What isn't a number is 0.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	var s flexString
	if err := s.UnmarshalJSON(b); err != nil {
		return err
	}
	*f = 0
	if n, err := strconv.ParseFloat(strings.TrimSpace(string(s)), 64); err == nil && n >= -1<<53 && n <= 1<<53 {
		*f = flexInt(n)
	}
	return nil
}
