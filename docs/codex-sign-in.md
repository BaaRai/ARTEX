# Standalone Codex sign-in

On Windows, install the Codex CLI and ensure `codex` is on `PATH`. Run these commands from the repository root in PowerShell.

```powershell
.\scripts\codex-sign-in.ps1 login
.\scripts\codex-sign-in.ps1 status
.\scripts\codex-sign-in.ps1 logout
```

For `login`, follow the URL and device-code instructions printed by Codex. If Codex reports that device authorization is disabled, enable it in ChatGPT security settings and retry.

The CLI manages credentials for this profile under `%LOCALAPPDATA%\ARTEX\codex-sign-in`. `logout` clears the credentials for that profile. `status` is the default command and returns the CLI's exit code.
