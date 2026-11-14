// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package api

import (
	"context"
	"encoding/json"
	"fmt"
)

// Finding is one thing WittyTuner's own checks found, for the server's Repair page. The
// server asks with the service call doctor.Check; module.json lists the checks under
// capabilities.doctor.
type Finding struct {
	Check    string   `json:"check"`
	Key      string   `json:"key"`
	Severity string   `json:"severity"` // info | warn | problem
	Title    string   `json:"title"`
	What     string   `json:"what"`
	Steps    []string `json:"steps,omitempty"`
}

// DoctorCheck answers the service call doctor.Check.
func DoctorCheck(ctx context.Context, _ json.RawMessage, _ []json.RawMessage) ([]any, any, error) {
	m, err := Get()
	if err != nil {
		return nil, nil, err
	}
	return []any{m.Doctor(ctx)}, nil, nil
}

// Doctor reports tuners and playlists whose channels failed their last health check.
func (m *Manager) Doctor(ctx context.Context) []Finding {
	out := []Finding{}
	tuners, err := m.Tuners(ctx)
	if err != nil {
		return out
	}
	for _, t := range tuners {
		h := m.Health(t.ID)
		if h == nil || h.Done == 0 || h.Failing == 0 {
			continue
		}
		f := Finding{Check: "channels", Key: fmt.Sprintf("channels-%d", t.ID), Severity: "warn",
			Title: fmt.Sprintf("%d of %d channels from %s failed their health check", h.Failing, h.Done, t.Name),
			What:  "Those channels won't play until the source answers again. When a channel has a backup, the backup plays instead.",
			Steps: []string{"Open Live TV, Sources, and check the source's address and login, or whether your subscription has ended.",
				"Settings, WittyLog, Live TV area: the health check's lines say what each channel answered."}}
		if h.OK == 0 {
			f.Severity, f.Title = "problem", "Every channel checked from "+t.Name+" failed its health check"
		}
		out = append(out, f)
	}
	return out
}
