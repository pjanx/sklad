package main

import (
	"errors"
	"html/template"
	"image"
	"image/draw"
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

var tmplFont = template.Must(template.New("font").Parse(`
<!DOCTYPE html>
<html><body>
<h1>PT-CBP label printing tool</h1>
<h2>Choose font</h2>
{{ range $i, $f := . }}
<p><a href='?font={{ $i }}'>
<img src='?font={{ $i }}&amp;preview' title='{{ $f.Path }}'></a>
{{ end }}
</body></html>
`))

var tmplForm = template.Must(template.New("form").Parse(`
<!DOCTYPE html>
<html><body>
<h1>PT-CBP label printing tool</h1>
<table><tr>
<td valign=top>
	<img border=1 src='?font={{ .FontIndex }}&amp;scale={{ .Scale }}{{/*
	*/}}&amp;text={{ .Text }}&amp;render'>
</td>
<td valign=top><form>
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
		<legend>Font</legend>
		<p>{{ .Font.Name }} <a href='?'>Change</a>
			<input type=hidden name=font value='{{ .FontIndex }}'>
		<p><label for=scale>Scale:</label>
			<input id=scale name=scale value='{{.Scale}}' size=1>
	</fieldset>
	<fieldset>
		<legend>Label</legend>
		<p><textarea name=text>{{.Text}}</textarea>
		<p>Kind:
			<input type=radio id=kind-text name=kind value=text
				{{ if eq .Kind "text" }} checked{{ end }}>
			<label for=kind-text>plain text (horizontal)</label>
			<input type=radio id=kind-qr name=kind value=qr
				{{ if eq .Kind "qr" }} checked{{ end }}>
			<label for=kind-qr>QR code (vertical)</label>
		<p><input type=submit value='Update'>
			<input type=submit name=print value='Update and Print'>
	</fieldset>
</form></td>
</tr></table>
</body></html>
`))

type fontItem struct {
	Path    string
	Font    *bdf.Font
	Preview image.Image
}

var fonts = []*fontItem{}

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
	if r.Method == http.MethodGet {
		w.Header().Set("Cache-Control", "no-store")
	}

	var (
		font      *fontItem
		fontIndex int
		err       error
	)
	if fontIndex, err = strconv.Atoi(r.FormValue("font")); err == nil {
		font = fonts[fontIndex]
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmplFont.Execute(w, fonts); err != nil {
			http.Error(w, err.Error(), 500)
		}
		return
	}

	if _, ok := r.Form["preview"]; ok {
		w.Header().Set("Content-Type", "image/png")
		if err := png.Encode(w, font.Preview); err != nil {
			http.Error(w, err.Error(), 500)
		}
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
		FontIndex  int
		Text       string
		Scale      int
		Kind       string
	}{
		Printer:    printer,
		PrinterErr: printerErr,
		InitErr:    initErr,
		MediaInfo:  mediaInfo,
		Font:       font.Font,
		FontIndex:  fontIndex,
		Text:       r.FormValue("text"),
		Kind:       r.FormValue("kind"),
	}

	params.Scale, err = strconv.Atoi(r.FormValue("scale"))
	if err != nil {
		params.Scale = 3
	}
	if params.Kind == "" {
		params.Kind = "text"
	}

	var img image.Image
	if mediaInfo != nil {
		if params.Kind == "qr" {
			img = &imgutil.LeftRotate{Image: label.GenLabelForHeight(
				font.Font, params.Text, mediaInfo.PrintAreaPins, params.Scale)}
		} else {
			img = label.GenLabelForWidth(
				font.Font, params.Text, mediaInfo.PrintAreaPins, params.Scale)
		}
		if r.FormValue("print") != "" {
			if err := printer.Print(img); err != nil {
				log.Println("print error:", err)
			}
		}
	}

	if _, ok := r.Form["render"]; !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmplForm.Execute(w, &params); err != nil {
			http.Error(w, err.Error(), 500)
		}
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
	if len(os.Args) < 3 {
		log.Fatalf("usage: %s ADDRESS BDF-FILE...\n", os.Args[0])
	}

	address, bdfPaths := os.Args[1], os.Args[2:]
	for _, path := range bdfPaths {
		fi, err := os.Open(path)
		if err != nil {
			log.Fatalln(err)
		}
		font, err := bdf.NewFromBDF(fi)
		if err != nil {
			log.Fatalf("%s: %s\n", path, err)
		}
		if err := fi.Close(); err != nil {
			log.Fatalln(err)
		}

		r, _ := font.BoundString(font.Name)
		super := r.Inset(-3)

		img := image.NewRGBA(super)
		draw.Draw(img, super, image.White, image.ZP, draw.Src)
		font.DrawString(img, image.ZP, font.Name)

		fonts = append(fonts, &fontItem{Path: path, Font: font, Preview: img})
	}

	log.Println("starting server")
	http.HandleFunc("/", handle)
	log.Fatalln(http.ListenAndServe(address, nil))
}
