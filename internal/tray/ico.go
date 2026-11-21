package tray

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

// EncodeICO builds a Windows .ico file from the tray artwork. Each size becomes
// one image: a 32-bit bottom-up bitmap followed by a 1-bit transparency mask,
// which is the layout LoadImage expects.
func EncodeICO(size int) ([]byte, error) {
	img := RenderIcon(size)
	if int(img.Width) != size || int(img.Height) != size {
		return nil, fmt.Errorf("icon %dx%d does not match requested size %d", img.Width, img.Height, size)
	}
	dib := encodeDIB(img)

	var out bytes.Buffer
	// ICONDIR
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // type: icon
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // one image
	// ICONDIRENTRY
	dim := byte(size)
	if size >= 256 {
		dim = 0
	}
	out.WriteByte(dim)                                      // width
	out.WriteByte(dim)                                      // height
	out.WriteByte(0)                                        // colour count (0 for 32bpp)
	out.WriteByte(0)                                        // reserved
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
	_ = binary.Write(&out, binary.LittleEndian, uint16(32)) // bits per pixel
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(dib)))
	_ = binary.Write(&out, binary.LittleEndian, uint32(6+16))
	out.Write(dib)
	return out.Bytes(), nil
}

// encodeDIB writes BITMAPINFOHEADER + BGRA pixels (bottom-up) + AND mask.
func encodeDIB(img Pixmap) []byte {
	w, h := int(img.Width), int(img.Height)
	var out bytes.Buffer

	// BITMAPINFOHEADER: height is doubled because the mask follows the pixels.
	_ = binary.Write(&out, binary.LittleEndian, uint32(40))
	_ = binary.Write(&out, binary.LittleEndian, int32(w))
	_ = binary.Write(&out, binary.LittleEndian, int32(h*2))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
	_ = binary.Write(&out, binary.LittleEndian, uint16(32)) // bpp
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))  // BI_RGB
	_ = binary.Write(&out, binary.LittleEndian, uint32(w*h*4))
	_ = binary.Write(&out, binary.LittleEndian, int32(0)) // x pixels per metre
	_ = binary.Write(&out, binary.LittleEndian, int32(0)) // y pixels per metre
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))
	_ = binary.Write(&out, binary.LittleEndian, uint32(0))

	// Pixels: BGRA, rows from the bottom up.
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			a, r, g2, b := img.Data[i], img.Data[i+1], img.Data[i+2], img.Data[i+3]
			out.Write([]byte{b, g2, r, a})
		}
	}

	// AND mask: 1 bit per pixel, MSB first, rows padded to 4 bytes. Zero means
	// "use the alpha channel", so the mask is all zeros.
	rowBytes := ((w + 31) / 32) * 4
	mask := make([]byte, rowBytes*h)
	out.Write(mask)
	return out.Bytes()
}

// WriteICO writes the icon to path.
func WriteICO(path string, size int) error {
	data, err := EncodeICO(size)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
