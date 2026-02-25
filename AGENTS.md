# Agent Instructions

You are working on an FDO Voucher Managment System. It uses the go-fdo library. You are working on a high-llevel application that uses this library (submodule). Do not make changes to the library itself unless very explicitly instructed by the user.

You will need to run this server to test - but user will be responsible for running (client) to test again. It is advasable to always capture output when running server. As it is a server, you will often need to:

- Run it in the background
- Log it's output for subsequent introspection
- Kill and restart when/where needed

## Definition of "Done"

Before we call stuff done and complete, we must be able to:

- Run all tests in tests/run_all_test.sh
- gofmt all go files
- `golangci-lint` all
- `shellcheck` all shell scripts
- Fix shell script formatting `shellcheck -s sh -f checkstyle -x ./...`
- Stuff should have unit tests
- User-visible stuff (commands, config file parameters, etc) should be documented. If not directly in README.md, in other .md docs which reference it. 

## Work Tracking

It may be okay to leave stuff incomplete, but when done, keep track in TODO.md so we don't lose track.

When we do stuff - make sure we update TODO.md to mark status (completion, etc)

Tests todo should be tracked at the end of this file, separately.
