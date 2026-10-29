# gplay

Not affiliated with Google.

`gplay` is a safety-first command-line client for the Google Play Developer API, built for AI agents and CI. It is the Android counterpart to an App Store Connect CLI: explicit credentials, a production lock, and one JSON error object on every failure.

```text
gplay (github.com/TECMANIC/gplay) v0.1.0 <commit> <date>
```

If `gplay version` does not start with `gplay (github.com/TECMANIC/gplay)`, you have a different binary. Two other projects also install a command named `gplay`: [tamtom/play-console-cli](https://github.com/tamtom/play-console-cli) and [PollyGlot/google-play-cli](https://github.com/PollyGlot/google-play-cli).

Module: `github.com/TECMANIC/gplay`. The full contract is [SPEC.md](SPEC.md).

## Install

```bash
go install github.com/TECMANIC/gplay/cmd/gplay@latest
```

Homebrew, once a release exists, is only through the Tecmanic tap. It is not in Homebrew core:

```bash
brew install tecmanic/tap/gplay
```

Verify a release checksum with cosign keyless (GitHub OIDC):

```bash
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity-regexp '^https://github.com/TECMANIC/gplay/\.github/workflows/release\.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

## Credentials

Only a Google service-account JSON key is accepted, and only when `type` is `service_account` and `token_uri` is `https://oauth2.googleapis.com/token`. Application default credentials are used only when you pass `--credentials adc` or set `GPLAY_CREDENTIALS=adc`. A configured source that is missing or invalid is a failure. gplay does not try the next source.

Resolution order:

1. `--credentials` (a file path, or `adc`)
2. `GPLAY_CREDENTIALS` (a file path, or `adc`, including when the variable is set but empty)
3. `GPLAY_CREDENTIALS_JSON` (the JSON document, capped at 64 KiB)
4. `~/.config/gplay/config.json` from `gplay auth login`

These names do not overlap PollyGlot's `GPLAY_SERVICE_ACCOUNT`, `GPLAY_ACCOUNT`, or `GPLAY_READONLY`. gplay does not read those variables. The config file must contain `"tool": "TECMANIC/gplay"` or it is refused.

`gplay auth login` stores the absolute path of the key. It does not copy the key.

## Two service accounts

The production lock is a mistake-guard for agents. It is not a security boundary. A caller who can edit the binary, the flags, and `.gplay/policy.json` can allow a production write. The permission boundary is the service account.

| Account | Use | Play Console permissions |
|---|---|---|
| Agent | Day-to-day commands from an agent or CI | View app information (read-only), and manage testing releases (`CAN_VIEW_NON_FINANCIAL_DATA`, `CAN_MANAGE_TRACK_APKS`) |
| Human or CD | The only identity allowed to ship production | The agent permissions, plus release to production (`CAN_MANAGE_PUBLIC_APKS`) |

Do not grant `CAN_MANAGE_PERMISSIONS` to either account. A new grant can return 403 for up to 24 hours while Play propagates it. That 403 is exit 3, code `auth_failed`.

| Permission | What it allows here |
|---|---|
| `CAN_VIEW_NON_FINANCIAL_DATA` | `apps list`, `release status`, `auth doctor` |
| `CAN_MANAGE_TRACK_APKS` | Upload and testing-track releases |
| `CAN_MANAGE_PUBLIC_APKS` | Production track writes. Withhold this from the agent account |
| `CAN_MANAGE_PERMISSIONS` | Not used, and not granted |

`apps list` and the doctor Reporting probe also need the Play Developer Reporting API enabled on the GCP project. That is a separate API from the Android Publisher API.

## What the lock does

A track is production when, after trimming and lower-casing, it is `production` or `<prefix>:production` (`wear:production`, `TV:Production`). `internal`, `alpha`, `beta`, and `qa` are not locked. `beta` is open testing and is public to anyone who has the link; v0.1 does not treat that as production.

A production write (`release create`, `rollout`, `halt`, `resume`, `complete`, `promote`) needs both `allowProduction: true` in `.gplay/policy.json` and `--allow-production`. The resulting user fraction also has to stay within `maxProductionUserFraction` (default 0.2) unless `--allow-production-fraction` repeats that fraction exactly, or `completed` for a full rollout. `--dry-run` does not skip the lock.

The closest `.gplay/policy.json` wins. Discovery walks parents and stops at the first directory that contains `.git`, at your home directory, and at the filesystem root. Unknown keys are an error.

## Edits

Any command that opens an edit invalidates every other open edit for that service account, including a concurrent CI job. gplay keeps an edit inside one process and deletes it unless that process committed it. `release status` and `gplay api GET` do not open an edit. `tracks list` and `tracks get` do, briefly.

`upload --dry-run` really uploads the bundle, validates, and then deletes the edit. It spends quota and bandwidth. The bundle does not stay available. Release commands commit unless `--dry-run` is set. `upload` commits only with `--commit`.

## Errors

A non-zero exit writes one JSON object as the last line of stderr. stdout is empty. `retryable` is always present. Optional fields are omitted.

| Exit | Code | When |
|---|---|---|
| 0 | | Success, including a dry-run whose validate passed |
| 1 | `unexpected` | 5xx, timeout, DNS (`retryable: true`), or another unclassified failure |
| 2 | `usage` or `config_invalid` | Flags, notes, or a policy/config file |
| 3 | `auth_failed` or `api_not_enabled` | Credentials, 401/403, or an API that is not enabled |
| 4 | `not_found` | Unknown track, missing release, HTTP 404 |
| 5 | `changes_in_review` or `edit_invalidated` | Commit blocked by a review, or the edit expired |
| 6 | `validation_failed` | Play or a local check rejected the upload or release |
| 7 | `rate_limited` | HTTP 429 or a quota 403 (`retryable: true`) |
| 8 | `policy_blocked` | Production lock, fraction cap, or cancel-in-review |
| 130 | `interrupted` | SIGINT or SIGTERM, after a best-effort edit delete |

`gplay schema` prints the machine-readable command and error contract.

## Manual end-to-end

There is no test app in this repository. `docs/e2e-checklist.md` is the unchecked list for a real Play app. Automated tests talk to a local fake and do not use the network.
