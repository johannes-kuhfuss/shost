package main

import (
	"demo-service/app"
	"log"
)

func main() {
	if err := app.StartApp(); err != nil {
		log.Fatal(err)
	}
}
