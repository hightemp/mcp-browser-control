package app

import "fmt"

// Release metadata is overridden with -ldflags by reproducible release builds.
var (
	Version   = "0.3.0"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func versionRequested(args []string) bool {
	for _, argument := range args {
		if argument == "-version" || argument == "--version" {
			return true
		}
	}
	return false
}

func versionText() string {
	return fmt.Sprintf(
		"mcp-browser-control %s\ncommit: %s\nbuilt: %s",
		Version,
		Commit,
		BuildDate,
	)
}
