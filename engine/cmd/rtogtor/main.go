// Command rtogtor is the RtoGtoR command-line interface.
package main

import (
	"os"

	"github.com/TuanFaied/rtogtor/engine/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
