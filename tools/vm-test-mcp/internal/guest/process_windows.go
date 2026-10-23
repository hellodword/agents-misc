//go:build windows

package guest

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
	"golang.org/x/sys/windows"
)

type windowsChild struct {
	process, job windows.Handle
	wg           sync.WaitGroup
	stdin        *os.File
}

func launch(ctx context.Context, s wire.CommandSpec, in io.Reader, out, errout io.Writer) (Child, error) {
	job, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	p := &windowsChild{job: job}
	ok := false
	defer func() {
		if !ok {
			p.Close()
		}
	}()
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		return nil, e
	}
	ri, wi, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer ri.Close()
	p.stdin = wi
	ro, wo, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer wo.Close()
	re, we, e := os.Pipe()
	if e != nil {
		ro.Close()
		return nil, e
	}
	defer we.Close()
	for _, f := range []*os.File{ri, wo, we} {
		if e = windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); e != nil {
			ro.Close()
			re.Close()
			return nil, e
		}
	}
	attrs, e := windows.NewProcThreadAttributeList(1)
	if e != nil {
		return nil, e
	}
	defer attrs.Delete()
	handles := []windows.Handle{windows.Handle(ri.Fd()), windows.Handle(wo.Fd()), windows.Handle(we.Fd())}
	if e = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); e != nil {
		return nil, e
	}
	si := windows.StartupInfoEx{}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = handles[0]
	si.StdOutput = handles[1]
	si.StdErr = handles[2]
	si.ProcThreadAttributeList = attrs.List()
	exe, e := commandExecutable(s.Argv[0], s.Cwd, s.Env)
	if e != nil {
		return nil, e
	}
	app, e := windows.UTF16PtrFromString(exe)
	if e != nil {
		return nil, e
	}
	args := []string{}
	for _, a := range s.Argv {
		args = append(args, syscall.EscapeArg(a))
	}
	line, e := windows.UTF16PtrFromString(strings.Join(args, " "))
	if e != nil {
		return nil, e
	}
	env := map[string]string{}
	for _, v := range os.Environ() {
		k, value, ok := strings.Cut(v, "=")
		if ok && k != "" {
			env[strings.ToUpper(k)] = value
		}
	}
	for k, v := range s.Env {
		env[strings.ToUpper(k)] = v
	}
	keys := []string{}
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var block []uint16
	for _, k := range keys {
		block = append(block, utf16.Encode([]rune(k+"="+env[k]))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	var cwd *uint16
	if s.Cwd != "" {
		cwd, e = windows.UTF16PtrFromString(s.Cwd)
		if e != nil {
			return nil, e
		}
	}
	pi := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if e = windows.CreateProcess(app, line, nil, nil, true, flags, &block[0], cwd, &si.StartupInfo, &pi); e != nil {
		ro.Close()
		re.Close()
		return nil, e
	}
	p.process = pi.Process
	defer windows.CloseHandle(pi.Thread)
	// The process cannot execute a single instruction until it belongs to the
	// kill-on-close job. Assignment failure terminates the suspended process.
	if e = windows.AssignProcessToJobObject(job, pi.Process); e != nil {
		windows.TerminateProcess(pi.Process, 1)
		ro.Close()
		re.Close()
		return nil, e
	}
	if _, e = windows.ResumeThread(pi.Thread); e != nil {
		windows.TerminateJobObject(job, 1)
		ro.Close()
		re.Close()
		return nil, e
	}
	go func() { io.Copy(wi, in); wi.Close() }()
	for _, stream := range []struct {
		r *os.File
		w io.Writer
	}{{ro, out}, {re, errout}} {
		p.wg.Add(1)
		go func() { defer p.wg.Done(); defer stream.r.Close(); io.Copy(stream.w, stream.r) }()
	}
	ok = true
	return p, nil
}
func (p *windowsChild) Wait() (int, error) {
	_, e := windows.WaitForSingleObject(p.process, windows.INFINITE)
	var code uint32
	if e == nil {
		e = windows.GetExitCodeProcess(p.process, &code)
	}
	windows.TerminateJobObject(p.job, 1)
	p.stdin.Close()
	p.wg.Wait()
	return int(code), e
}
func (p *windowsChild) Kill() error { return windows.TerminateJobObject(p.job, 1) }
func (p *windowsChild) Close() error {
	if p.stdin != nil {
		p.stdin.Close()
	}
	if p.process != 0 {
		windows.CloseHandle(p.process)
	}
	if p.job != 0 {
		return windows.CloseHandle(p.job)
	}
	return nil
}
func Reboot() error { return exec.Command("shutdown.exe", "/r", "/t", "0").Start() }
