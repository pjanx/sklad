package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"janouch.name/sklad/bdf"
)

type Series struct {
	Prefix      string // PK: prefix
	Description string // what kind of containers this is for
}

type ContainerId string

type Container struct {
	Series      string      // PK: what series does this belong to
	Number      uint        // PK: order within the series
	Parent      ContainerId // the container we're in, if any, otherwise ""
	Description string      // description and/or contents of this container
}

func (c *Container) Id() ContainerId {
	return ContainerId(fmt.Sprintf("%s%s%d", db.Prefix, c.Series, c.Number))
}

func (c *Container) Children() []*Container {
	// TODO: Sort this by Id, or maybe even return a map[string]*Container,
	// text/template would sort that automatically.
	return indexChildren[c.Id()]
}

func (c *Container) Path() (result []ContainerId) {
	for c != nil && c.Parent != "" {
		c = indexContainer[c.Parent]
		result = append(result, c.Id())
	}
	return
}

type Database struct {
	Password   string       // password for web users
	Prefix     string       // prefix for all container IDs
	Series     []*Series    // all known series
	Containers []*Container // all known containers

	BDFPath  string // path to bitmap font file
	BDFScale int    // integer scaling for the bitmap font
}

var (
	dbPath string
	db     Database
	dbLast Database
	dbLog  *os.File

	indexSeries    = map[string]*Series{}
	indexContainer = map[ContainerId]*Container{}
	indexChildren  = map[ContainerId][]*Container{}

	labelFont *bdf.Font
)

// TODO: Some functions to add, remove and change things in the database.
// Indexes must be kept valid, just like any invariants.

func dbSearchSeries(query string) (result []*Series) {
	query = strings.ToLower(query)
	added := map[string]bool{}
	for _, s := range db.Series {
		if query == strings.ToLower(s.Prefix) {
			result = append(result, s)
			added[s.Prefix] = true
		}
	}
	for _, s := range db.Series {
		if strings.Contains(
			strings.ToLower(s.Description), query) && !added[s.Prefix] {
			result = append(result, s)
		}
	}
	return
}

func dbSearchContainers(query string) (result []*Container) {
	query = strings.ToLower(query)
	added := map[ContainerId]bool{}
	for id, c := range indexContainer {
		if query == strings.ToLower(string(id)) {
			result = append(result, c)
			added[id] = true
		}
	}
	for id, c := range indexContainer {
		if strings.Contains(
			strings.ToLower(c.Description), query) && !added[id] {
			result = append(result, c)
		}
	}
	return
}

func dbCommit() error {
	// Write a timestamp.
	e := json.NewEncoder(dbLog)
	e.SetIndent("", "  ")
	if err := e.Encode(time.Now().Format(time.RFC3339)); err != nil {
		return err
	}

	// Back up the current database contents.
	if err := e.Encode(&dbLast); err != nil {
		return err
	}
	if err := dbLog.Sync(); err != nil {
		return err
	}

	// Atomically replace the current database file.
	tempPath := dbPath + ".new"
	temp, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer temp.Close()

	e = json.NewEncoder(temp)
	e.SetIndent("", "  ")
	if err := e.Encode(&db); err != nil {
		return err
	}

	if err := os.Rename(tempPath, dbPath); err != nil {
		return err
	}

	dbLast = db
	return nil
}

// loadDatabase loads the database from a simple JSON file. We do not use
// any SQL stuff or even external KV storage because there is no real need
// for our trivial use case, with our general amount of data.
func loadDatabase() error {
	dbFile, err := os.Open(dbPath)
	if err != nil {
		return err
	}
	if err := json.NewDecoder(dbFile).Decode(&db); err != nil {
		return err
	}

	// Further validate the database.
	if db.Prefix == "" {
		return errors.New("misconfigured prefix")
	}

	// Construct indexes for primary keys, validate against duplicates.
	for _, pv := range db.Series {
		if _, ok := indexSeries[pv.Prefix]; ok {
			return fmt.Errorf("duplicate series: %s", pv.Prefix)
		}
		indexSeries[pv.Prefix] = pv
	}
	for _, pv := range db.Containers {
		id := pv.Id()
		if _, ok := indexContainer[id]; ok {
			return fmt.Errorf("duplicate container: %s", id)
		}
		indexContainer[id] = pv
	}

	// Construct an index that goes from parent containers to their children.
	for _, pv := range db.Containers {
		if pv.Parent != "" {
			if _, ok := indexContainer[pv.Parent]; !ok {
				return fmt.Errorf("container %s has a nonexistent parent %s",
					pv.Id(), pv.Parent)
			}
		}
		indexChildren[pv.Parent] = append(indexChildren[pv.Parent], pv)
	}

	// Validate that no container is a parent of itself on any level.
	// This could probably be optimized but it would stop being obvious.
	for _, pv := range db.Containers {
		parents := map[ContainerId]bool{pv.Id(): true}
		for pv.Parent != "" {
			if parents[pv.Parent] {
				return fmt.Errorf("%s contains itself", pv.Parent)
			}
			parents[pv.Parent] = true
			pv = indexContainer[pv.Parent]
		}
	}

	// Prepare label printing.
	if db.BDFScale <= 0 {
		db.BDFScale = 1
	}

	if f, err := os.Open(db.BDFPath); err != nil {
		return fmt.Errorf("cannot load label font: %s", err)
	} else {
		defer f.Close()
		if labelFont, err = bdf.NewFromBDF(f); err != nil {
			return fmt.Errorf("cannot load label font: %s", err)
		}
	}

	// Open database log file for appending.
	if dbLog, err = os.OpenFile(dbPath+".log",
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err != nil {
		return err
	}

	// Remember the current state of the database.
	dbLast = db
	return nil
}
