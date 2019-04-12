package imgutil

import (
	"image"
	"image/color"
)

// Scale is a scaling image.Image wrapper.
type Scale struct {
	Image image.Image
	Scale int
}

// ColorModel implements image.Image.
func (s *Scale) ColorModel() color.Model {
	return s.Image.ColorModel()
}

// Bounds implements image.Image.
func (s *Scale) Bounds() image.Rectangle {
	r := s.Image.Bounds()
	return image.Rect(r.Min.X*s.Scale, r.Min.Y*s.Scale,
		r.Max.X*s.Scale, r.Max.Y*s.Scale)
}

// At implements image.Image.
func (s *Scale) At(x, y int) color.Color {
	if x < 0 {
		x = x - s.Scale + 1
	}
	if y < 0 {
		y = y - s.Scale + 1
	}
	return s.Image.At(x/s.Scale, y/s.Scale)
}

// LeftRotate is a 90 degree rotating image.Image wrapper.
type LeftRotate struct {
	Image image.Image
}

// ColorModel implements image.Image.
func (lr *LeftRotate) ColorModel() color.Model {
	return lr.Image.ColorModel()
}

// Bounds implements image.Image.
func (lr *LeftRotate) Bounds() image.Rectangle {
	r := lr.Image.Bounds()
	// Min is inclusive, Max is exclusive.
	return image.Rect(r.Min.Y, -(r.Max.X - 1), r.Max.Y, -(r.Min.X - 1))
}

// At implements image.Image.
func (lr *LeftRotate) At(x, y int) color.Color {
	return lr.Image.At(-y, x)
}
