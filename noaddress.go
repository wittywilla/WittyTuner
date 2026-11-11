// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"errors"
	"net/url"
)

// withoutAddress is an HTTP client's error without the address it quotes: a stream's address can hold
// a login, and this error can reach whoever tried to tune, and the log.
func withoutAddress(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
