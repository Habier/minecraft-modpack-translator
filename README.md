# Minecraft Modpack Translator

Translate Minecraft Java modpacks to a selected Minecraft locale.

Modpack Translator extracts translatable text from supported modpack sources, translates it with configured providers, and creates a ready-to-share resource pack ZIP for the selected locale. If you press Enter at the locale prompt, the default target locale is `es_es`.

> **Most users:** use a compiled release and follow the [Installation and User Guide](docs/INSTALLATION.md). You do not need Go.

## Basic usage

Automatically find modpacks and choose one to translate:

```powershell
modpack-translator --translate
```

Extract sources only:

```powershell
modpack-translator "C:\path\to\your\modpack"
```

Extract and translate:

```powershell
modpack-translator --translate "C:\path\to\your\modpack"
```

Continue past malformed target language JSON when extracting:

```powershell
modpack-translator --force "C:\path\to\your\modpack"
```

`--force` only ignores malformed existing target language files such as `es_es.json`. Source language files such as `en_us.json` still fail because they are the extraction source of truth.

Refresh existing target translations:

```powershell
modpack-translator --translate --refresh "C:\path\to\your\modpack"
```

By default, existing target-language files are read and already-covered keys are skipped. `--refresh` re-extracts those keys and overwrites the generated target entries. It does not make malformed target files valid; use `--force` if you want to continue past malformed target-language files.

When no path is provided, the tool searches common Minecraft launcher locations. If it finds one modpack, it uses it. If it finds several, it asks you to choose one by number.

## Requirements

- A Minecraft Java modpack folder with a `mods` directory
- Version metadata from `mmc-pack.json` or `manifest.json`
- Optional cloud provider API keys and model names
- Optional local Ollama fallback with the `qwen3:8b` model

## What it supports

| Source | Status |
|---|---|
| Mod `.jar` language files | Supported |
| Patchouli books | Supported |
| FTB Quests `.snbt` files | Supported |
| FTB Quests JSON5 language files | Supported |
| KubeJS language files | Supported |
| Existing target-language files inside mods | Read to skip already translated keys unless `--refresh`; original JARs are not modified |

## Output

The tool writes everything inside the selected modpack folder:

```text
<modpack>/
└── modpack-translator-output/
    ├── workspace/                         # working files, cache, failures
    └── export/
        ├── overrides/
        │   └── resourcepacks/
        │       └── ModpackTranslations/   # generated resource pack
        └── modpack-translations-<locale>.zip # shareable ZIP
```

Use either:

- `modpack-translator-output/export/modpack-translations-<locale>.zip`
- `modpack-translator-output/export/overrides/resourcepacks/ModpackTranslations/`

`workspace/` is internal working state. Do not distribute it.

## Translation Providers

Configure provider order with `PROVIDER_CHAIN`. Each non-`ollama` entry is treated as an OpenAI-compatible API and must have its own explicit block.

```text
PROVIDER_CHAIN=deepinfra,together,ollama

PROVIDER_DEEPINFRA_BASE_URL=https://api.deepinfra.com/v1/openai
PROVIDER_DEEPINFRA_API_KEY=<api-key>
PROVIDER_DEEPINFRA_MODEL=<model>
PROVIDER_DEEPINFRA_MODE=json_schema

PROVIDER_TOGETHER_BASE_URL=https://api.together.xyz/v1
PROVIDER_TOGETHER_API_KEY=<api-key>
PROVIDER_TOGETHER_MODEL=<model>
PROVIDER_TOGETHER_MODE=json_object
```

Names in `PROVIDER_CHAIN` may contain letters, numbers, underscores, and hyphens. For env lookup, names are uppercased and hyphens become underscores, so `together-ai` uses `PROVIDER_TOGETHER_AI_*`.

Supported provider modes are `json_schema` and `json_object`. Cloud provider base URLs must be HTTPS URLs without credentials, query strings, or fragments.

Ollama is used only when `ollama` appears in `PROVIDER_CHAIN`. If `PROVIDER_CHAIN` is unset, the default chain is `ollama` only.

API keys can come from your environment, from a `.env` file beside the compiled executable, or from a `.env` file in the current working directory when that directory is different. Keep `.env` private; it is plain text.

## Supported modpack discovery

Automatic discovery checks common locations for folders that contain a `mods` directory:

- The current working directory
- The official `.minecraft` folder
- CurseForge instances
- Prism Launcher, PolyMC, MultiMC, ATLauncher, and Modrinth App instances

It does not scan entire drives.

## Minecraft compatibility

Supported Minecraft Java releases:

| Minecraft Java releases | Resource pack format |
|---|---:|
| 1.16.5 | 6 |
| 1.17-1.17.1 | 7 |
| 1.18-1.18.2 | 8 |
| 1.19-1.19.2 | 9 |
| 1.19.3 | 12 |
| 1.19.4 | 13 |
| 1.20-1.20.1 | 15 |
| 1.20.2 | 18 |
| 1.20.3-1.20.4 | 22 |
| 1.20.5-1.20.6 | 32 |
| 1.21-1.21.1 | 34 |
| 1.21.2-1.21.3 | 42 |
| 1.21.4 | 46 |
| 1.21.5 | 55 |
| 1.21.6 | 63 |
| 1.21.7-1.21.8 | 64 |

Minecraft `1.21.9` and newer are not supported yet.

The tool detects the Minecraft version from `mmc-pack.json` or `manifest.json`. Snapshots, pre-releases, release candidates, modified version strings, and unknown versions are rejected.

## For developers

Use this section only if you want to build or test the project from source.

### Build locally

```powershell
go build -o .\modpack-translator.exe .
.\modpack-translator.exe --translate "C:\path\to\your\modpack"
```

Linux/macOS:

```bash
go build -o ./modpack-translator .
./modpack-translator --translate "/path/to/your/modpack"
```

### Verify

```powershell
go test ./...
go vet ./...
```
