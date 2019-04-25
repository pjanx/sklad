package label

import (
	"image"
	"image/draw"
	"strings"

	"janouch.name/sklad/bdf"
	"janouch.name/sklad/imgutil"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
)

// TODO: Rename to GenQRLabelForHeight.
func GenLabelForHeight(font *bdf.Font,
	text string, height, scale int) image.Image {
	// Create a scaled bitmap of the text label.
	textRect, _ := font.BoundString(text)
	textImg := image.NewRGBA(textRect)
	draw.Draw(textImg, textRect, image.White, image.ZP, draw.Src)
	font.DrawString(textImg, image.ZP, text)

	scaledTextImg := imgutil.Scale{Image: textImg, Scale: scale}
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

func GenLabelForWidth(font *bdf.Font,
	text string, width, scale int) image.Image {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		lines = append(lines, strings.TrimSuffix(line, "\r"))
	}

	height := 0
	var rects []image.Rectangle
	for _, line := range lines {
		r, _ := font.BoundString(line)
		rects = append(rects, r)
		height += r.Dy() * scale
	}

	imgRect := image.Rect(0, 0, width, height)
	img := image.NewRGBA(imgRect)
	draw.Draw(img, imgRect, image.White, image.ZP, draw.Src)

	y := 0
	for i := 0; i < len(lines); i++ {
		textImg := image.NewRGBA(rects[i])
		draw.Draw(textImg, rects[i], image.White, image.ZP, draw.Src)
		font.DrawString(textImg, image.ZP, lines[i])

		scaledImg := imgutil.Scale{Image: textImg, Scale: scale}
		scaledRect := scaledImg.Bounds()

		target := image.Rect(0, y, imgRect.Max.X, imgRect.Max.Y)
		draw.Draw(img, target, &scaledImg, scaledRect.Min, draw.Src)
		y += scaledRect.Dy()
	}
	return img
}
