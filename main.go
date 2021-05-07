package main

import (
	"fmt"
	"os"

	"github.com/fjogeleit/policy-reporter-kyverno-plugin/cmd"
)

func main() {
	if err := cmd.NewCLI().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
