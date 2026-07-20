//go:build !windows

package main

func isReparsePoint(string) bool {
	return false
}
