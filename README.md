# AutoPackMC

**mcpackctl** is a production-ready Go CLI tool that automatically downloads, configures, and runs Minecraft modpack servers from CurseForge, Google Drive, or raw pack formats using Forge or Fabric loaders.

---

## Features

- **Auto-detection** of CurseForge (`manifest.json`) and raw (`/mods` folder) pack formats
- **Google Drive support** — download directly from `.zip` or `.rar` files on Google Drive
- **Parallel mod downloads** with a configurable worker pool and exponential-backoff retry
- **Local cache** at `~/.cache/mcpackctl/` — identical mods are not re-downloaded
- **Client-mod cleaner** — automatically removes client-only JARs using the CurseForge API's authoritative `gameVersions` field; falls back to filename patterns for raw packs
- **Loader installation** — downloads and runs the Forge installer or fetches the Fabric server JAR automatically
- **Server bootstrap** — writes `eula.txt`, `server.properties`, and `run.sh`
- **Custom Java support** — set `JAVA` env var or use `--java-path` to use specific Java version
- **Graceful shutdown** — handles `SIGINT`/`SIGTERM` cleanly when running the server

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
# From a CurseForge ZIP
mcpackctl setup --input MyPack-1.0.0.zip --output ./server

# From an extracted directory
mcpackctl setup --input ./my-pack-dir --output ./server

# From Google Drive (supports .zip and .rar)
mcpackctl setup --input "https://drive.google.com/file/d/13fyE_SdT0k-j-ucYXGERtQUyZ9e89B-b/view?usp=sharing" --output ./server

# Custom RAM and Java path
mcpackctl setup --input pack.zip --output ./server --ram 8G --java-path /usr/lib/jvm/java-21/bin/java

# Force a specific loader and skip client-mod cleaning
mcpackctl setup --input pack.zip --output ./server --force-loader fabric --skip-clean
```

### Start the server

```bash
mcpackctl start ./server
mcpackctl start ./server --ram 6G --java-path /usr/bin/java
```

### Clean client-only mods

The `clean` command removes client-only mods from an existing server's mods directory. It supports two modes:

**API-based (recommended for CurseForge packs)** — queries the CurseForge API for each mod's `gameVersions` field. A mod is only removed if it is tagged `"Client"` and has *no* `"Server"` tag. This is authoritative and will never accidentally delete a mod that is required on both sides.

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

The `setup` command runs the cleaner automatically after downloading mods (disable with `--skip-clean`). For CurseForge packs, `setup` always uses the API path since the manifest is already loaded.



```bash
# Download a CurseForge mod by project ID and file ID
mcpackctl download --mod 306612 --file 5159498 --output ./mods

# Download from a direct URL
mcpackctl download --url "https://example.com/mod.jar" --output ./mods

# With CurseForge API key (for accurate filenames)
mcpackctl download --mod 306612 --file 5159498 --output ./mods --api-key YOUR_API_KEY
```

### Custom Java version

The `run.sh` script accepts a `JAVA` environment variable:

```bash
# Use a specific Java version
JAVA=/usr/lib/jvm/java-21/bin/java ./server/run.sh

# Or export it globally
export JAVA=/usr/lib/jvm/java-21/bin/java
./server/run.sh
```

### All `setup` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--input` | *(required)* | Pack ZIP, directory, or Google Drive URL |
| `--output` | `./server` | Destination server directory |
| `--ram` | `2G` | JVM max heap (`-Xmx`) |
| `--java-path` | `java` | Path to `java` executable |
| `--force-loader` | | Override detected loader (`forge` \| `fabric`) |
| `--skip-clean` | `false` | Skip client-only mod removal |
| `--log-level` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `--config` | | Path to config file |

---

## Configuration

mcpackctl reads `~/.config/mcpackctl/config.yaml` at startup. All keys can also be set via environment variables prefixed with `MCPACKCTL_`.

```yaml
# ~/.config/mcpackctl/config.yaml
curseforge_api_key: "your-key-here"
java_path: /usr/lib/jvm/java-21/bin/java
ram: 6G
cache_dir: ~/.cache/mcpackctl
workers: 8
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
  downloader/        Parallel mod downloader with cache & retry
  installer/         Forge/Fabric server installer + scripts
  cleaner/           Remove client-only mods
  runtime/           Start and supervise the server process
pkg/
  logger/            zerolog wrapper with pretty console output
  utils/             Shared utilities (zip, rar, HTTP download)
main.go              Thin root entrypoint
Dockerfile           Container image based on openjdk:21-jdk-slim
```

---

## License

MIT