//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	createNoWindow                    = 0x08000000
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processTerminate                  = 0x0001
	processSetQuota                   = 0x0100
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	shell32                      = syscall.NewLazyDLL("shell32.dll")
	ole32                        = syscall.NewLazyDLL("ole32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
	procSHGetKnownFolderPath     = shell32.NewProc("SHGetKnownFolderPath")
	procCoTaskMemFree            = ole32.NewProc("CoTaskMemFree")

	jobOnce   sync.Once
	jobHandle syscall.Handle
)

// FOLDERID_Downloads = {374DE290-123F-4565-9164-39C4925E467B}
var folderIDDownloads = syscall.GUID{
	Data1: 0x374DE290,
	Data2: 0x123F,
	Data3: 0x4565,
	Data4: [8]byte{0x91, 0x64, 0x39, 0xC4, 0x92, 0x5E, 0x46, 0x7B},
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	s := unsafe.Slice(p, 1024)
	for i, v := range s {
		if v == 0 {
			return syscall.UTF16ToString(s[:i])
		}
	}
	return syscall.UTF16ToString(s)
}

// userDownloadsDir queries the Windows Known Folder API for the user's real Downloads
// folder (handles moved/OneDrive Downloads folders), falling back to ~/Downloads.
func userDownloadsDir() string {
	// Respect custom HOME/USERPROFILE override in isolated tests.
	if os.Getenv("ANYSAVE_TEST_HOME") != "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "Downloads")
	}
	var pathPtr *uint16
	hr, _, _ := procSHGetKnownFolderPath.Call(
		uintptr(unsafe.Pointer(&folderIDDownloads)),
		0,
		0,
		uintptr(unsafe.Pointer(&pathPtr)),
	)
	if hr == 0 && pathPtr != nil {
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pathPtr)))
		p := utf16PtrToString(pathPtr)
		if p != "" {
			return p
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobObjectExtendedLimitInfo struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func initJobObject() {
	h, _, _ := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		return
	}
	info := jobObjectExtendedLimitInfo{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	r, _, _ := procSetInformationJobObject.Call(
		h,
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if r != 0 {
		jobHandle = syscall.Handle(h)
	}
}

// Don't flash a console window for yt-dlp / ffmpeg.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// attachChildJob places the started process in a Windows Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE so child processes never survive if Chrome kills the host.
func attachChildJob(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	jobOnce.Do(initJobObject)
	if jobHandle == 0 {
		return
	}
	p, err := syscall.OpenProcess(processTerminate|processSetQuota, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer syscall.CloseHandle(p)
	procAssignProcessToJobObject.Call(uintptr(jobHandle), uintptr(p))
}

// yt-dlp.exe is a launcher that starts a second process, and ffmpeg runs
// under it, so kill the whole tree.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	k := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	k.Run()
}

func revealInFileManager(path string) {
	exec.Command("explorer", "/select,", path).Run()
}

// copyFileToClipboard puts the file on the clipboard the way Explorer's Copy does; text
// files also carry their text. Paths and text go in through the environment, never
// into the script itself.
func copyFileToClipboard(path, text, imageType string) error {
	script := `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.DataObject
$l = New-Object System.Collections.Specialized.StringCollection
[void]$l.Add($env:ANYSAVE_CLIP_PATH)
$d.SetFileDropList($l)
if ($env:ANYSAVE_CLIP_TEXT) { $d.SetText($env:ANYSAVE_CLIP_TEXT) }
if ($env:ANYSAVE_CLIP_IMAGE) { try { $d.SetImage([System.Drawing.Image]::FromFile($env:ANYSAVE_CLIP_PATH)) } catch {} }
[System.Windows.Forms.Clipboard]::SetDataObject($d, $true)`
	c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script)
	c.Env = append(os.Environ(), "ANYSAVE_CLIP_PATH="+path, "ANYSAVE_CLIP_TEXT="+text)
	if imageType != "" {
		c.Env = append(c.Env, "ANYSAVE_CLIP_IMAGE=1")
	}
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// openWithDefaultApp opens a file in the app the user picked for its type
// (ShellExecute, the same as double-clicking it in Explorer).
func openWithDefaultApp(path string) error {
	c := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c.Start()
}

// pickFile shows the Windows "Open" dialog. Returns "" if the user cancels.
func pickFile() (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = "Choose a video or audio file to convert"
$d.Filter = "Video and audio|*.mp4;*.mkv;*.webm;*.mov;*.avi;*.m4v;*.flv;*.wmv;*.ts;*.mp3;*.m4a;*.wav;*.flac;*.ogg;*.opus;*.aac|All files|*.*"
$f = New-Object System.Windows.Forms.Form -Property @{TopMost = $true; ShowInTaskbar = $false}
if ($d.ShowDialog($f) -eq "OK") { [Console]::OutputEncoding = [Text.Encoding]::UTF8; Write-Output $d.FileName }`
	c := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// pickFiles shows the Windows "Open" dialog for any kind of file. Returns nil if the user cancels.
func pickFiles(multi bool) ([]string, error) {
	m := "$false"
	if multi {
		m = "$true"
	}
	script := `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = "Choose files to convert"
$d.Multiselect = ` + m + `
$d.Filter = "All files|*.*"
$f = New-Object System.Windows.Forms.Form -Property @{TopMost = $true; ShowInTaskbar = $false}
if ($d.ShowDialog($f) -eq "OK") { [Console]::OutputEncoding = [Text.Encoding]::UTF8; $d.FileNames | ForEach-Object { Write-Output $_ } }`
	c := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := c.Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths, nil
}

func hideWindow(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= createNoWindow
}

// pickFolder shows the Windows folder dialog. Returns "" if the user cancels.
func pickFolder(prompt string) (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = "` + strings.ReplaceAll(prompt, `"`, "'") + `"
$f = New-Object System.Windows.Forms.Form -Property @{TopMost = $true; ShowInTaskbar = $false}
if ($d.ShowDialog($f) -eq "OK") { [Console]::OutputEncoding = [Text.Encoding]::UTF8; Write-Output $d.SelectedPath }`
	c := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// moveToTrash puts files in the Recycle Bin (so they can be put back).
func moveToTrash(paths []string) error {
	for _, p := range paths {
		script := `Add-Type -AssemblyName Microsoft.VisualBasic; [Microsoft.VisualBasic.FileIO.FileSystem]::DeleteFile($env:ANYSAVE_TRASH, 'OnlyErrorDialogs', 'SendToRecycleBin')`
		c := exec.Command("powershell", "-NoProfile", "-Command", script)
		c.Env = append(os.Environ(), "ANYSAVE_TRASH="+p)
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
	}
	return nil
}

var procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// freeSpace is the free space on the disk that holds dir, or -1 when unknown.
func freeSpace(dir string) int64 {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return -1
	}
	var free uint64
	if r, _, _ := procGetDiskFreeSpaceExW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), 0, 0); r == 0 {
		return -1
	}
	return int64(free)
}
