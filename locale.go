package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"modpack-translator/internal/minecraftlocale"
)

type localeEnv func(string) string
type systemLocale func() string

func selectTargetLocale(input io.Reader, output io.Writer, getenv localeEnv, system systemLocale) (string, error) {
	defaultLocale := detectDefaultTargetLocale(getenv, system)
	fmt.Fprintf(output, "Enter target language [%s]: ", defaultLocale)

	reader := bufio.NewReader(input)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read target language: %w", err)
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return defaultLocale, nil
	}
	locale, ok := normalizeMinecraftLocale(value)
	if !ok {
		return "", fmt.Errorf("unsupported Minecraft locale %q; use a language supported by Minecraft, like es_es, en_us, fr_fr, de_de, or pt_br", value)
	}
	return locale, nil
}

func detectDefaultTargetLocale(getenv localeEnv, system systemLocale) string {
	if locale, ok := detectEnvironmentLocale(getenv, "LC_ALL", "LC_MESSAGES"); ok {
		return locale
	}
	if locale, ok := normalizeMinecraftLocale(system()); ok {
		return locale
	}
	if locale, ok := detectEnvironmentLocale(getenv, "LANG", "LANGUAGE"); ok {
		return locale
	}
	return defaultTargetLanguageCode
}

func detectEnvironmentLocale(getenv localeEnv, names ...string) (string, bool) {
	for _, name := range names {
		for _, value := range strings.Split(getenv(name), ":") {
			if locale, ok := normalizeMinecraftLocale(value); ok {
				return locale, true
			}
		}
	}
	return "", false
}

func normalizeMinecraftLocale(value string) (string, bool) {
	return minecraftlocale.Normalize(value)
}
