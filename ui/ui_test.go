// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package ui

import (
	"slices"
	"testing"

	"github.com/wittywilla/wittymodule"
	"github.com/wittywilla/wittymodule/sdui"
	core "github.com/wittywilla/wittymodule/sdui/core"
	"github.com/wittywilla/wittymodule/sdui/sduitest"
	"github.com/wittywilla/wittytuner"
)

// sdui.json is this definition, and it passes the kit's checks. Run with -update-sdui to
// write it again.
func TestSDUIFile(t *testing.T) {
	sduitest.Golden(t, UI(), "../sdui.json")
}

// The whole file, commands and all, as the server reads and checks it.
func TestServerReadsIt(t *testing.T) {
	b, err := sdui.JSON(UI())
	if err != nil {
		t.Fatal(err)
	}
	m, err := core.ParseModuleUI(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Commands) != len(Commands()) {
		t.Fatalf("%d commands read back, of %d", len(m.Commands), len(Commands()))
	}
	for _, is := range core.ValidateModuleUI(m, Module, core.ModuleChecks{ColorToken: func(s string) bool { return slices.Contains(sdui.ColorTokens, s) }}) {
		if is.Severity == "error" {
			t.Errorf("%s: %s", is.Path, is.Message)
		}
	}
}

// The file belongs to this module, and integration.json's setup step opens one of its pages.
func TestItIsThisModules(t *testing.T) {
	m, err := wittymodule.ParseManifest(wittytuner.ManifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != Module || UI().Module != m.ID || UI().Theme.Scope != m.ID {
		t.Errorf("manifest %q, sdui.json module %q, scope %q: all three must be the same", m.ID, UI().Module, UI().Theme.Scope)
	}
	in, err := wittymodule.ParseIntegration(wittytuner.IntegrationJSON)
	if err != nil {
		t.Fatal(err)
	}
	pages := map[string]bool{}
	for _, p := range UI().Pages {
		pages[p.ID] = true
	}
	for _, s := range in.Web.Setup {
		if s.Page != "" && !pages[s.Page] {
			t.Errorf("setup step %q opens %q, which isn't one of the pages", s.ID, s.Page)
		}
	}
}
