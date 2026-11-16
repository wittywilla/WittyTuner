# WittyTuner

Brings live TV into WittyMediaServer: network tuners, IPTV playlists, capture cards and guide data. You need it if you want live TV or recording.

It's a module of [WittyMediaServer](https://github.com/wittywilla/WittyMediaServer) ([wittymediaserver.com](https://wittymediaserver.com)), on module API 5.

<!-- pictures -->

![The programme guide, with a film playing in the preview](docs/images/wittytv-guide.webp)

<sub>The guide, here with channels made from a library, playing [Aladdin and His Wonderful Lamp](https://commons.wikimedia.org/wiki/File:Aladdin_And_His_Wonderful_Lamp_(1939).webm), Dave Fleischer, Fleischer Studios, 1939, public domain. From a test server. The server's [credits list](https://github.com/wittywilla/WittyMediaServer/blob/master/docs/images/CREDITS.md) names everything shown.</sub>

![The channels and tuners page in Settings](docs/images/settings-channels.webp)

<sub>Settings, Channels & tuners, on a server with no tuner attached.</sub>

![The recordings page](docs/images/wittytv-recordings.webp)

<sub>Recordings, on a server with none yet.</sub>

<!-- /pictures -->

## What it does

A driver lists channels and opens one, as raw MPEG-TS or as options for FFmpeg.

| Driver | Reaches | Systems | How far it's been tested |
|---|---|---|---|
| `iptv` | M3U and M3U8 playlists | All | Automated tests, and FFmpeg against local streams |
| `xtream` | IPTV provider accounts on Xtream Codes-style panels: channels, groups, guide and catch-up | All | Against a stand-in panel, not a real provider |
| `stream` | One HTTP, HLS, RTSP, RTP, UDP, RTMP or SRT address | All | Automated tests, and FFmpeg against local streams |
| `network` | HDHomeRun-style network tuner boxes | All | Against a stand-in device, not real hardware |
| `backend` | TV servers over open protocols: an HTTP API with M3U and XMLTV, or SVDRP | All | Against stand-in servers, not a real one |
| `satip` | SAT>IP servers | All | Protocol tests, not real hardware |
| `dvb` | Linux DVB-T/T2, DVB-C, DVB-S/S2, ATSC, ClearQAM, ISDB-T, with band scans and `channels.conf` import | Linux | Parsers and band plans, not real hardware. No DiSEqC yet |
| `v4l2` | Analogue tuner and capture cards | Linux | Argument tests, not real hardware |
| `dshow` | Capture cards through DirectShow | Windows | Listing tested, not with a card |
| `avfoundation` | Capture devices | macOS | Parser tests only |
| `command` | Your own program writing MPEG-TS. Off unless the server is started with `WMS_TUNER_COMMANDS=1` | All | Automated tests with a test program |
| `bda` | Digital tuners through BDA | Windows | **Not built.** Listed so the gap is visible |

It also reads guide data from broadcasts (DVB EIT and ATSC PSIP) and from XMLTV files.

**Encrypted and copy-protected channels are refused,** with a clear message, and nothing is decrypted. It checks lineups, stream tables, the packets themselves as they play, DRM lines in playlists, and any key line in an HLS playlist. The check applies to every driver, and there's no way to change it. The server also watches every stream itself, whatever a module says.

No channel lists or stream addresses come with it. Band scans only say where a channel could be.

Since 0.4.0 (module API 5) WittyTuner owns its whole part of live TV: the tuners, their channels and the guide sources are in its own database, and Settings → Live TV & tuners, the first-run step and the encrypted-channels page are its own screens. The guide, recordings, favourites and sets stay with the server, which opens every channel through WittyTuner and watches every stream itself.

How to use it is in **Help** inside WittyMediaServer: see Channels and live TV.

## How to get it

An admin adds it from the module store inside WittyMediaServer: **Settings → Modules**, then **Browse the store**, then **Add** beside WittyTuner. This version needs a WittyMediaServer of module API 5 (0.8.0-alpha or later). The range this version fits is in `module.json`: 0.8.0-alpha through 0.8.x. The server takes API 5 modules only, so older servers can't use this version. How modules work on the server is in its [docs/MODULES.md](https://github.com/wittywilla/WittyMediaServer/blob/master/docs/MODULES.md).

## What it needs

- FFmpeg, which comes with the server.
- A tuner or an IPTV playlist.
- [WittyModule](https://github.com/wittywilla/WittyModule) 0.5.4, which is how a server talks to it, built in.

It asks the server for four permissions: network, running programs, devices, and a data folder of its own for its database. It's written in Go with no cgo.

## Platforms

A program is built for each system below, and the server uses the right one.

| System | Where it stands |
|---|---|
| Linux (x64, arm64, 32-bit ARM, x86) | Its tests pass on x64. No real tuner or capture card has been tried on any system. The other CPUs are only built |
| Windows (x64, arm64) | Built. This version hasn't been run on Windows since it became its own program |
| macOS (Intel, Apple silicon) | Built by the release tool. It has not been run on a real Mac |
| FreeBSD (amd64, arm64) | Cross-compiled only. It has not been run |

On macOS and FreeBSD the server has no sandbox for a module's helper: it gets a small environment and a soft memory limit, and nothing more.

## In the apps

WittyTuner is a service (module API 5: WittyMediaServer `docs/MODULES-API5.md`):

- **Its routes** (`integration.json`, package `api`) are mounted by the server at `/api/modules/wittytuner/…`: the tuners, their channels and checks, duplicate channels, the guide sources, and the first-run step. Every one but the encrypted-channels page (`/encrypted-help`, for anyone signed in) is an administrator's.
- **Its own routes for the server** (`/tv/…`, never a person's): every channel for the guide, opening one within its tuner's limit (MPEG-TS handed over, or an address FFmpeg opens under the server's own rules), letting it go, looking at a playlist again, and what the server's guard saw. The server never takes WittyTuner's word that a channel is clear.
- **Its data** is a SQLite database in its own folder (package `store`): tuners, channels and guide sources. A guide source is fetched here and handed to the server (the host API's `POST /tv/guide/{source}`), which keeps the guide.
- **`sdui.json`** (written from package `ui`; `go test ./ui -update-sdui`) has its pages for every app, the Apple TV and Android TV apps among them: the tuners with what's on this network to add, one tuner's channels, and the first-run step. Their sources are answered by the service, and every row runs one of its own commands.
- **Its web part** (`web/`, built with WittyModule's `kit/web/build-bundle.mjs`; `npm test` checks it) is Settings → Live TV & tuners, the first-run step and the encrypted-channels page in the browser.

## Building and testing

You need Go 1.26 or newer. In a worktree, first run WittyMediaServer's `scripts/dev-modules.mjs` (or the copy its `--root` option keeps in the folder above the repositories). It writes a `go.work` that git ignores, so the checkout builds against the repositories beside it. Never run `go work use`.

```bash
go vet ./...
```

```bash
go test ./... -count=1
```

```bash
go run github.com/wittywilla/wittymodule/cmd/wittymodule check .
```

The last one checks the manifest, `integration.json` and `sdui.json`. The web part (`web/`) is tested and built as CI does it:

```bash
npm ci
npm test
npm run build:bundle
```

That builds with WittyModule's web builder (`kit/web/build-bundle.mjs`, from a WittyModule checkout beside this one, or `WITTYMODULE=<checkout>`).

`go run github.com/wittywilla/wittymodule/cmd/wittymodule pack -targets linux/amd64 -out release .` builds a release for one system. CI also builds for every system.

To use it as a library, `wittytuner.New(driver, config, env)` gives a source with `Channels` and `Open`. `wittytuner.Drivers(env)` describes every driver and its settings, and `wittytuner.Discover(ctx, env)` looks for tuners. The custom command driver stays off unless the host sets `Env.AllowCommands`.

## Licence

GNU Affero General Public License v3.0 (or later): see [LICENSE](LICENSE) and [NOTICE](NOTICE). If you redistribute it or build on it, please keep the NOTICE attribution. Each release archive carries a `THIRD-PARTY-NOTICES.txt` for what its programs link.

WittyTuner is part of the WittyMediaServer project: [server repository](https://github.com/wittywilla/WittyMediaServer), [wittymediaserver.com](https://wittymediaserver.com).

<!-- module-kit -->
## Releasing

A version tag builds a program for every platform, signs the checksum list, and publishes a release that servers can install.

Versions move in small steps as work lands. `go run github.com/wittywilla/wittymodule/cmd/wittymodule@v0.5.4 bump .` raises it
everywhere it is written (add `-part minor` for a bigger step). A bump isn't a release: nothing is
tagged or published until you tag it. CI fails when code changed since the last tag and the version
didn't, because tags never move.

To release:

1. Check that `version` in `module.json` is the one to publish, with the bump committed.
2. Tag that commit and push the tag: `git tag vX.Y.Z`, then `git push origin vX.Y.Z`. A tag with a
   suffix, such as `v1.2.0-beta`, is published as a prerelease.
3. Watch the *Actions* tab. The signing step uses the `WMS_SIGNING_KEY` secret of the `release` environment, and nothing is published without it.

To take a version back, run the *withdraw* workflow from the *Actions* tab. It adds the version to a signed list on the release tagged `withdrawals`, which servers read.

Licensed under AGPL-3.0-or-later: see [LICENSE](LICENSE) and [NOTICE](NOTICE).
<!-- /module-kit -->

## Legal

WittyTuner is an independent open-source project. It isn't associated with, sponsored by or endorsed
by any tuner maker, broadcaster, IPTV provider or guide service. No channel lists or stream
addresses come with it. Encrypted channels are refused and nothing is decrypted. What a tuner or a
provider offers belongs to them, and you're responsible for having the rights to what you receive
and record. Any third-party names here only describe what it works with, and belong to their owners.
It's provided “as is”, with no warranty; see [LICENSE](LICENSE). Local law applies to how you use
it. This isn't legal advice.
