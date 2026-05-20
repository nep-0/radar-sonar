package main

import (
	"log"

	"radar-sonar/mockserver"
)

const (
	httpAddr = "127.0.0.1:8082"
	height   = 0.0
)

func main() {
	if err := mockserver.Serve(httpAddr, "right", height); err != nil {
		log.Fatal(err)
	}
}
