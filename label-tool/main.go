package main

import (
	"errors"
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"net/http"
	"os"
	"strconv"

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
			{{ if .Printer.LastStatus }}
			{{ .Printer.LastStatus.MediaWidthMM }} mm &times;
			{{ .Printer.LastStatus.MediaLengthMM }} mm

			{{ if .MediaInfo }}
			(offset: {{ .MediaInfo.SideMarginPins }} pt,
			print area: {{ .MediaInfo.PrintAreaPins }} pt)
			{{ else }}
			(unknown media)
			{{ end }}

			{{ if .Printer.LastStatus.Errors }}
			{{ range .Printer.LastStatus.Errors }}
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

func getStatus(printer *ql.Printer) error {
	if err := printer.Initialize(); err != nil {
		return err
	}
	if err := printer.UpdateStatus(); err != nil {
		return err
	}
	return nil
}

func handle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var (
		initErr   error
		mediaInfo *ql.MediaInfo
	)
	printer, printerErr := getPrinter()
	if printerErr == nil {
		defer printer.Close()
		printer.StatusNotify = func(status *ql.Status) {
			log.Printf("\x1b[1mreceived status\x1b[m\n%s", status)
		}

		if initErr = getStatus(printer); initErr == nil {
			mediaInfo = ql.GetMediaInfo(
				printer.LastStatus.MediaWidthMM(),
				printer.LastStatus.MediaLengthMM(),
			)
		}
	}

	var params = struct {
		Printer    *ql.Printer
		PrinterErr error
		InitErr    error
		MediaInfo  *ql.MediaInfo
		Text       string
		Scale      int
	}{
		Printer:    printer,
		PrinterErr: printerErr,
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
			if err := printer.Print(label); err != nil {
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

	log.Println("starting server")
	http.HandleFunc("/", handle)
	log.Fatal(http.ListenAndServe(":8080", nil))
}