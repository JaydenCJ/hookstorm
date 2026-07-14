// Command hookstorm stress-tests a webhook handler by delivering a
// deterministic storm of duplicates, retries, reordered and slow deliveries,
// and bad signatures, then reporting whether the handler held up.
package main

import (
	"os"

	"github.com/JaydenCJ/hookstorm/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
