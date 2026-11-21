package inject

// Physical key codes. The numbers are Linux evdev codes; that is the language
// the layout tables and the shortcuts speak. Windows translates them to
// virtual-key codes in keyboard_windows.go.
const (
	KeyEsc        uint16 = 1
	KeyTab        uint16 = 15
	KeyEnter      uint16 = 28
	KeyLeftCtrl   uint16 = 29
	KeyLeftShift  uint16 = 42
	KeyLeftAlt    uint16 = 56
	KeySpace      uint16 = 57
	KeyCapsLock   uint16 = 58
	KeyBackspace  uint16 = 14
	KeyInsert     uint16 = 110
	KeyDelete     uint16 = 111
	KeyHome       uint16 = 102
	KeyEnd        uint16 = 107
	KeyLeft       uint16 = 105
	KeyRight      uint16 = 106
	KeyUp         uint16 = 103
	KeyDown       uint16 = 108
	KeyPause      uint16 = 119
	KeyLeftMeta   uint16 = 125
	KeyRightCtrl  uint16 = 97
	KeyRightShift uint16 = 54
)
