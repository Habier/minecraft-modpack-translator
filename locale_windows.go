//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const localeNameMaxLength = 85

var getUserDefaultLocaleName = syscall.NewLazyDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")

func detectSystemLocale() string {
	buffer := make([]uint16, localeNameMaxLength)
	written, _, _ := getUserDefaultLocaleName.Call(
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
	)
	if written == 0 {
		return ""
	}
	return syscall.UTF16ToString(buffer)
}
