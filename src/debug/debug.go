package debug

import (
	"log"
)

// Dbg controls whether debug output is printed.
// Set via the --dbg CLI flag.
var Dbg bool

// Log prints a debug message if Dbg is enabled.
func Log(format string, args ...any) {
	if Dbg {
		log.Printf("[debug] "+format, args...)
	}
}
