// Package buildinfo exposes immutable metadata about the current binary.
// Release builds inject version, commit, and date with -ldflags -X.
package buildinfo

import "runtime"

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Info describes one FinalWeave binary build. These fields are diagnostic
// metadata and are not part of any consensus transcript or protocol identity.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Current returns build metadata for the running binary.
func Current() Info {
	return Info{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
