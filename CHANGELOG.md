# Changelog

What changed in WittyTuner, newest first. What changed before it had a repository of its own is in the
[WittyMediaServer changelog](https://github.com/wittywilla/WittyMediaServer/blob/master/CHANGELOG.md).

## 0.4.4

- **New: the demo tuner** (#19). **Add a demo tuner** in Settings, or **Try a demo tuner** in the first-run step (and on every app's setup page, among what was found), adds a pretend network tuner that runs inside WittyTuner on this computer: test-pattern channels with their name and a clock, made by the server's FFmpeg, clips from a folder you choose played in a loop, a channel marked as protected to show how encrypted channels look, and a guide of its own. Live TV, the guide, recordings and WittyTV work with no hardware and no internet. It answers the network tuner discovery and lineup protocol on `127.0.0.1`, so the real network tuner code runs end to end. Its settings choose how many tuners it has, slow tuning and a signal that drops now and then, for testing error handling. Labelled as a demo everywhere.
- **New: matching channels to the guide** (#20). Channels are matched by guide id (whatever its case or suffix), by name (without a playlist's country tags and HD-style words, in any alphabet), by call sign and by number, each with a confidence score; sure matches are made after every guide and playlist read. **Match channels to the guide** lists channels with no guide or an unsure match, with their likeliest guide entries (one press takes one) and a search of the whole guide. A match chosen by hand is remembered by the channel's name, so a playlist added again keeps it. On a TV that page says to use a computer.
- **New:** a guide source says how many channels it covers, how far ahead its listings go, and warns when it hasn't been read for two days or its listings run out within 12 hours.
- **New:** a playlist's `tvg-language`, `tvg-country` and `tvg-shift`; per-channel headers from `#EXTVLCOPT:http-user-agent`/`http-referrer`, `address|User-Agent=…&Referer=…`, `#EXTHTTP` and `#KODIPROP` stream headers. The Channels page filters by group, country, language, hidden, no guide and missing, and **Show these** / **Hide these** change every channel a filter picks. (#18)
- **Fixed:** reading a playlist again lost channels whose address had changed (a login token in it, as many providers have), with their favourites, recordings, numbers, names and guide match. A channel is now kept when its name and group say it's the same, and one that disappears is kept, hidden, for a week in case it comes back. (#18)
- **Fixed:** the same channel listed twice in a playlist kept only the first address; the others are now its fallbacks, tried in turn when it won't open. (#18)
- **Fixed:** two reads of one guide source at once (adding a tuner reads its guide, and so does the refresher) replaced each other's programmes and could leave the guide empty.
- **Fixed:** errors say what to try: a 403 that the link may have expired or the provider wants a user agent or referrer, a 404 that the playlist is out of date, an HLS channel whose variants are all missing says so at once instead of leaving the player waiting, and a guide source that can't be reached says it's the guide's address. (#18)
- **Discovery** finds network tuners that answer on another port than 80, by the address their discovery answer names (on the same machine only).
- The web part's tests run on Windows (they imported a `D:` path instead of a file URL).

## 0.4.3

- **Fixed:** the web part asked for `/tuners/drivers`, `/tuners/discover` and `/tuners/duplicates`, which the service serves as `/drivers`, `/discover` and `/duplicates`. The first-run step said "Live TV couldn't be loaded", and Settings couldn't list the kinds of tuner, search for tuners or check for duplicate channels. (#12)
- **Fixed:** nothing checked that the paths the web part asks for are the ones `integration.json` declares, which is how #12 got through. A Go test now reads every call in `web/app` and fails on a path or method the server wouldn't pass on. (#14)
- **Fixed:** when the first-run step can't reach WittyTuner, it says live TV can be set up later in Settings, Live TV & tuners, and setup carries on with **Set it up later** (Try again is still there), instead of "Live TV couldn't be loaded". Moving on from the step tells the server it was gone through, or put off when nothing was added (`POST /setup/tv`), so "Finish setting up WittyTuner" doesn't stay on Home. (#14)

## 0.4.2

- The README's guide and recordings pictures are retaken: two channels with schedules of their own, and longer programmes in the guide.

## 0.4.1

- The README's pictures are retaken from WittyMediaServer 0.8.0-alpha.

## 0.4.0

- Built on WittyModule 0.5.4 and module API 5; its workflows use WittyModule 0.5.4 and build the web part with it.
- WittyLog: the service logs in the Live TV area at the level the server asks for, and declares its `tuner` log option in `module.json`. Repair: it answers `doctor.Check` with tuners and playlists whose channels failed their last health check.
- **WittyTuner 0.4.0, module API 5: it owns its whole part of live TV.** The tuners, their channels and the guide sources move out of the server into WittyTuner's own database (starting empty: nothing is brought over), with its own routes at `/api/modules/wittytuner/…` for adding, checking, scanning and changing them, checking channels, duplicates and bulk changes, guide sources and the encrypted-channels page. It's a service now, so it keeps the tuners it opened, their limits, scans and channel checks; the server's guide, recordings and catch-up open channels through it (`/tv/…`) and watch every stream themselves. A guide source is fetched here and handed to the server, which keeps the guide. Settings → Live TV & tuners, the first-run step "tv" and the encrypted-channels page are its own web part, and `sdui.json` has pages for every app (the tuners, one tuner's channels, the first-run step), so the TV apps can set it up too. The old one-question-per-process helper protocol is gone. Needs the `data` permission and WittyModule 0.5.4.
- **Logging:** the helper's drivers now say what they did. They log with slog's text lines on stderr, with `cat=tv`, at the level the server asks for in `WITTYMODULE_LOG` (Debug when any `WITTYMODULE_LOG_TOPICS` are on), so how a stream's FFmpeg ended, frequencies with no signal and a crashed discovery reach the server's log instead of nowhere. An IPTV provider that refuses the account, or says it's expired, banned or disabled, is a warning there, with the tuner's name and why, and never the login.
- **WittyTuner 0.3.0:** Fits WittyMediaServer 0.8.x. Catch-up from a source's archive, and Xtream Codes-style providers.
- **Module API 4:** it carries `sdui.json`, written from `UI()` in Go with WittyModule's `sdui` kit and kept equal to it by a test. It holds only the theme scope, `wittytuner`: live TV, the guide, recordings and the tuner settings are the server's own pages, the helper reads no settings of its own, and it has nothing a card could show, so it declares no pages, menu entries, settings or sources. Needs WittyMediaServer 0.8.0-alpha or later.
- The server's own questions (`sdui.items`, `sdui.image`, `settings.changed`) are answered by a helper that stays for more, as the server expects, instead of one that leaves after a single answer like a tuner's question. Asking for a picture of the module's, which anyone signed in can do, no longer makes the next question look like a crash that counts towards resting the module.

- **New:** catch-up. A playlist's `catchup`, `catchup-type`, `catchup-days`, `timeshift` and `catchup-source`, on a channel or once on the `#EXTM3U` line, say how far back a channel's source keeps what it showed and how to ask for it: `default`, `append`, `shift`, `flussonic` or `xc`, with the usual `{utc}` and `${start}` placeholders. A channel carries them as `catchup`, `catchupDays` and `catchupSource`, and `open` plays from the archive when it's given `archiveStart` (and `archiveEnd`). An archive address is held to every rule a live one is, and a time the source no longer keeps, or hasn't shown yet, is refused.
- **New:** the `xtream` driver, for an IPTV provider's account on an Xtream Codes-style panel: channels with their groups, guide ids and catch-up, the provider's guide, and how many streams the account may have at once. A wrong login, or an account that's expired, banned or disabled, says so. Tried against a stand-in panel only.
- A connection error says it is one, across the process line, so a server keeps what it holds off the page.
- Every kind of tuner now runs in the helper, so there's no copy compiled into the server. It asks for permission to start programs and open this machine's devices. New calls: `validate`, `drivers` and `recheck`.
- **Security:** only a plain `METHOD=NONE` in an HLS key line counts as no key, and a long line can't hide a key or a variant.
- **Security:** a stream's address, which can hold a login, isn't quoted in errors.
- **Security:** a helper's connections are checked as they are made: a playlist from the internet can't reach the home network or this machine, and nothing reaches a cloud host's metadata service.
- **Security:** discovery only believes the device that answered, by its address, and follows no redirect. A SAT>IP channel list can't send FFmpeg to another machine.
- **Security:** a playlist from elsewhere can't name this machine by a number that only the C library reads as an address, such as `2130706433`.
- **Changed:** a playlist at a name only a local network can resolve, such as `nas.local`, counts as being at home.
- WittyTuner is now a project of its own, developed and released from this repository.
