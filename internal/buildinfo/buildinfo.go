// Package buildinfo carries the version stamped into the gpty/gmux binaries.
package buildinfo

// Version is the gpty release version. The installers and goreleaser override
// it at link time with -ldflags "-X .../internal/buildinfo.Version=vX.Y.Z".
// Keep this in lockstep with CHANGELOG.md (semver: patch=fix, minor=feature,
// major=breaking).
var Version = "0.7.3"
