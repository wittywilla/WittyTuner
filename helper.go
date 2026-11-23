// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// CheckLocator says whether this source would use an address for a channel.
func (c checked) CheckLocator(locator string) error {
	if l, ok := c.Source.(interface{ checkLocator(string) error }); ok {
		return l.checkLocator(locator)
	}
	return errors.New("this kind of tuner takes no addresses from anywhere else")
}

func (t *netTuner) checkLocator(locator string) error {
	// It may ask the device what it calls itself, which takes a moment at most.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := t.streamURL(ctx, Channel{Locator: locator})
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

// A provider's channels stay on its own server, where the account is.
func (x *xtream) checkLocator(locator string) error {
	u, err := url.Parse(locator)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("that is not a stream address")
	}
	base, err := url.Parse(x.base)
	if err != nil {
		return err
	}
	if !strings.EqualFold(u.Host, base.Host) {
		return errors.New("streams stay on the provider's own server")
	}
	return checkStreamURL(locator, x.local)
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
