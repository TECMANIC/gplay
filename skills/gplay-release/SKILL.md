# Release an Android app with gplay

Use this when shipping a build to a Play testing track with `github.com/TECMANIC/gplay`.

## Step 0

Run `gplay version`.

The output must start with `gplay (github.com/TECMANIC/gplay)`. If it does not, stop. A different project installed a binary named `gplay`.

## Before changing a track

1. `gplay auth doctor --package <pkg>`
2. `gplay tracks list --package <pkg>` and use a track name from that list. Do not invent `qa` or `production` if the list does not contain them.
3. Read `.gplay/policy.json` if the track is production or ends in `:production`. Both the file and `--allow-production` are required. Do not edit the policy file to get a command through.

## Internal testing

Prefer one edit:

```bash
gplay release create --package <pkg> --track internal --aab build/app/outputs/bundle/release/app-release.aab --notes-file notes/
```

Then:

```bash
gplay release status --package <pkg> --track internal
```

Repeat `release status` until `releaseLifecycleState` is `RELEASE_LIFECYCLE_STATE_PUBLISHED`. A commit that returned 0 is not the same thing as a published release.

`upload --commit` followed by `release create --version-code` is two commits. Use it only when the bundle is already uploaded.

## Closed track

```bash
gplay release create --package <pkg> --track <closed> --version-code <n> --fraction 0.1
gplay release rollout  --package <pkg> --track <closed> --fraction 0.5
gplay release halt     --package <pkg> --track <closed>
gplay release resume   --package <pkg> --track <closed>
gplay release complete --package <pkg> --track <closed>
```

`--fraction` must be greater than 0 and less than 1. Full rollout is `release complete`.

## When the command fails

- Exit 5, `changes_in_review`: a change is already in review. Do not pass `--cancel-in-review` unless the human asked for that and the policy file allows it.
- Exit 6: read `message` and `hint`. Do not commit again with the same version code.
- Exit 8: the production lock or the fraction cap refused the command. Do not weaken the policy file.
- Exit 130: the process was interrupted and the edit was discarded.

`--debug` logs the method, URL, status, and latency. It does not log bodies, tokens, or keys.
