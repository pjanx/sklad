package main

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"flag"
	"fmt"
	"log"
	"os"

	"janouch.name/sklad/imgutil"
	"janouch.name/sklad/ql"
)

var scale = flag.Int("scale", 1, "integer upscaling")
var rotate = flag.Bool("rotate", false, "print sideways")

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s IMAGE\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	// Open the picture.
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	// Load and eventually transform the picture.
	img, _, err := image.Decode(f)
	if err != nil {
		log.Fatalln(err)
	}
	if *scale > 1 {
		img = &imgutil.Scale{Image: img, Scale: *scale}
	}
	if *rotate {
		img = &imgutil.LeftRotate{Image: img}
	}

	// Open and initialize the printer.
	p, err := ql.Open()
	if err != nil {
		log.Fatalln(err)
	}
	if p == nil {
		log.Fatalln("no suitable printer found")
	}
	if err := p.Initialize(); err != nil {
		log.Fatalln(err)
	}
	if err := p.UpdateStatus(); err != nil {
		log.Fatalln(err)
	}

	// Check the picture against the media in the printer.
	mi := ql.GetMediaInfo(
		p.LastStatus.MediaWidthMM(),
		p.LastStatus.MediaLengthMM(),
	)
	if mi == nil {
		log.Fatalln("unknown media")
	}

	bounds := img.Bounds()
	dx, dy := bounds.Dx(), bounds.Dy()
	if dx > mi.PrintAreaPins {
		log.Fatalln("the image is too wide,", dx, ">", mi.PrintAreaPins, "pt")
	}
	if dy > mi.PrintAreaLength && mi.PrintAreaLength != 0 {
		log.Fatalln("the image is too high,", dy, ">", mi.PrintAreaLength, "pt")
	}

	if err := p.Print(img); err != nil {
		log.Fatalln(err)
	}
}
