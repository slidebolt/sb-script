# Git Workflow for sb-script

This repository contains the Slidebolt Scripting service, which executes Lua-based automations and scripts. It produces a standalone binary.

## Dependencies
- **Internal:**
  - `sb-contract`: Core interfaces and shared structures.
  - `sb-domain`: Shared domain models for entities.
  - `sb-logging`: Logging implementation.
  - `sb-logging-sdk`: Logging client interfaces.
  - `sb-messenger-sdk`: Shared messaging interfaces.
  - `sb-runtime`: Core execution environment.
  - `sb-storage-sdk`: Shared storage interfaces.
  - `sb-storage-server`: Storage implementation.
- **External:** 
  - `github.com/yuin/gopher-lua`: Lua VM implementation in Go.
  - `github.com/cucumber/godog`: BDD testing framework.

## Build Process
- **Type:** Go Application (Service).
- **Consumption:** Run as the automation engine for Slidebolt.
- **Artifacts:** Produces a binary named `sb-script`.
- **Command:** `go build -o sb-script ./cmd/sb-script`
- **Validation:** 
  - Validated through unit tests: `go test -v ./...`
  - Validated through BDD tests: `go test -v ./cmd/sb-script`
  - Validated by successful compilation of the binary.

## Pre-requisites & Publishing
As the automation engine, `sb-script` must be updated whenever the core domain, logging, or messaging SDKs are changed.

**Before publishing:**
1. Determine current tag: `git tag | sort -V | tail -n 1`
2. Ensure all local tests pass: `go test -v ./...`
3. Ensure the binary builds: `go build -o sb-script ./cmd/sb-script`

**Publishing Order:**
1. Ensure all internal dependencies are tagged and pushed.
2. Update `sb-script/go.mod` to reference the latest tags.
3. Determine next semantic version for `sb-script` (e.g., `v1.0.4`).
4. Commit and push the changes to `main`.
5. Tag the repository: `git tag v1.0.4`.
6. Push the tag: `git push origin main v1.0.4`.

## Update Workflow & Verification
1. **Modify:** Update engine logic in `internal/engine/` or service logic in `server/`.
2. **Verify Local:**
   - Run `go mod tidy`.
   - Run `go test ./...`.
   - Run `go test ./cmd/sb-script` (BDD features).
   - Run `go build -o sb-script ./cmd/sb-script`.
3. **Commit:** Ensure the commit message clearly describes the scripting change.
4. **Tag & Push:** (Follow the Publishing Order above).
