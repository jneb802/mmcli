<p align="center">
  <img src="mmcliBanner.png" alt="mmcli banner" />
</p>

# mmcli

A command-line Valheim mod manager for macOS. Installs mods from [Thunderstore](https://thunderstore.io/c/valheim/), manages profiles, and launches the game with BepInEx.

## Install

Download the latest binary for your Mac:

- [Apple Silicon (M1/M2/M3)](https://github.com/jneb802/mmcli/releases/download/v0.1.0/mmcli-darwin-arm64)
- [Intel](https://github.com/jneb802/mmcli/releases/download/v0.1.0/mmcli-darwin-amd64)

Then make it executable and move it to your PATH:

```
chmod +x mmcli-darwin-*
mv mmcli-darwin-* /usr/local/bin/mmcli
```

## Getting Started

```
mmcli init
```

This detects your Valheim install, installs BepInEx, and creates a default profile.

## Interactive TUI

```
mmcli tui
```

A terminal UI for browsing, toggling, installing, updating, and removing mods with keyboard shortcuts.

## Launching the Game

```
mmcli start
```

Launches Valheim with BepInEx loaded and streams logs to the terminal.

## Installing Mods

```
mmcli install RandyKnapp-EpicLoot
```

Dependencies are resolved and installed automatically.

## Managing Mods

```
mmcli list                        # show installed mods
mmcli remove <mod>                # remove a mod and orphaned dependencies
```

## Profiles

Profiles let you maintain separate sets of mods (e.g. one for solo, one for a modded server).

```
mmcli profile create <name>
mmcli profile switch <name>
mmcli profile list
mmcli profile delete <name>
mmcli profile import <url|code>   # import from r2modman/Thunderstore profile code
mmcli profile open                # open profile folder in Finder
```
