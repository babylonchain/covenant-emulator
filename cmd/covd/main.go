package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli"
)

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "[covd] %v\n", err)
	os.Exit(1)
}

func main() {
	app := cli.NewApp()
	app.Name = "covd"
	app.Usage = "Covenant Emulator Daemon (covd)."
	app.Commands = append(app.Commands, startCommand, initCommand, createKeyCommand)

	if err := app.Run(os.Args); err != nil {
		fatal(err)
	}
}
