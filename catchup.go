// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Catch-up: many IPTV sources keep what a channel showed for a few days, and play it back
// from an address of its own. Playlists say how to build that address in one of a handful
// of ways that players such as Kodi's IPTV Simple client made common, and those are the
// ones read here. The address built is held to every rule a live one is.

// timeNow is the clock archive requests are measured against; tests set their own.
var timeNow = time.Now

// The ways a source names its archive.
const (
	catchupDefault   = "default"   // CatchupSource is the whole address
	catchupAppend    = "append"    // CatchupSource goes on the end of the live address
	catchupShift     = "shift"     // the live address with utc= and lutc= added
	catchupFlussonic = "flussonic" // a Flussonic server's own archive paths
	catchupXC        = "xc"        // an Xtream Codes panel's /timeshift/ path
)

// maxCatchupDays is longer than any source keeps; a bigger number is a mistake.
const maxCatchupDays = 365

// maxArchiveSpan is longer than any programme, so a request for more is a mistake.
const maxArchiveSpan = 24 * time.Hour

// catchupMode is a playlist's word for its catch-up kind, as one of ours, or "" when it's
// one nothing here can play.
func catchupMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "default":
		return catchupDefault
	case "append":
		return catchupAppend
	case "shift", "timeshift":
		return catchupShift
	case "flussonic", "flussonic-hls", "flussonic-ts", "fs":
		return catchupFlussonic
	case "xc":
		return catchupXC
	}
	return ""
}

// m3uCatchup reads a playlist entry's catch-up attributes, falling back on those of the
// #EXTM3U line, which some playlists give once for every channel. A channel gets catch-up
// only when it's complete: a way of asking, a number of days, and a template where the
// way needs one. Half of one would only fail later, on a person pressing play.
func m3uCatchup(attrs, header map[string]string) (mode string, days int, source string) {
	pick := func(keys ...string) string {
		for _, m := range []map[string]string{attrs, header} {
			for _, k := range keys {
				if v := strings.TrimSpace(m[k]); v != "" {
					return v
				}
			}
		}
		return ""
	}
	raw := pick("catchup", "catchup-type")
	days, _ = strconv.Atoi(pick("catchup-days", "timeshift"))
	source = pick("catchup-source")
	mode = catchupMode(raw)
	if raw == "" && days > 0 {
		// Only a number of days: the old timeshift="7" form, which means shift unless a
		// template says otherwise.
		mode = catchupShift
		if source != "" {
			mode = catchupDefault
		}
	}
	days = min(days, maxCatchupDays)
	if mode == "" || days <= 0 || ((mode == catchupDefault || mode == catchupAppend) && source == "") {
		return "", 0, ""
	}
	return mode, days, source
}

// archiveSpan is the time an archive request asks for, once it's known the source keeps it.
// No end means up to now, as far as a day.
func archiveSpan(ch Channel, now time.Time) (start, end time.Time, err error) {
	if ch.Catchup == "" || ch.CatchupDays <= 0 {
		return start, end, errors.New("this channel has no catch-up: its source keeps nothing it has shown")
	}
	start = time.Unix(ch.ArchiveStart, 0)
	if start.After(now) {
		return start, end, errors.New("that hasn't been shown yet")
	}
	days := min(ch.CatchupDays, maxCatchupDays)
	if start.Before(now.Add(-time.Duration(days) * 24 * time.Hour)) {
		return start, end, fmt.Errorf("that's too long ago: this channel's source keeps the last %d %s", days, plural(days, "day", "days"))
	}
	// Up to now, or a day's worth when that's longer: no programme is.
	end = now
	if day := start.Add(maxArchiveSpan); day.Before(end) {
		end = day
	}
	if ch.ArchiveEnd != 0 {
		end = time.Unix(ch.ArchiveEnd, 0)
	}
	if !end.After(start) {
		return start, end, errors.New("the archive request ends before it starts")
	}
	if end.Sub(start) > maxArchiveSpan {
		return start, end, errors.New("the archive request is longer than a day")
	}
	return start, end, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ArchiveURL is the address of what a channel showed from ArchiveStart to ArchiveEnd, built
// the way its source says. It refuses a channel with no catch-up, and a time the source no
// longer keeps or hasn't shown yet.
func ArchiveURL(ch Channel, now time.Time) (string, error) {
	start, end, err := archiveSpan(ch, now)
	if err != nil {
		return "", err
	}
	t := archiveTimes{start: start.UTC(), end: end.UTC(), now: now.UTC()}
	live := ch.Locator
	switch ch.Catchup {
	case catchupDefault:
		return catchupTemplate(live, ch.CatchupSource, t)
	case catchupAppend:
		if ch.CatchupSource == "" {
			return "", errors.New("this channel's catch-up has no template")
		}
		tail, err := expandCatchup(ch.CatchupSource, t)
		if err != nil {
			return "", err
		}
		if strings.HasPrefix(tail, "?") && strings.Contains(live, "?") {
			tail = "&" + tail[1:]
		}
		return live + tail, nil
	case catchupShift:
		sep := "?"
		if strings.Contains(live, "?") {
			sep = "&"
		}
		return fmt.Sprintf("%s%sutc=%d&lutc=%d", live, sep, t.start.Unix(), t.now.Unix()), nil
	case catchupFlussonic:
		if ch.CatchupSource != "" {
			return catchupTemplate(live, ch.CatchupSource, t)
		}
		return flussonicArchive(live, t)
	case catchupXC:
		if ch.CatchupSource != "" {
			return catchupTemplate(live, ch.CatchupSource, t)
		}
		return xcArchive(live, t)
	}
	return "", fmt.Errorf("%q catch-up isn't a kind this can play", ch.Catchup)
}

type archiveTimes struct{ start, end, now time.Time }

func (t archiveTimes) duration() int64 { return int64(t.end.Sub(t.start) / time.Second) }

// catchupTemplate fills a template that is the whole address, or one relative to the live one.
func catchupTemplate(live, tmpl string, t archiveTimes) (string, error) {
	if tmpl == "" {
		return "", errors.New("this channel's catch-up has no template")
	}
	addr, err := expandCatchup(tmpl, t)
	if err != nil {
		return "", err
	}
	if base, err := url.Parse(live); err == nil && live != "" {
		if u, err := base.Parse(addr); err == nil {
			return u.String(), nil
		}
	}
	return addr, nil
}

var catchupToken = regexp.MustCompile(`\$\{([^{}]*)\}|\{([^{}]*)\}`)

// expandCatchup fills a template's placeholders, in both the {utc} and ${start} families.
// Times are in UTC. One it doesn't know is refused rather than sent to the source as it is.
func expandCatchup(tmpl string, t archiveTimes) (string, error) {
	var bad string
	out := catchupToken.ReplaceAllStringFunc(tmpl, func(tok string) string {
		m := catchupToken.FindStringSubmatch(tok)
		var v string
		var ok bool
		if strings.HasPrefix(tok, "$") {
			v, ok = dollarToken(m[1], t)
		} else {
			v, ok = braceToken(m[2], t)
		}
		if !ok && bad == "" {
			bad = tok
		}
		return v
	})
	if bad != "" {
		return "", fmt.Errorf("this channel's catch-up template has %s, which isn't known here", bad)
	}
	return out, nil
}

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

// dollarToken is one of ${start}, ${end}, ${now}, ${timestamp}, ${duration}, ${offset}, and
// ${(b)FORMAT} or ${(e)FORMAT} for the start or end written as yyyy, MM, dd, HH, mm and ss say.
func dollarToken(name string, t archiveTimes) (string, bool) {
	switch name {
	case "start":
		return unix(t.start), true
	case "end":
		return unix(t.end), true
	case "now", "timestamp":
		return unix(t.now), true
	case "duration":
		return strconv.FormatInt(t.duration(), 10), true
	case "offset":
		return strconv.FormatInt(int64(t.now.Sub(t.start)/time.Second), 10), true
	}
	if f, ok := strings.CutPrefix(name, "(b)"); ok {
		return longDate.Replace(f, t.start), true
	}
	if f, ok := strings.CutPrefix(name, "(e)"); ok {
		return longDate.Replace(f, t.end), true
	}
	return "", false
}

// braceToken is one of {utc}, {utcend}, {lutc}, {duration}, {offset}, {Y} {m} {d} {H} {M} {S}
// for the start, {duration:N} and {offset:N} in units of N seconds, and {utc:FORMAT},
// {utcend:FORMAT} or {lutc:FORMAT} written with the letters Y m d H M S.
func braceToken(name string, t archiveTimes) (string, bool) {
	name, arg, hasArg := strings.Cut(name, ":")
	switch name {
	case "utc", "start":
		if hasArg {
			return shortDate.Replace(arg, t.start), true
		}
		return unix(t.start), true
	case "utcend", "end":
		if hasArg {
			return shortDate.Replace(arg, t.end), true
		}
		return unix(t.end), true
	case "lutc", "now", "timestamp":
		if hasArg {
			return shortDate.Replace(arg, t.now), true
		}
		return unix(t.now), true
	case "duration", "offset":
		secs := t.duration()
		if name == "offset" {
			secs = int64(t.now.Sub(t.start) / time.Second)
		}
		if hasArg {
			div, err := strconv.ParseInt(arg, 10, 64)
			if err != nil || div <= 0 {
				return "", false
			}
			secs /= div
		}
		return strconv.FormatInt(secs, 10), true
	}
	if hasArg {
		return "", false
	}
	if v, ok := shortDate.part(name, t.start); ok {
		return v, true
	}
	return "", false
}

// dateWords writes a time in a format made of words such as yyyy and MM.
type dateWords []struct {
	word   string
	layout string
}

var (
	longDate  = dateWords{{"yyyy", "2006"}, {"MM", "01"}, {"dd", "02"}, {"HH", "15"}, {"mm", "04"}, {"ss", "05"}}
	shortDate = dateWords{{"Y", "2006"}, {"m", "01"}, {"d", "02"}, {"H", "15"}, {"M", "04"}, {"S", "05"}}
)

func (w dateWords) part(word string, t time.Time) (string, bool) {
	for _, d := range w {
		if d.word == word {
			return t.Format(d.layout), true
		}
	}
	return "", false
}

// Replace writes t into a format, leaving whatever isn't one of its words as it is.
func (w dateWords) Replace(format string, t time.Time) string {
	var b strings.Builder
	for i := 0; i < len(format); {
		matched := false
		for _, d := range w {
			if strings.HasPrefix(format[i:], d.word) {
				b.WriteString(t.Format(d.layout))
				i += len(d.word)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(format[i])
			i++
		}
	}
	return b.String()
}

// flussonicArchive turns a Flussonic live address into its archive: .../index.m3u8 becomes
// .../archive-START-DURATION.m3u8, .../NAME.m3u8 becomes .../NAME-START-DURATION.m3u8, and
// .../mpegts or .../NAME.ts becomes .../timeshift_abs-START.ts. A token in the query stays.
func flussonicArchive(live string, t archiveTimes) (string, error) {
	u, err := url.Parse(live)
	if err != nil || u.Host == "" {
		return "", errors.New("this channel's address isn't one Flussonic catch-up can follow")
	}
	p := u.EscapedPath()
	slash := strings.LastIndexByte(p, '/')
	if slash <= 0 {
		return "", errors.New("this channel's address isn't one Flussonic catch-up can follow")
	}
	dir, file := p[:slash], p[slash+1:]
	start, dur := t.start.Unix(), t.duration()
	switch {
	case file == "mpegts" || strings.HasSuffix(file, ".ts"):
		file = fmt.Sprintf("timeshift_abs-%d.ts", start)
	case strings.HasSuffix(file, ".m3u8"):
		name := strings.TrimSuffix(file, ".m3u8")
		if name == "index" || name == "" {
			name = "archive"
		}
		file = fmt.Sprintf("%s-%d-%d.m3u8", name, start, dur)
	default:
		return "", errors.New("this channel's address isn't one Flussonic catch-up can follow")
	}
	out := *u
	if err := setEscapedPath(&out, dir+"/"+file); err != nil {
		return "", err
	}
	return out.String(), nil
}

// xcArchive turns an Xtream Codes live address, .../live/USER/PASS/ID.ext or .../USER/PASS/ID,
// into its archive: .../timeshift/USER/PASS/MINUTES/YYYY-MM-DD:HH-MM/ID.ext, which is .ts
// unless the live one was HLS. Minutes are rounded up so the end isn't cut off.
func xcArchive(live string, t archiveTimes) (string, error) {
	u, err := url.Parse(live)
	if err != nil || u.Host == "" {
		return "", errors.New("this channel's address isn't an Xtream Codes one")
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) == 4 && parts[0] == "live" {
		parts = parts[1:]
	}
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", errors.New("this channel's address isn't an Xtream Codes one")
	}
	user, pass, file := parts[0], parts[1], parts[2]
	id, ext, _ := strings.Cut(file, ".")
	if id == "" {
		return "", errors.New("this channel's address isn't an Xtream Codes one")
	}
	if ext != "m3u8" {
		ext = "ts"
	}
	minutes := (t.duration() + 59) / 60
	p := fmt.Sprintf("/timeshift/%s/%s/%d/%s/%s.%s", user, pass, minutes, t.start.Format("2006-01-02:15-04"), id, ext)
	out := *u
	if err := setEscapedPath(&out, p); err != nil {
		return "", err
	}
	return out.String(), nil
}

// setEscapedPath sets a path that is already escaped, so a login with odd characters in it
// reaches the source as it came.
func setEscapedPath(u *url.URL, escaped string) error {
	p, err := url.PathUnescape(escaped)
	if err != nil {
		return errors.New("this channel's address can't be read")
	}
	u.Path, u.RawPath = p, escaped
	if u.EscapedPath() != escaped {
		u.RawPath = ""
	}
	return nil
}
