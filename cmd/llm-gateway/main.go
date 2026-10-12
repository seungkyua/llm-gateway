// Command llm-gateway starts the llm-gateway reverse proxy process.
package main

import "os"

func main() {
	os.Exit(run(os.Args[1:]))
}
