package convert

import (
	"strings"
	"unicode"
)

// Direction says which layout produced the text we are looking at.
type Direction int

const (
	// ToEN means the text looks Russian and must be re-typed as English.
	ToEN Direction = iota
	// ToRU means the text looks English and must be re-typed as Russian.
	ToRU
)

// Detect guesses the layout the text was typed in. Cyrillic and Latin letters
// are counted; ties and non-letters fall back to Latin (i.e. convert to RU),
// which is what a user typing English on a Russian layout expects.
func Detect(s string) Direction {
	var cyr, lat int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.Is(unicode.Latin, r):
			lat++
		}
	}
	if cyr > lat {
		return ToEN
	}
	return ToRU
}

// Layouts returns the source and target layouts for a direction.
func (d Direction) Layouts() (from, to LayoutID) {
	if d == ToEN {
		return LayoutRU, LayoutUS
	}
	return LayoutUS, LayoutRU
}

// Swap re-types s in the opposite layout, guessing the direction.
func Swap(s string) (string, Direction) {
	d := Detect(s)
	return SwapDir(s, d), d
}

// SwapDir re-types s from the given layout into the other one.
//
// Characters that exist on the source layout are mapped through their physical
// key, so that the result is exactly what the same keystrokes produce in the
// other layout. Characters the layout cannot produce (digits, spaces,
// punctuation shared by both layouts, emoji) are kept as they are.
func SwapDir(s string, d Direction) string {
	from, to := d.Layouts()
	return SwapBetween(s, from, to)
}

// SwapBetween converts s from the "from" layout to the "to" layout.
func SwapBetween(s string, from, to LayoutID) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		k, ok := KeyFor(from, r)
		if !ok {
			b.WriteRune(r)
			continue
		}
		if out, ok := RuneFor(to, k); ok {
			b.WriteRune(out)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// IsPrintableKey reports whether the conversion tables know this character.
func IsPrintableKey(l LayoutID, r rune) bool {
	_, ok := KeyFor(l, r)
	return ok
}
