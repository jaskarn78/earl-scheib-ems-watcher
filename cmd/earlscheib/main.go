package main

import (
	"fmt"
	"os"
)

// secretKey is injected at build time via:
//
//	-ldflags "-X main.secretKey=<value>"
//
// Never set a real secret here — this default is for dev/test builds only.
var secretKey = "dev-test-secret-do-not-use-in-production"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	switch os.Args[1] {
	case "--tray":
		runStub("tray")
	case "--scan":
		runStub("scan")
	case "--wizard":
		runStub("wizard")
	case "--test":
		runStub("test")
	case "--status":
		runStub("status")
	case "--install":
		runStub("install")
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func runStub(name string) {
	fmt.Printf("earlscheib %s: not yet implemented\n", name)
	os.Exit(0)
}

func printUsage() {
	fmt.Println("usage: earlscheib [--tray|--scan|--wizard|--test|--status|--install]")
}
