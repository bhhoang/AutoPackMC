# AutoPackMC

**mcpackctl** is a production-ready Go CLI tool that automatically downloads, configures, and runs Minecraft modpack servers from CurseForge, Google Drive, or raw pack formats using Forge or Fabric loaders.

---

## Features

- **CurseForge URL setup** — pass a CurseForge modpack URL directly; the tool resolves the mod ID, selects the best file, prefers a server pack if available, and downloads everything automatically
- **Auto-detection** of CurseForge (`manifest.json`) and raw (`/mods` folder) pack formats
- **Google Drive support** — download directly from `.zip` or `.rar` files on Google Drive
- **Parallel mod downloads** with a configurable worker pool and exponential-backoff retry
- **Verified downloads** — mods are checked against the size and SHA-1 CurseForge publishes, JDKs against Adoptium's SHA-256
- **Local cache** at `~/.cache/mcpackctl/` — identical mods are not re-downloaded
- **Client-mod cleaner** — removes client-only JARs using CurseForge's Client/Server tags plus the community [exclude list](#client-only-mods) for mods that are not tagged; falls back to filename patterns for raw packs
- **Automatic Java** — downloads a portable JDK matching the pack's Minecraft version when the `java` on your PATH is missing or the wrong version
- **Loader installation** — downloads and runs the Forge/NeoForge installer or fetches the Fabric server JAR automatically
- **Server bootstrap** — writes `eula.txt`, `server.properties`, `user_jvm_args.txt`, `run.sh` and `run.bat`
- **Pack updates** — re-running `setup` removes jars the new pack version no longer uses, and keeps mods you added yourself
- **Safe shutdown** — Ctrl+C sends the server's `stop` command so the world is saved
- **Crash diagnosis** — names the client-only mod when one crashes the server

---

## Installation

**Requirements:** Go ≥ 1.22

```bash
git clone https://github.com/bhhoang/AutoPackMC.git
cd AutoPackMC
go build -o mcpackctl ./cmd/mcpackctl
# Optionally move to a directory on your PATH
sudo mv mcpackctl /usr/local/bin/
```

### Docker

```bash
# Build the Go binary first, then build the image
go build -o mcpackctl ./cmd/mcpackctl
docker build -t mcpackctl .
docker run -p 25565:25565 -v $(pwd)/server:/minecraft/server mcpackctl \
  setup --input /path/to/pack.zip --output /minecraft/server
```

---

## Usage

### Set up a modpack server

```bash
# Directly from a CurseForge URL (recommended — resolves mod, selects server pack automatically)
mcpackctl setup https://www.curseforge.com/minecraft/modpacks/deceasedcraft

# Same URL via --input flag
mcpackctl setup --input "https://www.curseforge.com/minecraft/modpacks/deceasedcraft" --output ./server

# From a CurseForge ZIP
mcpackctl setup --input MyPack-1.0.0.zip --output ./server

# From an extracted directory
mcpackctl setup --input ./my-pack-dir --output ./server

# From Google Drive (supports .zip and .rar)
mcpackctl setup --input "https://drive.google.com/file/d/13fyE_SdT0k-j-ucYXGERtQUyZ9e89B-b/view?usp=sharing" --output ./server

# Custom RAM (written to user_jvm_args.txt) and Java path
mcpackctl setup --input pack.zip --output ./server --ram 8G --java-path /usr/lib/jvm/java-21/bin/java

# Keep a mod off the server, or keep one that is wrongly treated as client-only
mcpackctl setup --input pack.zip --output ./server --exclude-mods mekalus-oculus-fork-with-fixed-mekanism-mekasuit --include-mods ctm

# Force a specific loader and version, skip client-mod cleaning
mcpackctl setup --input pack.zip --output ./server --force-loader forge --loader-version 47.4.0 --skip-clean
```

#### How CurseForge URL resolution works

When a CurseForge URL is provided, `mcpackctl` performs the following steps using only the [official CurseForge API](https://api.curseforge.com/v1/):

1. Extracts the modpack slug from the URL path.
2. Calls `GET /v1/mods/search` to resolve the numeric mod ID (exact slug match → normalized name match → highest download count).
3. Calls `GET /v1/mods/{modId}/files?sortField=3&sortOrder=desc&pageSize=20` — a **single-page** fetch of the 20 newest files.
4. Selects the best file: first available Release build, falling back to any available file.
5. Prefers a server pack (`isServerPack == true`) from the same page when one exists.
6. Fetches the download URL via `GET /v1/mods/{modId}/files/{fileId}/download-url`.

Example log output:

```
[resolver] Searching mod: deceasedcraft
[resolver] Found modId=490660
[resolver] Fetching latest files (pageSize=20)
[resolver] Selected file 7623211
[resolver] Found server pack 7623218
[resolver] Using server pack
```

### Start the server

```bash
mcpackctl start ./server
mcpackctl start ./server --ram 6G --java-path /usr/bin/java
```

You can also use the generated `run.sh` (Linux/macOS) or `run.bat` (Windows) in the server directory.

- **Memory:** the max heap comes from `--ram` when given, otherwise from `-Xmx` in `user_jvm_args.txt`, otherwise 2G. `setup --ram` writes the value into `user_jvm_args.txt`, so the run scripts and a plain `start` use it too. Other JVM flags go in the same file.
- **Stopping:** press Ctrl+C once to send the server's `stop` command, which saves the world; press it again, or wait 60 seconds, to kill the process.
- **Crashes:** if a client-only mod crashes the server, `start` names the mod and its jar in `mods/`.

### Updating a pack

Run `setup` again with the new pack version and the same `--output`. Jars installed by the previous setup that the new version no longer uses are removed; mods you added to `mods/` yourself are kept. If the setup fails, `mods/` is put back as it was. The list of installed jars is kept in `.mcpackctl/installed-mods.json`.

Servers set up before this feature existed keep their current jars on the first update; later updates clean up normally.

### Clean client-only mods

The `clean` command removes client-only mods from an existing server's mods directory. It supports two modes:

**API-based (recommended for CurseForge packs)** — a mod is removed when either:

- CurseForge tags its file `"Client"` without `"Server"`, or
- it is on the client-only exclude list (see [Client-only mods](#client-only-mods)).

```bash
mcpackctl clean --mods-dir ./server/mods --manifest ./manifest.json
```

**Pattern-based (fallback)** — uses a built-in list of known client-only filename substrings (OptiFine, Sodium, Iris, JourneyMap, etc.). This is less precise and may produce false positives. A warning is emitted when this mode is active.

```bash
mcpackctl clean --mods-dir ./server/mods
```

#### `clean` flags

| Flag | Description |
|------|-------------|
| `--mods-dir` | *(required)* Path to the mods directory to clean |
| `--manifest` | Path to `manifest.json` — enables API-based detection (recommended) |
| `--api-key` | CurseForge API key (falls back to `MCPACKCTL_CURSEFORGE_API_KEY` / config) |
| `--exclude-mods` | CurseForge slugs or project IDs to remove as well |
| `--include-mods` | CurseForge slugs or project IDs to keep even if treated as client-only |

The `setup` command runs the cleaner automatically after downloading mods (disable with `--skip-clean`). For CurseForge packs, `setup` always uses the API path since the manifest is already loaded.

#### Client-only mods

Many client-only mods are not tagged on CurseForge, so tags alone let them through, and they then crash the server (for example `Attempted to load class .../Screen for invalid dist DEDICATED_SERVER`). mcpackctl therefore also uses the [client-only exclude list](https://github.com/itzg/docker-minecraft-server/blob/master/files/cf-exclude-include.json) maintained for itzg/docker-minecraft-server, including its per-modpack exceptions when the pack comes from a CurseForge URL. The list is cached, and the cached copy is used when it cannot be downloaded.

- Point `cf_exclude_include_file` in the config at another URL or local file in the same format, or set it to `""` to disable the list.
- `--exclude-mods` and `--include-mods` (config: `exclude_mods`, `include_mods`) take CurseForge project slugs or numeric project IDs, separated by commas or spaces. They take precedence over the list, and `--include-mods` also keeps mods that CurseForge wrongly tags client-only.

### Download individual mods or files

```bash
# Download a CurseForge mod by project ID and file ID
mcpackctl download --mod 306612 --file 5159498 --output ./mods

# Download from a direct URL
mcpackctl download --url "https://example.com/mod.jar" --output ./mods

# With CurseForge API key (for accurate filenames)
mcpackctl download --mod 306612 --file 5159498 --output ./mods --api-key YOUR_API_KEY
```

### Java

By default mcpackctl picks the Java version the pack's Minecraft version needs:

| Minecraft | Java |
|-----------|------|
| 1.16.5 and older | 8 |
| 1.17 – 1.20.4 | 17 |
| 1.20.5 – 1.21.x | 21 |
| 26.x | 25 |

If the `java` on your PATH is exactly that version it is used; otherwise a portable [Eclipse Temurin](https://adoptium.net/) JDK for your OS and CPU is downloaded into the server directory (`jdk-17/` etc.), verified, and reused. `start`, `run.sh` and `run.bat` use it, falling back to `java` on PATH if the folder is missing (for example after copying the server to another OS).

To choose yourself, use `--java-path` (a specific executable) or `--java-version` (download that major version), or set `java_path` in the config. The run scripts also accept a `JAVA` environment variable:

```bash
JAVA=/usr/lib/jvm/java-21/bin/java ./server/run.sh
```

### All `setup` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--input` | *(optional)* | Pack ZIP, directory, CurseForge URL, or Google Drive URL. Can also be passed as a positional argument. |
| `--output` | `./server` | Destination server directory |
| `--ram` | | JVM max heap (`-Xmx`, e.g. `8G`), written to `user_jvm_args.txt` |
| `--java-path` | | Path to a `java` executable (disables automatic Java) |
| `--java-version` | | Download this Java major version instead of picking one |
| `--force-loader` | | Override detected loader (`forge` \| `fabric`) |
| `--loader-version` | | Override detected loader version (e.g. `47.4.0`) |
| `--skip-clean` | `false` | Skip client-only mod removal |
| `--exclude-mods` | | CurseForge slugs or project IDs to leave off the server |
| `--include-mods` | | CurseForge slugs or project IDs to keep even if treated as client-only |
| `--log-level` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `--config` | | Path to config file |

---

## Configuration

mcpackctl reads `~/.config/mcpackctl/config.yaml` at startup. All keys can also be set via environment variables prefixed with `MCPACKCTL_`.

```yaml
# ~/.config/mcpackctl/config.yaml
curseforge_api_key: "your-key-here"
java_path: /usr/lib/jvm/java-21/bin/java   # omit to pick Java automatically
ram: 6G
cache_dir: ~/.cache/mcpackctl
workers: 8
cf_exclude_include_file: https://raw.githubusercontent.com/itzg/docker-minecraft-server/master/files/cf-exclude-include.json
exclude_mods: [some-client-mod]
include_mods: []
```

### CurseForge API Key

A CurseForge API key is required to download mods via the official API. Without it, mcpackctl attempts a best-effort direct CDN download that may fail for some mods.

1. Visit [https://console.curseforge.com/](https://console.curseforge.com/) and create a free API key.
2. Add it to your config file **or** set the environment variable:

```bash
export MCPACKCTL_CURSEFORGE_API_KEY="your-key-here"
mcpackctl setup --input pack.zip --output ./server
```

---

## Supported Formats

### Input Sources
- **CurseForge URLs** — resolve and download directly from `https://www.curseforge.com/minecraft/modpacks/{slug}`
- **ZIP files** — CurseForge modpack archives (`.zip`)
- **RAR archives** — Google Drive downloads (`.rar`)
- **Directories** — Extracted packs or raw `/mods` folders
- **Google Drive URLs** — Direct download support

### Mod Loaders
- **Forge** — Full support
- **NeoForge** — Full support
- **Fabric** — Full support

---

## Project Structure
```
cmd/mcpackctl/       CLI entrypoint (main.go)
internal/
  cmd/               Cobra command definitions
  detector/          Detect pack type and Google Drive URLs
  parser/            Parse manifest.json or raw folder
  downloader/        Parallel mod downloader with cache, checksums & exclude list
  installer/         Forge/Fabric server installer + run scripts
  java/              Pick and download a portable JDK
  modstate/          Track installed jars across pack updates
  cleaner/           Remove client-only mods
  resolver/          Resolve CurseForge URLs to download URLs (official API)
  runtime/           Start, stop and diagnose the server process
pkg/
  logger/            zerolog wrapper with pretty console output
  utils/             Shared utilities (zip, rar, HTTP download)
main.go              Thin root entrypoint
Dockerfile           Container image based on openjdk:21-jdk-slim
```

---

## License

MIT