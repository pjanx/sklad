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
	case 0x4a:
		log.Println("media: continuous length tape")
	case 0x4b:
		log.Println("media: die-cut labels")
	default:
		log.Println("media:", b)
	}

	// Mode.
	log.Println("mode:", d[15])

	// Media length.
	log.Println("media width:", d[17], "mm")

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
func genLabelData(src image.Image, offset int) (data []byte) {
	// TODO: Margins? For 29mm, it's 6 pins from the start, 306 printing pins.
	bounds := src.Bounds()
	pixels := make([]bool, 720)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
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
	return
}

func printLabel(src image.Image) error {
	data := []byte(nil)

	// Raster mode.
	// Should be the only supported mode for QL-800.
	data = append(data, 0x1b, 0x69, 0x61, 0x01)

	// Automatic status mode (though it's the default).
	data = append(data, 0x1b, 0x69, 0x21, 0x00)

	// Print information command.
	dy := src.Bounds().Dy()
	data = append(data, 0x1b, 0x69, 0x7a, 0x02|0x04|0x40|0x80, 0x0a, 29, 0,
		byte(dy), byte(dy>>8), byte(dy>>16), byte(dy>>24), 0, 0x00)

	// Auto cut, each 1 label.
	data = append(data, 0x1b, 0x69, 0x4d, 0x40)
	data = append(data, 0x1b, 0x69, 0x41, 0x01)

	// Cut at end (though it's the default).
	// Not sure what it means, doesn't seem to have any effect to turn it off.
	data = append(data, 0x1b, 0x69, 0x4b, 0x08)

	// 3mm margins along the direction of feed. 0x23 = 35 dots, the minimum.
	data = append(data, 0x1b, 0x69, 0x64, 0x23, 0x00)

	// Compression mode: no compression.
	// Should be the only supported mode for QL-800.
	data = append(data, 0x4d, 0x00)

	// The graphics data itself.
	data = append(data, genLabelData(src, 6)...)

	// Print command with feeding.
	data = append(data, 0x1a)

	// ---

	// Linux usblp module, located in /drivers/usb/class/usblp.c
	// (at least that's where the trails go, I don't understand the code)
	f, err := os.OpenFile("/dev/usb/lp0", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	// Flush any former responses in the printer's queue.
	for {
		dummy := make([]byte, 32)
		if _, err := f.Read(dummy); err == io.EOF {
			break
		}
	}

	// Clear the print buffer.
	invalidate := make([]byte, 400)
	if _, err := f.Write(invalidate); err != nil {
		return err
	}

	// Initialize.
	if _, err := f.WriteString("\x1b\x40"); err != nil {
		return err
	}

	// Request status information.
	if _, err := f.WriteString("\x1b\x69\x53"); err != nil {
		return err
	}

	// We need to poll the device.
	status := make([]byte, 32)
	for {
		if n, err := f.Read(status); err == io.EOF {
			time.Sleep(10 * time.Millisecond)
		} else if err != nil {
			return err
		} else if n < 32 {
			return errors.New("invalid read")
		} else {
			break
		}
	}
	printStatusInformation(status)

	// Print the prepared data.
	if _, err := f.Write(data); err != nil {
		return err
	}

	// TODO: We specifically need to wait for a transition to the receiving
	// state, and try to figure out something from the statuses.
	// We may also receive an error status instead of the transition to
	// the printing state. Or even after it.
	start := time.Now()
	for {
		if time.Now().Sub(start) > 3*time.Second {
			break
		}
		if n, err := f.Read(status); err == io.EOF {
			time.Sleep(100 * time.Millisecond)
		} else if err != nil {
			return err
		} else if n < 32 {
			return errors.New("invalid read")
		} else {
			printStatusInformation(status)
		}
	}
	return nil
}

// -----------------------------------------------------------------------------

var font *bdf.Font

func genLabel(text string, width int) image.Image {
	// Create a scaled bitmap of the QR code.
	qrImg, _ := qr.Encode(text, qr.H, qr.Auto)
	qrImg, _ = barcode.Scale(qrImg, width, width)
	qrRect := qrImg.Bounds()

	// Create a scaled bitmap of the text label.
	textRect, _ := font.BoundString(text)
	textImg := image.NewRGBA(textRect)
	draw.Draw(textImg, textRect, image.White, image.ZP, draw.Src)
	font.DrawString(textImg, image.ZP, text)

	// TODO: We can scale as needed to make the text fit.
	scaledTextImg := scaler{image: textImg, scale: 3}
	scaledTextRect := scaledTextImg.Bounds()

	// Combine.
	combinedRect := qrRect
	combinedRect.Max.Y += scaledTextRect.Dy() + 20

	combinedImg := image.NewRGBA(combinedRect)
	draw.Draw(combinedImg, combinedRect, image.White, image.ZP, draw.Src)
	draw.Draw(combinedImg, combinedRect, qrImg, image.ZP, draw.Src)

	target := image.Rect(
		(width-scaledTextRect.Dx())/2, qrRect.Dy()+10,
		combinedRect.Max.X, combinedRect.Max.Y)
	draw.Draw(combinedImg, target, &scaledTextImg, scaledTextRect.Min, draw.Src)
	return combinedImg
}

func genLabelForHeight(text string, height int) image.Image {
	// Create a scaled bitmap of the text label.
	textRect, _ := font.BoundString(text)
	textImg := image.NewRGBA(textRect)
	draw.Draw(textImg, textRect, image.White, image.ZP, draw.Src)
	font.DrawString(textImg, image.ZP, text)

	// TODO: Make it possible to choose scale, or use some heuristic.
	scaledTextImg := scaler{image: textImg, scale: 3}
	//scaledTextImg := scaler{image: textImg, scale: 3}
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
	<table><tr>
	<td valign=top>
		<img border=1 src='?img&amp;width={{.Width}}&amp;text={{.Text}}'>
	</td>
	<td valign=top>
		<form><fieldset>
			<p><label for=width>Tape width in pt:</label>
				<input id=width name=width value='{{.Width}}'>
			<p><label for=text>Text:</label>
				<input id=text name=text value='{{.Text}}'>
			<p><input type=submit value='Update'>
				<input type=submit name=print value='Update and Print'>
		</fieldset></form>
	</td>
	</tr></table>
	</body></html>
`))

func handle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var params = struct {
		Width int
		Text  string
	}{
		Text: r.FormValue("text"),
	}

	var err error
	params.Width, err = strconv.Atoi(r.FormValue("width"))
	if err != nil {
		params.Width = 306 // Default to 29mm tape.
	}

	// TODO: Possibly just remove the for-width mode.
	label := genLabel(params.Text, params.Width)
	label = &leftRotate{image: genLabelForHeight(params.Text, params.Width)}
	if r.FormValue("print") != "" {
		if err := printLabel(label); err != nil {
			log.Println("print error:", err)
		}
	}

	if _, ok := r.Form["img"]; !ok {
		w.Header().Set("Content-Type", "text/html")
		tmpl.Execute(w, &params)
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
