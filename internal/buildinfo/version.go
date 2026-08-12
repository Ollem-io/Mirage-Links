// Package buildinfo holds build metadata supplied by the build system.
package buildinfo

// Version is overridden at link time with -X. Development builds deliberately
// retain this useful, explicit value.
var Version = "dev"
