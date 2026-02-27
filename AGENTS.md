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

## Testing

Be aware of different _types_ of tests to be concidered when adding (or changing) functionality:

### Unit vs. Integration

- Unit testing: test_something.go tests done to test specific functions and code in go framework
- Integration tests: Use of (external, shell scripts/commands) to exercise the end application built from actual CLI or config files, looking at command output, network protocol or generated files as evidence of tests

### Positive vs. Negative

- Positive: Did the test perform the desired result when run (most tests are "positive" in nature)
- Negative: When presented with something we were trying to block or disallow, creation of tests which should _fail_ when run. These are particularly important when dealing with security or credentialling - e.g. Did an operation fail (as expected) when attpepted with incorrect credentials, nonces, hashes, permissions, etc.

Please add tests with some level of discression. We don't need _every_ possible code change to do full positive and negative, unit and integration testing. But certinally high-level functionality, user commands, etc should be well-covered.

### Evidence-Based Testing

**Mantra:** "I don't trust you"

When you run tests, we generally want to see some **evidence** that the test did what it was supposed to do. i.e. If we are creating vouchers or keys, we want to see (probably not the key or voucher in it's entirety) some evidence that it was created (e.g. hash, filename, etc.) Even if it is a file that we can subsequetly go look at. We want to see what it is, and some evidence that it was **used** in whatever operations we were doing. (This is often why we like negative test - i.e. Break the key - watch the protocol fail).

## Work Tracking

It may be okay to leave stuff incomplete, but when done, keep track in TODO.md so we don't lose track.

When we do stuff - make sure we update TODO.md to mark status (completion, etc)

Tests todo should be tracked at the end of this file, separately.
