// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

//go:build !linux

package wittytuner

import (
	"context"
	"errors"
)

func setV4L2(string, int, float64) error { return errors.New("V4L2 is on Linux") }

func discoverV4L2(context.Context, Env) []Found { return nil }
