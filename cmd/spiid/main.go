// Package main runs Spiid and its local browser interface.
package main

import (
	"flag"
	"fmt"
	"os"

	"spiid/internal/app"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Println(new(app.Service).Version())
		return 0
	}
	fs := flag.NewFlagSet("spiid", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: spiid [--port N] [--no-browser] [--verbose]\n       spiid version")
	}
	port := fs.Int("port", 0, "loopback port (0 = random)")
	noBrowser := fs.Bool("no-browser", false, "do not open browser")
	verbose := fs.Bool("verbose", false, "log HTTP requests")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fs.Usage()
		return 2
	}
	if fs.NArg() != 0 || *port < 0 || *port > 65535 {
		fs.Usage()
		return 2
	}
	if err := serve(*port, *noBrowser, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
