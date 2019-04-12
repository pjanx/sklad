package main

import (
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

	if err := printer.Initialize(); err != nil {
		log.Fatalln(err)
	}
	if err := printer.UpdateStatus(); err != nil {
		log.Fatalln(err)
	}
	log.Printf("status\n%s", printer.LastStatus)
}
