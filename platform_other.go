//go:build !windows

package main

// platformMain runs OmniPing as a plain server everywhere but Windows.
func platformMain(cli cliOptions) int { return runClassic(cli) }
