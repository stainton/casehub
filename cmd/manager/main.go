package main

import (
	"log"

	app "github.com/stainton/casehub/cmd/manager/app"
)

func main() {
	opts := app.NewOptions()
	opts.AddFlags()
	if err := app.Run(opts); err != nil {
		log.Fatal(err)
	}
}
