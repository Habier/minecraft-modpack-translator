# Modpack Translator

Translate Minecraft Java modpacks to Spanish.

Modpack Translator extracts translatable text from supported modpack sources, translates it with configured providers, and creates a ready-to-share `es_es` resource pack ZIP.

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
| Existing `es_es` files inside mods | Left untouched |

## Output

The tool writes everything inside the selected modpack folder:

```text
<modpack>/
└── modpack-translator-output/
    ├── workspace/                         # working files, cache, failures
    └── export/
        ├── overrides/                     # generated translated files
        └── modpack-translations-es_es.zip # shareable ZIP
```

Use either:

- `modpack-translator-output/export/modpack-translations-es_es.zip`
- `modpack-translator-output/export/overrides/`

`workspace/` is internal working state. Do not distribute it.

## Translation providers

Provider order:

```text
Gemini -> Cerebras -> Groq -> Mistral -> OpenRouter -> Ollama (local fallback)
```

Cloud providers are enabled only when their API key is configured. Each enabled cloud provider also needs an explicit model name.

If no cloud provider is configured, the tool uses local Ollama.

Common variables:

| Variable | Purpose |
|---|---|
| `GEMINI_API_KEY`, `GEMINI_MODEL` | Gemini provider |
| `CEREBRAS_API_KEY`, `CEREBRAS_MODEL` | Cerebras provider |
| `GROQ_API_KEY`, `GROQ_MODEL` | Groq provider |
| `MISTRAL_API_KEY`, `MISTRAL_MODEL` | Mistral provider |
| `OPENROUTER_API_KEY`, `OPENROUTER_MODEL` | OpenRouter provider |
| `OLLAMA_HOST` | Ollama endpoint, defaults to `http://localhost:11434` |
| `OLLAMA_MODEL` | Ollama model, defaults to `qwen3:8b` |
| `OLLAMA_TIMEOUT` | Ollama request timeout, defaults to `30m` |

API keys can come from your environment or from a `.env` file beside the compiled executable.

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
