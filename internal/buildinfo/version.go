// Package buildinfo holds build metadata supplied by the build system.
package buildinfo

// Version is overridden at link time with -X. Release builds override this stable v1 identifier with -X.
var Version = "v1.0.0"
