//go:build !windows

package main

func detectSystemLocale() string {
	return ""
}
