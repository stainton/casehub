package main

import (
	"log"

	app "github.com/stainton/casehub/cmd/api/app"
)

func main() {
	opts := app.NewOptions()
	opts.AddFlags()
	if err := app.Run(opts); err != nil {
		log.Fatal(err)
	}
}
