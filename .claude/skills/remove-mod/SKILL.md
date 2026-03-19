---
name: remove-mod
description: Remove a mod from local profile, server, and modpack, then restart and publish
---

# Remove Mod (Full Workflow)

Completely remove a mod from all environments: local profile, remote server, and Thunderstore modpack.

The user will provide the mod name as the argument (e.g., `/remove-mod DiscordBot` or `/remove-mod RustyMods-DiscordBot`).

## Steps

1. **Remove from local profile**
   - Run `mmcli remove <mod>` to uninstall the mod and its orphaned dependencies

2. **Clean stale configs**
   - Run `mmcli config clean <mod> --yes` to remove config files for the specific mod

3. **Push to server**
   - Run `mmcli server push --with-config` to sync the server (removed mods are deleted, anticheat/moderation configs rebuild automatically)
   - If the push prompts for confirmation, confirm it

4. **Restart server**
   - Run `mmcli server restart`
   - Wait for confirmation that the server restarted

5. **Sync modpack manifest**
   - Run `mmcli modpack sync --yes` to update manifest.json dependencies

6. **Publish modpack**
   - Run `mmcli modpack publish --yes` to upload updated modpack to Thunderstore

## After each step

Report what happened (success or error). If a step fails, stop and report the error to the user before continuing.

## Notes

- The `server push` handles anticheat cleanup automatically — removed mods are dropped from whitelist/greylist/moderation configs on the next push
- Config clean with a mod argument uses substring matching to find that mod's config files specifically, avoiding collateral deletion
- The modpack publish requires `thunderstore_token` and `thunderstore_author` to be configured
