package main

// buildVersion is set at link time (-ldflags "-X main.buildVersion=...";
// the Dockerfile's VERSION build argument, fed by `make` from git
// describe). A plain `go build` reports "dev". The api shows it and every
// worker reports its own in its heartbeat, so a half-updated installation
// (ADR-038) is visible on the status screen.
var buildVersion = "dev"
