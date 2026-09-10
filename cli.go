package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"modpack-translator/internal/provider"
)

type cliOptions struct {
	modpackPath string
	translate   bool
	refresh     bool
	force       bool
	debug       bool
}

type commandDependencies struct {
	languageLimits languageLimits
	getenv         func(string) string
	output         io.Writer
	runTranslation func(cliOptions, languageLimits) error
}

type duplicateRejectingBool struct {
	name  string
	value *bool
	set   bool
}

func (flag *duplicateRejectingBool) Set(value string) error {
	if flag.set {
		return fmt.Errorf("--%s may only be specified once", flag.name)
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	*flag.value = parsed
	flag.set = true
	return nil
}

func (flag *duplicateRejectingBool) String() string { return strconv.FormatBool(*flag.value) }
func (flag *duplicateRejectingBool) Type() string   { return "bool" }

func newRootCommand(dependencies commandDependencies) *cobra.Command {
	var options cliOptions
	root := &cobra.Command{
		Use:           "modpack-translator [flags] [modpack-path]",
		Short:         "Translate Minecraft Java modpacks",
		Args:          cobra.MaximumNArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 1 {
				options.modpackPath = args[0]
			}
			return dependencies.runTranslation(options, dependencies.languageLimits)
		},
	}
	root.SetOut(dependencies.output)
	root.SetErr(io.Discard)
	addRootBoolFlag(root, "translate", &options.translate, "translate catalog and write completed translations back")
	addRootBoolFlag(root, "refresh", &options.refresh, "refresh extracted language sources")
	addRootBoolFlag(root, "force", &options.force, "continue past safe malformed target language files")
	addRootBoolFlag(root, "debug", &options.debug, "quarantine malformed provider response content for this invocation")
	root.AddCommand(newModelsCommand(dependencies.getenv, dependencies.output))
	return root
}

func addRootBoolFlag(command *cobra.Command, name string, target *bool, usage string) {
	command.Flags().Var(&duplicateRejectingBool{name: name, value: target}, name, usage)
	command.Flags().Lookup(name).NoOptDefVal = "true"
}

func newModelsCommand(getenv func(string) string, output io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "models",
		Short: "List models available from every configured provider",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			results, err := provider.ListModels(context.Background(), getenv)
			for _, result := range results {
				fmt.Fprintf(output, "Provider: %s\n", result.Provider)
				if result.ConfiguredModel != "" {
					found := false
					for _, model := range result.Models {
						if model == result.ConfiguredModel {
							found = true
						}
					}
					status := "missing"
					if found {
						status = "available"
					}
					fmt.Fprintf(output, "Configured model: %s (%s)\n", result.ConfiguredModel, status)
				}
				if result.Err != nil {
					fmt.Fprintf(output, "Error: %v\n\n", result.Err)
					continue
				}
				if len(result.Models) == 0 {
					fmt.Fprintln(output, "Models: none")
				} else {
					fmt.Fprintln(output, "Models:")
					for _, model := range result.Models {
						marker := ""
						if model == result.ConfiguredModel {
							marker = " (configured)"
						}
						fmt.Fprintf(output, "  - %s%s\n", model, marker)
					}
				}
				fmt.Fprintln(output)
			}
			return err
		},
	}
}
