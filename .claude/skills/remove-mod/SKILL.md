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

3. **Remove from server**
   - Use the TUI or API to remove the mod from the server (server tab → x on the mod)
   - The server manages its own mods independently — no push/sync

4. **Check for online players before restarting**
   - The server is live with players. Check player count before restarting:
   ```bash
   ssh warp@praetoris "curl -s -H 'X-API-Key: 32f7d782ddd8364e1527f32955d4f9f0024a41ea5db103fb9cba0e571781d843' http://localhost:9877/api/v1/players"
   ```
   - If players are online, **warn the user and ask for confirmation** before restarting
   - If no players, proceed with restart

5. **Restart server** (only after player check)
   - Run `mmcli server restart`
   - Wait for confirmation that the server restarted

6. **Sync modpack manifest**
   - Run `mmcli modpack sync --yes` to update manifest.json dependencies

7. **Publish modpack**
   - Run `mmcli modpack publish --yes` to upload updated modpack to Thunderstore

## After each step

Report what happened (success or error). If a step fails, stop and report the error to the user before continuing.

## Notes

- Each surface (local, server, modpack) is managed independently — there is no push/sync
- Moderation (Mods.yaml) is only changed via the `a` key in the TUI — removing a mod doesn't auto-update it
- Config clean with a mod argument uses substring matching to find that mod's config files specifically
- The modpack publish requires `thunderstore_token` and `thunderstore_author` to be configured
