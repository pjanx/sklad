// Package ql is a Linux driver for Brother QL-series printers.
package ql

// Resources:
//  http://etc.nkadesign.com/Printers/QL550LabelPrinterProtocol
//  https://github.com/torvalds/linux/blob/master/drivers/usb/class/usblp.c
//  http://www.undocprint.org/formats/page_description_languages/brother_p-touch
//  http://www.undocprint.org/formats/communication_protocols/ieee_1284

import (
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// #include <linux/ioctl.h>
import "C"

// -----------------------------------------------------------------------------

func _IOC(dir, typ, nr, size int) uintptr {
	return (uintptr(dir) << C._IOC_DIRSHIFT) |
		(uintptr(typ) << C._IOC_TYPESHIFT) |
		(uintptr(nr) << C._IOC_NRSHIFT) |
		(uintptr(size) << C._IOC_SIZESHIFT)
}

const (
	iocnrGetDeviceID = 1
)

// lpiocGetDeviceID reads the IEEE-1284 Device ID string of a printer.
func lpiocGetDeviceID(fd uintptr) ([]byte, error) {
	var buf [1024]byte
	if _, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd,
		_IOC(C._IOC_READ, 'P', iocnrGetDeviceID, len(buf)),
		uintptr(unsafe.Pointer(&buf))); err != 0 {
		return nil, err
	}

	// In theory it might get trimmed along the way.
	length := int(buf[0])<<8 | int(buf[1])
	if 2+length > len(buf) {
		return buf[2:], errors.New("the device ID string got trimmed")
	}

	return buf[2 : 2+length], nil
}

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

type Printer struct {
	File         *os.File
	Manufacturer string
	Model        string
	LastStatus   *Status
	MediaInfo    *MediaInfo
}

func compatible(id deviceID) bool {
	for _, commandSet := range id.Find("COMMAND SET", "CMD") {
		if commandSet == "PT-CBP" {
			return true
		}
	}
	return false
}

// Open finds and initializes the first USB printer found supporting
// the appropriate protocol. Returns nil if no printer could be found.
func Open() (*Printer, error) {
	// Linux usblp module, located in /drivers/usb/class/usblp.c
	paths, err := filepath.Glob("/dev/usb/lp[0-9]*")
	if err != nil {
		return nil, err
	}
	for _, candidate := range paths {
		f, err := os.OpenFile(candidate, os.O_RDWR, 0)
		if err != nil {
			continue
		}
		// Filter out obvious non-printers.
		deviceID, err := lpiocGetDeviceID(f.Fd())
		if err != nil {
			f.Close()
			continue
		}
		parsedID := parseIEEE1284DeviceID(deviceID)
		// Filter out printers that wouldn't understand the protocol.
		if !compatible(parsedID) {
			f.Close()
			continue
		}
		return &Printer{
			File:         f,
			Manufacturer: parsedID.FindFirst("MANUFACTURER", "MFG"),
			Model:        parsedID.FindFirst("MODEL", "MDL"),
		}, nil
	}
	return nil, nil
}

// Initialize initializes the printer for further operations.
func (p *Printer) Initialize() error {
	// Clear the print buffer.
	invalidate := make([]byte, 400)
	if _, err := p.File.Write(invalidate); err != nil {
		return err
	}

	// Initialize.
	if _, err := p.File.WriteString("\x1b\x40"); err != nil {
		return err
	}

	// Flush any former responses in the printer's queue.
	//
	// I'm not sure if this is necessary, or rather whether the kernel driver
	// does any buffering that could cause data to be returned at this point.
	/*
		var dummy [32]byte
		for {
			if _, err := f.Read(dummy[:]); err == io.EOF {
				break
			}
		}
	*/

	return nil
}

var errTimeout = errors.New("timeout")
var errInvalidRead = errors.New("invalid read")

// pollStatusBytes waits for the printer to send a status packet and returns
// it as raw data.
func (p *Printer) pollStatusBytes(
	timeout time.Duration) (status [32]byte, err error) {
	start, n := time.Now(), 0
	for {
		if n, err = p.File.Read(status[:]); err == io.EOF {
			time.Sleep(10 * time.Millisecond)
		} else if err != nil {
			return status, err
		} else if n < 32 {
			return status, errInvalidRead
		} else {
			return status, nil
		}
		if time.Now().Sub(start) > timeout {
			return status, errTimeout
		}
	}
}

// Request new status information from the printer. The printer
// must be in an appropriate mode, i.e. on-line and not currently printing.
func (p *Printer) UpdateStatus() error {
	// Request status information.
	if _, err := p.File.WriteString("\x1b\x69\x53"); err != nil {
		return err
	}

	// Retrieve status information.
	status, err := p.pollStatusBytes(time.Second)
	if err != nil {
		p.LastStatus = nil
		return err
	}

	s := Status(status)
	p.LastStatus = &s
	return nil
}

// Close closes the underlying file.
func (p *Printer) Close() error {
	return p.File.Close()
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

// makeBitmapData converts an image to the printer's raster format.
func makeBitmapData(src image.Image, offset, length int) (data []byte) {
	bounds := src.Bounds()
	pixels := [720]bool{}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		length--
		if length <= 0 {
			break
		}

		off := offset
		for x := bounds.Max.X - 1; x >= bounds.Min.X; x-- {
			if off >= len(pixels) {
				break
			}

			// TODO: Anything to do with the ColorModel?
			r, g, b, a := src.At(x, y).RGBA()
			pixels[off] = r == 0 && g == 0 && b == 0 && a != 0
			off++
		}

		data = append(data, 'g', 0x00, 90)
		for i := 0; i < 90; i++ {
			var b byte
			for j := 0; j < 8; j++ {
				b <<= 1
				if pixels[i*8+j] {
					b |= 1
				}
			}
			data = append(data, b)
		}
	}
	for ; length > 0; length-- {
		data = append(data, 'g', 0x00, 90)
		data = append(data, make([]byte, 90)...)
	}
	return
}

func (p *Printer) makePrintData(image image.Image) (data []byte) {
	mediaInfo := GetMediaInfo(
		p.LastStatus.MediaWidthMM(),
		p.LastStatus.MediaLengthMM(),
	)

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
	if p.LastStatus.MediaLengthMM() != 0 {
		mediaType = byte(0x0b)
	}

	data = append(data, 0x1b, 0x69, 0x7a, 0x02|0x04|0x40|0x80, mediaType,
		byte(p.LastStatus.MediaWidthMM()), byte(p.LastStatus.MediaLengthMM()),
		byte(dy), byte(dy>>8), byte(dy>>16), byte(dy>>24), 0, 0x00)

	// Auto cut, each 1 label.
	data = append(data, 0x1b, 0x69, 0x4d, 0x40)
	data = append(data, 0x1b, 0x69, 0x41, 0x01)

	// Cut at end (though it's the default).
	// Not sure what it means, doesn't seem to have any effect to turn it off.
	data = append(data, 0x1b, 0x69, 0x4b, 0x08)

	if p.LastStatus.MediaLengthMM() != 0 {
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
	data = append(data, makeBitmapData(image, mediaInfo.SideMarginPins, dy)...)

	// Print command with feeding.
	return append(data, 0x1a)
}

func (p *Printer) Print(image image.Image) error {
	data := p.makePrintData(image)

	// Print the prepared data.
	if _, err := p.File.Write(data); err != nil {
		return err
	}

	// TODO: We specifically need to wait for a transition to the receiving
	// state, and try to figure out something from the statuses.
	// We may also receive an error status instead of the transition to
	// the printing state. Or even after it.
	start, b := time.Now(), [32]byte{}
	for {
		if n, err := p.File.Read(b[:]); err == io.EOF {
			time.Sleep(100 * time.Millisecond)
		} else if err != nil {
			return err
		} else if n < 32 {
			return errors.New("invalid read")
		} else {
			status := Status(b)
			log.Printf("status\n%s", &status)
		}
		if time.Now().Sub(start) > 3*time.Second {
			break
		}
	}
	return nil
}

// -----------------------------------------------------------------------------

// Status is a decoder for the status packed returned by the printer.
type Status [32]byte

func (s *Status) MediaWidthMM() int  { return int(s[10]) }
func (s *Status) MediaLengthMM() int { return int(s[17]) }

func decodeBitfieldErrors(b byte, errors [8]string) []string {
	var result []string
	for i := uint(0); i < 8; i++ {
		if b&(1<<i) != 0 {
			result = append(result, errors[i])
		}
	}
	return result
}

func (s *Status) Errors() (errors []string) {
	errors = append(errors, decodeBitfieldErrors(s[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"})...)
	errors = append(errors, decodeBitfieldErrors(s[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"})...)
	return
}

// String implements the Stringer interface.
func (s *Status) String() string {
	var b strings.Builder
	s.Dump(&b)
	return b.String()
}

// Dump writes the status data to an io.Writer in a human-readable format.
func (s *Status) Dump(f io.Writer) {
	/*
		if s[0] != 0x80 || s[1] != 0x20 || s[2] != 0x42 || s[3] != 0x34 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Model code.
	switch m := s[4]; m {
	case 0x38:
		fmt.Fprintln(f, "model: QL-800")
	case 0x39:
		fmt.Fprintln(f, "model: QL-810W")
	case 0x41:
		fmt.Fprintln(f, "model: QL-820NWB")
	case 0x43:
		fmt.Fprintln(f, "model: QL-1100")
	case 0x44:
		fmt.Fprintln(f, "model: QL-1110NWB")
	case 0x45:
		fmt.Fprintln(f, "model: QL-1115NWB")
	default:
		fmt.Fprintln(f, "model:", m)
	}

	/*
		// s[6] seems to be 0x00 in a real-world QL-800, as in QL-1100 docs.
		if s[5] != 0x30 || s[6] != 0x30 || s[7] != 0x00 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Error information 1.
	for _, e := range decodeBitfieldErrors(s[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"}) {
		fmt.Fprintln(f, "error 1:", e)
	}

	// Error information 2.
	for _, e := range decodeBitfieldErrors(s[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"}) {
		fmt.Fprintln(f, "error 2:", e)
	}

	// Media width.
	fmt.Fprintln(f, "media width:", s[10], "mm")

	// Media type.
	switch t := s[11]; t {
	case 0x00:
		fmt.Fprintln(f, "media: no media")
	case 0x4a, 0x0a: // 0x4a = J, in reality we get 0x0a, as in QL-1100 docs.
		fmt.Fprintln(f, "media: continuous length tape")
	case 0x4b, 0x0b: // 0x4b = K, in reality we get 0x0b, as in QL-1100 docs.
		fmt.Fprintln(f, "media: die-cut labels")
	default:
		fmt.Fprintln(f, "media:", t)
	}

	/*
		// In a real-world QL-800, s[14] seems to be:
		//  0x01 with die-cut 29mm long labels,
		//  0x14 with 29mm tape,
		//  0x23 with red-black 62mm tape,
		// and directly corresponds to physical pins on the tape.
		if s[12] != 0x00 || s[13] != 0x00 || s[14] != 0x3f {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Mode.
	fmt.Fprintln(f, "mode:", s[15])

	/*
		if s[16] != 0x00 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/

	// Media length.
	fmt.Fprintln(f, "media length:", s[17], "mm")

	// Status type.
	switch t := s[18]; t {
	case 0x00:
		fmt.Fprintln(f, "status type: reply to status request")
	case 0x01:
		fmt.Fprintln(f, "status type: printing completed")
	case 0x02:
		fmt.Fprintln(f, "status type: error occurred")
	case 0x04:
		fmt.Fprintln(f, "status type: turned off")
	case 0x05:
		fmt.Fprintln(f, "status type: notification")
	case 0x06:
		fmt.Fprintln(f, "status type: phase change")
	default:
		fmt.Fprintln(f, "status type:", t)
	}

	// Phase type.
	switch t := s[19]; t {
	case 0x00:
		fmt.Fprintln(f, "phase state: receiving state")
	case 0x01:
		fmt.Fprintln(f, "phase state: printing state")
	default:
		fmt.Fprintln(f, "phase state:", t)
	}

	// Phase number.
	fmt.Fprintln(f, "phase number:", int(s[20])*256+int(s[21]))

	// Notification number.
	switch n := s[22]; n {
	case 0x00:
		fmt.Fprintln(f, "notification number: not available")
	case 0x03:
		fmt.Fprintln(f, "notification number: cooling (started)")
	case 0x04:
		fmt.Fprintln(f, "notification number: cooling (finished)")
	default:
		fmt.Fprintln(f, "notification number:", n)
	}

	/*
		// In a real-world QL-800, s[25] seems to be:
		//  0x01 with 29mm tape or die-cut 29mm long labels,
		//  0x81 with red-black 62mm tape.
		if s[23] != 0x00 || s[24] != 0x00 || s[25] != 0x00 || s[26] != 0x00 ||
			s[27] != 0x00 || s[28] != 0x00 || s[29] != 0x00 || s[30] != 0x00 ||
			s[31] != 0x00 {
			fmt.Fprintln(f, "unexpected status fixed bytes")
		}
	*/
}
