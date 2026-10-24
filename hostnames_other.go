//go:build !windows

package main

// localDNSSuffixes is only needed by the Windows app.
func localDNSSuffixes() []string { return nil }
