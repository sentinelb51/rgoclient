package video

// The Windows sandbox is a restricted, low-integrity token and a job object.
// The token strips every privilege and denies writes to anything at medium
// integrity or above — which is the user's own files, the registry and the
// client's config; reading the input file needs nothing. The job caps the
// child's memory, forbids it children, and kills it when the last handle
// closes, so a child the client crashed away from dies with it. Both are
// best-effort: a machine where either call fails still gets the process
// boundary, which is most of the win.

import (
	"log"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// childMemoryCap bounds the child's committed memory: generous for any
	// real decode at chat-card sizes, fatal for an allocation bomb.
	childMemoryCap = 2 << 30

	// Token flags CreateRestrictedToken takes; x/sys/windows does not name
	// them. DISABLE_MAX_PRIVILEGE strips every privilege but SeChangeNotify;
	// LUA_TOKEN drops the administrator half of a UAC token.
	disableMaxPrivilege = 0x1
	luaToken            = 0x4
)

var (
	tokenOnce sync.Once
	lowToken  windows.Token // 0 when restriction failed; reused for every child

	// x/sys/windows stops short of CreateRestrictedToken, so it is declared
	// by hand the way internal/cpu declares its calls.
	advapi32                  = windows.NewLazySystemDLL("advapi32.dll")
	procCreateRestrictedToken = advapi32.NewProc("CreateRestrictedToken")
)

// createRestrictedToken wraps advapi32's CreateRestrictedToken for the one
// call this package makes: no SID lists, flags only.
func createRestrictedToken(existing windows.Token, flags uint32, restricted *windows.Token) error {
	r1, _, err := procCreateRestrictedToken.Call(
		uintptr(existing), uintptr(flags),
		0, 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(restricted)),
	)
	if r1 == 0 {
		return err
	}

	return nil
}

// sandboxArgv wraps nothing on Windows — the restriction rides the token and
// the job, not the command line.
func sandboxArgv(tool string, args []string, media string) []string {
	return append([]string{tool}, args...)
}

// platformAttrs hides the console window a GUI subsystem process would
// otherwise flash per decode, lowers the child's scheduling class, and hands
// it the restricted token where one could be made.
func platformAttrs(cmd *exec.Cmd) {
	attrs := &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW | windows.BELOW_NORMAL_PRIORITY_CLASS,
	}

	tokenOnce.Do(makeLowToken)
	if lowToken != 0 {
		attrs.Token = syscall.Token(lowToken)
	}

	cmd.SysProcAttr = attrs
}

// makeLowToken builds the one token every child runs under: this process's
// own, restricted, then marked low integrity. Failure logs once and leaves
// children on the plain token.
func makeLowToken() {
	var self windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_ADJUST_DEFAULT, &self)
	if err != nil {
		log.Println("video: no process token, children run unrestricted:", err)
		return
	}
	defer self.Close()

	var restricted windows.Token
	if err := createRestrictedToken(self, disableMaxPrivilege|luaToken, &restricted); err != nil {
		log.Println("video: restricted token refused, children run unrestricted:", err)
		return
	}

	// Low integrity is what turns "fewer privileges" into "cannot write the
	// user's files": everything the user owns sits at medium.
	sid, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err == nil {
		label := windows.Tokenmandatorylabel{
			Label: windows.SIDAndAttributes{Sid: sid, Attributes: windows.SE_GROUP_INTEGRITY},
		}
		err = windows.SetTokenInformation(restricted,
			windows.TokenIntegrityLevel, (*byte)(unsafe.Pointer(&label)), label.Size())
	}
	if err != nil {
		log.Println("video: low integrity refused, children keep medium:", err)
	}

	lowToken = restricted
}

// harden puts the started child in its own job object. The handle keeps the
// job alive; releasing it after the child is reaped is what makes the job's
// kill-on-close a no-op for a clean exit and a guarantee for every other.
func harden(cmd *exec.Cmd) func() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
				windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
				windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS,
			ActiveProcessLimit: 1,
		},
		ProcessMemoryLimit: childMemoryCap,
	}
	_, err = windows.SetInformationJobObject(job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil
	}

	handle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil
	}
	err = windows.AssignProcessToJobObject(job, handle)
	_ = windows.CloseHandle(handle)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil
	}

	return func() { _ = windows.CloseHandle(job) }
}
