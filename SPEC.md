# gplay v0.1 specification

Source of truth for v0.1. This is the original product spec with every must-fix from the 29 Oct 2026 review folded in, plus the review's exit-code table, error object, dry-run definitions, and milestone table. Those override the original spec wherever they conflict.

Module `github.com/TECMANIC/gplay`. Binary `gplay`. License MIT, Copyright (c) 2026 Tecmanic, LLC.

Not affiliated with Google. Two other projects also install a `gplay` binary (tamtom/play-console-cli, PollyGlot/google-play-cli). `gplay version` prints this module path so an agent can tell them apart.

## 1. Goal

Safety-first Google Play Developer API CLI for AI agents and CI, in the shape of `asc`: `gplay <noun> <verb>`, TTY-aware output, documented exit codes, no prompts.

The production lock is a mistake-guard, not a security boundary. An agent that can write the repo can write `.gplay/policy.json` and pass `--allow-production`. The real boundary is the Play permission on the service account. Recommended setup is two service accounts:

| Account | Play app-level permissions | Used for |
|---|---|---|
| Agent / CI | `CAN_VIEW_NON_FINANCIAL_DATA` ("View app information (read-only)") and `CAN_MANAGE_TRACK_APKS` ("Release to testing tracks") | Everything an agent runs |
| Human / production CD | also `CAN_MANAGE_PUBLIC_APKS` ("Release to production") | Production writes a human asked for |

Do not grant `CAN_MANAGE_PERMISSIONS` (Admin). New grants can take up to 24 hours to propagate; a 403 in that window is exit 3, and `auth doctor` says so. Whether a view-only account can `edits.insert` is unverified; if the manual E2E shows it cannot, reads that open an edit require `CAN_MANAGE_TRACK_APKS`.

## 2. Scope

v0.1 does: authenticate with a service account, list apps, list and get tracks, show release status, upload an AAB (optional ProGuard mapping), create a release (including upload and release in one edit), roll out, halt, resume, complete, and promote.

Cut from v0.1:

- `gplay api` writes. `api` is GET-only. No `--confirm` write passthrough.
- `gplay edits validate` and `gplay edits discard`. An edit cannot be reused across processes.
- `gplay apps get`. `AppDetails` is only contact fields and costs an edit.
- pubspec.yaml parsing. The uploaded bundle's `versionCode` is authoritative. The default AAB path remains `build/app/outputs/bundle/release/app-release.aab`.
- Multi-profile `--profile`. One config file, one credential path.
- `/llms.txt`. README plus `gplay schema` are the agent surface.
- `nativeCode` deobfuscation upload (v0.2). `--mapping` is `proguard` only.
- Keychain storage (v0.2).
- Reviews, listings, images, testers, vitals writes, subscriptions (v0.2+).
- `tracks.patch`. Updates go through `edits.tracks.update` only.

`beta` (open testing) is installable by anyone who has the link. It is not covered by the production lock in v0.1. A future `lockedTracks` policy key may extend the lock; unknown policy keys are rejected, so that key is not accepted yet.

## 3. Stack

- Go 1.26 (`go 1.26.0` in `go.mod`; CI uses the current 1.26 patch).
- `google.golang.org/api` v0.310.0 (`androidpublisher/v3`, `playdeveloperreporting/v1beta1`).
- `github.com/spf13/cobra`.
- `text/tabwriter` for table output.
- No viper. No `golang.org/x/oauth2/google`. Credentials use `option.WithAuthCredentialsJSON(option.ServiceAccount, …)` and, for the explicit ADC opt-in only, the `cloud.google.com/go/auth` ADC detector.

Scopes requested for service-account credentials: `https://www.googleapis.com/auth/androidpublisher` and `https://www.googleapis.com/auth/playdeveloperreporting`.

## 4. Global rules

- No prompts. Missing input is exit 2 with a hint. No command reads stdin.
- No telemetry, update checks, or star prompts.
- Fail on the first error. No silent fallback and no automatic retry of `edits.commit`. In particular, never retry a commit with `changesNotSentForReview=true` after Google asks for it.
- Every API call goes through one `http.Client`. Do not construct a second client. The transport allowlist is `androidpublisher.googleapis.com`, `playdeveloperreporting.googleapis.com`, `oauth2.googleapis.com`, plus the test endpoint host when one is injected. Anything else fails before the dial.
- Timeouts are context deadlines on that client (the client itself has no `Timeout`): 30 minutes for a call that uploads media, 60 seconds otherwise.
- On SIGINT or SIGTERM, best-effort `edits.delete` for an edit this process opened and did not commit, then exit 130.
- Idempotent GETs retry 429 and 5xx at most 3 times with capped backoff (200 ms, 400 ms, 800 ms, plus a little jitter). POST, PUT, PATCH, and DELETE are not retried. Resumable upload chunk retries inside `google.golang.org/api` stay as the library implements them.
- `--debug` logs method, URL including query, status, latency, and response headers `x-goog-request-id` and `x-guploader-uploadid`. It also logs `expiryTimeSeconds` from `edits.insert`. It never logs request or response bodies.
- Warnings are plain `warning: …` lines on stderr. They are not part of the JSON payload.
- stdout is empty when the exit code is non-zero. The error object is the last line of stderr, on a TTY or not.

## 5. Commands

Root help line: `gplay — safety-first Google Play Developer API CLI for AI agents (TECMANIC)`.

Global flags: `--debug`, `--output json|table`, `--credentials <path|adc>`.

JSON is the default when stdout is not a TTY. `--output table` forces a table; `--output json` forces JSON.

### 5.1 `gplay version`

Prints one line:

```text
gplay (github.com/TECMANIC/gplay) v0.1.0 <commit> <date>
```

No credentials. Exit 0.

### 5.2 `gplay schema [command]`

Prints a JSON document describing the tool, commands, flags, exit codes, error-object fields, and success bodies. `"tool"` is `"TECMANIC/gplay"`. No credentials. With an argument, print only that command (unknown command → exit 2).

### 5.3 `gplay auth login --credentials <path>`

Validate the file locally (service-account rules in §6). Store its absolute path in the config file. Do not copy the key, do not call the network, do not print the key.

### 5.4 `gplay auth status [--validate]`

Print which source was used and the identity. `--validate` performs one cheap read that does not open an edit: Reporting `apps.search` with `pageSize=1`. A failing validate is an API error, not a fall-through to another credential source.

Success body:

```json
{"source":"flag","client_email":"app@proj.iam.gserviceaccount.com","project_id":"proj","key_fingerprint":"sha256:<hex>","validated":false}
```

`source` is `flag`, `env`, `env_json`, `profile`, or `adc`. `key_fingerprint` is SHA-256 of the `private_key` PEM and is omitted for ADC. `client_email` and `project_id` are omitted when that source has none. Never print `private_key`, `private_key_id`, or the raw JSON.

### 5.5 `gplay auth doctor [--package <pkg>]`

Findings and probes, then exit 0 if every probe that ran succeeded, otherwise the probe's exit code (auth, api_not_enabled, and so on).

Probes:

1. Key-file mode, when the source is a file. On non-Windows, mode with any group/other bits (`mode & 0077 != 0`) is a warning finding, not a failure. CI mounts are often 0644. Windows skips the check. `auth doctor` repeats the finding inside the JSON `findings` array; other commands emit only the stderr `warning:` line.
2. Clock skew from the `Date` header of the Reporting probe. Absolute skew above 30 seconds is a warning finding. The command still succeeds.
3. Reporting `apps.search` (API enabled, token works).
4. When `--package` is set: `edits.insert` followed by `edits.delete` on that package (app access). When `--package` is omitted, the edit probe is reported as skipped with an explicit reason. Omitting it is not a silent success of that probe.

### 5.6 `gplay apps list`

Reporting `GET v1beta1/apps:search`, `pageSize` 1000, follow `pageToken` until exhausted. Each app object keeps Google's `name`, `packageName`, and `displayName`.

This needs the playdeveloperreporting scope and the Reporting API enabled on the GCP project (separate from androidpublisher). `SERVICE_DISABLED` / `accessNotConfigured` is exit 3, code `api_not_enabled`, with the API enable URL in `hint`.

### 5.7 `gplay tracks list --package <pkg>` and `gplay tracks get --package <pkg> --track <name>`

Open an edit, call `edits.tracks.list` or `.get`, delete the edit in a `defer`. There is no list-tracks API that avoids an edit.

Any command that opens an edit invalidates another open edit belonging to the same service account, including a concurrent CI job. Document this in the README. Keep the edit inside one process. Always delete it unless this process successfully committed it.

`tracks get` resolves `--track` against `tracks.list` in the same edit. An unknown name is exit 4, code `not_found`, and `hint` lists the names Google returned. Do not hardcode a track table. The internal testing track is whatever `tracks.list` returns (shipping tools see `internal`; the written guide says `qa`). Closed tracks have custom names. Form-factor tracks look like `<prefix>:<name>` with prefixes `wear`, `automotive`, `tv`, `android_xr`, `google_play_games_pc`.

### 5.8 `gplay release status --package <pkg> --track <name>`

`GET androidpublisher/v3/applications/{pkg}/tracks/{track}/releases`. No edit. This is how an agent tells "commit returned 200" apart from "the release is live".

Response items keep Google's names: `releaseName`, `releaseLifecycleState` (`DRAFT`, `NOT_SENT_FOR_REVIEW`, `IN_REVIEW`, `APPROVED_NOT_PUBLISHED`, `NOT_APPROVED`, `PUBLISHED`), `activeArtifacts`. The API returns at most 20 releases.

### 5.9 `gplay upload`

```text
gplay upload --package <pkg> [--aab <path>] [--mapping <path>] [--commit]
             [--dry-run] [--changes-not-sent-for-review] [--cancel-in-review]
```

`--aab` defaults to `build/app/outputs/bundle/release/app-release.aab`.

Sequence, one edit: `insert` → `bundles.upload` → optional deobfuscation → `validate` → `commit` or `delete`.

- `--commit` commits after a successful validate. Without `--commit`, validate and delete. `--dry-run` also validates and deletes (see §8). `--commit` and `--dry-run` together are exit 2.
- Media upload uses `call.Media(r, googleapi.ChunkSize(8<<20), googleapi.ContentType("application/octet-stream"))`. Files larger than the chunk size use the resumable protocol. `ResumableMedia` is not used.
- Compare `Bundle.sha256` with the SHA-256 of the local file. Mismatch is exit 6, code `validation_failed`, and the edit is deleted. Do not commit.
- `--mapping` uploads a ProGuard file to `edits/{editId}/apks/{versionCode}/deobfuscationFiles/proguard`, where `versionCode` is the value returned by the bundle upload. Reject a mapping larger than 2 000 MiB and an AAB larger than 50 GiB before calling the API (exit 2).
- Commit query parameters follow §7.3.

Success body (no `track` field, because upload does not touch a track):

```json
{"dry_run":false,"committed":true,"edit_id":"123","version_code":42,"sha256":"<hex>","plan":[{"method":"POST","path":"/androidpublisher/v3/applications/com.x/edits"}]}
```

### 5.10 `gplay release create`

```text
gplay release create --package <pkg> --track <name>
                     [--version-code <n> | --aab <path>] [--mapping <path>]
                     [--status draft|inProgress|halted|completed]
                     [--fraction <f>] [--release-name <name>]
                     [--notes <text>] [--notes-file <path>] [--language <bcp47>]
                     [--dry-run] [--changes-not-sent-for-review] [--cancel-in-review]
                     [--allow-production] [--allow-production-fraction <f|completed>]
```

Primary path for agents: `--aab` uploads and attaches the new bundle in the same edit. `--version-code` alone attaches a bundle that is already uploaded. Passing both is allowed only when they are the same number; a mismatch is exit 2. Passing neither is exit 2.

Sequence: policy pre-flight (§7) → `insert` → `tracks.list` (resolve the name) → optional upload and mapping → `tracks.update` → `validate` → `commit` or, on `--dry-run` or any failure, `delete`.

`tracks.update` sends `Track{track, releases:[the new release only]}`. A new `completed` release supersedes the previous one. That is the API's documented behaviour; this command does not send the previous releases back.

Release object:

- `versionCodes` are JSON strings (int64 format), never numbers.
- `name` comes from `--release-name`. When omitted, the name is the decimal `versionCode`. The bundle upload response has no versionName, and v0.1 does not parse the AAB.
- `releaseNotes` come from `--notes` / `--notes-file` (§7.6). Zero notes is allowed.
- Status rules are §7.2. Testing tracks default to `completed`. Production has no default: `--fraction` or `--status` is required (exit 2 if both are missing).

### 5.11 `gplay release rollout|halt|resume|complete`

```text
gplay release rollout  --package <pkg> --track <name> --fraction <f> [--version-code <n>] [policy and commit flags]
gplay release halt     --package <pkg> --track <name> [--version-code <n>] [policy and commit flags]
gplay release resume   --package <pkg> --track <name> [--version-code <n>] [policy and commit flags]
gplay release complete --package <pkg> --track <name> [--version-code <n>] [policy and commit flags]
```

These four share one read-modify-write helper. They never build a release from flags alone.

Sequence: policy pre-flight for everything that can be known without the network → `insert` → `tracks.list` → `tracks.get` → select the release → re-run the policy decision on the resulting state → `tracks.update` with that one release → `validate` → `commit` or `delete`.

Selection: if `--version-code` is set, the release whose `versionCodes` contain that code. Otherwise the single release whose status is `inProgress` or `halted`. Zero matches is exit 4. More than one match is exit 2.

Then change only `status` and `userFraction`, and send the server's `versionCodes`, `name`, `releaseNotes`, `countryTargeting`, and `inAppUpdatePriority` back unchanged:

| Command | Required current status | Result |
|---|---|---|
| `rollout` | `draft`, `inProgress`, `halted`, or `completed` | `inProgress` and `userFraction` = `--fraction` |
| `halt` | `inProgress`, or `completed` when `--version-code` selected it | `halted`, same `userFraction` |
| `resume` | `halted` | `inProgress`, same `userFraction` |
| `complete` | `inProgress` or `halted` | `completed`, `userFraction` omitted |

A wrong current status is exit 2. `halt` of a `completed` production release reverts users to the previous completed release; it is still a production write and needs the lock.

`--fraction` is required for `rollout` and rejected for the other three (exit 2). The legal range is the open interval `(0, 1)`. `1` and `1.0` are rejected. Full rollout is `release complete`, which sends `status: completed` and no `userFraction`.

### 5.12 `gplay release promote --package <pkg> --from <track> --to <track>`

Same edit, same policy path as `release create` applied to `--to`. Read the source release (same selection rules, `--version-code` optional), then `tracks.update` on the destination with a new release that copies `versionCodes` and `releaseNotes`. The source track is not modified. The API has no move.

Destination status follows §7.2 (`--status` / `--fraction` as on `release create`). The copied notes can be replaced with `--notes` / `--notes-file`. `--release-name` overrides the source name; otherwise the source name is kept.

### 5.13 `gplay api GET <path>`

GET under the androidpublisher v3 base. The path is a URL path plus an optional query, for example `/applications/com.x/tracks/internal/releases`. Any other method is exit 2. The response body is written to stdout unchanged (it is already JSON) when `--output json` is in effect, which is the non-TTY default. `--output table` is exit 2 for this command. Non-2xx responses use the normal error mapper.

## 6. Credentials

Resolution order. The first *configured* source is the only source. If it is missing, unreadable, or invalid, exit 3, code `auth_failed`. Never continue to the next source.

1. `--credentials` when the flag is present. Value `adc` selects ADC. Any other value is a file path.
2. `GPLAY_CREDENTIALS` when the variable is set in the environment, including set-but-empty (empty is exit 3). Value `adc` selects ADC. Any other value is a file path.
3. `GPLAY_CREDENTIALS_JSON` when set: the raw JSON contents. Cap 64 KiB; over the cap is exit 3 and the error text does not contain any part of the value. Do not re-export the variable.
4. Profile file from `os.UserConfigDir()/gplay/config.json`, when that file exists.

ADC is not implicit. A dev machine's `gcloud auth application-default login` identity is often a Play Admin; using it must be a conscious `--credentials adc` or `GPLAY_CREDENTIALS=adc`.

These names were checked against PollyGlot/google-play-cli, which uses `GPLAY_SERVICE_ACCOUNT`, `GPLAY_ACCOUNT`, and `GPLAY_READONLY`. v0.1 does not read those variables.

### 6.1 Service-account JSON

Accepted only when all of the following hold:

- `type` is exactly `service_account`.
- `token_uri` is exactly `https://oauth2.googleapis.com/token`.
- `client_email`, `private_key`, and `project_id` are non-empty.

`external_account` and `impersonated_service_account` are exit 3. A credential file must not be able to start an executable credential source or call an attacker-controlled URL. The bytes are then passed to `option.WithAuthCredentialsJSON(option.ServiceAccount, raw)`. Deprecated `option.WithCredentialsJSON` and `option.WithCredentialsFile` are not used.

The ADC opt-in is the one path that may use workload identity federation, because the user asked for ADC by name. ADC is not parsed as a service-account file.

### 6.2 Config file

```json
{"tool":"TECMANIC/gplay","credentials":"/absolute/path.json"}
```

Directory mode `0700`, file mode `0600`. `credentials` is absolute. `auth login` resolves a relative path before writing.

Refuse to read a config whose `tool` field is missing or not `TECMANIC/gplay` (exit 2, code `config_invalid`, hint to fix or remove the file). A foreign `gplay` config must not be picked up. Unknown keys are exit 2. There is no environment variable that overrides the policy file or the `tool` check.

### 6.3 Filesystem warnings

When the active credential is a file and `mode & 0077 != 0`, print `warning: credential file is readable by group or others: <path>` on stderr. Do not include that warning inside the success JSON. Skip the check on Windows.

## 7. Safety policy

### 7.1 Discovery and schema

Look for `.gplay/policy.json` in the working directory, then in each parent, including the first directory that contains a `.git` entry, and stop there. Also stop at `$HOME` and at the filesystem root. The closest file wins. No environment variable overrides the path or the contents.

Strict JSON object. Unknown keys are exit 2, code `config_invalid`, not exit 8. A missing file means the defaults below. A file that exists and is not valid JSON is exit 2.

```json
{
  "allowProduction": false,
  "allowCancelInReview": false,
  "maxProductionUserFraction": 0.2
}
```

| Key | Meaning | Default |
|---|---|---|
| `allowProduction` | Allow writes whose target track is production, and only when `--allow-production` is also set | `false` |
| `allowCancelInReview` | Allow `--cancel-in-review` | `false` |
| `maxProductionUserFraction` | Cap for the resulting production fraction. Must be in `(0, 1]` when set | `0.2` |

The decision function is pure: inputs are the operation, the target track, the resulting status, the resulting fraction when known, the flags, and the policy. Output is allow, or deny with exit code and hint. It does no I/O.

### 7.2 Production lock

A track is production when, after trimming whitespace and lower-casing, it is `production` or it matches `*:production` (at least one character before a single colon). `wear:production` and `TV:Production` are production. `internal`, `beta`, `alpha`, and `qa` are not.

The lock covers `release create`, `rollout`, `halt`, `resume`, `complete`, and `promote` (the `--to` track). It is evaluated before `edits.insert`, with zero network calls. `--dry-run` that hits the lock exits 8; it does not return 0.

Both `--allow-production` and `allowProduction: true` are required. Either alone is exit 8, code `policy_blocked`.

Production `release create` and production `promote` have no default status. The user must pass `--fraction` (which means `inProgress`) or `--status`. Testing tracks default to `completed`.

Status and fraction combinations:

| Input | Result | Reject |
|---|---|---|
| `--fraction F` and no `--status` | `inProgress`, fraction `F` | `F` not in `(0, 1)` |
| `--status inProgress` | `inProgress` | missing `--fraction`, or fraction not in `(0, 1)` |
| `--status halted` | `halted` | missing `--fraction`, or fraction not in `(0, 1)` |
| `--status completed` | `completed`, no fraction | `--fraction` also set |
| `--status draft` | `draft`, no fraction | `--fraction` also set |
| neither, testing track | `completed` | |
| neither, production track | exit 2 | |

Draft-app failures from Google ("only releases with status draft may be created on a draft app", matched on the message, because it is not a discovery-doc reason) are exit 6 with hint `--status draft`.

### 7.3 Cap, commit behaviour, cancel

One function applies the cap to the *resulting* release state on every production write, including `halt`, `resume`, and `complete`.

- Resulting `userFraction` greater than `maxProductionUserFraction` needs `--allow-production-fraction` equal to that fraction.
- Resulting `completed` needs `--allow-production-fraction completed`, unless the cap is `1` (completed is not above the cap).
- A fraction less than or equal to the cap does not need the flag.
- A flag that does not equal the resulting fraction (or `completed` for a completed result) is exit 8 even if the number would have been high enough. The flag is an exact acknowledgement, not a ceiling.

`resume` and `halt` learn the fraction from `tracks.get`. The production flag-and-policy check still happens before `edits.insert`. The cap is applied after the read and before `tracks.update`. A cap denial deletes the edit and makes no further calls. `rollout`, `complete`, `create`, and `promote` know the resulting state from flags, so their cap check is pre-flight too.

Every commit sends `changesInReviewBehavior=ERROR_IF_IN_REVIEW` unless `--cancel-in-review` is set, in which case it sends `CANCEL_IN_REVIEW_AND_SUBMIT`. `--cancel-in-review` also requires `allowCancelInReview: true`, checked pre-flight, exit 8 otherwise. The in-review change might be a production review, and gplay cannot see that before the commit, so the policy key is the gate. Do not also require `--allow-production` for an internal push.

`--changes-not-sent-for-review` sends `changesNotSentForReview=true`. It is never set otherwise, and never set automatically after an error.

HTTP 400 on commit with `status FAILED_PRECONDITION` and `ErrorInfo.reason CHANGES_ALREADY_IN_REVIEW` (or the legacy `errors[].reason`) is exit 5, code `changes_in_review`. Hint: wait for the review to finish, or pass `--cancel-in-review` with `allowCancelInReview` set. That error does not invalidate the edit. Delete the edit once in the `defer`; do not delete it twice, and do not commit again.

Google's message that changes cannot be sent for review automatically, which tells the caller to set `changesNotSentForReview`, is exit 6, code `validation_failed`, with a hint to add the flag. Do not retry.

Other edit-invalidated or edit-expired failures map to exit 5, code `edit_invalidated`, by reason or message (`edit` together with `invalid` or `expired`, and reasons containing `EDIT_EXPIRED` or `EDIT_NOT_FOUND` or similar). Do not key this only off HTTP 409. Field reports include 400 and 404.

Internal-track commits do not need review, but the default `ERROR_IF_IN_REVIEW` means `release create --track internal` fails with exit 5 while any change is in review, instead of cancelling that review. That is intentional.

### 7.4 Precedence

When several codes could apply, decide in this order and return the first: usage (2) → config (2) → policy (8) → auth (3) → the API error table. Example: production `release create` with neither `--fraction` nor `--status` is exit 2 even if `--allow-production` is also missing.

### 7.5 Notes

`--language` defaults to `en-US` and applies to `--notes` and to a single notes file.

`--notes` is one note in `--language`, at most 500 Unicode code points, valid UTF-8.

`--notes-file` is either:

- a regular file, in `--language`, or
- a directory whose children are regular files named `<bcp47>.txt` and nothing else. Subdirectories, unexpected names, and extra files are exit 2.

For every file: `filepath.Abs` and `filepath.EvalSymlinks` must stay inside the directory that was named (a single file must stay itself). Reject `..`. Valid UTF-8. At most 500 code points. BCP-47 tags match `^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`.

### 7.6 Upload integrity and common 400s

Map HTTP 400 from `tracks.update`, `validate`, and upload to exit 6, code `validation_failed`, and pass `google_reason` and the message through. Add a hint when the message matches:

| Message contains | Hint |
|---|---|
| `APK signature is invalid` or `upload certificate` | The AAB is signed with a different upload key than Play Console has on file. |
| `version code` and `already` | Bump `versionCode`. It must be higher than every code already uploaded. |
| `changesNotSentForReview` | Re-run with `--changes-not-sent-for-review`. |
| `draft` and `status` | Re-run with `--status draft`. The Play Console tracks guide documents draft releases. |
| release notes too long | Shorten the note to 500 characters. Client-side checks should make this rare. |

## 8. Dry-run

`--dry-run` is not "no network". It performs the edit, including the upload when the command uploads, then `tracks.update` when the command updates a track, then `validate`, then `delete`. Exit 0 only when validate passed and policy allowed the command. Exit 8 when policy blocks it, and in that case the fake server sees no request at all.

`upload --dry-run` really uploads the AAB and then discards the edit. Help text says that. It has real quota and bandwidth cost. The bundle does not remain available, because the edit is deleted rather than committed.

`--dry-run` combined with `--commit` is exit 2. Release commands commit unless `--dry-run` is set; they do not have a separate `--commit` flag. `upload` commits only when `--commit` is set.

Write success body:

```json
{
  "dry_run": false,
  "committed": true,
  "edit_id": "123",
  "track": {},
  "plan": [{"method": "PUT", "path": "/androidpublisher/v3/applications/com.x/edits/123/tracks/internal"}]
}
```

`track` is the `Track` object returned by `tracks.update` or, for a read-modify-write that was discarded, the body we were about to send. Omit `track` when the command did not update a track. Keep Google's field names inside `track`. `plan` lists the methods and paths actually attempted, in order. Omit empty optional fields. Never send JSON `null` for an absent optional field.

## 9. Errors

One JSON object, last line of stderr, always:

```json
{"code":"policy_blocked","exit_code":8,"message":"…","hint":"…","retryable":false,"http_status":400,"google_status":"FAILED_PRECONDITION","google_reason":"CHANGES_ALREADY_IN_REVIEW","edit_id":"…","request_id":"…"}
```

`code` is a stable snake_case value advertised by `gplay schema`. `exit_code` is repeated so an agent does not need a side table. Optional fields are omitted, never null. `retryable` is always present.

| Exit | `code` | When | `retryable` |
|---|---|---|---|
| 0 | | Success, including a dry-run whose validate passed | |
| 1 | `unexpected` | 5xx, timeout, DNS, other unclassified failures, local hash mismatch after a transport error | `true` for 5xx, timeout, and DNS; `false` otherwise |
| 2 | `usage` | Unknown command, missing or conflicting flags, bad fraction, bad notes path, `--commit` with `--dry-run` | `false` |
| 2 | `config_invalid` | Policy or config file unreadable, unknown keys, wrong `tool`, bad `maxProductionUserFraction` | `false` |
| 3 | `auth_failed` | Unconfigured credentials, invalid service-account JSON, 401, 403 that is not quota and not API-disabled, including a grant that has not propagated | `false` |
| 3 | `api_not_enabled` | 403 `SERVICE_DISABLED` or `accessNotConfigured` | `false` |
| 4 | `not_found` | HTTP 404, unknown track, release version code not on the track | `false` |
| 5 | `changes_in_review` | Commit rejected with `CHANGES_ALREADY_IN_REVIEW` | `false` |
| 5 | `edit_invalidated` | Edit expired or invalidated, matched on reason or message, any of 400, 404, 409 | `false` |
| 6 | `validation_failed` | HTTP 400 from update, validate, or upload; draft-app rule; notes rejected by the API; local SHA-256 mismatch | `false` |
| 7 | `rate_limited` | HTTP 429, or 403 `quotaExceeded` / `rateLimitExceeded` | `true` |
| 8 | `policy_blocked` | Production lock, cap acknowledgement, or `--cancel-in-review` without the policy key | `false` |
| 130 | `interrupted` | SIGINT or SIGTERM after best-effort edit delete | `false` |

Read `googleapi.Error.Code`, `Message`, `Errors[].Reason`, and `Details` (`google.rpc.ErrorInfo.reason`). Request id comes from `x-goog-request-id` when present.

`gplay nope` exits 2 and writes exactly one JSON line on stderr.

## 10. Redaction

Redact, in debug logs and in error strings, the keys `private_key`, `private_key_id`, `client_secret`, `refresh_token`, `access_token`, `id_token`, the `Authorization` header, and any value containing `-----BEGIN` or matching `ya29.`.

Test: run every command with a fake service-account key under `--debug`, capture stdout and stderr, and assert the output has no `PRIVATE KEY`, no `ya29.`, and no `private_key_id` value. The fake server's requests still carry `Authorization`. The debug log of those requests does not.

## 11. Testing

Tests do not touch the network. Use `httptest` plus `option.WithEndpoint` and a static token source on the single allowlisted client.

The fake server asserts:

- Call order `insert` → upload → optional deobfuscation → `tracks.update` → `validate` → `commit` or `delete`, and `delete` on every failure path that did not commit.
- Resumable upload (`uploadType=resumable`, `X-Upload-Content-Length`, `308` then final body) for a payload larger than the chunk size, and a single request for a smaller one. Tests may pass a smaller chunk size than the 8 MiB production default; a separate assertion locks the default at `8 << 20`.
- Every `:commit` carries `changesInReviewBehavior=ERROR_IF_IN_REVIEW` unless `--cancel-in-review`, and `changesNotSentForReview` only when that flag is set.
- A 400 from validate is not followed by commit.
- 400 `CHANGES_ALREADY_IN_REVIEW` returns exit 5 and the edit is deleted once.
- A production denial (no flag, or flag without policy) performs zero requests, including under `--dry-run`.
- No request leaves the allowlist. `Authorization` is present on API calls and absent from logs.

Also: policy table tests for `wear:production`, the cap on resume and complete, and dry-run → 8; 100% block coverage of the policy decision function; a contract test that every error path emits exactly one parseable JSON line whose `exit_code` matches the process status; `go test -race`; fuzz tests for the policy JSON parser and the notes parser.

Golden fixtures from a real Play app are recorded only during the manual E2E. Do not invent them.

### 11.1 Manual E2E

Not run by the implementation agent. There is no test app in this environment. `docs/e2e-checklist.md` lists the review's ordered checks, all unchecked, for Carlos. No tag is cut as part of that checklist.

The checklist covers: `auth doctor`; `apps list` and `tracks list` (record real track names); `upload --dry-run` and `upload --commit`; internal `release create` until `release status` shows `PUBLISHED`; closed-track fraction, halt, resume, complete; promote; production negatives (8 without flag, 8 with flag and no policy, 3 from Google when both are set, proving the permission boundary); duplicate versionCode → 6; bad key → 3; unknown track → 4; in-review conflict → 5 without cancelling the review, then one `--cancel-in-review` that does cancel; a `--debug` grep for `PRIVATE KEY`, `ya29.`, and `private_key_id`; a security pass over §6 and §10. No production write is successful at any point.

## 12. Repository and release engineering

- `CGO_ENABLED=0`, `-trimpath`, ldflags `-s -w -X main.version=… -X main.commit=… -X main.date=…`.
- GoReleaser project name `gplay`, archives `gplay_<ver>_<os>_<arch>`, sboms via syft (SPDX JSON). Homebrew only through a Tecmanic tap (`brew install tecmanic/tap/gplay`), never Homebrew core.
- Cosign keyless (GitHub OIDC) over `checksums.txt`, publishing `.sig` and `.pem`. README shows `cosign verify-blob` with `--certificate-identity-regexp` and `--certificate-oidc-issuer https://token.actions.githubusercontent.com`.
- GitHub artifact attestations (`actions/attest-build-provenance`) on the release workflow.
- Actions pinned by full SHA. Release job permissions are `contents: write` and `id-token: write` only, and the workflow runs only on tags. Dependabot for `gomod` and `github-actions`. `govulncheck` in CI and in the release job. `go.sum` committed.
- CI on every PR and on main: `go test -race ./...`, `go vet`, `staticcheck`, `govulncheck`, `gosec`.
- A weekly workflow diffs the androidpublisher v3 and playdeveloperreporting v1beta1 discovery documents against committed revision pins.
- Second install path, documented not published from this repo's CI: `go install github.com/TECMANIC/gplay/cmd/gplay@latest`.

v0.1 implementation does not create tags, GitHub releases, packages, or Actions secrets, and does not change repository settings.

## 13. Milestones

Each milestone is one commit on the feature branch, with `go test ./...` and `go vet ./...` passing. Tests do not use the network.

| Milestone | Deliverable | Done when |
|---|---|---|
| M0 | `go.mod` (go 1.26), cobra root, `version`, `schema`, output layer, errors package with the exit table and JSON writer, CI | `gplay version` prints the module path; `gplay nope` exits 2 with one JSON line; the workflow file is present |
| M1 | `auth`: resolution order, service-account-only validation, redaction, `auth status`, `auth doctor` | Unit tests with a fake key pass, including the redaction grep; an `external_account` JSON is exit 3. `auth status --validate` against a real service account is part of the manual E2E, not this milestone |
| M2 | `policy` package: operation, track, resulting status and fraction, flags, file → allow or deny | Table tests cover production form-factor tracks, halt, the cap on resume and complete, dry-run → 8, and discovery rules. The decision function has 100% block coverage |
| M3 | `apps list`, `tracks list`, `tracks get`, `release status` | Fake server asserts `insert` → list → `delete` for track reads, and no edit for `release status` |
| M4 | `upload`: chunked upload, optional `--mapping`, SHA-256, validate, commit or delete, `--dry-run` | Fake server asserts the sequence, both upload styles, and commit query parameters |
| M5 | `release create` with `--aab`, `--status`, `--fraction`, notes, commit flags | Fake server covers the one-edit path and maps 400s to exits 5 and 6 |
| M6 | `rollout`, `halt`, `resume`, `complete`, `promote` on the shared helper plus policy | Production negatives exit 8 with zero requests. Read-modify-write sends the server's `versionCodes` |
| M7 | `api GET`, final `schema`, `AGENTS.md`, `skills/gplay-release/SKILL.md`, README | README has the permission table, the two-account pattern, the "lock is not a security boundary" statement, name disambiguation, and the cosign verify command. Skill step 0 checks `gplay version` |
| M8 | GoReleaser, cosign keyless workflow, SBOM, attestations, pinned actions, Dependabot, weekly discovery diff | `goreleaser` config is valid. Workflows do not run a real release from this work |
| M9 | Manual E2E and security sign-off | Unchecked checklist committed. No tag |

## 14. Decisions recorded here

These are fixed so the implementation does not grow a second interpretation.

1. `--release-name` defaults to the decimal version code, not a version name parsed out of the AAB.
2. `--language` defaults to `en-US`.
3. Credential environment variables are `GPLAY_CREDENTIALS` and `GPLAY_CREDENTIALS_JSON`, not PollyGlot's `GPLAY_SERVICE_ACCOUNT`.
4. SIGTERM is handled like SIGINT (exit 130). CI cancels with SIGTERM.
5. Clock-skew warning threshold is 30 seconds.
6. `key_fingerprint` is the hex SHA-256 of the `private_key` PEM, prefixed with `sha256:`.
7. Tests may shrink the upload chunk size. Production code uses 8 MiB.
8. `auth status --validate` and the doctor Reporting probe use `apps.search`, never an edit.
9. Local SHA-256 mismatch is exit 6, `validation_failed`.
10. `api GET` refuses `--output table`.
