// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

package wittytuner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The server passes on to the service only what integration.json declares, so a path the web part
// asks for that isn't there is a 404 in a running server and nowhere else: it type-checks, builds
// and passes every other test (#12, #14). Every call the web part makes is checked here against
// the declared paths and methods.

// webCall is api.get/post/put/del/patch(…) whose address starts with T, the module's own prefix:
// T + '/path' or `${T}/path/${id}`.
var webCall = regexp.MustCompile("api\\.(get|post|put|del|delete|patch)\\s*(?:<[^(]*?>)?\\(\\s*(?:T\\s*\\+\\s*['\"`]([^'\"`]*)['\"`]|`\\$\\{T\\}([^`]*)`)")

func TestWebPartAsksOnlyForDeclaredPaths(t *testing.T) {
	var integ struct {
		HTTP []struct {
			Path    string   `json:"path"`
			Methods []string `json:"methods"`
		} `json:"http"`
	}
	b, err := os.ReadFile("integration.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &integ); err != nil {
		t.Fatal(err)
	}
	declared := func(method, path string) bool {
		want := strings.Split(strings.Trim(path, "/"), "/")
		for _, h := range integ.HTTP {
			have := strings.Split(strings.Trim(h.Path, "/"), "/")
			if len(have) != len(want) || !slices.Contains(h.Methods, method) {
				continue
			}
			ok := true
			for i := range have {
				wild := strings.HasPrefix(have[i], "{")
				// A part filled in by the page (${id}) needs a {name} there; a fixed part may be
				// either, as /tuners/check sits beside /tuners/{id}.
				if (want[i] == "{}" && !wild) || (want[i] != "{}" && !wild && want[i] != have[i]) {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
		return false
	}

	files, _ := filepath.Glob("web/app/*.ts*")
	more, _ := filepath.Glob("web/app/*/*.ts*")
	calls := 0
	for _, f := range append(files, more...) {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range webCall.FindAllStringSubmatch(string(src), -1) {
			method := strings.ToUpper(m[1])
			if method == "DEL" {
				method = "DELETE"
			}
			path := m[2] + m[3]
			path = regexp.MustCompile(`\$\{[^}]*\}`).ReplaceAllString(strings.SplitN(path, "?", 2)[0], "{}")
			calls++
			if !declared(method, path) {
				t.Errorf("%s: %s %s isn't in integration.json, so the server answers 404", f, method, path)
			}
		}
	}
	// Fewer than this means the pattern above stopped seeing the calls, not that they went.
	if calls < 30 {
		t.Errorf("only %d calls found in web/app; the pattern no longer matches how the web part calls the service", calls)
	}
}
