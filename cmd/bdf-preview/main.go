package main

import (
	"html/template"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"net/http"
	"os"

	"janouch.name/sklad/bdf"
)

type fontItem struct {
	Font    *bdf.Font
	Preview image.Image
}

var fonts = map[string]fontItem{}

var tmpl = template.Must(template.New("list").Parse(`
<!DOCTYPE html>
<html><body>
<table border='1' cellpadding='3' style='border-collapse: collapse'>
	<tr>
		<th>Name</th>
		<th>Preview</th>
	<tr>
	{{- range $k, $v := . }}
	<tr>
		<td>{{ $k }}</td>
		<td><img src='?name={{ $k }}'></td>
	</tr>
	{{- end }}
</table>
</body></html>
`))

func handle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	name := r.FormValue("name")
	if name == "" {
		w.Header().Set("Content-Type", "text/html")
		tmpl.Execute(w, fonts)
		return
	}

	item, ok := fonts[name]
	if !ok {
		http.Error(w, "No such font.", 400)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	if err := png.Encode(w, item.Preview); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
}

func main() {
	for _, filename := range os.Args[1:] {
		fi, err := os.Open(filename)
		if err != nil {
			log.Fatalln(err)
		}
		font, err := bdf.NewFromBDF(fi)
		if err != nil {
			log.Fatalf("%s: %s\n", filename, err)
		}
		if err := fi.Close(); err != nil {
			log.Fatalln(err)
		}

		r, _ := font.BoundString(font.Name)
		super := r.Inset(-3)

		img := image.NewRGBA(super)
		draw.Draw(img, super, image.White, image.ZP, draw.Src)
		font.DrawString(img, image.ZP, color.Black, font.Name)

		fonts[filename] = fontItem{Font: font, Preview: img}
	}

	log.Println("starting server")
	http.HandleFunc("/", handle)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
