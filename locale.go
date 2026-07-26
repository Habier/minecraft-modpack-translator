package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"modpack-translator/internal/minecraftlocale"
)

type localeEnv func(string) string

func selectTargetLocale(input io.Reader, output io.Writer, getenv localeEnv) (string, error) {
	defaultLocale := detectDefaultTargetLocale(getenv)
	fmt.Fprintf(output, "Introduzca idioma al que traducir [%s]: ", defaultLocale)

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

func detectDefaultTargetLocale(getenv localeEnv) string {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG", "LANGUAGE"} {
		for _, value := range strings.Split(getenv(name), ":") {
			if locale, ok := normalizeMinecraftLocale(value); ok {
				return locale
			}
		}
	}
	return defaultTargetLanguageCode
}

func normalizeMinecraftLocale(value string) (string, bool) {
	return minecraftlocale.Normalize(value)
}
