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

// TODO: Consider wrapping the data object in something that always contains
// a LoggedIn member, so that we don't need to duplicate it.
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
		LoggedIn          bool
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
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	children := []*Container{}
	id := ContainerId(r.FormValue("id"))
	description := ""

	if id == "" {
		children = db.Containers
	} else if container, ok := indexContainer[id]; ok {
		children = indexChildren[id]
		description = container.Description
	}

	params := struct {
		LoggedIn    bool
		Id          ContainerId
		Description string
		Children    []*Container
	}{
		LoggedIn:    true,
		Id:          id,
		Description: description,
		Children:    children,
	}

	executeTemplate("container.tmpl", w, &params)
}

// TODO: Consider a wrapper function that automatically calls ParseForm
// and disables client-side caching.

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

	// TODO: Some routing and pages.
	//
	//  - GET /container?id=UA1
	//  - GET /series?id=A
	//  - GET /search?q=bottle
	//
	//  - https://stackoverflow.com/a/33880971/76313
	//  - POST /label?id=UA1

	http.HandleFunc("/", sessionWrap(wrap(handleContainer)))
	http.HandleFunc("/container", sessionWrap(wrap(handleContainer)))

	http.HandleFunc("/login", wrap(handleLogin))
	http.HandleFunc("/logout", sessionWrap(wrap(handleLogout)))

	log.Fatalln(http.ListenAndServe(address, nil))
}
