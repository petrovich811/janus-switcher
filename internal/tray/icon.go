package tray

// Pixmap is one SNI icon representation: ARGB32 pixels, row major.
type Pixmap struct {
	Width  int32
	Height int32
	Data   []byte
}

type canvas struct {
	w, h int
	px   []byte
}

func newCanvas(size int) *canvas {
	return &canvas{w: size, h: size, px: make([]byte, size*size*4)}
}

func (c *canvas) set(x, y int, r, g, b, a uint8) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h || a == 0 {
		return
	}
	i := (y*c.w + x) * 4
	c.px[i+0] = a
	c.px[i+1] = r
	c.px[i+2] = g
	c.px[i+3] = b
}

// blend mixes colour into the pixel with the given alpha.
func (c *canvas) blend(x, y int, r, g, b, a uint8) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	i := (y*c.w + x) * 4
	sa := float64(a) / 255.0
	dr := float64(c.px[i+1]) * (1 - sa)
	dg := float64(c.px[i+2]) * (1 - sa)
	db := float64(c.px[i+3]) * (1 - sa)
	c.px[i+0] = uint8(float64(c.px[i+0])*(1-sa) + 255*sa)
	c.px[i+1] = uint8(dr + float64(r)*sa)
	c.px[i+2] = uint8(dg + float64(g)*sa)
	c.px[i+3] = uint8(db + float64(b)*sa)
}

// roundedRect fills a rounded rectangle with (mostly) anti-aliased corners.
func (c *canvas) roundedRect(x0, y0, x1, y1 float64, radius float64, r, g, b uint8) {
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			cov := 1.0
			cx := float64(x) + 0.5
			cy := float64(y) + 0.5
			// distance to the nearest corner centre when outside the inner box
			dx, dy := 0.0, 0.0
			if cx < x0+radius {
				dx = (x0 + radius) - cx
			} else if cx > x1-radius {
				dx = cx - (x1 - radius)
			}
			if cy < y0+radius {
				dy = (y0 + radius) - cy
			} else if cy > y1-radius {
				dy = cy - (y1 - radius)
			}
			if dx > 0 && dy > 0 {
				d := (dx*dx + dy*dy)
				if d > radius*radius {
					continue
				}
				if d > (radius-1)*(radius-1) {
					cov = radius - sqrt(d)
					if cov < 0 {
						cov = 0
					}
					if cov > 1 {
						cov = 1
					}
				}
			}
			c.blend(x, y, r, g, b, uint8(cov*255))
		}
	}
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// thickLine draws a straight segment with round caps.
func (c *canvas) thickLine(x0, y0, x1, y1, width float64, r, g, b uint8) {
	steps := int(abs(x1-x0)+abs(y1-y0))*3 + 1
	rad := width / 2
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		cx := x0 + (x1-x0)*t
		cy := y0 + (y1-y0)*t
		for dy := -rad - 1.5; dy <= rad+1.5; dy++ {
			for dx := -rad - 1.5; dx <= rad+1.5; dx++ {
				d := sqrt(dx*dx + dy*dy)
				cov := rad - d + 0.5
				if cov <= 0 {
					continue
				}
				if cov > 1 {
					cov = 1
				}
				c.blend(int(cx+dx), int(cy+dy), r, g, b, uint8(cov*255))
			}
		}
	}
}

// triangle fills a triangle with a coverage estimate.
func (c *canvas) triangle(ax, ay, bx, by, cx, cy float64, r, g, b uint8) {
	minX := int(min3(ax, bx, cx)) - 2
	maxX := int(max3(ax, bx, cx)) + 2
	minY := int(min3(ay, by, cy)) - 2
	maxY := int(max3(ay, by, cy)) + 2
	area := (bx-ax)*(cy-ay) - (cx-ax)*(by-ay)
	if area == 0 {
		return
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px := float64(x) + 0.5
			py := float64(y) + 0.5
			w0 := ((bx-ax)*(py-ay) - (px-ax)*(by-ay)) / area
			w1 := ((px-ax)*(cy-ay) - (cx-ax)*(py-ay)) / area
			w2 := 1 - w0 - w1
			if w0 >= 0 && w1 >= 0 && w2 >= 0 {
				c.blend(x, y, r, g, b, 255)
			}
		}
	}
}

func min3(a, b, c float64) float64 {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func max3(a, b, c float64) float64 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

// RenderIcon draws the tray icon at the given pixel size: a blue rounded square
// with two opposite arrows, the usual "swap" mark.
func RenderIcon(size int) Pixmap {
	c := newCanvas(size)
	s := float64(size)
	pad := s * 0.03
	c.roundedRect(pad, pad, s-pad, s-pad, s*0.22, 0x35, 0x84, 0xe4)

	white := func(x0, y0, x1, y1, w float64) {
		c.thickLine(x0*s, y0*s, x1*s, y1*s, w*s, 0xff, 0xff, 0xff)
	}
	// top arrow pointing right
	white(0.26, 0.36, 0.72, 0.36, 0.085)
	c.triangle(0.72*s, 0.27*s, 0.72*s, 0.45*s, 0.86*s, 0.36*s, 0xff, 0xff, 0xff)
	// bottom arrow pointing left
	white(0.74, 0.64, 0.28, 0.64, 0.085)
	c.triangle(0.28*s, 0.55*s, 0.28*s, 0.73*s, 0.14*s, 0.64*s, 0xff, 0xff, 0xff)
	return Pixmap{Width: int32(size), Height: int32(size), Data: c.px}
}

// Icons returns the representations a tray is likely to pick from.
func Icons() []Pixmap {
	return []Pixmap{RenderIcon(32), RenderIcon(24), RenderIcon(22), RenderIcon(16)}
}
