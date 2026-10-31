// Package filters holds the built-in groups, one rules file each in the
// shared filter syntax, compiled into the binary. filterrules reads them.
package filters

import "embed"

//go:embed *.filter
var FS embed.FS
