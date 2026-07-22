package main

import "fmt"

type cliOptions struct {
	modpackPath string
	translate   bool
	refresh     bool
}

func parseCLI(args []string) (cliOptions, error) {
	var options cliOptions
	for _, arg := range args {
		switch arg {
		case "--translate":
			if options.translate {
				return cliOptions{}, fmt.Errorf("--translate may only be specified once")
			}
			options.translate = true
		case "--refresh":
			if options.refresh {
				return cliOptions{}, fmt.Errorf("--refresh may only be specified once")
			}
			options.refresh = true
		default:
			if len(arg) > 0 && arg[0] == '-' {
				return cliOptions{}, fmt.Errorf("unknown option %q", arg)
			}
			if options.modpackPath != "" {
				return cliOptions{}, fmt.Errorf("usage: modpack-translator [--translate] [--refresh] [modpack-path]")
			}
			options.modpackPath = arg
		}
	}
	return options, nil
}
