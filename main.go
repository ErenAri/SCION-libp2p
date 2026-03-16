package main

import (
	"os"

	"github.com/ErenAri/PathAware-libp2p/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
