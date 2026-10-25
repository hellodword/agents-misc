//go:build windows

package guest

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestObservationACLRetainsExistingRights(t *testing.T) {
	const observer = "S-1-5-21-1-2-3-1001"
	sid, err := windows.StringToSid(observer)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:(D;;0x1;;;" + observer + ")(A;;0x1fffff;;;SY)(A;;0x1fffff;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		acl, err = observationACL(acl, sid)
		if err != nil {
			t.Fatal(err)
		}
		// Read the native ACL, including existing deny rules; repeated setup
		// must preserve unrelated access and not broaden the observer's rights.
		if acl.AceCount != 4 {
			t.Fatalf("unexpected ACE count: %d", acl.AceCount)
		}
		grants := map[string]windows.ACCESS_MASK{}
		denies := map[string]windows.ACCESS_MASK{}
		for i := uint32(0); i < uint32(acl.AceCount); i++ {
			var entry *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(acl, i, &entry); err != nil {
				t.Fatal(err)
			}
			principal := (*windows.SID)(unsafe.Pointer(&entry.SidStart)).String()
			switch entry.Header.AceType {
			case windows.ACCESS_ALLOWED_ACE_TYPE:
				grants[principal] |= entry.Mask
			case windows.ACCESS_DENIED_ACE_TYPE:
				denies[principal] |= entry.Mask
			default:
				t.Fatalf("unexpected ACE type: %d", entry.Header.AceType)
			}
		}
		if grants[observer] != windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE || denies[observer] != windows.PROCESS_TERMINATE {
			t.Fatalf("observer access: grants=%#v denies=%#v", grants, denies)
		}
		if grants["S-1-5-18"] != 0x1fffff || grants["S-1-5-32-544"] != 0x1fffff || len(grants) != 3 || len(denies) != 1 {
			t.Fatalf("unrelated access changed: grants=%#v denies=%#v", grants, denies)
		}
	}
}

func TestProcessIdentityObservedExit(t *testing.T) {
	if os.Getenv("VM_TEST_IDENTITY_HELPER") == "1" {
		_, _ = io.WriteString(os.Stdout, "ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		// Exit 259 must not be mistaken for STILL_ACTIVE. A process handle's
		// signaled state confirms exit independently of the actual exit code.
		os.Exit(259)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProcessIdentityObservedExit$")
	cmd.Env = append(os.Environ(), "VM_TEST_IDENTITY_HELPER=1")
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ready := make([]byte, 5)
	if _, err = io.ReadFull(out, ready); err != nil {
		t.Fatal(err)
	}
	first, alive, err := processIdentity(cmd.Process.Pid)
	if err != nil || !alive || first.Start == "" {
		t.Fatalf("live identity: %#v %v %v", first, alive, err)
	}
	if err = in.Close(); err != nil {
		t.Fatal(err)
	}
	if err = cmd.Wait(); err == nil || cmd.ProcessState.ExitCode() != 259 {
		t.Fatalf("exit: %v %#v", err, cmd.ProcessState)
	}
	last, alive, err := processIdentity(cmd.Process.Pid)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if alive && last == first {
		t.Fatalf("exited process is still reported alive: %#v", last)
	}
}
