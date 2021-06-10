// Package ql is a Linux driver for Brother QL-series printers.
package ql

// Resources:
//  http://etc.nkadesign.com/Printers/QL550LabelPrinterProtocol
//  https://github.com/torvalds/linux/blob/master/drivers/usb/class/usblp.c
//  http://www.undocprint.org/formats/page_description_languages/brother_p-touch
//  http://www.undocprint.org/formats/communication_protocols/ieee_1284

import (
	"image"
	"regexp"
	"strings"
)

// -----------------------------------------------------------------------------

var deviceIDRegexp = regexp.MustCompile(
	`(?s:\s*([^:,;]+?)\s*:\s*([^:;]*)\s*(?:;|$))`)

type deviceID map[string][]string

// parseIEEE1284DeviceID leniently parses an IEEE 1284 Device ID string
// and returns a map containing a slice of values for each key.
func parseIEEE1284DeviceID(id []byte) deviceID {
	m := make(deviceID)
	for _, kv := range deviceIDRegexp.FindAllStringSubmatch(string(id), -1) {
		var values []string
		for _, v := range strings.Split(kv[2], ",") {
			values = append(values, strings.Trim(v, "\t\n\v\f\r "))
		}
		m[kv[1]] = values
	}
	return m
}

func (id deviceID) Find(key, abbreviation string) []string {
	if values, ok := id[key]; ok {
		return values
	}
	if values, ok := id[abbreviation]; ok {
		return values
	}
	return nil
}

func (id deviceID) FindFirst(key, abbreviation string) string {
	for _, s := range id.Find(key, abbreviation) {
		return s
	}
	return ""
}

// -----------------------------------------------------------------------------

func compatible(id deviceID) bool {
	for _, commandSet := range id.Find("COMMAND SET", "CMD") {
		if commandSet == "PT-CBP" {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------

type mediaSize struct {
	WidthMM  int
	LengthMM int
}

type MediaInfo struct {
	// Note that these are approximates, many pins within the margins will work.
	SideMarginPins int
	PrintAreaPins  int
	// If non-zero, length of the die-cut label print area in 300dpi pins.
	PrintAreaLength int
}

var media = map[mediaSize]MediaInfo{
	// Continuous length tape
	{12, 0}: {29, 106, 0},
	{29, 0}: {6, 306, 0},
	{38, 0}: {12, 413, 0},
	{50, 0}: {12, 554, 0},
	{54, 0}: {0, 590, 0},
	{62, 0}: {12, 696, 0},

	// Die-cut labels
	{17, 54}:  {0, 165, 566},
	{17, 87}:  {0, 165, 956},
	{23, 23}:  {42, 236, 202},
	{29, 42}:  {6, 306, 425},
	{29, 90}:  {6, 306, 991},
	{38, 90}:  {12, 413, 991},
	{39, 48}:  {6, 425, 495},
	{52, 29}:  {0, 578, 271},
	{54, 29}:  {59, 602, 271},
	{60, 86}:  {24, 672, 954},
	{62, 29}:  {12, 696, 271},
	{62, 100}: {12, 696, 1109},

	// Die-cut diameter labels
	{12, 12}: {113, 94, 94},
	{24, 24}: {42, 236, 236},
	{58, 58}: {51, 618, 618},
}

func GetMediaInfo(widthMM, lengthMM int) *MediaInfo {
	if mi, ok := media[mediaSize{widthMM, lengthMM}]; ok {
		return &mi
	}
	return nil
}

// -----------------------------------------------------------------------------

const (
	printBytes = 90
	printPins  = printBytes * 8
)

// pack packs a bool array into a byte array for the printer to print out.
func pack(data [printPins]bool, out *[]byte) {
	for i := 0; i < printBytes; i++ {
		var b byte
		for j := 0; j < 8; j++ {
			b <<= 1
			if data[i*8+j] {
				b |= 1
			}
		}
		*out = append(*out, b)
	}
}

// makeBitmapDataRB converts an image to the printer's red-black raster format.
func makeBitmapDataRB(src image.Image, margin, length int) []byte {
	data, bounds := []byte{}, src.Bounds()
	if bounds.Dy() > length {
		bounds.Max.Y = bounds.Min.Y + length
	}
	if bounds.Dx() > printPins-margin {
		bounds.Max.X = bounds.Min.X + printPins
	}

	redcells, blackcells := [printPins]bool{}, [printPins]bool{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		length--

		// The graphics needs to be inverted horizontally, iterating backwards.
		offset := margin
		for x := bounds.Max.X - 1; x >= bounds.Min.X; x-- {
			r, g, b, a := src.At(x, y).RGBA()
			redcells[offset] = r >= 0xc000 && g < 0x4000 && b < 0x4000 &&
				a >= 0x8000
			blackcells[offset] = r < 0x4000 && g < 0x4000 && b < 0x4000 &&
				a >= 0x8000
			offset++
		}

		data = append(data, 'w', 0x01, printBytes)
		pack(blackcells, &data)
		data = append(data, 'w', 0x02, printBytes)
		pack(redcells, &data)
	}
	for ; length > 0; length-- {
		data = append(data, 'w', 0x01, printBytes)
		data = append(data, make([]byte, printBytes)...)
		data = append(data, 'w', 0x02, printBytes)
		data = append(data, make([]byte, printBytes)...)
	}
	return data
}

// makeBitmapData converts an image to the printer's raster format.
func makeBitmapData(src image.Image, rb bool, margin, length int) []byte {
	// It's a necessary nuisance, so just copy and paste.
	if rb {
		return makeBitmapDataRB(src, margin, length)
	}

	data, bounds := []byte{}, src.Bounds()
	if bounds.Dy() > length {
		bounds.Max.Y = bounds.Min.Y + length
	}
	if bounds.Dx() > printPins-margin {
		bounds.Max.X = bounds.Min.X + printPins
	}

	pixels := [printPins]bool{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		length--

		// The graphics needs to be inverted horizontally, iterating backwards.
		offset := margin
		for x := bounds.Max.X - 1; x >= bounds.Min.X; x-- {
			r, g, b, a := src.At(x, y).RGBA()
			pixels[offset] = r < 0x4000 && g < 0x4000 && b < 0x4000 &&
				a >= 0x8000
			offset++
		}

		data = append(data, 'g', 0x00, printBytes)
		pack(pixels, &data)
	}
	for ; length > 0; length-- {
		data = append(data, 'g', 0x00, printBytes)
		data = append(data, make([]byte, printBytes)...)
	}
	return data
}

// XXX: It would be preferrable to know for certain if this is a red-black tape,
// because the printer refuses to print on a mismatch.
func makePrintData(status *Status, image image.Image, rb bool) (data []byte) {
	mediaInfo := GetMediaInfo(
		status.MediaWidthMM(),
		status.MediaLengthMM(),
	)
	if mediaInfo == nil {
		return nil
	}

	// Raster mode.
	// Should be the only supported mode for QL-800.
	data = append(data, 0x1b, 0x69, 0x61, 0x01)

	// Automatic status mode (though it's the default).
	data = append(data, 0x1b, 0x69, 0x21, 0x00)

	// Print information command.
	dy := image.Bounds().Dy()
	if mediaInfo.PrintAreaLength != 0 {
		dy = mediaInfo.PrintAreaLength
	}

	mediaType := byte(0x0a)
	if status.MediaLengthMM() != 0 {
		mediaType = byte(0x0b)
	}

	data = append(data, 0x1b, 0x69, 0x7a, 0x02|0x04|0x40|0x80, mediaType,
		byte(status.MediaWidthMM()), byte(status.MediaLengthMM()),
		byte(dy), byte(dy>>8), byte(dy>>16), byte(dy>>24), 0, 0x00)

	// Auto cut, each 1 label.
	data = append(data, 0x1b, 0x69, 0x4d, 0x40)
	data = append(data, 0x1b, 0x69, 0x41, 0x01)

	// Cut at end (though it's the default).
	// Not sure what it means, doesn't seem to have any effect to turn it off.
	if rb {
		data = append(data, 0x1b, 0x69, 0x4b, 0x08|0x01)
	} else {
		data = append(data, 0x1b, 0x69, 0x4b, 0x08)
	}

	if status.MediaLengthMM() != 0 {
		// 3mm margins along the direction of feed. 0x23 = 35 dots, the minimum.
		data = append(data, 0x1b, 0x69, 0x64, 0x23, 0x00)
	} else {
		// May not set anything other than zero.
		data = append(data, 0x1b, 0x69, 0x64, 0x00, 0x00)
	}

	// Compression mode: no compression.
	// Should be the only supported mode for QL-800.
	data = append(data, 0x4d, 0x00)

	// The graphics data itself.
	bitmapData := makeBitmapData(image, rb, mediaInfo.SideMarginPins, dy)
	data = append(data, bitmapData...)

	// Print command with feeding.
	return append(data, 0x1a)
}
