# Modpack Translator

Modpack Translator creates a Minecraft resource pack workspace from language files found inside mod JARs. It reports mods that already include Spanish and writes English language sources to a separate pending-translation folder when Spanish is unavailable. AI-assisted translation is planned for future work and is **not implemented**.

## Prerequisites

- Go 1.22 or newer
- A Minecraft modpack directory containing a `mods` folder

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

After generation, start Minecraft and enable **ModpackTranslations** in the Resource Packs menu.

## Automatic Modpack Discovery

Discovery looks only for folders that contain a `mods` directory. It does not scan entire drives.

The current search includes:

- The current working directory
- The official `.minecraft` folder
- CurseForge instance folders
- Prism Launcher, PolyMC, MultiMC, ATLauncher, and Modrinth app instance/profile folders

Some launchers store the actual Minecraft folder as `<instance>/.minecraft`, so the tool checks both the instance folder and that nested `.minecraft` folder.

## Input And Output

The expected modpack layout is:

```text
<modpack>/
└── mods/
    ├── example-mod.jar
    └── another-mod.jar
```

The command creates this resource pack inside the same modpack directory:

```text
<modpack>/
└── resourcepacks/
    └── ModpackTranslations/
        ├── pack.mcmeta
        └── assets/
            └── <namespace>/
                └── lang/
                    └── es_es.pending.json
```

Pending files mirror the normal Minecraft assets structure, but Minecraft does not load `es_es.pending.json` because it is not a valid language-code filename. Future translation steps can turn it into `es_es.json` when the content is actually translated.

Existing Spanish files from mods are not copied because Minecraft can already load them from the original mod JARs.

Only top-level `*.jar` files in `<modpack>/mods` are processed.

## Language Precedence

Language selection is performed independently for each namespace in each mod JAR:

1. If `assets/<namespace>/lang/es_es.json` exists, the namespace is reported as `OK` and nothing is copied.
2. Otherwise, if `assets/<namespace>/lang/en_us.json` exists, its bytes are copied to `assets/<namespace>/lang/es_es.pending.json`. This file is a future translation source; Minecraft does not load it.
3. If neither file exists, that namespace is skipped.

Missing Spanish entries can still fall back through Minecraft's normal resource-pack and language fallback behavior.

The current source language is fixed to `en_us` and the current target language is fixed to `es_es`. Other output languages are not accepted by the CLI yet; the language names are centralized in code so that a future flag or configuration option can be added without changing the extraction rules.

The source JARs are read only and are not modified.

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
```

## Minecraft Compatibility

The generated `pack.mcmeta` currently uses a fixed resource pack format of `34`. Pack format compatibility depends on the Minecraft version, so verify that format `34` is suitable for the version used by your modpack. Automatic Minecraft version detection and pack-format selection are not currently implemented.
