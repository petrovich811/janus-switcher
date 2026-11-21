package convert

import "testing"

// The pairs below follow the physical keys of the standard йцукен and qwerty
// layouts: ghbdtn is "привет", lhue is "друг", руддщ is "hello".
func TestSwap(t *testing.T) {
	cases := []struct {
		in, want string
		dir      Direction
	}{
		{"ghbdtn", "привет", ToRU},
		{"Ghbdtn", "Привет", ToRU},
		{"ghbdtn vjq lheu", "привет мой друг", ToRU},
		{"привет", "ghbdtn", ToEN},
		{"Привет", "Ghbdtn", ToEN},
		{"привет мой друг", "ghbdtn vjq lheu", ToEN},
		{"руддщ", "hello", ToEN},
		{"Ghbdtn!", "Привет!", ToRU},
		{"z", "я", ToRU},
		{"я", "z", ToEN},
		{"", "", ToRU},
		{"12345", "12345", ToRU},
		{"   ", "   ", ToRU},
	}
	for _, c := range cases {
		got, dir := Swap(c.in)
		if got != c.want {
			t.Errorf("Swap(%q) = %q, want %q", c.in, got, c.want)
		}
		if c.in != "" && dir != c.dir {
			t.Errorf("Swap(%q) direction = %v, want %v", c.in, dir, c.dir)
		}
	}
}

func TestSwapIsReversible(t *testing.T) {
	for _, s := range []string{
		"ghbdtn vjq lheu", "привет мой друг", "Ghbdtn, vjq!", "съешь же ещё этих мягких булок",
	} {
		once, dir := Swap(s)
		from, to := dir.Layouts()
		if back := SwapBetween(once, to, from); back != s {
			t.Errorf("round trip %q -> %q -> %q", s, once, back)
		}
	}
}

func TestKeyTablesAreComplete(t *testing.T) {
	// Every physical key that types something in one layout must type something
	// in the other, otherwise the flip would silently drop characters.
	for _, l := range []LayoutID{LayoutUS, LayoutRU} {
		for k := range keyToChar[l] {
			if _, ok := keyToChar[l.Other()][k]; !ok {
				t.Errorf("layout %s: key %+v missing from %s", l, k, l.Other())
			}
		}
	}
}

func TestDigitsAreStable(t *testing.T) {
	if got := SwapBetween("123", LayoutUS, LayoutRU); got != "123" {
		t.Errorf("digits changed: %q", got)
	}
}

func TestDirectionDetection(t *testing.T) {
	if Detect("привет") != ToEN {
		t.Error("Cyrillic text must convert to English")
	}
	if Detect("ghbdtn") != ToRU {
		t.Error("Latin text must convert to Russian")
	}
	if Detect("привет hello world") != ToRU {
		t.Error("more Latin than Cyrillic wins")
	}
}
