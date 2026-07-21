# Modpack Translator

Modpack Translator prepares a translation workspace and a separate distributable export from modpack language sources. Extraction remains the default. Opt-in translation uses configured cloud providers in order, then local Ollama, and writes a validated resumable cache; final JSON5/SNBT export writeback is not implemented yet.

> **Using a compiled release?** Follow the [Installation and User Guide](docs/INSTALLATION.md) for installation and provider configuration. You do not need Go.

## Prerequisites

- Go 1.22 or newer
- A Minecraft modpack directory containing a `mods` folder
- Optional cloud provider API keys and explicitly selected models
- [Ollama](https://ollama.com/) and the `qwen3:8b` model for the final fallback

## Quick Start

Run the project with the modpack directory as its only argument:

```powershell
go run . "C:\path\to\your\modpack"
```

Quote Windows paths that contain spaces:

```powershell
go run . "C:\Users\Player\Minecraft Instances\My Modpack"
```

You can also run it without arguments:

```powershell
go run .
```

When no path is provided, the tool searches the current directory first, then common Minecraft launcher locations. If it finds exactly one modpack, it uses that folder. If it finds several, it lets you choose one by number.

The default command extracts pending sources only. It prepares the export resource pack, but it does not claim to produce useful translated assets until a future writeback step writes real `es_es.json` files there.

## Translation Provider Chain

The fixed order is **Gemini -> Cerebras -> Groq -> Mistral -> OpenRouter -> Ollama**. A cloud provider is enabled only when its API key is present; its model variable is then required. The tool retries bounded transient transport failures, advances permanently for that run only after a provider-specific recognized quota/rate-limit response, and retries the same batch. Authentication, permission, configuration, request/model incompatibility, network/unavailability, cancellation, and unknown failures stop with an actionable error. OpenRouter insufficient credits can advance the chain, but this does not prove that a free tier ended.

At startup, the program loads `.env` beside its resolved executable, then falls back to the current working directory if no file exists there. Existing process variables always win over file values. It never searches modpack or repository parent directories. A missing file is normal; an existing malformed or unreadable file stops safely with its path and remediation, without printing contents or secrets. Copy `.env.example` as `.env` and restrict access because it is plain text, not encrypted.

Using `go run .` builds a temporary executable, so the repository `.env` is found through the CWD fallback rather than the executable directory. To test the standard loading path, compile first:

Install Ollama and download its fallback model:

```powershell
ollama pull qwen3:8b
```

PowerShell example:

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
modpack-translator --translate "C:\path\to\your\modpack"
```

POSIX example:

```bash
GEMINI_API_KEY="<gemini-api-key>" GEMINI_MODEL="<model-supporting-structured-output>" \
CEREBRAS_API_KEY="<cerebras-api-key>" CEREBRAS_MODEL="qwen-3-32b" \
GROQ_API_KEY="<groq-api-key>" GROQ_MODEL="<model-supporting-json-mode>" \
MISTRAL_API_KEY="<mistral-api-key>" MISTRAL_MODEL="<model-supporting-structured-output>" \
OPENROUTER_API_KEY="<openrouter-api-key>" OPENROUTER_MODEL="<provider/model-supporting-structured-output>" \
./modpack-translator --translate "/path/to/your/modpack"
```

All cloud variables are optional as pairs. With no cloud keys, translation uses Ollama directly. Each `*_BASE_URL` must use its provider's official HTTPS host; the override can only select an alternative path on that host. Defaults are the providers' official API roots.

Run extraction followed by translation:

```powershell
modpack-translator --translate "C:\path\to\your\modpack"
modpack-translator "C:\path\to\your\modpack" --translate
modpack-translator --translate
```

When the path is omitted, normal modpack auto-discovery still applies. API keys come from the process environment or the binary-adjacent `.env` and are never printed. Cloud translation sends protected source text outside your machine and is subject to each provider's data practices. Default Ollama is local. **The tool cannot guarantee free usage:** Cerebras free-tier access is account- and rate-limited, and provider terms can change; configure provider-side hard spend limits and billing controls before enabling cloud keys.

Translation prints a safe startup chain and provider provenance without keys or credential-bearing URLs, for example:

```text
Provider chain:
  gemini: disabled (GEMINI_API_KEY not set)
  cerebras: enabled model=qwen-3-32b
  groq: disabled (GROQ_API_KEY not set)
  mistral: disabled (MISTRAL_API_KEY not set)
  openrouter: disabled (OPENROUTER_API_KEY not set)
  ollama: enabled model=qwen3:8b (final fallback)
Provider attempt: cerebras model=qwen-3-32b entries=20
Provider transition: cerebras -> ollama reason=rate or quota limit
Validated batch: provider=ollama model=qwen3:8b entries=10
```

| Environment variable | Default | Purpose |
|---|---|---|
| `OLLAMA_HOST` | `http://localhost:11434` | Ollama HTTP/HTTPS endpoint |
| `OLLAMA_MODEL` | `qwen3:8b` | Installed local model name |
| `OLLAMA_TIMEOUT` | `30m` | Per-request limit; use a positive Go duration such as `2h` for slow CPU translation |

The tool never downloads or discovers models automatically. Cloud model IDs are required because model availability and structured-output support change independently of API compatibility.

Validated results are published after each successful batch or recovered sub-batch under `workspace/translations/translations.v2.json`; every entry records its successful provider/model. Provider/model are provenance and do not invalidate semantically valid text. Invalid structured results retry once on the current provider, then split deterministically. Singleton validation failures appear in bounded `failures.v2.json`; fatal chain/configuration errors are not converted into entry failures. If v2 is absent, one strictly valid cache for the configured Ollama model may be imported from `translations/ollama/<safe-model>/translations.v1.json`; v1 remains untouched, arbitrary model directories are ignored, and v2 takes precedence.

Translation currently stops at this workspace cache. It does **not** create final files under `export`, because safe generic writeback for the supported JSON5 and SNBT source structures is a separate unit of work.

## Automatic Modpack Discovery

Discovery looks only for folders that contain a `mods` directory. It does not scan entire drives.

The current search includes:

- The current working directory
- The official `.minecraft` folder
- CurseForge instance folders
- Prism Launcher, PolyMC, MultiMC, ATLauncher, and Modrinth app instance/profile folders

Some launchers store the actual Minecraft folder as `<instance>/.minecraft`, so the tool checks both the instance folder and that nested `.minecraft` folder.

## Input And Output

The expected modpack layout includes a supported metadata file and a `mods` directory:

```text
<modpack>/
├── mmc-pack.json or manifest.json
└── mods/
    ├── example-mod.jar
    └── another-mod.jar
```

One run creates or prepares both output areas inside the same modpack directory:

```text
<modpack>/
└── modpack-translator-output/
    ├── workspace/
    │   ├── assets/
    │   │   └── <namespace>/lang/es_es.pending.json
    │   └── sources/
    │       ├── patchouli/
    │       │   ├── jars/<stable-source-id>/<original-JAR-path>
    │       │   └── instance/<book>/<original-book-relative-path>
    │       └── ftbquests/
    │           └── config/ftbquests/quests/<original-relative-path>
    │               or defaultconfigs/ftbquests/quests/<original-relative-path>
    │   ├── catalog/catalog.v1.json
    │   └── translations/
    │       ├── translations.v2.json
    │       ├── failures.v2.json (only when entries remain pending)
    │       └── ollama/<model-safe>/translations.v1.json (legacy, untouched)
    └── export/
        └── overrides/
            └── resourcepacks/
                └── ModpackTranslations/
                    ├── pack.mcmeta
                    └── assets/ (future translated files only)
```

`workspace` is translation work state and is not distributed. Pending files and validated cache entries remain there and are never copied into `export`.

`workspace/catalog/catalog.v1.json` is the deterministic, versioned input for a future translation provider. It contains decoded source strings, protected-token metadata, stable source locations, and writeback metadata, but no API keys, provider requests, or translations. It is atomically replaced only after all selected sources validate; a failed build preserves the previous complete catalog.

`export/overrides` follows the layout that launchers and modpack importers copy onto a modpack root. Its `resourcepacks/ModpackTranslations` directory contains `pack.mcmeta` and, in future, only real translated assets such as `assets/<namespace>/lang/es_es.json`.

On every successful extraction, the tool replaces its tool-owned `workspace/assets` and `workspace/sources` output; do not store unrelated files there. If an optional source is malformed or unsafe, extraction fails before staged sources are published. The tool also removes stale `es_es.pending.json` entries only from its owned export resource-pack directory before writing `pack.mcmeta`. Pending Patchouli and FTB Quests sources are never written under `export`.

Existing Spanish files from mods are not copied because Minecraft can already load them from the original mod JARs.

Only top-level `*.jar` files in `<modpack>/mods` are processed.

## Language Precedence

Language selection is performed per key after merging each namespace across mod JARs:

1. Every selected `en_us.json` is parsed and merged by key, including files beside a same-JAR `es_es.json`.
2. Keys present in any selected `es_es.json` are removed from the merged English keys. Duplicate target keys are tolerated because only their presence matters; source keys remain unique and strict.
3. Remaining English keys are written to `workspace/assets/<namespace>/lang/es_es.pending.json`. If no keys remain, no pending file is written.
4. If neither language file exists, that namespace is skipped.

Missing Spanish entries can still fall back through Minecraft's normal resource-pack and language fallback behavior.

The current source language is fixed to `en_us` and the current target language is fixed to `es_es`. Other output languages are not accepted by the CLI yet; the language names are centralized in code so that a future flag or configuration option can be added without changing the extraction rules.

The source JARs are read only and are not modified.

## Translation Safety

Translation providers pass every human-readable value through the reusable `tokenprotect` package. It shields printf placeholders, brace variables, Minecraft formatting codes, line breaks, Patchouli macros, resource identifiers, and URLs with opaque deterministic markers before any text reaches a provider. URLs and complete Patchouli macros take precedence over their embedded resource identifiers, so overlapping syntax is protected exactly once.

Restoration requires every marker identity exactly once. Missing, duplicated, unknown, altered, or malformed markers reject the entire batch before cache publication. Providers may move independent markers and indexed printf placeholders, but unindexed printf arguments and the Patchouli macro stream retain source order because reordering those constructs can change meaning or break markup nesting.

## Catalog Field Policy

Standard language catalogs accept only UTF-8 root objects with unique keys and string values. Empty and Unicode-whitespace-only values are skipped; non-empty whitespace and every repeated occurrence are preserved. Language keys are locators and are never translation text.

Patchouli includes `book.json` `name`, `landing_text`, and `subtitle`; category `name` and `description`; entry `name`; page string shorthand; built-in page `title` and `text`; multiblock/entity `name`; link-page `link_text`; and literal template component `text` plus string-array `tooltip`. Technical identifiers, recipes, items/entities, images, URL properties, colors, coordinates, flags, macro definitions, and `#` template expressions are excluded. Detectable i18n books do not catalog localization keys. Custom template/include variable inference is intentionally unsupported because safe dataflow across arbitrary templates is outside this slice.

FTB Quests SNBT includes root `title`/`lock_message`, chapter-group `title`, chapter `title` and string-list `subtitle`, quest `title`/scalar `subtitle`/`description`, task/reward `title`, reward-table `title`, toast `description`, and chapter-image `hover`. Chapter subtitle entries retain their array index in the locator and writeback metadata; mixed or non-string subtitle lists fail the catalog transaction. For FTB 1.20.1 compatibility, catalog parsing accepts whitespace-separated compound members, adjacent compound elements in lists, adjacent quoted-string list elements, and valid typed numeric array elements when FTB omits their commas. Typed-array recovery is limited to the declared byte, int, or long grammar and range; ordinary bare and numeric lists remain strict. A bounded in-memory copy receives only separators confirmed by parser state and key lookahead; staged quest files, source hashes, locators, and writeback metadata remain based on the original bytes. Comments, bare or numeric ambiguous list separators, and malformed structure fail closed. Commands, IDs/types, item/entity/advancement/config fields, custom-name matching, URLs, the `{@pagebreak}` sentinel, and exact lowercase `{ftbquests.<key>}` localization references using letters, digits, `_`, `.`, or `-` are excluded. Human brace variables, prose containing a reference, and unrelated brace strings remain catalog text. JSON5 accepts comments, trailing commas, unquoted keys, and single quotes, but rejects duplicate members; it catalogs verified `title`, `quest_subtitle`, `quest_desc`, and `chapter_subtitle` locale tables keyed by 16-hex object IDs.

Catalog limits are 64 nesting levels, 100,000 members per object/array, 100,000 entries, 64 KiB per source string, 256 MiB aggregate source text, and a 16 MiB limit for every selected source or target language ZIP entry and pending source file. Declared and actual ZIP entry sizes are checked. Limits and malformed UTF-8 fail the build rather than truncating data.

## Patchouli Sources

Patchouli files are copied byte for byte, then the conservative fields listed above are read into the catalog. Originals remain unchanged.

From each mod JAR, the tool copies only regular `.json` files under these paths:

- `assets/<namespace>/patchouli_books/<book>/en_us/categories/**`
- `assets/<namespace>/patchouli_books/<book>/en_us/entries/**`
- `assets/<namespace>/patchouli_books/<book>/en_us/templates/**`
- `data/<namespace>/patchouli_books/<book>/book.json` declaration metadata

JAR sources are stored below `workspace/sources/patchouli/jars/<stable-source-id>/` with their complete original JAR paths. The stable ID combines a sanitized JAR basename with a short SHA-256 content hash, so different same-basename JARs do not overwrite each other.

For external books, the tool reads `<modpack>/patchouli_books/<book>/` and copies `<book>/book.json` plus regular `.json` files recursively under `<book>/en_us/categories`, `entries`, and `templates`. These files retain their book-relative structure below `workspace/sources/patchouli/instance/`.

Other locales, textures, non-JSON files, and unrelated Patchouli paths are excluded. Filesystem links/reparse points and ZIP symlink entries are not followed or copied.

## FTB Quests Sources

The tool selects one reusable quest-definition root using this precedence:

1. `<modpack>/config/ftbquests/quests` when it contains regular `data.snbt` or `data.json5`.
2. Otherwise, `<modpack>/defaultconfigs/ftbquests/quests` when it contains either sentinel.

It copies only regular `.snbt` and `.json5` files recursively, preserving the selected root and relative structure below `workspace/sources/ftbquests/`. The copies are parsed read-only to build the catalog and are never used for generic writeback.

The first slice excludes `saves/**`, world-specific `world/ftbquests` progress, `world/serverconfig`, arbitrary FTB Quests TOML settings, and candidate roots without a supported sentinel. Filesystem links/reparse points are not followed or copied.

All pending source extraction uses per-file, total-byte, and file-count limits. Unsafe archive paths, differing-byte destination collisions, and malformed selected sources stop source publication with a contextual error.

## Build And Run Locally

### Windows (PowerShell)

```powershell
go build -o .\modpack-translator.exe .
.\modpack-translator.exe "C:\path\to\your\modpack"
.\modpack-translator.exe
```

### Linux

```bash
go build -o ./modpack-translator .
./modpack-translator "/path/to/your/modpack"
```

### macOS

```bash
go build -o ./modpack-translator .
./modpack-translator "/path/to/your/modpack"
```

## Cross-Compilation

From Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force -Path .\dist | Out-Null

$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -o .\dist\modpack-translator-linux-amd64 .

$env:GOOS = "darwin"
$env:GOARCH = "arm64"
go build -o .\dist\modpack-translator-darwin-arm64 .

Remove-Item Env:GOOS
Remove-Item Env:GOARCH
```

From Linux or macOS:

```bash
mkdir -p ./dist

GOOS=windows GOARCH=amd64 go build -o ./dist/modpack-translator-windows-amd64.exe .
GOOS=linux GOARCH=amd64 go build -o ./dist/modpack-translator-linux-amd64 .
GOOS=darwin GOARCH=arm64 go build -o ./dist/modpack-translator-darwin-arm64 .
```

## Verification

Run the tests and Go static analysis from the project directory:

```powershell
go test ./...
go vet ./...
go build ./...
git diff --check
```

## Minecraft Compatibility

Before creating `pack.mcmeta`, the tool detects the exact Minecraft Java release using this deterministic precedence:

1. `mmc-pack.json`: the `version` of the `components` entry whose `uid` is `net.minecraft` (MultiMC, Prism Launcher, and PolyMC).
2. `manifest.json`: `minecraft.version` (CurseForge/export format).

For launcher instances whose selected game directory is `<instance>/.minecraft`, the same files are also checked in the immediate instance directory. No recursive metadata scan is performed. A malformed higher-precedence file is reported with its path and stops detection; a missing file allows the next format to be tried. If neither file provides a version, generation stops before `pack.mcmeta` is written.

Only exact release strings are accepted. Snapshots, pre-releases, release candidates, modified version strings, and unknown versions are rejected rather than guessed.

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

Minecraft 1.21.9 and newer use decimal resource pack formats and `min_format`/`max_format` metadata. Those versions are explicitly unsupported until that schema is implemented; the tool never emits an integer `pack_format` for them.

Format values come from the Minecraft Wiki [resource pack format history](https://minecraft.wiki/w/Pack_format#Resource_pack_format_history). This is intentionally the resource-pack table, not the separate data-pack table.
