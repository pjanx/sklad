package main

import (
	"context"
	"encoding/hex"
	"math/rand"
	"net/http"
	"net/url"
)

// session storage indexed by a random UUID
var sessions = map[string]*Session{}

type Session struct {
	LoggedIn bool // may access the DB
}

type sessionContextKey struct{}

func sessionGenId() string {
	u := make([]byte, 16)
	if _, err := rand.Read(u); err != nil {
		panic("cannot generate random bytes")
	}
	return hex.EncodeToString(u)
}

// TODO: We don't want to keep an unlimited amount of cookies in the storage.
//  - The essential question is: how do we avoid DoS?
//  - Which cookies are worth keeping?
//     - Definitely logged-in users, only one person should know the password.
//  - Evict by FIFO? LRU?
func sessionGet(w http.ResponseWriter, r *http.Request) (session *Session) {
	if c, _ := r.Cookie("sessionid"); c != nil {
		session, _ = sessions[c.Value]
	}
	if session == nil {
		id := sessionGenId()
		session = &Session{LoggedIn: false}
		sessions[id] = session
		http.SetCookie(w, &http.Cookie{Name: "sessionid", Value: id})
	}
	return
}

func sessionWrap(inner func(http.ResponseWriter, *http.Request)) func(
	http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		// We might also try no-cache with an ETag for the whole database,
		// though I don't expect any substantial improvements of anything.
		w.Header().Set("Cache-Control", "no-store")

		redirect := "/login"
		if r.RequestURI != "/" {
			redirect += "?redirect=" + url.QueryEscape(r.RequestURI)
		}

		session := sessionGet(w, r)
		if !session.LoggedIn {
			http.Redirect(w, r, redirect, http.StatusSeeOther)
			return
		}
		inner(w, r.WithContext(
			context.WithValue(r.Context(), sessionContextKey{}, session)))
	}
}
