package tray

import (
	"encoding/binary"
	"testing"
)

func TestEncodeICO(t *testing.T) {
	data, err := EncodeICO(32)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 6+16+40 {
		t.Fatalf("icon too short: %d bytes", len(data))
	}
	if binary.LittleEndian.Uint16(data[0:]) != 0 {
		t.Error("reserved field must be zero")
	}
	if binary.LittleEndian.Uint16(data[2:]) != 1 {
		t.Error("type must be 1 (icon)")
	}
	if n := binary.LittleEndian.Uint16(data[4:]); n != 1 {
		t.Errorf("image count = %d, want 1", n)
	}
	if data[6] != 32 || data[7] != 32 {
		t.Errorf("width/height = %d/%d, want 32/32", data[6], data[7])
	}
	// image data size must match what the header claims
	size := binary.LittleEndian.Uint32(data[14:])
	offset := binary.LittleEndian.Uint32(data[18:])
	if int(offset)+int(size) != len(data) {
		t.Errorf("offset %d + size %d != file length %d", offset, size, len(data))
	}
	// BITMAPINFOHEADER: doubled height, 32 bpp
	dib := data[offset:]
	if h := int32(binary.LittleEndian.Uint32(dib[8:])); h != 64 {
		t.Errorf("dib height = %d, want 64", h)
	}
	if bpp := binary.LittleEndian.Uint16(dib[14:]); bpp != 32 {
		t.Errorf("bpp = %d, want 32", bpp)
	}
	wantLen := 40 + 32*32*4 + 4*32 // header + pixels + mask
	if len(dib) != wantLen {
		t.Errorf("dib length = %d, want %d", len(dib), wantLen)
	}
}

func TestEncodeICOAllSizes(t *testing.T) {
	for _, size := range []int{16, 22, 24, 32} {
		if _, err := EncodeICO(size); err != nil {
			t.Errorf("size %d: %v", size, err)
		}
	}
}
