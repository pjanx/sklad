package main

import (
	"html/template"
	"log"
	"net/http"
	"os"
)

var (
	templates *template.Template

	// session storage: UUID -> net.SplitHostPort(http.Server.RemoteAddr)[0]
	sessions = map[string]string{}
)

func main() {
	if len(os.Args) != 3 {
		log.Fatalf("usage: %s ADDRESS DATABASE\n", os.Args[0])
	}

	var address string
	address, dbPath = os.Args[1], os.Args[2]

	// Load database.
	if err := loadDatabase(); err != nil {
		log.Fatalln(err)
	}

	// Load HTML templates from the current working directory.
	var err error
	templates, err = template.ParseGlob("*.tmpl")
	if err != nil {
		log.Fatalln(err)
	}

	// TODO: Eventually we will need to load a font file for label printing.
	//  - The path might be part of configuration, or implicit by filename.

	// TODO: Some routing, don't forget about sessions.
	//  - https://stackoverflow.com/a/33880971/76313
	//
	//  - GET /login
	//  - GET /container?id=UA1
	//  - GET /series?id=A
	//  - GET /search?q=bottle
	//
	//  - POST /login?pass=hue
	//  - POST /logout
	//  - POST /label?id=UA1

	log.Fatalln(http.ListenAndServe(address, nil))
}
