# Changelog

What changed in WittyTuner, newest first. What changed before it had a repository of its own is in the
[WittyMediaServer changelog](https://github.com/wittywilla/WittyMediaServer/blob/master/CHANGELOG.md).

## Unreleased

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
