# IDISMAM

*I Dunno If Someone Made Automodpack for Minecraft*: automatic Minecraft modpack servers. This project was called **AutoPack** / **AutoPackMC** before; see [Renamed from AutoPack](#renamed-from-autopack).

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
- **Server bootstrap** — writes `eula.txt` (accepting the [Minecraft EULA](https://aka.ms/MinecraftEULA), which you agree to by running the server), `server.properties`, `user_jvm_args.txt`, `run.sh` and `run.bat`
- **Pack updates** — re-running `setup` removes jars the new pack version no longer uses, and keeps mods you added yourself
- **Safe shutdown** — Ctrl+C sends the server's `stop` command so the world is saved
- **Crash diagnosis** — names the client-only mod when one crashes the server

---

## Desktop app (Windows)

**IDISMAM** is a window for people who would rather not use a terminal. It does everything `mcpackctl` does, in English or Vietnamese:

- **New server** — paste a CurseForge or Google Drive link, or choose a `.zip`, pick a folder and how much memory to give the server, and accept the Minecraft EULA. Progress is shown step by step.
- **Start and stop** — one button each. Stopping sends `stop`, so the world is saved. Closing the window while a server runs asks first, then stops it cleanly.
- **How friends join** — shows the address to share on the same Wi-Fi, and finds the internet address when asked (this contacts `api.ipify.org`).
- **Crashes** — when a client-only mod crashes the server, the app names it and offers to turn it off and start again.
- **Mods** — see which mods are on the server and which were left off, turn mods off or back on, search CurseForge for mods made for the server's Minecraft version and loader, paste a mod link, or add `.jar` files. A mod that needs other mods brings them along, and removing it removes them again unless something else needs them. Pack updates keep the mods you add and the ones you turn off.
- **Server settings** — the common `server.properties` settings as plain switches and choices (who can join, game mode, difficulty, PvP, flying, spawn protection, most players), and every other key under **Advanced settings**, searchable, with a note on what each one does. Changes apply the next time the server starts.
- **Server picture** — choose a picture (PNG, JPEG, GIF, WebP, BMP or TIFF) or use the modpack's logo; it is cropped and shrunk to the 64 × 64 `server-icon.png` shown in the Multiplayer list.
- **Server messages** — the live console, with a box to type commands.
- **Open folder** — opens the server's folder in File Explorer.
- **Updates** — IDISMAM checks GitHub for a new version when it starts (this can be turned off in Settings), downloads it, checks it against `SHA256SUMS.txt`, and restarts into it.
- **Look** — frosted glass with gentle motion. Settings has the animation length (Off, 0.5× to 4×, or Auto, which follows Windows) and **Glass effects**: Auto, Full, or Light, which uses solid panels for PCs that draw slowly. In Auto the app switches to Light by itself when Windows draws it without the graphics card or it cannot keep up.

Settings, the server list and a log file (`idismam.log`) live in `%APPDATA%\IDISMAM`. Mods you turn off are renamed to `*.jar.disabled` in `mods/`.

Build it on Windows (needs the WebView2 runtime, which Windows 10 and 11 include):

```bash
# Optional: the icon and version details Windows shows for the file
go run github.com/tc-hib/go-winres@v0.3.3 simply --arch amd64 --manifest gui \
  --icon cmd/idismam/build/appicon.png --product-name IDISMAM --file-description IDISMAM \
  --product-version 0.0.0 --file-version 0.0.0 --original-filename IDISMAM.exe --out cmd/idismam/rsrc
go build -tags desktop,production -ldflags "-H windowsgui" -o IDISMAM.exe ./cmd/idismam
```

The page is plain HTML, CSS and JavaScript in `cmd/idismam/frontend/`, embedded into the executable; there is no npm build step. Fonts are bundled so the app works offline.

---

## Installation

**Download:** the [Releases page](https://github.com/bhhoang/IDISMAM/releases/latest) has the desktop app for Windows (`IDISMAM-<version>-windows-amd64.exe`) and `mcpackctl` for Windows, Linux and macOS, with `SHA256SUMS.txt` to check them. `mcpackctl --version` prints the version you have.

The Windows programs are signed (see [Code signing](#code-signing)). For a few days after a new version comes out, Windows SmartScreen may still say "Windows protected your PC", because it has not seen that download often yet. Choose **More info**, check that the publisher is **SignPath Foundation**, then choose **Run anyway**.

### Releases

Every push to `main` that passes the tests is released automatically by `.github/workflows/release.yml`. The version comes from the commit messages since the last tag ([Conventional Commits](https://www.conventionalcommits.org)): a `feat:` commit bumps the minor version, `feat!:` or a `BREAKING CHANGE:` footer bumps the major version, and anything else bumps the patch. Add `[skip release]` to a commit message to push to `main` without releasing. Other branches and pull requests are only tested.

A release builds `mcpackctl` for five platforms and IDISMAM for Windows, gives the Windows programs their icon and version details, sends them to SignPath to be signed, waits for the signing request to be approved, and publishes everything with `SHA256SUMS.txt`. Until SignPath is set up, the Windows programs are published unsigned and the run shows a warning.

### Build from source

**Requirements:** Go ≥ 1.25

```bash
git clone https://github.com/bhhoang/IDISMAM.git
cd IDISMAM
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

## Renamed from AutoPack

The desktop app was called AutoPack up to v0.2.0. From the next version it is IDISMAM:

- AutoPack's **Update** button installs IDISMAM like any other update. Releases also carry the app as `AutoPack-<version>-windows-amd64.exe` for this.
- On its first start IDISMAM moves the settings and server list from `%APPDATA%\AutoPack` to `%APPDATA%\IDISMAM`. Servers themselves stay where they are.
- The command-line tool is still called `mcpackctl`.

---

## Code signing

Free code signing provided by [SignPath.io](https://signpath.io), certificate by [SignPath Foundation](https://signpath.org).

### Code signing policy

- **Committers and reviewers:** [@bhhoang](https://github.com/bhhoang). Changes from anyone else come in as pull requests and are reviewed by a committer before they are merged.
- **Approvers:** [@bhhoang](https://github.com/bhhoang). Every signing request is approved by hand in SignPath before anything is signed.
- Only programs built by this repository's GitHub Actions workflow, from this repository's source code, are signed. Both Windows programs are signed: `IDISMAM-<version>-windows-amd64.exe` and `mcpackctl-<version>-windows-amd64.exe`.
- Everyone on the team uses multi-factor authentication for GitHub and SignPath.
- **Privacy:** see [Privacy](#privacy) for every service IDISMAM and mcpackctl contact, and when.

### Setting up signing (maintainers)

1. Apply for the free open-source program at [signpath.org/apply](https://signpath.org/apply). SignPath reviews the project first.
2. Once accepted, in SignPath:
   - Add the **GitHub.com** trusted build system to the organization, and link it to the project.
   - Install the SignPath GitHub App for this repository.
   - Paste [`.signpath/artifact-configuration.xml`](.signpath/artifact-configuration.xml) into the project's artifact configuration.
   - Check that the `release-signing` policy has you as approver.
   - Create an API token for a CI user that may submit signing requests to that policy.
3. In the GitHub repository settings, under **Secrets and variables → Actions**, add:
   - the secret `SIGNPATH_API_TOKEN` (the API token);
   - the variable `SIGNPATH_ORGANIZATION_ID`;
   - optionally the variables `SIGNPATH_PROJECT_SLUG` (default `IDISMAM`) and `SIGNPATH_SIGNING_POLICY_SLUG` (default `release-signing`), if yours differ.
4. From then on, every release sends a signing request to SignPath, and SignPath emails the approver. The release waits up to five hours for the approval; if it runs out, approve the request and re-run the failed jobs.

---

## Privacy

Neither IDISMAM nor mcpackctl collects analytics or sends anything about you or your PC. The log file stays on your PC. They only contact these services, and only for the reasons listed:

| Service | When |
|---------|------|
| CurseForge (`api.curseforge.com`, `www.curseforge.com`, `*.forgecdn.net`) | Setting up or updating a pack from CurseForge, downloading its mods, searching for mods or adding one. |
| Google Drive (`drive.google.com`, `drive.usercontent.google.com`) | Setting up a pack from a Google Drive link. |
| Mod loader sites (`maven.minecraftforge.net`, `maven.neoforged.net`, `meta.fabricmc.net`) | Installing Forge, NeoForge or Fabric for a server. |
| Adoptium (`api.adoptium.net`, which downloads from GitHub) | Downloading Java when a server needs a version your PC does not have. |
| GitHub (`raw.githubusercontent.com`) | Downloading the community list of client-only mods during setup. |
| GitHub (`api.github.com`, `github.com`) | IDISMAM only: checking for a new version at start-up, which can be turned off in Settings, and downloading the update when you choose to. |
| ipify (`api.ipify.org`) | IDISMAM only: when you press **Find address** to see your internet address. |

These services have their own privacy policies. The Minecraft server that IDISMAM starts is Mojang's program; with **Check players' Minecraft accounts** on (`online-mode`), it checks each player's account with Mojang.

---

## Project Structure
```
cmd/mcpackctl/       CLI entrypoint (main.go)
cmd/idismam/        Desktop app (Wails window + embedded frontend/, build/appicon.png)
internal/
  app/               Desktop app logic: servers, setup jobs, running servers, mods
  cmd/               Cobra command definitions
  setup/             Turn a pack into a ready server (shared by the CLI and the app)
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
.github/workflows/   Test, version, build, sign and release
.signpath/           SignPath artifact configuration (a copy of what is set in SignPath)
```

---

## License

[MIT](LICENSE)