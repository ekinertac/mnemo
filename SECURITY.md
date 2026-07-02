# Security Policy

## Reporting a vulnerability

If you find a security issue in Mnemo, please report it privately rather than opening a public
issue. Use GitHub's [private vulnerability reporting](https://github.com/ekinertac/mnemo/security/advisories/new)
or email **ekinertac@gmail.com**. You'll get a response as soon as reasonably possible.

Please include enough detail to reproduce the issue, and give a reasonable window to address it
before any public disclosure.

## How Mnemo handles your data

- **Encryption and integrity are `restic`'s.** Mnemo shells out to [`restic`](https://restic.net);
  all data is AES-256 encrypted at rest, content-addressed, and integrity-checkable (`mnemo verify`).
- **The repository password is the encryption root.** Lose it and the data is unrecoverable — by
  design. Mnemo never invents, prints, or stores it; it comes from `RESTIC_PASSWORD*` or a config
  secret reference.
- **Secrets are references, not values.** `config.json` stores no plaintext credential — only how to
  fetch one (an OS-keychain command, a file, or an env var). Secrets are never passed as CLI flags
  (they'd leak via the process list).
- **Sessions only.** Mnemo syncs conversation/session data, never config or capabilities — so MCP
  server configs, tokens embedded in settings, etc. are never uploaded.

Note that your Claude Code **session transcripts may themselves contain sensitive content** (code,
credentials you pasted, etc.). They are encrypted in the repository, but treat the repository
password and backend credentials accordingly.
