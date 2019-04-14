package main

import (
	"fmt"
	"log"

	"janouch.name/sklad/ql"
)

func main() {
	printer, err := ql.Open()
	if err != nil {
		log.Fatalln(err)
	}
	if printer == nil {
		log.Fatalln("no suitable printer found")
	}

	defer printer.Close()

	fmt.Printf("\x1b[1m%s %s\x1b[m\n", printer.Manufacturer, printer.Model)
	if err := printer.Initialize(); err != nil {
		log.Fatalln(err)
	}
	if err := printer.UpdateStatus(); err != nil {
		log.Fatalln(err)
	}

	status := printer.LastStatus
	fmt.Print(status)

	fmt.Println("\x1b[1mMedia information\x1b[m")
	if mi := ql.GetMediaInfo(
		status.MediaWidthMM(), status.MediaLengthMM()); mi != nil {
		fmt.Println("side margin pins:", mi.SideMarginPins)
		fmt.Println("print area pins:", mi.PrintAreaPins)
		fmt.Println("print area length:", mi.PrintAreaLength)
	} else {
		fmt.Println("unknown media")
	}
}
