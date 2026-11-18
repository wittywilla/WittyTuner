// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// Package match finds a channel's entry in guide data: by its guide id, its name, its number and
// its call sign, forgiving the ways playlists and guides write them differently ("UK: BBC One HD"
// and "BBC One", "bbcone.uk" and "BBCOne.uk"). Every answer has a score from 0 to 100 saying how
// sure it is, and what it went by.
package match

import (
	"slices"
	"strings"
	"unicode"
)

// Entry is one channel of the guide.
type Entry struct {
	ID      string
	Name    string
	Aliases []string // its other display names: numbers and call signs are often among them
}

// Channel is what is known of a channel to match.
type Channel struct {
	Name     string
	Number   string
	CallSign string
	GuideID  string // what its source says, which may not be in the guide as written
}

// Result is a guide entry for a channel, how sure the match is, and what it went by:
// "id", "name", "callsign", "number" or "similar".
type Result struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Score int    `json:"score"`
	How   string `json:"how"`
}

// Sure is the score from which a match is made without asking, when nothing else comes close.
const Sure = 80

// commonToken: a word in more guide names than this says nothing about which one is meant.
const commonToken = 400

// Index is guide data ready to be searched many times.
type Index struct {
	entries []Entry
	byID    map[string]int
	byIDLow map[string][]int
	byName  map[string][]int // normalised name or alias
	byWord  map[string][]int
	words   [][]string // each entry's words, for similarity
}

// New indexes a guide's channels.
func New(entries []Entry) *Index {
	x := &Index{entries: entries, byID: map[string]int{}, byIDLow: map[string][]int{}, byName: map[string][]int{}, byWord: map[string][]int{},
		words: make([][]string, len(entries))}
	for i, e := range entries {
		x.byID[e.ID] = i
		lid := looseID(e.ID)
		x.byIDLow[lid] = append(x.byIDLow[lid], i)
		seenWord := map[string]bool{}
		for _, n := range append([]string{e.Name}, e.Aliases...) {
			if k := Key(n); k != "" {
				x.byName[k] = appendOnce(x.byName[k], i)
			}
			for _, w := range Words(n) {
				if !seenWord[w] {
					seenWord[w] = true
					x.byWord[w] = append(x.byWord[w], i)
				}
			}
		}
		x.words[i] = Words(e.Name)
	}
	return x
}

func appendOnce(l []int, i int) []int {
	if len(l) > 0 && l[len(l)-1] == i {
		return l
	}
	return append(l, i)
}

// Has says whether the guide has this id exactly.
func (x *Index) Has(id string) bool {
	_, ok := x.byID[id]
	return ok
}

// Entry is the guide's entry with this id.
func (x *Index) Entry(id string) (Entry, bool) {
	i, ok := x.byID[id]
	if !ok {
		return Entry{}, false
	}
	return x.entries[i], true
}

// Len is how many channels the guide has.
func (x *Index) Len() int { return len(x.entries) }

// Best lists the likeliest guide entries for a channel, best first, at most n.
func (x *Index) Best(c Channel, n int) []Result {
	score := map[int]Result{}
	put := func(i, s int, how string) {
		if r, ok := score[i]; !ok || s > r.Score {
			score[i] = Result{ID: x.entries[i].ID, Name: x.entries[i].Name, Score: s, How: how}
		}
	}
	if c.GuideID != "" {
		if i, ok := x.byID[c.GuideID]; ok {
			put(i, 100, "id")
		}
		for _, i := range x.byIDLow[looseID(c.GuideID)] {
			put(i, 95, "id")
		}
	}
	key := Key(c.Name)
	for _, i := range x.byName[key] {
		put(i, 90, "name")
	}
	if cs := Key(c.CallSign); cs != "" {
		for _, i := range x.byName[cs] {
			put(i, 85, "callsign")
		}
	}
	words := Words(c.Name)
	// The number, as a guide often lists it among a channel's names, says which of several
	// channels of a similar name is meant; by itself it says little.
	num := strings.TrimSpace(c.Number)
	var byNum []int
	if num != "" {
		byNum = x.byName[Key(num)]
	}
	// Names that share a telling word with this one, scored by how alike they are.
	cand := map[int]bool{}
	for _, w := range words {
		if l := x.byWord[w]; len(l) <= commonToken {
			for _, i := range l {
				cand[i] = true
			}
		}
	}
	for i := range cand {
		sim := similarity(words, x.words[i])
		if sim < 0.5 {
			continue
		}
		s := int(sim * 85)
		how := "similar"
		if slices.Contains(byNum, i) {
			s, how = min(s+10, 89), "number"
		}
		put(i, s, how)
	}
	for _, i := range byNum {
		if _, ok := score[i]; !ok && key == "" {
			put(i, 50, "number")
		}
	}
	out := make([]Result, 0, len(score))
	for _, r := range score {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b Result) int {
		if a.Score != b.Score {
			return b.Score - a.Score
		}
		return strings.Compare(a.Name, b.Name)
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// Pick is the match to make without asking: the best, when it is sure enough and the next is
// well behind (two equally good answers are for a person to choose between).
func Pick(list []Result) (Result, bool) {
	if len(list) == 0 || list[0].Score < Sure {
		return Result{}, false
	}
	if len(list) > 1 && list[1].Score >= list[0].Score-5 && list[1].ID != list[0].ID {
		return Result{}, false
	}
	return list[0], true
}

// Search lists guide entries whose names hold every word of a query, best first, at most n.
func (x *Index) Search(q string, n int) []Result {
	words := Words(q)
	lq := strings.ToLower(strings.TrimSpace(q))
	var out []Result
	for i, e := range x.entries {
		hay := strings.ToLower(e.ID + " " + e.Name + " " + strings.Join(e.Aliases, " "))
		ok := len(words) > 0 || lq != ""
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if len(words) == 0 && lq != "" && !strings.Contains(hay, lq) {
			ok = false
		}
		if !ok {
			continue
		}
		out = append(out, Result{ID: e.ID, Name: e.Name, Score: int(similarity(words, x.words[i]) * 100), How: "search"})
	}
	slices.SortStableFunc(out, func(a, b Result) int { return b.Score - a.Score })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// noise is what playlists add to a name that says nothing about the channel.
var noise = map[string]bool{"hd": true, "fhd": true, "uhd": true, "sd": true, "4k": true, "8k": true, "hevc": true, "h264": true, "h265": true,
	"1080p": true, "1080i": true, "720p": true, "576p": true, "480p": true, "50fps": true, "60fps": true, "tv": true, "channel": true, "the": true,
	"backup": true, "alt": true, "raw": true, "vip": true, "live": true}

// Words splits a name into the words that say which channel it is: lower case, letters and
// numbers only, without "HD", "1080p" and the like, and without a playlist's country prefix
// ("UK: ", "US | ", "[DE] ").
func Words(name string) []string {
	name = stripPrefix(name)
	var out []string
	for _, f := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+' }) {
		f = strings.Trim(f, "+")
		if f == "" || noise[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

// Key is a name with everything but its telling words taken out, for exact comparison.
func Key(name string) string { return strings.Join(Words(name), "") }

// stripPrefix takes a playlist's country or provider tag off the front of a name.
func stripPrefix(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") || strings.HasPrefix(s, "(") {
		if i := strings.IndexAny(s, "])"); i > 0 && i <= 6 {
			return strings.TrimSpace(s[i+1:])
		}
	}
	for _, sep := range []string{":", "|", " - "} {
		if i := strings.Index(s, sep); i >= 2 && i <= 4 && isTag(s[:i]) {
			return strings.TrimSpace(s[i+len(sep):])
		}
	}
	return s
}

func isTag(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 || len(s) > 4 {
		return false
	}
	for _, r := range s {
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// looseID is a guide id as guides and playlists disagree about writing it: case, and what
// follows an "@" ("bbcone.uk@HD").
func looseID(id string) string {
	id, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(id)), "@")
	return id
}

// similarity is how alike two names' words are, 0 to 1: the share of letter pairs they have in
// common, so "Sky Sports Main Event" and "Sky Sport Main Event" are close.
func similarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	pa, pb := pairs(strings.Join(a, " ")), pairs(strings.Join(b, " "))
	if len(pa) == 0 || len(pb) == 0 {
		if strings.Join(a, "") == strings.Join(b, "") {
			return 1
		}
		return 0
	}
	common := 0
	left := map[string]int{}
	for _, p := range pb {
		left[p]++
	}
	for _, p := range pa {
		if left[p] > 0 {
			left[p]--
			common++
		}
	}
	d := 2 * float64(common) / float64(len(pa)+len(pb))
	// Numbers must agree: "Channel 4" isn't "Channel 5", however alike the letters are.
	if !sameNumbers(a, b) {
		d *= 0.5
	}
	return d
}

func pairs(s string) []string {
	r := []rune(s)
	if len(r) < 2 {
		return nil
	}
	out := make([]string, 0, len(r)-1)
	for i := 0; i+1 < len(r); i++ {
		if r[i] == ' ' || r[i+1] == ' ' {
			continue
		}
		out = append(out, string(r[i:i+2]))
	}
	return out
}

func sameNumbers(a, b []string) bool {
	nums := func(l []string) string {
		var out []string
		for _, w := range l {
			if strings.IndexFunc(w, unicode.IsDigit) >= 0 {
				out = append(out, w)
			}
		}
		return strings.Join(out, " ")
	}
	return nums(a) == nums(b)
}
