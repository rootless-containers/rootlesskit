package version

// Version is set at build time from the repository tag.
//
// Keep a useful fallback for source archives that do not contain Git metadata.
var Version = "unknown"
