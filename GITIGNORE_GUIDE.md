# Git Ignore Guide

This document explains what files are tracked in git vs ignored, and why.

## Files to COMMIT (Permanent)

### Source Code

- `*.go` - All Go source files
- `go.mod`, `go.sum` - Go module dependencies
- `go-fdo/` - Go-FDO submodule

### Configuration & Documentation

- `config.yaml` - Example configuration
- `README.md` - Project documentation
- `.vscode/settings.json` - VS Code settings (optional, but useful for team consistency)

### Test Infrastructure (Permanent)

- `tests/config-a.yaml` - Instance A test configuration
- `tests/config-b.yaml` - Instance B test configuration
- `tests/*.sh` - All test scripts (lib.sh, test-*.sh, run-all-tests.sh)
- `tests/README.md` - Test documentation
- `tests/TEST_PLAN.md` - Test plan

## Files to IGNORE (Ephemeral)

### Compiled Binaries

- `fdo-voucher-manager` - Compiled executable (rebuild with `go build`)

### Test Runtime Artifacts

- `tests/data/` - **All test runtime data**
  - `*.log` - Server logs
  - `*.db` - SQLite databases
  - `*.pem` - Generated keys and vouchers
  - `*.fdoov` - Voucher files
  - `config-validate.yaml` - Generated test config
  - `voucher-*.pem` - Generated test vouchers
  - `service-key.pem` - Exported service keys
  - `other-key.pem` - Generated test keys

- `tests/keys/` - **Test-generated keys**
  - `key-*.pem` - Keys exported during tests

- `tests/vouchers/` - **Test-generated vouchers**
  - `*.pem` - Voucher files

- `tests/tests/` - **Nested test artifacts directory**
  - `data/vouchers-a/` - Instance A voucher storage
  - `data/vouchers-b/` - Instance B voucher storage

### IDE & Editor Files

- `.vscode/` (except settings.json)
- `.idea/`
- `*.swp`, `*.swo`, `*~`
- `.DS_Store`

### Build Cache

- `vendor/`
- `.cache/`

## Quick Reference

### To Clean Up Test Artifacts

```bash
# Remove all ephemeral test files
rm -rf tests/data tests/keys tests/vouchers tests/tests

# Rebuild the binary
go build -o fdo-voucher-manager
```

### To Check What Git Will Track

```bash
# See what git will commit
git status

# Verify specific files are ignored
git check-ignore -v tests/data/*.log
git check-ignore -v fdo-voucher-manager
```

### What to Commit After Development

```bash
# Add all permanent files
git add .gitignore README.md config.yaml *.go go.mod go.sum
git add tests/config-*.yaml tests/*.sh tests/README.md tests/TEST_PLAN.md

# Verify nothing ephemeral is staged
git status

# Commit
git commit -m "Your commit message"
```

## Why This Structure?

- **Source code & configs**: Needed to build and run the service
- **Test scripts & configs**: Needed to run tests consistently
- **Test artifacts**: Generated fresh each test run, not needed in git
- **Compiled binary**: Rebuilt from source, not needed in git
- **Logs & databases**: Temporary runtime data, not needed in git
- **Generated keys**: Test-only, regenerated each run
