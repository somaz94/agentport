package main

import (
	"os"

	"github.com/somaz94/agentport/cmd/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
