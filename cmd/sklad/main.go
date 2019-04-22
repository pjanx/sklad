package main

import (
	"context"
	"errors"
	"html"
	"html/template"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"janouch.name/sklad/imgutil"
	"janouch.name/sklad/label"
	"janouch.name/sklad/ql"
)

var templates = map[string]*template.Template{}

func executeTemplate(name string, w io.Writer, data interface{}) {
	if err := templates[name].Execute(w, data); err != nil {
		panic(err)
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	redirect := r.FormValue("redirect")
	if redirect == "" {
		redirect = "container"
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
	http.Redirect(w, r, "login", http.StatusSeeOther)
}

func handleContainerPost(r *http.Request) error {
	id := ContainerId(r.FormValue("id"))
	description := strings.TrimSpace(r.FormValue("description"))
	series := r.FormValue("series")
	parent := ContainerId(strings.TrimSpace(r.FormValue("parent")))
	_, remove := r.Form["remove"]

	if container, ok := indexContainer[id]; ok {
		if remove {
			return dbContainerRemove(container)
		} else {
			c := *container
			c.Description = description
			c.Series = series
			c.Parent = parent
			return dbContainerUpdate(container, c)
		}
	} else if remove {
		return errNoSuchContainer
	} else {
		return dbContainerCreate(&Container{
			Series:      series,
			Parent:      parent,
			Description: description,
		})
	}
}

func handleContainer(w http.ResponseWriter, r *http.Request) {
	// When deleting, do not try to show the deleted entry but the context.
	shownId := r.FormValue("context")
	if shownId == "" {
		shownId = r.FormValue("id")
	}

	var err error
	if r.Method == http.MethodPost {
		if err = handleContainerPost(r); err == nil {
			redirect := "container"
			if shownId != "" {
				redirect += "?id=" + url.QueryEscape(shownId)
			}
			http.Redirect(w, r, redirect, http.StatusSeeOther)
			return
		}
		// TODO: Use the last data as a prefill.
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	allSeries := map[string]string{}
	for _, s := range indexSeries {
		allSeries[s.Prefix] = s.Description
	}

	var container *Container
	children := indexChildren[""]

	if c, ok := indexContainer[ContainerId(shownId)]; ok {
		children = c.Children()
		container = c
	}

	params := struct {
		Error                           error
		ErrorNoSuchSeries               bool
		ErrorContainerAlreadyExists     bool
		ErrorNoSuchContainer            bool
		ErrorCannotChangeSeriesNotEmpty bool
		ErrorCannotChangeNumber         bool
		ErrorWouldContainItself         bool
		ErrorContainerInUse             bool
		Container                       *Container
		Children                        []*Container
		AllSeries                       map[string]string
	}{
		Error:                           err,
		ErrorNoSuchSeries:               err == errNoSuchSeries,
		ErrorContainerAlreadyExists:     err == errContainerAlreadyExists,
		ErrorNoSuchContainer:            err == errNoSuchContainer,
		ErrorCannotChangeSeriesNotEmpty: err == errCannotChangeSeriesNotEmpty,
		ErrorCannotChangeNumber:         err == errCannotChangeNumber,
		ErrorWouldContainItself:         err == errWouldContainItself,
		ErrorContainerInUse:             err == errContainerInUse,
		Container:                       container,
		Children:                        children,
		AllSeries:                       allSeries,
	}

	executeTemplate("container.tmpl", w, &params)
}

func handleSeriesPost(r *http.Request) error {
	prefix := strings.TrimSpace(r.FormValue("prefix"))
	description := strings.TrimSpace(r.FormValue("description"))
	_, remove := r.Form["remove"]

	if series, ok := indexSeries[prefix]; ok {
		if remove {
			return dbSeriesRemove(series)
		} else {
			s := *series
			s.Description = description
			return dbSeriesUpdate(series, s)
		}
	} else if remove {
		return errNoSuchSeries
	} else {
		return dbSeriesCreate(&Series{
			Prefix:      prefix,
			Description: description,
		})
	}
}

func handleSeries(w http.ResponseWriter, r *http.Request) {
	var err error
	if r.Method == http.MethodPost {
		if err = handleSeriesPost(r); err == nil {
			http.Redirect(w, r, "series", http.StatusSeeOther)
			return
		}
		// XXX: This is rather ugly.
		r.Form = url.Values{}
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	allSeries := map[string]*Series{}
	for _, s := range indexSeries {
		allSeries[s.Prefix] = s
	}

	prefix := r.FormValue("prefix")
	description := ""

	if prefix == "" {
	} else if series, ok := indexSeries[prefix]; ok {
		description = series.Description
	} else {
		err = errNoSuchSeries
	}

	params := struct {
		Error                    error
		ErrorInvalidPrefix       bool
		ErrorSeriesAlreadyExists bool
		ErrorCannotChangePrefix  bool
		ErrorNoSuchSeries        bool
		ErrorSeriesInUse         bool
		Prefix                   string
		Description              string
		AllSeries                map[string]*Series
	}{
		Error:                    err,
		ErrorInvalidPrefix:       err == errInvalidPrefix,
		ErrorSeriesAlreadyExists: err == errSeriesAlreadyExists,
		ErrorCannotChangePrefix:  err == errCannotChangePrefix,
		ErrorNoSuchSeries:        err == errNoSuchSeries,
		ErrorSeriesInUse:         err == errSeriesInUse,
		Prefix:                   prefix,
		Description:              description,
		AllSeries:                allSeries,
	}

	executeTemplate("series.tmpl", w, &params)
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	query := r.FormValue("q")
	params := struct {
		Query      string
		Series     []*Series
		Containers []*Container
	}{
		Query:      query,
		Series:     dbSearchSeries(query),
		Containers: dbSearchContainers(query),
	}

	executeTemplate("search.tmpl", w, &params)
}

func printLabel(id string) error {
	printer, err := ql.Open()
	if err != nil {
		return err
	}
	if printer == nil {
		return errors.New("no suitable printer found")
	}
	defer printer.Close()

	/*
		printer.StatusNotify = func(status *ql.Status) {
			log.Printf("\x1b[1mreceived status\x1b[m\n%+v\n%s",
				status[:], status)
		}
	*/

	if err := printer.Initialize(); err != nil {
		return err
	}
	if err := printer.UpdateStatus(); err != nil {
		return err
	}

	mediaInfo := ql.GetMediaInfo(
		printer.LastStatus.MediaWidthMM(),
		printer.LastStatus.MediaLengthMM(),
	)
	if mediaInfo == nil {
		return errors.New("unknown media")
	}

	return printer.Print(&imgutil.LeftRotate{Image: label.GenLabelForHeight(
		labelFont, id, mediaInfo.PrintAreaPins, db.BDFScale)})
}

func handleLabel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	params := struct {
		Id        string
		UnknownId bool
		Error     error
	}{
		Id: r.FormValue("id"),
	}

	if c := indexContainer[ContainerId(params.Id)]; c == nil {
		params.UnknownId = true
	} else {
		params.Error = printLabel(params.Id)
	}

	executeTemplate("label.tmpl", w, &params)
}

var mutex sync.Mutex

func handle(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodGet {
		w.Header().Set("Cache-Control", "no-store")
	}

	mutex.Lock()
	defer mutex.Unlock()

	switch _, base := path.Split(r.URL.Path); base {
	case "login":
		handleLogin(w, r)
	case "logout":
		sessionWrap(handleLogout)(w, r)

	case "container":
		sessionWrap(handleContainer)(w, r)
	case "series":
		sessionWrap(handleSeries)(w, r)
	case "search":
		sessionWrap(handleSearch)(w, r)
	case "label":
		sessionWrap(handleLabel)(w, r)

	case "":
		http.Redirect(w, r, "container", http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

var funcMap = template.FuncMap{
	"max": func(i, j int) int {
		if i > j {
			return i
		}
		return j
	},
	"lines": func(s string) int {
		return strings.Count(s, "\n") + 1
	},
	"highlight": func(highlight, s string) template.HTML {
		b, last := strings.Builder{}, 0
		for _, m := range regexp.MustCompile(
			`(?i:`+regexp.QuoteMeta(highlight)+`)`).FindAllStringIndex(s, -1) {
			b.WriteString(html.EscapeString(s[last:m[0]]))
			b.WriteString(`<mark>`)
			b.WriteString(html.EscapeString(s[m[0]:m[1]]))
			b.WriteString(`</mark>`)
			last = m[1]
		}
		b.WriteString(html.EscapeString(s[last:]))
		return template.HTML(b.String())
	},
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
		templates[name] = template.Must(template.New("base.tmpl").
			Funcs(funcMap).ParseFiles("base.tmpl", name))
	}

	http.HandleFunc("/", handle)
	server := &http.Server{Addr: address}

	sigs := make(chan os.Signal, 1)
	errs := make(chan error, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() { errs <- server.ListenAndServe() }()

	select {
	case <-sigs:
	case err := <-errs:
		log.Println(err)
	}

	// Wait for all HTTP goroutines to finish so that not even the database
	// log gets corrupted by an interrupted update.
	if err := server.Shutdown(context.Background()); err != nil {
		log.Fatalln(err)
	}
}
