package main

import (
	"html/template"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var templates = map[string]*template.Template{}

func executeTemplate(name string, w io.Writer, data interface{}) {
	if err := templates[name].Execute(w, data); err != nil {
		panic(err)
	}
}

func wrap(inner func(http.ResponseWriter, *http.Request)) func(
	http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Cache-Control", "no-store")
		}
		inner(w, r)
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	redirect := r.FormValue("redirect")
	if redirect == "" {
		redirect = "/"
	}

	session := sessionGet(w, r)
	if session.LoggedIn {
		http.Redirect(w, r, redirect, http.StatusSeeOther)
		return
	}

	params := struct {
		IncorrectPassword bool
	}{}

	switch r.Method {
	case http.MethodGet:
		// We're just going to render the template.
	case http.MethodPost:
		if r.FormValue("password") == db.Password {
			session.LoggedIn = true
			http.Redirect(w, r, redirect, http.StatusSeeOther)
			return
		}
		params.IncorrectPassword = true
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	executeTemplate("login.tmpl", w, &params)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	session := r.Context().Value(sessionContextKey{}).(*Session)
	session.LoggedIn = false
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleContainer(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// TODO
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	allSeries := map[string]string{}
	for _, s := range indexSeries {
		allSeries[s.Prefix] = s.Description
	}

	var container *Container
	children := []*Container{}

	if id := ContainerId(r.FormValue("id")); id == "" {
		children = indexChildren[""]
	} else if c, ok := indexContainer[id]; ok {
		children = c.Children()
		container = c
	}

	params := struct {
		Container *Container
		Children  []*Container
		AllSeries map[string]string
	}{
		Container: container,
		Children:  children,
		AllSeries: allSeries,
	}

	executeTemplate("container.tmpl", w, &params)
}

func handleSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		// TODO
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	allSeries := map[string]string{}
	for _, s := range indexSeries {
		allSeries[s.Prefix] = s.Description
	}

	prefix := r.FormValue("prefix")
	description := ""

	if prefix == "" {
	} else if series, ok := indexSeries[prefix]; ok {
		description = series.Description
	}

	params := struct {
		Prefix      string
		Description string
		AllSeries   map[string]string
	}{
		Prefix:      prefix,
		Description: description,
		AllSeries:   allSeries,
	}

	executeTemplate("series.tmpl", w, &params)
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	query := r.FormValue("q")
	_ = query

	// TODO: Query the database for exact matches and fulltext.
	//  - Will want to show the full path from the root "" container.

	params := struct{}{}

	executeTemplate("search.tmpl", w, &params)
}

func handleLabel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	id := r.FormValue("id")
	_ = id

	// TODO: See if such a container exists, print a label on the printer.

	params := struct{}{}

	executeTemplate("label.tmpl", w, &params)
}

func main() {
	// Randomize the RNG for session string generation.
	rand.Seed(time.Now().UnixNano())

	if len(os.Args) != 3 {
		log.Fatalf("Usage: %s ADDRESS DATABASE-FILE\n", os.Args[0])
	}

	var address string
	address, dbPath = os.Args[1], os.Args[2]

	// Load database.
	if err := loadDatabase(); err != nil {
		log.Fatalln(err)
	}

	// Load HTML templates from the current working directory.
	m, err := filepath.Glob("*.tmpl")
	if err != nil {
		log.Fatalln(err)
	}
	for _, name := range m {
		templates[name] = template.Must(template.ParseFiles("base.tmpl", name))
	}

	// TODO: Eventually we will need to load a font file for label printing.
	//  - The path might be part of configuration, or implicit by filename.

	http.HandleFunc("/login", wrap(handleLogin))
	http.HandleFunc("/logout", sessionWrap(wrap(handleLogout)))

	http.HandleFunc("/", sessionWrap(wrap(handleContainer)))
	http.HandleFunc("/series", sessionWrap(wrap(handleSeries)))
	http.HandleFunc("/search", sessionWrap(wrap(handleSearch)))
	http.HandleFunc("/label", sessionWrap(wrap(handleLabel)))

	log.Fatalln(http.ListenAndServe(address, nil))
}
