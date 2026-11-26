---
name: nina-persona-build
description: Build, test and pin Nina the persona (nina-live) — her Go binary, the Metis composition image (catalog/domains, image.golden), the fsttm-live voice module pin, and scripts/persona-build.sh. Use when building or testing nina-live, when a rules change alters the image hash, when bumping fsttm-live, when "./build.sh" fails its image check, or when Nina will not start after a build ("2 images in build/"). Not for deploying to a machine's Home Assistant (that is acasa's acasa-machine-update).
---

# Building Nina the persona (nina-live)

nina-live is the **persona**: her process (`cmd/nina`), her event log, her rules (a Metis composition
in `catalog/domains/`), her tools. It knows nothing about a particular home: acasa adapts it to a home
(Home Assistant, dashboards, kiosk, services). Keep that line: nothing here may call Home Assistant's
deployment or restart a service.

## The pieces

| piece | where | built by |
|---|---|---|
| her binary `bin/nina` (+ `bin/nina-whatsapp`) | `cmd/nina`, the packages | `./build.sh` (cgo: metis-lang's `libmetis_rt`) |
| the composition image | `catalog/domains/build/home.image.v-<hash>` (not committed) | `metisc build .` inside `./build.sh` |
| the image's pinned hash | `catalog/domains/image.golden` (committed) | you, when a rules change is meant |
| her voice | `github.com/ibnHatab/fsttm-live` (a Go module, pinned in go.mod) | `go get …@main` |
| the radar | fsttm-live `cmd/radar` | `scripts/persona-build.sh` |

Repos sit side by side under one root (`~/repo`): nina-live, fsttm-live, `perotheia-packages/metis-*`.
`METIS_LANG_DIR` (default `../perotheia-packages/metis-lang`) holds the runtime and `src/metisc`.

## Commands

```sh
./build.sh                     # image (checked against image.golden) → bin/nina, bin/nina-whatsapp
./build.sh test -count=1 ./... # every package; sets METISC so core's rule tests really run
scripts/persona-build.sh       # the persona whole: metisc if missing, ./build.sh, the radar
scripts/persona-build.sh --pull  # pull the persona's repos first (fast-forward only)
```

Always read the **real exit code**: `./build.sh test ... > out 2>&1; echo rc=$?` then grep `FAIL`.
A pipe (`| tail`, `| grep`) hides failures, and `go vet X && go test Y | grep` silently skips the
tests when vet fails. Core's rig tests (`core/core_test.go`) `t.Skip` without `METISC`; `./build.sh
test` exports it — run them with `-v` once after a rules change to see PASS, not SKIP.

## A rules change (llp, domain.yaml) changes the image hash

1. `./build.sh` prints `the composition's image (v-NEW) is not image.golden's` and removes v-NEW again
   (two images in `build/` stop Nina from starting: "no image.json, and 2 images in build/").
2. If the change is meant: `echo NEW > catalog/domains/image.golden`, `./build.sh` again. The build
   then removes the superseded image itself.
3. Commit `image.golden` with the rules. `intent.yaml` changes do **not** change the hash; a domain's
   `line:` in `domain.yaml` does (it is the tool's description).

## Bumping fsttm-live (her voice)

```sh
cd ../fsttm-live && go vet ./internal/... ./pkg/... ; go test -count=1 ./internal/... ./pkg/...   # each separately
# merge fsttm-live to main and push, then here:
GOPROXY=direct go get github.com/ibnHatab/fsttm-live@main && go mod tidy
./build.sh test -count=1 ./... && ./build.sh
```

For a quick live check before merging fsttm-live: `go mod edit -replace
github.com/ibnHatab/fsttm-live=../fsttm-live`, build, test — and `go mod edit -dropreplace` before
committing (never commit a replace).

## Toolchain

`go.mod` asks for a Go newer than some machines have installed; leave `GOTOOLCHAIN` alone (auto fetches
the release). Forcing `GOTOOLCHAIN=local` broke a desktop with Go 1.22. `GOFLAGS=-mod=mod` is set by
the scripts.

## Workflow rules

- A branch per fix (`fix/…`, `feat/…`, `deps/…`), tests green, `git merge --ff-only` to main, push.
- Then deploy through acasa (`acasa/install/update.sh`), never by copying binaries.
- After a restart, check the service is `active` a few seconds later: a crash loop shows as
  `activating (auto-restart)`; run the binary by hand for its first error line
  (`timeout 8 bin/nina -config CONFIG`), since a Pi may have no user journal.
