# Changelog

What changed in WittyTuner, newest first. What changed before it had a repository of its own is in the
[WittyMediaServer changelog](https://github.com/wittywilla/WittyMediaServer/blob/master/CHANGELOG.md).

## Unreleased

- **Security:** only a plain `METHOD=NONE` in an HLS key line counts as no key, and a long line can't hide a key or a variant.
- **Security:** a stream's address, which can hold a login, isn't quoted in errors.
- **Security:** a helper's connections are checked as they are made: a playlist from the internet can't reach the home network or this machine, and nothing reaches a cloud host's metadata service.
- **Security:** discovery only believes the device that answered, by its address, and follows no redirect. A SAT>IP channel list can't send FFmpeg to another machine.
- **Security:** a playlist from elsewhere can't name this machine by a number that only the C library reads as an address, such as `2130706433`.
- **Changed:** a playlist at a name only a local network can resolve, such as `nas.local`, counts as being at home.
- WittyTuner is now a project of its own, developed and released from this repository.
