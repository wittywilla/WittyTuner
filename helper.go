package wittytuner

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// What a helper process (cmd/wittytuner-module) and a server say to each other. Each
// question is a process of its own, and its answer comes as a stream: a stream of
// bytes for a channel, and for everything else JSON documents, because a list of
// channels can be longer than one line of the protocol and a scan reports as it goes.

// HelperCall is what a helper is asked: which tuner, since it keeps nothing, and what for.
type HelperCall struct {
	Driver  string            `json:"driver,omitempty"`
	Name    string            `json:"name,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Channel *Channel          `json:"channel,omitempty"` // "open" and "recheck"
	Source  string            `json:"source,omitempty"`  // "scan": ScanOptions.Source
	// What it can't find out for itself.
	FFmpeg        string `json:"ffmpeg,omitempty"`
	AllowCommands bool   `json:"allowCommands,omitempty"`
}

// HelperDoc is one document of an answer. Progress may come any number of times; any
// other document ends the answer.
type HelperDoc struct {
	Progress     *Progress   `json:"progress,omitempty"`
	Channels     []Channel   `json:"channels,omitempty"`
	Info         *Info       `json:"info,omitempty"`
	GuideHeaders http.Header `json:"guideHeaders,omitempty"`
	Found        []Found     `json:"found,omitempty"`
	Drivers      []Driver    `json:"drivers,omitempty"`
	Why          string      `json:"why,omitempty"`  // "recheck": why it's encrypted now
	Next         int64       `json:"next,omitempty"` // "recheck": seconds until it's worth asking again
	Error        string      `json:"error,omitempty"`
	Code         string      `json:"code,omitempty"`
	Done         bool        `json:"done,omitempty"`
}

// HelperOpen is the answer to "open" for a stream FFmpeg should open by itself. The server
// holds what it asks for to its own rules before FFmpeg sees any of it.
type HelperOpen struct {
	Input []string `json:"input,omitempty"`
	// Verified: it was inspected on opening and found clear. Recheck: it can change while it
	// plays, so it's worth asking again.
	Verified bool `json:"verified,omitempty"`
	Recheck  bool `json:"recheck,omitempty"`
}

// CheckLocator says whether this source would use an address for a channel. A helper's
// list of channels is only data, so the server asks its own copy.
func (c checked) CheckLocator(locator string) error {
	if l, ok := c.Source.(interface{ checkLocator(string) error }); ok {
		return l.checkLocator(locator)
	}
	return errors.New("this kind of tuner takes no addresses from anywhere else")
}

func (t *netTuner) checkLocator(locator string) error {
	_, err := t.streamURL(Channel{Locator: locator})
	return err
}

func (s *iptv) checkLocator(locator string) error {
	if s.single != "" {
		if locator != s.single {
			return errors.New("this channel has only one address")
		}
		return nil
	}
	return checkStreamURL(locator, s.localPlaylist())
}

func (b *backend) checkLocator(locator string) error {
	u, err := url.Parse(locator)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("that is not a stream address")
	}
	host := b.host
	if host == "" {
		base, err := url.Parse(b.base)
		if err != nil {
			return err
		}
		host = base.Hostname()
	}
	if !strings.EqualFold(u.Hostname(), host) {
		return errors.New("streams stay on the backend's own host")
	}
	return nil
}
