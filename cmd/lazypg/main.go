// Command lazypg is a Supabase-CLI-style PostgreSQL development workflow tool.
package main

import (
	"os"

	"github.com/daviddwlee84/lazypg/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
