package main

import (
	"errors"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"

	"janouch.name/sklad/bdf"
	"janouch.name/sklad/ql"
)

// scaler is a scaling image.Image wrapper.
type scaler struct {
	image image.Image
	scale int
}

// ColorModel implements image.Image.
func (s *scaler) ColorModel() color.Model {
	return s.image.ColorModel()
}

// Bounds implements image.Image.
func (s *scaler) Bounds() image.Rectangle {
	r := s.image.Bounds()
	return image.Rect(r.Min.X*s.scale, r.Min.Y*s.scale,
		r.Max.X*s.scale, r.Max.Y*s.scale)
}

// At implements image.Image.
func (s *scaler) At(x, y int) color.Color {
	if x < 0 {
		x = x - s.scale + 1
	}
	if y < 0 {
		y = y - s.scale + 1
	}
	return s.image.At(x/s.scale, y/s.scale)
}

// leftRotate is a 90 degree rotating image.Image wrapper.
type leftRotate struct {
	image image.Image
}

// ColorModel implements image.Image.
func (lr *leftRotate) ColorModel() color.Model {
	return lr.image.ColorModel()
}

// Bounds implements image.Image.
func (lr *leftRotate) Bounds() image.Rectangle {
	r := lr.image.Bounds()
	// Min is inclusive, Max is exclusive.
	return image.Rect(r.Min.Y, -(r.Max.X - 1), r.Max.Y, -(r.Min.X - 1))
}

// At implements image.Image.
func (lr *leftRotate) At(x, y int) color.Color {
	return lr.image.At(-y, x)
}

// -----------------------------------------------------------------------------

func decodeBitfieldErrors(b byte, errors [8]string) []string {
	var result []string
	for i := uint(0); i < 8; i++ {
		if b&(1<<i) != 0 {
			result = append(result, errors[i])
		}
	}
	return result
}

func printStatusInformation(d []byte) {
	log.Println("-- status")

	// Error information 1.
	for _, e := range decodeBitfieldErrors(d[8], [8]string{
		"no media", "end of media", "cutter jam", "?", "printer in use",
		"printer turned off", "high-voltage adapter", "fan motor error"}) {
		log.Println("error:", e)
	}

	// Error information 2.
	for _, e := range decodeBitfieldErrors(d[9], [8]string{
		"replace media", "expansion buffer full", "communication error",
		"communication buffer full", "cover open", "cancel key",
		"media cannot be fed", "system error"}) {
		log.Println("error:", e)
	}

	// Media width.
	log.Println("media width:", d[10], "mm")

	// Media type.
	switch b := d[11]; b {
	case 0x00:
		log.Println("media: no media")
	case 0x4a, 0x0a: // 0x4a = J, in reality we get 0x0a as in QL-1100 docs
		log.Println("media: continuous length tape")
	case 0x4b, 0x0b: // 0x4b = K, in reality we get 0x0b as in QL-1100 docs
		log.Println("media: die-cut labels")
	default:
		log.Println("media:", b)
	}

	// Mode.
	log.Println("mode:", d[15])

	// Media length.
	log.Println("media length:", d[17], "mm")

	// Status type.
	switch b := d[18]; b {
	case 0x00:
		log.Println("status type: reply to status request")
	case 0x01:
		log.Println("status type: printing completed")
	case 0x02:
		log.Println("status type: error occurred")
	case 0x04:
		log.Println("status type: turned off")
	case 0x05:
		log.Println("status type: notification")
	case 0x06:
		log.Println("status type: phase change")
	default:
		log.Println("status type:", b)
	}

	// Phase type.
	switch b := d[19]; b {
	case 0x00:
		log.Println("phase state: receiving state")
	case 0x01:
		log.Println("phase state: printing state")
	default:
		log.Println("phase state:", b)
	}

	// Phase number.
	log.Println("phase number:", int(d[20])*256+int(d[21]))

	// Notification number.
	switch b := d[22]; b {
	case 0x00:
		log.Println("notification number: not available")
	case 0x03:
		log.Println("notification number: cooling (started)")
	case 0x04:
		log.Println("notification number: cooling (finished)")
	default:
		log.Println("notification number:", b)
	}
}

// genLabelData converts an image to the printer's raster format.
func genLabelData(src image.Image, offset, length int) (data []byte) {
	bounds := src.Bounds()
	pixels := make([]bool, 720)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		length--
		if length <= 0 {
			break
		}
		off := offset
		for x := bounds.Max.X - 1; x >= bounds.Min.X; x-- {
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

func printLabel(printer *ql.Printer, src image.Image,
	status *ql.Status, mediaInfo *ql.MediaInfo) error {
	data := []byte(nil)

	// Raster mode.
	// Should be the only supported mode for QL-800.
	data = append(data, 0x1b, 0x69, 0x61, 0x01)

	// Automatic status mode (though it's the default).
	data = append(data, 0x1b, 0x69, 0x21, 0x00)

	// Print information command.
	dy := src.Bounds().Dy()
	if mediaInfo.PrintAreaLength != 0 {
		dy = mediaInfo.PrintAreaLength
	}

	mediaType := byte(0x0a)
	if status.MediaLengthMM != 0 {
		mediaType = byte(0x0b)
	}

	data = append(data, 0x1b, 0x69, 0x7a, 0x02|0x04|0x40|0x80, mediaType,
		byte(status.MediaWidthMM), byte(status.MediaLengthMM),
		byte(dy), byte(dy>>8), byte(dy>>16), byte(dy>>24), 0, 0x00)

	// Auto cut, each 1 label.
	data = append(data, 0x1b, 0x69, 0x4d, 0x40)
	data = append(data, 0x1b, 0x69, 0x41, 0x01)

	// Cut at end (though it's the default).
	// Not sure what it means, doesn't seem to have any effect to turn it off.
	data = append(data, 0x1b, 0x69, 0x4b, 0x08)

	if status.MediaLengthMM != 0 {
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
	data = append(data, genLabelData(src, mediaInfo.SideMarginPins, dy)...)

	// Print command with feeding.
	data = append(data, 0x1a)

	// ---

	// Print the prepared data.
	if _, err := printer.File.Write(data); err != nil {
		return err
	}

	// TODO: We specifically need to wait for a transition to the receiving
	// state, and try to figure out something from the statuses.
	// We may also receive an error status instead of the transition to
	// the printing state. Or even after it.
	start, b := time.Now(), make([]byte, 32)
	for {
		if time.Now().Sub(start) > 3*time.Second {
			break
		}
		if n, err := printer.File.Read(b); err == io.EOF {
			time.Sleep(100 * time.Millisecond)
		} else if err != nil {
			return err
		} else if n < 32 {
			return errors.New("invalid read")
		} else {
			printStatusInformation(b)
		}
	}
	return nil
}

// -----------------------------------------------------------------------------

var font *bdf.Font

func genLabelForHeight(text string, height, scale int) image.Image {
	// Create a scaled bitmap of the text label.
	textRect, _ := font.BoundString(text)
	textImg := image.NewRGBA(textRect)
	draw.Draw(textImg, textRect, image.White, image.ZP, draw.Src)
	font.DrawString(textImg, image.ZP, text)

	scaledTextImg := scaler{image: textImg, scale: scale}
	scaledTextRect := scaledTextImg.Bounds()

	remains := height - scaledTextRect.Dy() - 20

	width := scaledTextRect.Dx()
	if remains > width {
		width = remains
	}

	// Create a scaled bitmap of the QR code.
	qrImg, _ := qr.Encode(text, qr.H, qr.Auto)
	qrImg, _ = barcode.Scale(qrImg, remains, remains)
	qrRect := qrImg.Bounds()

	// Combine.
	combinedRect := image.Rect(0, 0, width, height)
	combinedImg := image.NewRGBA(combinedRect)
	draw.Draw(combinedImg, combinedRect, image.White, image.ZP, draw.Src)
	draw.Draw(combinedImg,
		combinedRect.Add(image.Point{X: (width - qrRect.Dx()) / 2, Y: 0}),
		qrImg, image.ZP, draw.Src)

	target := image.Rect(
		(width-scaledTextRect.Dx())/2, qrRect.Dy()+20,
		combinedRect.Max.X, combinedRect.Max.Y)
	draw.Draw(combinedImg, target, &scaledTextImg, scaledTextRect.Min, draw.Src)
	return combinedImg
}

var tmpl = template.Must(template.New("form").Parse(`
	<!DOCTYPE html>
	<html><body>
	<h1>PT-CBP label printing tool</h1>
	<table><tr>
	<td valign=top>
		<img border=1 src='?img&amp;scale={{.Scale}}&amp;text={{.Text}}'>
	</td>
	<td valign=top>
		<fieldset>
			{{ if .Printer }}

			<p>Printer: {{ .Printer.Manufacturer }} {{ .Printer.Model }}
			<p>Tape:
			{{ if .Status }}
			{{ .Status.MediaWidthMM }} mm &times;
			{{ .Status.MediaLengthMM }} mm

			{{ if .MediaInfo }}
			(offset: {{ .MediaInfo.SideMarginPins }} pt,
			print area: {{ .MediaInfo.PrintAreaPins }} pt)
			{{ else }}
			(unknown media)
			{{ end }}

			{{ if .Status.Errors }}
			{{ range .Status.Errors }}
			<p>Error: {{ . }}
			{{ end }}
			{{ end }}

			{{ end }}
			{{ if .InitErr }}
			{{ .InitErr }}
			{{ end }}

			{{ else }}
			<p>Error: {{ .PrinterErr }}
			{{ end }}
		</fieldset>
		<form><fieldset>
			<p><label for=text>Text:</label>
				<input id=text name=text value='{{.Text}}'>
				<label for=scale>Scale:</label>
				<input id=scale name=scale value='{{.Scale}}' size=1>
			<p><input type=submit value='Update'>
				<input type=submit name=print value='Update and Print'>
		</fieldset></form>
	</td>
	</tr></table>
	</body></html>
`))

func getPrinter() (*ql.Printer, error) {
	printer, err := ql.Open()
	if err != nil {
		return nil, err
	}
	if printer == nil {
		return nil, errors.New("no suitable printer found")
	}
	return printer, nil
}

func getStatus(printer *ql.Printer) (*ql.Status, error) {
	if err := printer.Initialize(); err != nil {
		return nil, err
	}
	if data, err := printer.GetStatus(); err != nil {
		return nil, err
	} else {
		printStatusInformation(data)
		return ql.DecodeStatus(data), nil
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var (
		status  *ql.Status
		initErr error
	)
	printer, printerErr := getPrinter()
	if printerErr == nil {
		defer printer.Close()
		status, initErr = getStatus(printer)
	}

	var mediaInfo *ql.MediaInfo
	if status != nil {
		mediaInfo = ql.GetMediaInfo(status.MediaWidthMM, status.MediaLengthMM)
	}

	var params = struct {
		Printer    *ql.Printer
		PrinterErr error
		Status     *ql.Status
		InitErr    error
		MediaInfo  *ql.MediaInfo
		Text       string
		Scale      int
	}{
		Printer:    printer,
		PrinterErr: printerErr,
		Status:     status,
		InitErr:    initErr,
		MediaInfo:  mediaInfo,
		Text:       r.FormValue("text"),
	}

	var err error
	params.Scale, err = strconv.Atoi(r.FormValue("scale"))
	if err != nil {
		params.Scale = 3
	}

	var label image.Image
	if mediaInfo != nil {
		label = &leftRotate{image: genLabelForHeight(
			params.Text, mediaInfo.PrintAreaPins, params.Scale)}
		if r.FormValue("print") != "" {
			if err := printLabel(
				printer, label, status, mediaInfo); err != nil {
				log.Println("print error:", err)
			}
		}
	}

	if _, ok := r.Form["img"]; !ok {
		w.Header().Set("Content-Type", "text/html")
		tmpl.Execute(w, &params)
		return
	}

	if mediaInfo == nil {
		http.Error(w, "unknown media", 500)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	if err := png.Encode(w, label); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func main() {
	var err error
	fi, err := os.Open("../../ucs-fonts-75dpi100dpi/100dpi/luBS24.bdf")
	if err != nil {
		log.Fatalln(err)
	}
	font, err = bdf.NewFromBDF(fi)
	if err != nil {
		log.Fatalln(err)
	}
	if err := fi.Close(); err != nil {
		log.Fatalln(err)
	}

	log.Println("Starting server")
	http.HandleFunc("/", handle)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
