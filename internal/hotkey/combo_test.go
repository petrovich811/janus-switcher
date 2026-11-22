package hotkey

import "testing"

func TestParseNamedAndPunctuation(t *testing.T) {
	cases := []struct {
		in        string
		key       string
		accel     string
		hasMod    bool
		bindable  bool
		humanized string
	}{
		{"ctrl+shift+f10", "f10", "<Control><Shift>F10", true, true, "Ctrl+Shift+F10"},
		{"<Control><Shift>F10", "f10", "<Control><Shift>F10", true, true, "Ctrl+Shift+F10"},
		{"ctrl+alt+backslash", "backslash", "<Control><Alt>backslash", true, true, "Ctrl+Alt+\\"},
		{"ctrl+shift+[", "bracketleft", "<Control><Shift>bracketleft", true, true, "Ctrl+Shift+["},
		{"ctrl+;", "semicolon", "<Control>semicolon", true, true, "Ctrl+;"},
		{"ctrl+grave", "grave", "<Control>grave", true, true, "Ctrl+`"},
		{"ctrl+kp_0", "numpad0", "<Control>KP_0", true, true, "Ctrl+Num 0"},
		{"ctrl+numpadadd", "numpadadd", "<Control>KP_Add", true, true, "Ctrl+Num +"},
		{"f9", "f9", "F9", false, true, "F9"},
		{"pause", "pause", "Pause", false, true, "Pause/Break"},
	}
	for _, tc := range cases {
		c, err := Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if c.Key != tc.key {
			t.Errorf("Parse(%q).Key = %q, want %q", tc.in, c.Key, tc.key)
		}
		if c.HasModifier() != tc.hasMod {
			t.Errorf("Parse(%q).HasModifier = %v", tc.in, c.HasModifier())
		}
		if c.IsBindable() != tc.bindable {
			t.Errorf("Parse(%q).IsBindable = %v", tc.in, c.IsBindable())
		}
		accel, err := c.Accelerator()
		if err != nil || accel != tc.accel {
			t.Errorf("Parse(%q).Accelerator = %q, %v; want %q", tc.in, accel, err, tc.accel)
		}
		if got := c.String(); got != tc.humanized {
			t.Errorf("Parse(%q).String = %q, want %q", tc.in, got, tc.humanized)
		}
	}
}

func TestParseRejectsUnbindableAndUnknown(t *testing.T) {
	if c, err := Parse("backslash"); err != nil {
		t.Fatalf("bare punctuation should parse: %v", err)
	} else if c.IsBindable() {
		t.Error("bare punctuation must not be bindable")
	}
	if _, err := Parse("ctrl+неведомая"); err == nil {
		t.Error("unknown key must be rejected")
	}
	if _, err := Parse("ctrl+numpad42"); err == nil {
		t.Error("unknown keypad key must be rejected")
	}
}
