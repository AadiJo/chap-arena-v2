// Copyright 2014 Team 254. All Rights Reserved.
// Author: pat@patfairbank.com (Patrick Fairbank)

package main

import (
	"flag"
	"github.com/Team254/cheesy-arena/model"
	"github.com/Team254/cheesy-arena/practice"
	"log"
	"net/http"
	"time"
)

func main() {
	address := flag.String("listen", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "chap-arena.db", "Configuration database path")
	flag.Parse()

	database, err := model.OpenDatabase(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer database.Close()
	app, err := practice.NewServer(database)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *address, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("chap-arena-v2 listening on %s", *address)
	log.Fatal(server.ListenAndServe())
}
