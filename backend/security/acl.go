//go:build windows

package security

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

// ProtectFile restricts access to a file so that only the current Windows user
// can read or write it. On Windows, os.WriteFile with Unix mode bits (0600) is
// silently ignored — this function applies a proper DACL.
//
// The DACL it writes has exactly one ACE, granting the current user full
// control, and is marked protected so nothing is inherited from the parent
// directory. That is what keeps other local users, and a stealer running as
// another account, out of key.bin and subscriptions.json.
//
// # Why not icacls
//
// This used to shell out to icacls.exe, which is the documented way to do it
// from a script and the wrong way to do it from a process: each call spawns a
// program, and it measured 36 ms — against 0.02 ms for the equivalent Win32
// calls, some 1500x. That cost was not paid once. storage.writeAtomic protects
// every atomic rewrite, because the ACL has to go on the temp file before the
// rename replaces the old one, so it landed on every save of the subscriptions
// and of the settings that carry credentials. The concurrency test, which
// performs 320 saves, spent roughly fourteen seconds inside CreateProcess and
// timed out because of it.
//
// The Win32 sequence below is what icacls itself performs: build one explicit
// GRANT ACE for the user's SID, turn it into an ACL, and set it as a protected
// DACL. "Protected" is the /inheritance:r flag; a single non-inheritable ACE is
// /grant:r <sid>:F.
func ProtectFile(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		// Matches the old icacls invocation, which named no (OI)/(CI) flags: the
		// ACE applies to this object and is not handed down to children.
		Inheritance: windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("failed to build DACL: %w", err)
	}

	// PROTECTED_DACL_SECURITY_INFORMATION is the load-bearing half. Without it
	// the new ACE is merged with whatever the parent directory hands down, and
	// AppData\Roaming grants the same user access anyway — the ACL would look
	// applied while protecting nothing against a broader inherited grant.
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	); err != nil {
		return fmt.Errorf("failed to set DACL on %s: %w", path, err)
	}
	return nil
}

var (
	userSIDOnce sync.Once
	userSID     *windows.SID
	userSIDErr  error
)

// currentUserSID returns the SID of the account this process runs as.
//
// The answer cannot change for the lifetime of the process, and the lookup is
// the only part of ProtectFile that is not a handful of instructions, so it is
// resolved once. The SID is copied out of the token information buffer because
// that buffer does not outlive the call that filled it.
func currentUserSID() (*windows.SID, error) {
	userSIDOnce.Do(func() {
		token, err := windows.OpenCurrentProcessToken()
		if err != nil {
			userSIDErr = fmt.Errorf("failed to open process token: %w", err)
			return
		}
		defer token.Close()

		tokenUser, err := token.GetTokenUser()
		if err != nil {
			userSIDErr = fmt.Errorf("failed to read token user: %w", err)
			return
		}
		sid, err := tokenUser.User.Sid.Copy()
		if err != nil {
			userSIDErr = fmt.Errorf("failed to copy user SID: %w", err)
			return
		}
		userSID = sid
	})
	return userSID, userSIDErr
}
