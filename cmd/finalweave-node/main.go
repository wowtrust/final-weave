package main

import (
	"fmt"
	"os"

	"github.com/wowtrust/final-weave/internal/buildinfo"
	"github.com/wowtrust/final-weave/internal/cli"
)

func main() {
	if err := cli.NewNodeCommand(os.Stdout, os.Stderr, buildinfo.Current()).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
