package main

import (
	"errors"
	"html/template"
	"image"
	"image/png"
	"log"
	"net/http"
	"os"
	"strconv"

	"janouch.name/sklad/bdf"
	"janouch.name/sklad/imgutil"
	"janouch.name/sklad/label"
	"janouch.name/sklad/ql"
)

var font *bdf.Font

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
		<fieldset>
			<p>Font: {{ .Font.Name }}
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
		Font       *bdf.Font
		Text       string
		Scale      int
	}{
		Printer:    printer,
		PrinterErr: printerErr,
		InitErr:    initErr,
		MediaInfo:  mediaInfo,
		Font:       font,
		Text:       r.FormValue("text"),
	}

	var err error
	params.Scale, err = strconv.Atoi(r.FormValue("scale"))
	if err != nil {
		params.Scale = 3
	}

	var img image.Image
	if mediaInfo != nil {
		img = &imgutil.LeftRotate{Image: label.GenLabelForHeight(
			font, params.Text, mediaInfo.PrintAreaPins, params.Scale)}
		if r.FormValue("print") != "" {
			if err := printer.Print(img); err != nil {
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
	if err := png.Encode(w, img); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: %s ADDRESS BDF-FILE\n", os.Args[0])
	}

	address, bdfPath := os.Args[1], os.Args[2]

	var err error
	fi, err := os.Open(bdfPath)
	if err != nil {
		log.Fatalln(err)
	}

	font, err = bdf.NewFromBDF(fi)
	if err := fi.Close(); err != nil {
		log.Fatalln(err)
	}
	if err != nil {
		log.Fatalln(err)
	}

	log.Println("starting server")
	http.HandleFunc("/", handle)
	log.Fatalln(http.ListenAndServe(address, nil))
}
