package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

func loadExecutableEnv() error {
	executable, err := os.Executable()
	if err != nil {
		return errors.New("locate executable for .env loading: check that the program was started from a valid executable")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	execDir := filepath.Dir(executable)
	execPath := filepath.Join(execDir, ".env")
	if _, err := os.Stat(execPath); err == nil {
		return loadEnvFile(execPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load environment file %s: file exists but cannot be accessed; check its permissions", execPath)
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr == nil && cwd != execDir {
		cwdPath := filepath.Join(cwd, ".env")
		if _, err := os.Stat(cwdPath); err == nil {
			return loadEnvFile(cwdPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load environment file %s: file exists but cannot be accessed; check its permissions", cwdPath)
		}
	}
	return nil
}

func loadEnvFile(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("load environment file %s: file exists but cannot be accessed; check its permissions", path)
	}
	if err := godotenv.Load(path); err != nil {
		return fmt.Errorf("load environment file %s: file exists but could not be read or parsed; check permissions and KEY=VALUE syntax", path)
	}
	return nil
}
