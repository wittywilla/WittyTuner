package wittytuner

import (
	"strings"
	"testing"
)

// A name that can't be looked up here may still be looked up by what opens it.
func TestANameThatWontResolveIsntCalledFine(t *testing.T) {
	withLookup(t, nil)
	if err := checkOpening("http://unknown.example/x", false); err == nil {
		t.Error("it went through")
	} else if strings.Contains(err.Error(), "point at this server") {
		t.Errorf("that isn't why: %v", err)
	}
	if err := checkOpening("http://unknown.example/x", true); err != nil {
		t.Errorf("the owner's own: %v", err)
	}
}
