package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"

	"janouch.name/sklad/bdf"
)

func main() {
	fi, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatalln(err)
	}
	defer fi.Close()
	font, err := bdf.NewFromBDF(fi)
	if err != nil {
		log.Fatalln(err)
	}

	r, _ := font.BoundString(font.Name)
	super := r.Inset(-20)

	img := image.NewRGBA(super)
	draw.Draw(img, super, image.White, image.ZP, draw.Src)
	font.DrawString(img, image.ZP, color.Black, font.Name)

	fo, err := os.Create("out.png")
	if err != nil {
		log.Fatalln(err)
	}
	if err := png.Encode(fo, img); err != nil {
		fo.Close()
		log.Fatal(err)
	}
	if err := fo.Close(); err != nil {
		log.Fatal(err)
	}
}
