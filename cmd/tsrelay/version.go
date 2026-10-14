package main

// version is the relay's version. The release workflow sets it with
// -ldflags "-X main.version=v1.2.3" (see .github/workflows/release.yml
// and the Dockerfile's VERSION build arg). An unmodified build says "dev".
var version = "dev"
