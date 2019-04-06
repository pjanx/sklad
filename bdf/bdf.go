package bdf

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"strconv"
)

// glyph is a singular bitmap glyph to be used as a mask, assumed to directly
// correspond to a rune. A zero value is also valid and drawable.
type glyph struct {
	// Coordinates are relative to the origin, on the baseline.
	// The ascent is thus negative, unlike the usual model.
	bounds  image.Rectangle
	bitmap  []byte
	advance int
}

// ColorModel implements image.Image.
func (g *glyph) ColorModel() color.Model { return color.Alpha16Model }

// Bounds implements image.Image.
func (g *glyph) Bounds() image.Rectangle { return g.bounds }

// At implements image.Image. This is going to be somewhat slow.
func (g *glyph) At(x, y int) color.Color {
	x -= g.bounds.Min.X
	y -= g.bounds.Min.Y

	dx, dy := g.bounds.Dx(), g.bounds.Dy()
	if x < 0 || y < 0 || x >= dx || y >= dy {
		return color.Transparent
	}

	stride, offset, bit := (dx+7)/8, x/8, byte(1<<uint(7-x%8))
	if g.bitmap[y*stride+offset]&bit == 0 {
		return color.Transparent
	}
	return color.Opaque
}

// -----------------------------------------------------------------------------

// Font represents a particular bitmap font.
type Font struct {
	Name     string
	glyphs   map[rune]glyph
	fallback glyph
}

// FindGlyph returns the best glyph to use for the given rune.
// The returned boolean indicates whether a fallback has been used.
func (f *Font) FindGlyph(r rune) (glyph, bool) {
	if g, ok := f.glyphs[r]; ok {
		return g, true
	}
	return f.fallback, false
}

// DrawString draws the specified text string onto dst horizontally along
// the baseline starting at dp, using black color.
func (f *Font) DrawString(dst draw.Image, dp image.Point, s string) {
	for _, r := range s {
		g, _ := f.FindGlyph(r)
		draw.DrawMask(dst, g.bounds.Add(dp),
			image.Black, image.ZP, &g, g.bounds.Min, draw.Over)
		dp.X += g.advance
	}
}

// BoundString measures the text's bounds when drawn along the X axis
// for the baseline. Also returns the total advance.
func (f *Font) BoundString(s string) (image.Rectangle, int) {
	var (
		bounds image.Rectangle
		dot    image.Point
	)
	for _, r := range s {
		g, _ := f.FindGlyph(r)
		bounds = bounds.Union(g.bounds.Add(dot))
		dot.X += g.advance
	}
	return bounds, dot.X
}

// -----------------------------------------------------------------------------

func latin1ToUTF8(latin1 []byte) string {
	buf := make([]rune, len(latin1))
	for i, b := range latin1 {
		buf[i] = rune(b)
	}
	return string(buf)
}

// tokenize splits a BDF line into tokens. Quoted strings may start anywhere
// on the line. We only enforce that they must end somewhere.
func tokenize(s string) (tokens []string, err error) {
	token, quotes, escape := []rune{}, false, false
	for _, r := range s {
		switch {
		case escape:
			switch r {
			case '"':
				escape = false
				token = append(token, r)
			case ' ', '\t':
				quotes, escape = false, false
				tokens = append(tokens, string(token))
				token = nil
			default:
				quotes, escape = false, false
				token = append(token, r)
			}
		case quotes:
			switch r {
			case '"':
				escape = true
			default:
				token = append(token, r)
			}
		default:
			switch r {
			case '"':
				// We could also enable quote processing on demand,
				// so that it is only turned on in properties.
				if len(tokens) < 1 || tokens[0] != "COMMENT" {
					quotes = true
				} else {
					token = append(token, r)
				}
			case ' ', '\t':
				if len(token) > 0 {
					tokens = append(tokens, string(token))
					token = nil
				}
			default:
				token = append(token, r)
			}
		}
	}
	if quotes && !escape {
		return nil, fmt.Errorf("strings may not contain newlines")
	}
	if quotes || len(token) > 0 {
		tokens = append(tokens, string(token))
	}
	return tokens, nil
}

// -----------------------------------------------------------------------------

// bdfParser is a basic and rather lenient parser of
// Bitmap Distribution Format (BDF) files.
type bdfParser struct {
	scanner *bufio.Scanner // input reader
	line    int            // current line number
	tokens  []string       // tokens on the current line
	font    *Font          // glyph storage

	defaultBounds  image.Rectangle
	defaultAdvance int
	defaultChar    int
}

// readLine reads the next line and splits it into tokens.
// Panics on error, returns false if the end of file has been reached normally.
func (p *bdfParser) readLine() bool {
	p.line++
	if !p.scanner.Scan() {
		if err := p.scanner.Err(); err != nil {
			panic(err)
		}
		p.line--
		return false
	}

	var err error
	if p.tokens, err = tokenize(latin1ToUTF8(p.scanner.Bytes())); err != nil {
		panic(err)
	}

	// Eh, it would be nicer iteratively, this may overrun the stack.
	if len(p.tokens) == 0 {
		return p.readLine()
	}
	return true
}

func (p *bdfParser) readCharEncoding() int {
	if len(p.tokens) < 2 {
		panic("insufficient arguments")
	}
	if i, err := strconv.Atoi(p.tokens[1]); err != nil {
		panic(err)
	} else {
		return i // Some fonts even use -1 for things outside the encoding.
	}
}

func (p *bdfParser) parseProperties() {
	// The wording in the specification suggests that the argument
	// with the number of properties to follow isn't reliable.
	for p.readLine() && p.tokens[0] != "ENDPROPERTIES" {
		switch p.tokens[0] {
		case "DEFAULT_CHAR":
			p.defaultChar = p.readCharEncoding()
		}
	}
}

// XXX: Ignoring vertical advance since we only expect purely horizontal fonts.
func (p *bdfParser) readDwidth() int {
	if len(p.tokens) < 2 {
		panic("insufficient arguments")
	}
	if i, err := strconv.Atoi(p.tokens[1]); err != nil {
		panic(err)
	} else {
		return i
	}
}

func (p *bdfParser) readBBX() image.Rectangle {
	if len(p.tokens) < 5 {
		panic("insufficient arguments")
	}
	w, e1 := strconv.Atoi(p.tokens[1])
	h, e2 := strconv.Atoi(p.tokens[2])
	x, e3 := strconv.Atoi(p.tokens[3])
	y, e4 := strconv.Atoi(p.tokens[4])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		panic("invalid arguments")
	}
	if w < 0 || h < 0 {
		panic("bounding boxes may not have negative dimensions")
	}
	return image.Rectangle{
		Min: image.Point{x, -(y + h)},
		Max: image.Point{x + w, -y},
	}
}

func (p *bdfParser) parseChar() {
	g := glyph{bounds: p.defaultBounds, advance: p.defaultAdvance}
	bitmap, rows, encoding := false, 0, -1
	for p.readLine() && p.tokens[0] != "ENDCHAR" {
		if bitmap {
			b, err := hex.DecodeString(p.tokens[0])
			if err != nil {
				panic(err)
			}
			if len(b) != (g.bounds.Dx()+7)/8 {
				panic("invalid bitmap data, width mismatch")
			}
			g.bitmap = append(g.bitmap, b...)
			rows++
		} else {
			switch p.tokens[0] {
			case "ENCODING":
				encoding = p.readCharEncoding()
			case "DWIDTH":
				g.advance = p.readDwidth()
			case "BBX":
				g.bounds = p.readBBX()
			case "BITMAP":
				bitmap = true
			}
		}
	}
	if rows != g.bounds.Dy() {
		panic("invalid bitmap data, height mismatch")
	}

	// XXX: We don't try to convert encodings, since we'd need x/text/encoding
	// for the conversion tables, though most fonts are at least going to use
	// supersets of ASCII. Use ISO10646-1 X11 fonts for proper Unicode support.
	if encoding >= 0 {
		p.font.glyphs[rune(encoding)] = g
	}
	if encoding == p.defaultChar {
		p.font.fallback = g
	}
}

// https://en.wikipedia.org/wiki/Glyph_Bitmap_Distribution_Format
// https://www.adobe.com/content/dam/acom/en/devnet/font/pdfs/5005.BDF_Spec.pdf
func (p *bdfParser) parse() {
	if !p.readLine() || len(p.tokens) != 2 || p.tokens[0] != "STARTFONT" {
		panic("invalid header")
	}
	if p.tokens[1] != "2.1" && p.tokens[1] != "2.2" {
		panic("unsupported version number")
	}
	for p.readLine() && p.tokens[0] != "ENDFONT" {
		switch p.tokens[0] {
		case "FONT":
			if len(p.tokens) < 2 {
				panic("insufficient arguments")
			}
			p.font.Name = p.tokens[1]
		case "FONTBOUNDINGBOX":
			// There's no guarantee that this includes all BBXs.
			p.defaultBounds = p.readBBX()
		case "METRICSSET":
			if len(p.tokens) < 2 {
				panic("insufficient arguments")
			}
			if p.tokens[1] == "1" {
				panic("purely vertical fonts are unsupported")
			}
		case "DWIDTH":
			p.defaultAdvance = p.readDwidth()
		case "STARTPROPERTIES":
			p.parseProperties()
		case "STARTCHAR":
			p.parseChar()
		}
	}
	if p.font.Name == "" {
		panic("the font file doesn't contain the font's name")
	}
	if len(p.font.glyphs) == 0 {
		panic("the font file doesn't seem to contain any glyphs")
	}
}

func NewFromBDF(r io.Reader) (f *Font, err error) {
	p := bdfParser{
		scanner:     bufio.NewScanner(r),
		font:        &Font{glyphs: make(map[rune]glyph)},
		defaultChar: -1,
	}
	defer func() {
		if r := recover(); r != nil {
			var ok bool
			if err, ok = r.(error); !ok {
				err = fmt.Errorf("%v", r)
			}
		}
		if err != nil {
			err = fmt.Errorf("line %d: %s", p.line, err)
		}
	}()

	p.parse()
	return p.font, nil
}
