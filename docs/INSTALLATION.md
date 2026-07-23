# Installation and User Guide

Use this guide if you want to run Modpack Translator from a compiled release. You do not need Go.

Modpack Translator translates supported Minecraft Java modpack text to Spanish and creates a shareable `es_es` resource pack ZIP.

## Quick start

1. Back up your modpack or use a launcher instance you can restore.
2. Download the release for your operating system.
3. Extract the archive into its own folder.
4. Optional: configure cloud provider keys or install Ollama.
5. Run translation:

   Windows PowerShell:

   ```powershell
   .\modpack-translator.exe --translate "C:\path\to\your\modpack"
   ```

   Linux/macOS:

   ```bash
   ./modpack-translator --translate "/path/to/your/modpack"
   ```

6. Use the generated ZIP:

   ```text
   <modpack>/modpack-translator-output/export/modpack-translations-es_es.zip
   ```

## Automatic modpack selection

You can run the tool without a path:

```powershell
.\modpack-translator.exe --translate
```

The tool searches common launcher locations. If it finds one modpack, it uses it. If it finds several, it asks you to choose one by number.

It checks folders that contain a `mods` directory. It does not scan entire drives.

## What you need

| Need | Details |
|---|---|
| Modpack folder | Must contain a `mods` directory |
| Minecraft metadata | `mmc-pack.json` or `manifest.json` |
| Translation provider | Cloud provider keys or local Ollama |
| Output language | Spanish `es_es` |

Use an installed/extracted modpack folder, not a downloaded modpack ZIP.

## What it supports

| Source | Status |
|---|---|
| Mod `.jar` language files | Supported |
| Patchouli books | Supported |
| FTB Quests `.snbt` files | Supported |
| FTB Quests JSON5 language files | Supported |
| KubeJS language files | Supported |
| Existing `es_es` files inside mods | Left untouched |

## Minecraft versions

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

Snapshots, pre-releases, release candidates, modified version strings, and unknown versions are rejected.

## Install a release

### 1. Download the right file

Choose the release artifact for your system:

| System | Typical architecture | Binary form |
|---|---|---|
| Windows 10/11 | amd64 / x86-64 | `.exe` |
| Linux | amd64 / x86-64 | executable |
| macOS Apple silicon | arm64 | executable |
| macOS Intel | amd64 / x86-64 | executable |

Do not run the program from inside the downloaded ZIP/archive. Extract it first.

### 2. Verify checksum when published

If the release includes a SHA-256 checksum, compare it with your downloaded file.

Windows PowerShell:

```powershell
Get-FileHash -Algorithm SHA256 ".\<downloaded-artifact>"
```

Linux:

```bash
sha256sum "./<downloaded-artifact>"
```

macOS:

```bash
shasum -a 256 "./<downloaded-artifact>"
```

Stop if the checksum does not match.

### 3. Allow execution

Linux/macOS:

```bash
chmod +x "./modpack-translator"
```

Windows SmartScreen may warn if the binary is not code-signed. Only allow it after confirming it came from the expected release and the checksum matches when one is published.

## Translation providers

Provider order:

```text
Gemini -> Cerebras -> Groq -> Mistral -> OpenRouter -> Ollama (local fallback)
```

Cloud providers are optional. A cloud provider is enabled only when its API key is configured. Each enabled cloud provider also needs a model name.

If no cloud provider is configured, the tool uses local Ollama.

### Configure cloud providers

Create a `.env` file beside the compiled binary, or set variables in your terminal.

Common `.env` entries:

```text
GEMINI_API_KEY=<gemini-api-key>
GEMINI_MODEL=<model-supporting-structured-output>

CEREBRAS_API_KEY=<cerebras-api-key>
CEREBRAS_MODEL=qwen-3-32b

GROQ_API_KEY=<groq-api-key>
GROQ_MODEL=<model-supporting-json-mode>

MISTRAL_API_KEY=<mistral-api-key>
MISTRAL_MODEL=<model-supporting-structured-output>

OPENROUTER_API_KEY=<openrouter-api-key>
OPENROUTER_MODEL=<provider/model-supporting-structured-output>
```

Only fill the providers you want to use. Keep `.env` private; it is plain text.

### Use local Ollama

Install Ollama from the official download page:

<https://ollama.com/download>

Then download the default model:

```text
ollama pull qwen3:8b
```

Useful Ollama variables:

| Variable | Default | Purpose |
|---|---|---|
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama endpoint |
| `OLLAMA_MODEL` | `qwen3:8b` | Local model name |
| `OLLAMA_TIMEOUT` | `30m` | Per-request timeout |

The selected model must already exist in Ollama. Modpack Translator does not download models automatically.

## Extraction-only mode

If you only want to prepare the workspace and catalog without translating, run without `--translate`:

Windows PowerShell:

```powershell
.\modpack-translator.exe "C:\path\to\your\modpack"
```

Linux/macOS:

```bash
./modpack-translator "/path/to/your/modpack"
```

## Output

The tool writes files inside the selected modpack folder:

```text
<modpack>/
└── modpack-translator-output/
    ├── workspace/
    │   ├── catalog/catalog.v1.json
    │   └── translations/translations.v2.json
    └── export/
        ├── overrides/
        └── modpack-translations-es_es.zip
```

Use either:

- `modpack-translator-output/export/modpack-translations-es_es.zip`
- `modpack-translator-output/export/overrides/`

Do not distribute `workspace/`. It contains working files, cache, and failure reports.

Original mod JARs, config files, worlds, and saves are not modified.

## Troubleshooting

### No modpack is found

Run again with the full quoted path:

```powershell
.\modpack-translator.exe --translate "C:\path\to\your\modpack"
```

Make sure the selected folder contains `mods`.

### Several modpacks are listed

Enter the number shown beside the modpack you want. If your terminal cannot answer prompts, run with an explicit path.

### Minecraft version is unsupported

The selected folder must contain supported metadata in `mmc-pack.json` or `manifest.json`. Versions newer than `1.21.8` are not supported yet.

### Ollama is unreachable

Check that Ollama is installed and running:

```text
ollama --version
ollama serve
```

Then verify the local API:

Windows PowerShell:

```powershell
Invoke-RestMethod "http://localhost:11434/api/version"
```

Linux/macOS:

```bash
curl "http://localhost:11434/api/version"
```

### Ollama model is missing

Pull the configured model:

```text
ollama pull qwen3:8b
ollama ls
```

If you changed `OLLAMA_MODEL`, pull that exact model name instead.

### A cloud provider fails

Check the provider key, model name, account access, quota, and billing limits. The tool does not print API keys or raw credential-bearing URLs.

Cloud translation sends protected modpack text to the configured provider. Ollama at the default local endpoint keeps text on your machine.

## Update or uninstall

### Update

1. Stop any running translation.
2. Download the new release for your system.
3. Replace the old Modpack Translator binary.
4. Keep `modpack-translator-output/workspace` if you want reusable cache entries.

### Uninstall

Delete the Modpack Translator binary.

To remove generated data, delete this folder from the modpack after backing up anything you need:

```text
<modpack>/modpack-translator-output
```

Do not delete the modpack's `mods`, `config`, `defaultconfigs`, `patchouli_books`, or `saves` folders as part of uninstalling this tool.

Ollama is separate software. If you no longer need the default model:

```text
ollama rm qwen3:8b
```

## For developers

Building from source is optional and requires Go 1.22 or newer.

Windows PowerShell:

```powershell
go build -o .\modpack-translator.exe .
go test ./...
go vet ./...
```

Linux/macOS:

```bash
go build -o ./modpack-translator .
go test ./...
go vet ./...
```

For the short project overview, see the repository [README](../README.md).
