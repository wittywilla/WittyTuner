package wittytuner

import (
	"context"
	"strings"
	"testing"
)

// A stream's address can hold a login, and this error reaches whoever tried to tune.
func TestAStreamThatCantBeReachedDoesntNameItself(t *testing.T) {
	addr := "http://user:SECRET@127.0.0.1:1/live/USERNAME/PASSWORD/1.ts?key=KEYVALUE"
	s, err := New("stream", Config{Name: "S", Options: map[string]string{"url": addr}}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := s.Channels(context.Background())
	if err != nil || len(ch) != 1 {
		t.Fatalf("%v %+v", err, ch)
	}
	_, err = s.Open(context.Background(), ch[0])
	if err == nil {
		t.Fatal("it opened")
	}
	for _, word := range []string{"SECRET", "USERNAME", "PASSWORD", "KEYVALUE", "/live/"} {
		if strings.Contains(err.Error(), word) {
			t.Errorf("%q is in %q", word, err)
		}
	}
	if !strings.Contains(err.Error(), "could not be reached") {
		t.Errorf("%v", err)
	}
}
