# TDD Evidence Report: Hide Console on Startup

This report documents the implementation and verification of the console-hiding mechanism for NeoBox on Windows.

## User Journey
*   **As a user**, I want the application to start without showing a console window when it launches automatically on Windows logon, so that my desktop remains clean and uninterrupted.

## Implementation Details
We implemented `HideConsoleIfNeeded()` in the `security` package (`backend/security/console_windows.go`).
*   It dynamically loads `GetConsoleProcessList` and `FreeConsole` from `kernel32.dll`.
*   It checks how many processes are attached to the current console.
*   If only 1 process is attached (our own app, meaning it was launched directly by double-clicking or from the registry Run key, rather than an active cmd/PowerShell window), it calls `FreeConsole()` to detach and close the console window immediately.
*   This is integrated into `main.go` as the very first operation in `main()`.

## Test Specification & Verification

We verified the behavior using unit tests:

| # | What is guaranteed | Test file | Test type | Result | Evidence |
|---|--------------------|-----------|-----------|--------|----------|
| 1 | `HideConsoleIfNeeded` returns false in a shared console environment (like `go test`) and does not detach the test runner. | `backend/security/console_windows_test.go` | Unit | PASS | `go test -v ./backend/security` |

### Test Run Output
```text
=== RUN   TestHideConsoleIfNeeded_InTestEnvironment
--- PASS: TestHideConsoleIfNeeded_InTestEnvironment (0.00s)
PASS
ok  	NeoBox/backend/security	0.457s
```
