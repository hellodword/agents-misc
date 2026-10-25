//go:build windows

package guest

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/windows"
)

// AllowProcessObservation grants the selected local Guest Control account only
// the rights needed to verify this process's creation identity and termination.
// Resolve the account on each boot: Sysprep changes local account SIDs.
func AllowProcessObservation(account string) error {
	if account == "" || strings.TrimSpace(account) != account || strings.ContainsAny(account, "\\/\r\n\x00\"") {
		return fmt.Errorf("Windows service and supervisor require a local --control-user account")
	}
	var computer [256]uint16
	size := uint32(len(computer))
	if err := windows.GetComputerName(&computer[0], &size); err != nil {
		return fmt.Errorf("resolve local computer identity: %w", err)
	}
	name := windows.UTF16ToString(computer[:size])
	sid, domain, kind, err := windows.LookupSID("", name+`\`+account)
	if err != nil {
		return fmt.Errorf("resolve Guest Control account: %w", err)
	}
	if kind != windows.SidTypeUser || !strings.EqualFold(domain, name) {
		return fmt.Errorf("Guest Control observer must be a local user")
	}
	process := windows.CurrentProcess()
	sd, err := windows.GetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read process observation permissions: %w", err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("read process DACL: %w", err)
	}
	if acl == nil {
		return fmt.Errorf("process has no restrictive DACL")
	}
	acl, err = observationACL(acl, sid)
	if err != nil {
		return fmt.Errorf("merge process observation permissions: %w", err)
	}
	if err = windows.SetSecurityInfo(process, windows.SE_KERNEL_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("grant process observation permissions: %w", err)
	}
	return nil
}

func observationACL(existing *windows.ACL, observer *windows.SID) (*windows.ACL, error) {
	var pin runtime.Pinner
	pin.Pin(observer)
	defer pin.Unpin()
	return windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.PROCESS_QUERY_LIMITED_INFORMATION | windows.SYNCHRONIZE,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee:           windows.TRUSTEE{TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER, TrusteeValue: windows.TrusteeValueFromSID(observer)},
	}}, existing)
}

func processIdentity(pid int) (wire.ProcessIdentity, bool, error) {
	var id wire.ProcessIdentity
	if pid < 1 {
		return id, false, fmt.Errorf("invalid process ID")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return id, false, os.ErrNotExist
	}
	if err != nil {
		return id, false, err
	}
	defer windows.CloseHandle(handle)
	var created, ended, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(handle, &created, &ended, &kernel, &user); err != nil {
		return id, false, err
	}
	state, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return id, false, err
	}
	id = wire.ProcessIdentity{PID: pid, Start: fmt.Sprintf("%08x%08x", created.HighDateTime, created.LowDateTime)}
	return id, state == uint32(windows.WAIT_TIMEOUT), nil
}
