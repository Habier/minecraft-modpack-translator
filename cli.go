package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/pflag"
)

type cliOptions struct {
	modpackPath string
	translate   bool
	refresh     bool
}

func parseCLI(args []string) (cliOptions, error) {
	var options cliOptions
	if err := rejectDuplicateFlag(args, "translate"); err != nil {
		return cliOptions{}, err
	}
	if err := rejectDuplicateFlag(args, "refresh"); err != nil {
		return cliOptions{}, err
	}

	flags := pflag.NewFlagSet("modpack-translator", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.SetInterspersed(true)
	flags.BoolVar(&options.translate, "translate", false, "translate catalog and write completed translations back")
	flags.BoolVar(&options.refresh, "refresh", false, "refresh extracted language sources")

	if err := flags.Parse(args); err != nil {
		return cliOptions{}, err
	}

	paths := flags.Args()
	if len(paths) > 1 {
		return cliOptions{}, fmt.Errorf("usage: modpack-translator [--translate] [--refresh] [modpack-path]")
	}
	if len(paths) == 1 {
		options.modpackPath = paths[0]
	}
	return options, nil
}

func rejectDuplicateFlag(args []string, name string) error {
	count := 0
	flag := "--" + name
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			count++
			if count > 1 {
				return fmt.Errorf("%s may only be specified once", flag)
			}
		}
	}
	return nil
}
