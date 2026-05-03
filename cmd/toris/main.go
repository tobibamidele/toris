// Command toris is the entry point for the toris CLI.
// All logic lives in internal/; this file is a thin dispatcher.
package main

import "github.com/tobibamidele/toris/internal/cli"

func main() {
	cli.Execute()
}
