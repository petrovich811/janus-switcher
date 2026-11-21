// Package convert implements keyboard-layout text conversion (RU <-> EN).
package convert

// LayoutID identifies a keyboard layout.
type LayoutID string

const (
	LayoutUS LayoutID = "us"
	LayoutRU LayoutID = "ru"
)

// Key is a physical key on the keyboard: an evdev key code plus whether Shift
// must be held to produce the character sitting on that key.
type Key struct {
	Code  uint16
	Shift bool
}

// keyTrans maps an evdev key code to the [unshifted, shifted] characters it
// produces in a given layout. An empty string means the key produces nothing
// printable in that layout.
type keyTrans struct {
	code               uint16
	unshifted, shifted string
}

// evdev key codes (linux/input-event-codes.h)
const (
	kGRAVE = 41
	k1     = 2
	k2     = 3
	k3     = 4
	k4     = 5
	k5     = 6
	k6     = 7
	k7     = 8
	k8     = 9
	k9     = 10
	k0     = 11
	kMINUS = 12
	kEQUAL = 13
	kQ     = 16
	kW     = 17
	kE     = 18
	kR     = 19
	kT     = 20
	kY     = 21
	kU     = 22
	kI     = 23
	kO     = 24
	kP     = 25
	kLBR   = 26
	kRBR   = 27
	kA     = 30
	kS     = 31
	kD     = 32
	kF     = 33
	kG     = 34
	kH     = 35
	kJ     = 36
	kK     = 37
	kL     = 38
	kSEMI  = 39
	kAPOS  = 40
	kBKSL  = 43
	kZ     = 44
	kX     = 45
	kC     = 46
	kV     = 47
	kB     = 48
	kN     = 49
	kM     = 50
	kCOMMA = 51
	kDOT   = 52
	kSLASH = 53
)

// usTable is the US QWERTY layout.
var usTable = []keyTrans{
	{kGRAVE, "`", "~"},
	{k1, "1", "!"}, {k2, "2", "@"}, {k3, "3", "#"}, {k4, "4", "$"}, {k5, "5", "%"},
	{k6, "6", "^"}, {k7, "7", "&"}, {k8, "8", "*"}, {k9, "9", "("}, {k0, "0", ")"},
	{kMINUS, "-", "_"}, {kEQUAL, "=", "+"},
	{kQ, "q", "Q"}, {kW, "w", "W"}, {kE, "e", "E"}, {kR, "r", "R"}, {kT, "t", "T"},
	{kY, "y", "Y"}, {kU, "u", "U"}, {kI, "i", "I"}, {kO, "o", "O"}, {kP, "p", "P"},
	{kLBR, "[", "{"}, {kRBR, "]", "}"}, {kBKSL, "\\", "|"},
	{kA, "a", "A"}, {kS, "s", "S"}, {kD, "d", "D"}, {kF, "f", "F"}, {kG, "g", "G"},
	{kH, "h", "H"}, {kJ, "j", "J"}, {kK, "k", "K"}, {kL, "l", "L"},
	{kSEMI, ";", ":"}, {kAPOS, "'", "\""},
	{kZ, "z", "Z"}, {kX, "x", "X"}, {kC, "c", "C"}, {kV, "v", "V"}, {kB, "b", "B"},
	{kN, "n", "N"}, {kM, "m", "M"},
	{kCOMMA, ",", "<"}, {kDOT, ".", ">"}, {kSLASH, "/", "?"},
}

// ruTable is the standard Russian (winkey / ru) layout.
var ruTable = []keyTrans{
	{kGRAVE, "ё", "Ё"},
	{k1, "1", "!"}, {k2, "2", "\""}, {k3, "3", "№"}, {k4, "4", ";"}, {k5, "5", "%"},
	{k6, "6", ":"}, {k7, "7", "?"}, {k8, "8", "*"}, {k9, "9", "("}, {k0, "0", ")"},
	{kMINUS, "-", "_"}, {kEQUAL, "=", "+"},
	{kQ, "й", "Й"}, {kW, "ц", "Ц"}, {kE, "у", "У"}, {kR, "к", "К"}, {kT, "е", "Е"},
	{kY, "н", "Н"}, {kU, "г", "Г"}, {kI, "ш", "Ш"}, {kO, "щ", "Щ"}, {kP, "з", "З"},
	{kLBR, "х", "Х"}, {kRBR, "ъ", "Ъ"}, {kBKSL, "\\", "/"},
	{kA, "ф", "Ф"}, {kS, "ы", "Ы"}, {kD, "в", "В"}, {kF, "а", "А"}, {kG, "п", "П"},
	{kH, "р", "Р"}, {kJ, "о", "О"}, {kK, "л", "Л"}, {kL, "д", "Д"},
	{kSEMI, "ж", "Ж"}, {kAPOS, "э", "Э"},
	{kZ, "я", "Я"}, {kX, "ч", "Ч"}, {kC, "с", "С"}, {kV, "м", "М"}, {kB, "и", "И"},
	{kN, "т", "Т"}, {kM, "ь", "Ь"},
	{kCOMMA, "б", "Б"}, {kDOT, "ю", "Ю"}, {kSLASH, ".", ","},
}

func tableFor(l LayoutID) []keyTrans {
	if l == LayoutRU {
		return ruTable
	}
	return usTable
}

var (
	// charToKey[layout][rune] -> physical key
	charToKey = map[LayoutID]map[rune]Key{}
	// keyToChar[layout][key] -> rune
	keyToChar = map[LayoutID]map[Key]rune{}
)

func init() {
	for _, l := range []LayoutID{LayoutUS, LayoutRU} {
		c2k := map[rune]Key{}
		k2c := map[Key]rune{}
		for _, t := range tableFor(l) {
			if t.unshifted != "" {
				r := []rune(t.unshifted)[0]
				k := Key{Code: t.code, Shift: false}
				c2k[r] = k
				k2c[k] = r
			}
			if t.shifted != "" {
				r := []rune(t.shifted)[0]
				k := Key{Code: t.code, Shift: true}
				c2k[r] = k
				k2c[k] = r
			}
		}
		charToKey[l] = c2k
		keyToChar[l] = k2c
	}
}

// KeyFor reports the physical key that types r in layout l, if any.
func KeyFor(l LayoutID, r rune) (Key, bool) {
	k, ok := charToKey[l][r]
	return k, ok
}

// RuneFor reports the character that k types in layout l, if any.
func RuneFor(l LayoutID, k Key) (rune, bool) {
	r, ok := keyToChar[l][k]
	return r, ok
}

// Other returns the opposite layout.
func (l LayoutID) Other() LayoutID {
	if l == LayoutRU {
		return LayoutUS
	}
	return LayoutRU
}
