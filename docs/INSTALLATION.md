# Installation and User Guide

Modpack Translator extracts English text from a Minecraft modpack, builds a normalized catalog, and prepares a translation workspace. It can translate through configured cloud providers in order and use Ollama as the final fallback, producing a validated resumable cache.

It does **not yet write cached translations back into final format-specific JSON5, SNBT, or resource-pack language files**. The generated export currently contains the resource-pack structure and `pack.mcmeta`, not a finished translated modpack. Keep the workspace for future writeback support; do not distribute it as a completed translation.

## Quick Path for Windows

1. Back up the modpack or work on a launcher instance you can restore.
2. Download the Windows amd64 release artifact and extract it to its own folder.
3. Optional: copy `.env.example` into that folder as `.env`, fill only the providers you use, and restrict access to the plain-text file.
4. Open PowerShell in that folder.
5. Run extraction:

   ```powershell
   .\modpack-translator.exe "C:\path\to\your\modpack"
   ```

6. Optional: [install Ollama](#optional-local-translation-with-ollama), pull the model, and run:

   ```powershell
   ollama pull qwen3:8b
   .\modpack-translator.exe --translate "C:\path\to\your\modpack"
   ```

The tool reads mod JARs and source files without modifying them. Its files are created under `<modpack>\modpack-translator-output`.

## What You Need

| Use case | Requirements |
|---|---|
| Run extraction only | A matching release binary and an extracted Minecraft modpack containing `mods` plus supported version metadata |
| Translate | The above, Ollama running with the selected fallback model, plus any optional cloud keys/models |
| Build from source | Go 1.22 or newer; see [Developer Build Appendix](#developer-build-appendix) |

Windows is the primary supported end-user platform. Linux amd64 and macOS binaries can be used when those variants are included in a release. Commands below distinguish platform differences; availability of a release artifact is not implied.

## Install a Release Binary

### 1. Download the matching artifact

Open the repository's Releases page and choose an artifact provided for your system:

| System | Typical architecture | Expected binary form |
|---|---|---|
| Windows 10/11 | amd64 (x86-64) | `.exe` |
| Linux | amd64 (x86-64) | executable without `.exe` |
| macOS on Apple silicon | arm64 | executable without `.exe` |
| macOS on Intel | amd64 | executable without `.exe` |

Do not use an artifact for a different operating system or CPU architecture. If the release does not provide your variant, use the [source build](#developer-build-appendix) instead.

Extract the downloaded archive before running the program. Do not run the binary from inside a ZIP file.

### 2. Verify a published SHA-256 checksum

Only use this workflow when the same release publishes a SHA-256 checksum file or documented checksum value. Match the exact filename; this project guide does not assume a particular release URL or checksum filename.

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

Compare the complete hexadecimal value with the checksum published by that release. Stop if the values differ.

### 3. Allow execution safely

On Linux or macOS, make the extracted binary executable:

```bash
chmod +x "./modpack-translator"
```

Windows SmartScreen may warn about a release binary that is not code-signed. Confirm that the file came from the expected release page and that its published checksum matches before choosing the per-file **More info** / **Run anyway** action. Do not disable SmartScreen, antivirus, or other system-wide protections.

## Prepare the Modpack

Use an already installed or extracted launcher instance, not a modpack ZIP. The selected directory must contain a `mods` folder and supported Minecraft metadata:

```text
<modpack>/
|-- mmc-pack.json or manifest.json
`-- mods/
    |-- example-mod.jar
    `-- another-mod.jar
```

For launcher instances whose game directory is `<instance>/.minecraft`, metadata may also be in the immediate `<instance>` directory. The tool supports exact Minecraft Java releases from 1.16.5 through 1.21.8. Snapshots, modified version strings, unknown releases, and 1.21.9 or newer are rejected rather than assigned a potentially incorrect resource-pack format.

## Extraction-Only Usage

Extraction is the default and does not require Ollama.

### Use an explicit path

Windows PowerShell:

```powershell
.\modpack-translator.exe "C:\Users\Player\Minecraft Instances\My Modpack"
```

Linux or macOS:

```bash
./modpack-translator "/home/player/Minecraft Instances/My Modpack"
```

Always quote paths, especially paths containing spaces.

### Use automatic discovery

Windows PowerShell:

```powershell
.\modpack-translator.exe
```

Linux or macOS:

```bash
./modpack-translator
```

With no path, the tool checks the current directory and common official launcher, CurseForge, Prism Launcher, PolyMC, MultiMC, ATLauncher, and Modrinth locations. It does not scan entire drives.

If exactly one modpack is found, it is selected automatically. If several are found, the program prints a numbered list; enter one valid number and press Enter. If none is found, rerun with the quoted explicit path.

## Translation Providers

The chain order is fixed: **Gemini -> Cerebras -> Groq -> Mistral -> OpenRouter -> Ollama**. Missing cloud keys disable those providers. Every configured cloud key requires its matching model variable; the tool has no automatic model discovery and deliberately provides no cloud model default because current model availability and structured-output capabilities can change.

### Configure `.env` beside the binary

The program loads `.env` beside its resolved executable, and falls back to the current working directory if no file exists there. It never searches modpack, source repository, parent directories, home directory, or launcher folders. Existing process environment variables always take precedence over `.env` values.

A missing `.env` is normal, including for extraction-only runs. An existing malformed or unreadable file is fatal: the error names the selected path and asks you to check permissions or `KEY=VALUE` syntax, but does not print file contents or secrets. The file is not encrypted; limit filesystem access and never include it in bug reports.

Copy the supplied safe template as `.env`:

```powershell
Copy-Item .env.example .env
```

Using `go run .` finds your repository `.env` through the CWD fallback because the temporary test executable has none beside it. To exercise the production path, compile and place `.env` beside the resulting binary.

| Provider | Required pair | Optional official-base override | Structured mode |
|---|---|---|---|
| Gemini | `GEMINI_API_KEY`, `GEMINI_MODEL` | `GEMINI_BASE_URL` (official host only) | strict JSON schema |
| Cerebras | `CEREBRAS_API_KEY`, `CEREBRAS_MODEL` | `CEREBRAS_BASE_URL` (official host only) | strict JSON schema |
| Groq | `GROQ_API_KEY`, `GROQ_MODEL` | `GROQ_BASE_URL` (official host only) | JSON object |
| Mistral | `MISTRAL_API_KEY`, `MISTRAL_MODEL` | `MISTRAL_BASE_URL` (official host only) | strict JSON schema |
| OpenRouter | `OPENROUTER_API_KEY`, `OPENROUTER_MODEL` | `OPENROUTER_BASE_URL` (official host only) | strict JSON schema with `provider.require_parameters` |
| Ollama | none; `OLLAMA_MODEL` optional | `OLLAMA_HOST` | Ollama JSON schema |

PowerShell configuration example:

```powershell
$env:GEMINI_API_KEY = "<gemini-api-key>"
$env:GEMINI_MODEL = "<model-supporting-structured-output>"
$env:CEREBRAS_API_KEY = "<cerebras-api-key>"
$env:CEREBRAS_MODEL = "qwen-3-32b"
$env:GROQ_API_KEY = "<groq-api-key>"
$env:GROQ_MODEL = "<model-supporting-json-mode>"
$env:MISTRAL_API_KEY = "<mistral-api-key>"
$env:MISTRAL_MODEL = "<model-supporting-structured-output>"
$env:OPENROUTER_API_KEY = "<openrouter-api-key>"
$env:OPENROUTER_MODEL = "<provider/model-supporting-structured-output>"
$env:OLLAMA_MODEL = "qwen3:8b"
```

POSIX one-command example:

```bash
GEMINI_API_KEY="<gemini-api-key>" GEMINI_MODEL="<model-supporting-structured-output>" \
CEREBRAS_API_KEY="<cerebras-api-key>" CEREBRAS_MODEL="qwen-3-32b" \
GROQ_API_KEY="<groq-api-key>" GROQ_MODEL="<model-supporting-json-mode>" \
MISTRAL_API_KEY="<mistral-api-key>" MISTRAL_MODEL="<model-supporting-structured-output>" \
OPENROUTER_API_KEY="<openrouter-api-key>" OPENROUTER_MODEL="<provider/model-supporting-structured-output>" \
OLLAMA_MODEL="qwen3:8b" ./modpack-translator --translate "/path/to/modpack"
```

Keys are accepted through process environment variables or the binary-adjacent `.env`. Cerebras defaults to `https://api.cerebras.ai/v1`; all base URL overrides must use HTTPS and cannot contain credentials, query strings, or fragments. Never place keys in command arguments, documentation, cache files, or bug reports.

Before translation, the program lists every provider in chain order with its enabled/disabled reason and enabled model. During work it identifies provider attempts, safe quota/rate transitions, and validated batches. It never prints keys or raw provider response bodies. Recursive validation splits retain entry counts so direct Ollama fallback and recovered sub-batches remain understandable.

### Quotas, billing, and privacy

The tool cannot guarantee free usage. Cerebras offers a free tier, but access is account- and rate-limited rather than guaranteed permanently zero-cost. Provider plans, routing, and billing rules can change; configure provider-side hard spend limits before enabling a key. After bounded retries, only a provider-aware recognized quota/rate-limit response advances the chain permanently for that run and retries the same batch. OpenRouter insufficient credits may advance, but it means only that OpenRouter reported insufficient credits, not that a free tier definitively ended. Authentication, permission, invalid configuration/request, model/schema incompatibility, network/unavailability, cancellation, and unknown errors stop the run.

Cloud translation sends protected modpack text to the configured cloud service. Review each provider's privacy and retention policy. Ollama at its default host stays on the local machine; a custom `OLLAMA_HOST` sends text to that endpoint.

## Ollama Fallback

Ollama and its models are separate downloads. They are not bundled with Modpack Translator and remain subject to their own software and model licenses.

### Install and verify Ollama

Windows and macOS: use the official [Ollama download page](https://ollama.com/download).

Linux: start from the official [Ollama download page](https://ollama.com/download). If you choose its installer script, download it, inspect it, and then run the reviewed local file:

```bash
curl -fL https://ollama.com/install.sh -o ollama-install.sh
less ollama-install.sh
sh ollama-install.sh
```

Remove the script afterward if you no longer need it.

Open a new terminal and verify the CLI:

```text
ollama --version
```

Ollama normally runs its local service after desktop installation. On a headless or manual installation, start it in a separate terminal if needed:

```text
ollama serve
```

Verify the local API is responding:

Windows PowerShell:

```powershell
Invoke-RestMethod "http://localhost:11434/api/version"
```

Linux or macOS:

```bash
curl "http://localhost:11434/api/version"
```

Download the default model once:

```text
ollama pull qwen3:8b
```

The default `qwen3:8b` model is a practical target for an RTX 3070-class GPU with 8 GB VRAM and 32 GB system RAM. Expect a model download requiring several gigabytes of storage plus working space. Ollama can fall back partly or fully to CPU/system memory when GPU memory is insufficient, but translation can become substantially slower. Actual memory use and speed vary with Ollama, model quantization, drivers, and source size.

### Run translation

With an explicit path:

```powershell
.\modpack-translator.exe --translate "C:\path\to\your\modpack"
```

```bash
./modpack-translator --translate "/path/to/your/modpack"
```

With automatic discovery:

```powershell
.\modpack-translator.exe --translate
```

```bash
./modpack-translator --translate
```

Translation always runs after a fresh extraction/catalog pass. The default endpoint is `http://localhost:11434`, and the default model is `qwen3:8b`.

### Select another host or model

Set variables for the current PowerShell session:

```powershell
$env:OLLAMA_HOST = "http://localhost:11434"
$env:OLLAMA_MODEL = "qwen3:8b"
.\modpack-translator.exe --translate "C:\path\to\your\modpack"
```

Set variables for one POSIX shell command:

```bash
OLLAMA_HOST="http://localhost:11434" OLLAMA_MODEL="qwen3:8b" \
  ./modpack-translator --translate "/path/to/your/modpack"
```

The selected model must already exist in Ollama. Modpack Translator does not download it automatically. Each request has a 30-minute timeout by default; set `OLLAMA_TIMEOUT` to a positive Go duration such as `2h` when CPU fallback needs longer.

### Privacy, validation, and resume behavior

When the chain reaches Ollama at its default host, source text stays on the same computer. Ollama needs no key. If you change `OLLAMA_HOST`, text is sent to that endpoint: use only a host you trust.

Before requests, the tool replaces formatting codes, placeholders, URLs, resource identifiers, Patchouli macros, and similar tokens with deterministic markers. Ollama must return structured JSON with exactly one result per requested ID. Missing, duplicated, unknown, reordered where unsafe, or malformed markers reject the entire batch before it reaches the cache.

Validated batches are atomically saved with per-entry provider/model provenance at:

```text
<modpack>/modpack-translator-output/workspace/translations/translations.v2.json
```

You can interrupt the program with `Ctrl+C`. Completed batches remain available. Source text, target locale, token signature, or prompt changes invalidate affected entries; provider/model changes do not because they are provenance, not semantic identity. When v2 is absent, the tool imports only the configured Ollama model's strictly valid legacy `translations/ollama/<safe-model>/translations.v1.json`, leaves it untouched, and ignores other legacy model directories. Existing v2 always wins.

## Output Layout

```text
<modpack>/
`-- modpack-translator-output/
    |-- workspace/
    |   |-- assets/
    |   |   `-- <namespace>/lang/es_es.pending.json
    |   |-- sources/
    |   |   |-- patchouli/
    |   |   |   |-- jars/<stable-source-id>/<original-JAR-path>
    |   |   |   `-- instance/<book>/<original-book-relative-path>
    |   |   `-- ftbquests/
    |   |       `-- config/ftbquests/quests/<relative-path>
    |   |           or defaultconfigs/ftbquests/quests/<relative-path>
    |   |-- catalog/
    |   |   `-- catalog.v1.json
    |   `-- translations/
    |       |-- translations.v2.json
    |       |-- failures.v2.json (only for singleton validation failures)
    |       `-- ollama/<model-safe-name>/translations.v1.json (legacy)
    `-- export/
        `-- overrides/
            `-- resourcepacks/
                `-- ModpackTranslations/
                    |-- pack.mcmeta
                    `-- assets/  (reserved for future translated files)
```

`workspace` is tool work state. It contains pending extraction output, copied source material, the normalized catalog, and optional validated translation caches. Preserve it to resume work, but do not treat it as a distributable resource pack.

`export/overrides` is the distribution-shaped boundary. Today it prepares `resourcepacks/ModpackTranslations/pack.mcmeta`; final format-specific translated export writeback is not implemented. An `export` directory is therefore **not evidence that a finished translated pack exists**.

Original mod JARs, configuration, and worlds remain outside this tool-owned output tree and are not modified.

## Troubleshooting

### A cloud provider stops the run

Authentication and permission errors usually mean the key, account, or model access is wrong. Invalid request/model errors usually mean the selected model does not support the documented structured mode. The tool never silently downgrades strict schema mode. Network and service errors remain actionable rather than being mistaken for quota exhaustion. Verify the named provider, model, account limits, and official service status; errors intentionally omit response bodies and credentials.

### Ollama is unreachable

The error starts with `cannot reach Ollama at ...; start Ollama and verify OLLAMA_HOST`.

- Confirm `ollama --version` works.
- Start the desktop application or run `ollama serve` in another terminal.
- Verify `http://localhost:11434/api/version` as shown above.
- Check that `OLLAMA_HOST` is a valid trusted HTTP or HTTPS URL. It cannot contain credentials, a query, or a fragment.

### The model is unavailable

The error states `Ollama model "..." is unavailable` and includes the required pull command.

```text
ollama pull qwen3:8b
ollama ls
```

If `OLLAMA_MODEL` names another model, pull that exact model name instead.

### The translation cache is malformed or does not match its path

Errors include `parse translation cache safely`, `translation cache metadata does not match its model path`, or `translation cache entry ... is invalid`.

Do not delete the cache immediately. Stop the tool, back up or rename the exact `translations.v2.json` file, then rerun to create a clean cache while preserving the old file for diagnosis:

Windows PowerShell:

```powershell
Rename-Item ".\translations.v2.json" "translations.v2.json.backup"
```

Linux or macOS:

```bash
mv "./translations.v2.json" "./translations.v2.json.backup"
```

Run those commands from the cache directory shown in the error/output, or use its full quoted path. A rebuilt cache retranslates entries because the renamed cache is no longer active.

### Token or structured-result validation fails

Errors start with `validate batch ...` and may report missing, duplicated, unknown, altered, malformed, or out-of-order markers; unknown, duplicate, or missing result IDs; or an unexpected result count.

No result from that failed batch is published. Rerun first: model output may vary. If failure repeats, preserve the cache and sanitized error text for a bug report. Changing providers or models does not create a separate cache; successful entries retain their original provenance.

### Minecraft version or metadata is unsupported

Errors identify missing/malformed `mmc-pack.json` or `manifest.json`, or state that the exact Minecraft version is unsupported.

- Confirm you selected the directory containing `mods`, not the downloaded ZIP.
- Confirm launcher metadata is present at the supported location.
- Do not edit metadata to impersonate another Minecraft version; resource-pack formats differ.
- Versions from 1.21.9 onward require metadata not yet implemented by this tool.

### No modpack is found or several choices appear

If discovery finds none, supply the quoted path explicitly. If several are listed, enter a number within the displayed range. A non-interactive terminal cannot answer the selection prompt, so use an explicit path there.

### Translation is slow or GPU memory is insufficient

Large catalogs can take a long time. Ollama may move work to system memory or CPU when VRAM is insufficient, which is slower but does not change the cache format. Close other GPU-heavy applications, allow completed batches to save, and rerun after interruption. Consult Ollama diagnostics before changing models or drivers.

## Update or Uninstall

### Update Modpack Translator

1. Stop any running translation.
2. Download and verify the new matching release artifact.
3. Replace only the old Modpack Translator binary.
4. Keep `modpack-translator-output/workspace` if you want compatible cache entries to be reused.

### Uninstall Modpack Translator

Delete the standalone binary. To remove generated data, delete only the tool-owned `<modpack>/modpack-translator-output` directory after backing up any workspace/cache you need.

Never delete the modpack's `mods`, `config`, `defaultconfigs`, `patchouli_books`, or `saves` directories as part of uninstalling this tool. Modpack Translator does not install files there.

Ollama is separate software. If you no longer need this model, the official CLI command is:

```text
ollama rm qwen3:8b
```

Use your operating system's normal application removal process to uninstall Ollama itself. Removing Ollama or a model affects other applications that use it.

## Security Notes

- Cloud keys are optional and read from the process environment or binary-adjacent `.env`; they are never logged. The `.env` itself is user-managed plain text.
- Cloud providers receive protected source text. Ollama is local only when its configured endpoint is local.
- Configure provider-side hard spend limits; this tool cannot enforce or guarantee free usage.
- Ollama binds to `127.0.0.1:11434` by default, so its API is local to your computer.
- Do not configure Ollama to listen publicly. Its local API is not a public authentication boundary.
- A custom `OLLAMA_HOST` sends modpack text to that server. Trust the server operator and network before using it.
- Keep normal operating-system security controls enabled and verify release checksums when published.

## Developer Build Appendix

Building from source is optional and separate from installing a compiled release. It requires Go 1.22 or newer and a source checkout.

Windows PowerShell:

```powershell
go build -o .\modpack-translator.exe .
```

Linux or macOS:

```bash
go build -o ./modpack-translator .
```

See the repository [README](../README.md#build-and-run-locally) for source-run, cross-compilation, and developer verification commands.
