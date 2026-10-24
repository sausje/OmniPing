//go:build !linux

package main

// dropPrivileges only does something on Linux, where the Docker image runs.
func dropPrivileges(dataDir string) {}
