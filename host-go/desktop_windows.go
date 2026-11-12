//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	cfUnicodeText = 13

	wmDestroy     = 0x0002
	wmCommand     = 0x0111
	wmHotkey      = 0x0312
	wmUser        = 0x0400
	wmTrayIcon    = wmUser + 1
	wmLButtonUp   = 0x0202
	wmLButtonDbl  = 0x0203
	wmRButtonUp   = 0x0205
	wmContextMenu = 0x007B

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010
	niifInfo   = 0x00000001

	mfString     = 0x00000000
	mfSeparator  = 0x00000800
	tpmBottom    = 0x0020
	tpmLeftAlign = 0x0000
	tpmRightBtn  = 0x0002

	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modNoRepeat = 0x4000
	vkY         = 0x59

	idiApplication = 32512

	cmdOpenMain    = 1001
	cmdDownloadClp = 1002
	cmdOpenDropBox = 1003
	cmdPauseAll    = 1004
	cmdResumeAll   = 1005
	cmdQuitApp     = 1006
)

var (
	user32                   = syscall.NewLazyDLL("user32.dll")
	procOpenClipboard        = user32.NewProc("OpenClipboard")
	procCloseClipboard       = user32.NewProc("CloseClipboard")
	procGetClipboardData     = user32.NewProc("GetClipboardData")
	procGlobalLock           = kernel32.NewProc("GlobalLock")
	procGlobalUnlock         = kernel32.NewProc("GlobalUnlock")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procLoadIconW            = user32.NewProc("LoadIconW")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu       = user32.NewProc("TrackPopupMenu")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procRegisterHotKey       = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey     = user32.NewProc("UnregisterHotKey")
	procMessageBeep          = user32.NewProc("MessageBeep")
	procShellNotifyIconW     = shell32.NewProc("Shell_NotifyIconW")

	trayHwnd   syscall.Handle
	trayIcon   syscall.Handle
	trayActive atomic.Bool
	activeAppD *daemonServer
	activeURL  string
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type notifyIconDataW struct {
	cbSize           uint32
	hWnd             syscall.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            syscall.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         syscall.GUID
	hBalloonIcon     syscall.Handle
}

type winPoint struct {
	x, y int32
}

type winMsg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      winPoint
}

func copyUTF16(dst []uint16, s string) {
	u, _ := syscall.UTF16FromString(s)
	n := len(u)
	if n > len(dst)-1 {
		n = len(dst) - 1
	}
	copy(dst[:n], u[:n])
	dst[n] = 0
}

func readClipboardText() string {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return ""
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)
	p := *(**uint16)(unsafe.Pointer(&ptr))
	return utf16PtrToString(p)
}

func notifyOS(title, msg string) {
	_, _, _ = procMessageBeep.Call(0)
	if !trayActive.Load() || trayHwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = trayHwnd
	nid.uID = 1
	nid.uFlags = nifInfo
	nid.dwInfoFlags = niifInfo
	copyUTF16(nid.szInfoTitle[:], title)
	copyUTF16(nid.szInfo[:], msg)
	_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func openAppWindow(url string, isDropBox bool) {
	sizeArg := "--window-size=1040,720"
	if isDropBox {
		sizeArg = "--window-size=320,220"
	}
	pf := os.Getenv("ProgramFiles")
	pfx86 := os.Getenv("ProgramFiles(x86)")
	localApp := os.Getenv("LOCALAPPDATA")
	candidates := []string{
		filepath.Join(pfx86, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(pf, "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(pf, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(localApp, "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(pf, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
	}
	for _, c := range candidates {
		if isFile(c) {
			cmd := exec.Command(c, "--app="+url, sizeArg)
			setProcAttrs(cmd)
			if cmd.Start() == nil {
				return
			}
		}
	}
	_ = openWithDefaultApp(url)
}

func showTrayContextMenu(hwnd syscall.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	appendItem := func(flags uintptr, id uintptr, label string) {
		var ptr uintptr
		if label != "" {
			u, _ := syscall.UTF16PtrFromString(label)
			ptr = uintptr(unsafe.Pointer(u))
		}
		_, _, _ = procAppendMenuW.Call(hMenu, flags, id, ptr)
	}

	appendItem(mfString, cmdOpenMain, "Open AnySave")
	appendItem(mfString, cmdDownloadClp, "Download Copied Link (Ctrl+Shift+Y)")
	appendItem(mfString, cmdOpenDropBox, "Show Floating Drop Box")
	appendItem(mfSeparator, 0, "")
	appendItem(mfString, cmdPauseAll, "Pause All Downloads")
	appendItem(mfString, cmdResumeAll, "Resume All Downloads")
	appendItem(mfSeparator, 0, "")
	appendItem(mfString, cmdQuitApp, "Quit AnySave")

	var pt winPoint
	_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	_, _, _ = procSetForegroundWindow.Call(uintptr(hwnd))
	_, _, _ = procTrackPopupMenu.Call(
		hMenu,
		tpmBottom|tpmLeftAlign|tpmRightBtn,
		uintptr(pt.x),
		uintptr(pt.y),
		0,
		uintptr(hwnd),
		0,
	)
}

func trayWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmTrayIcon:
		switch uint32(lParam) {
		case wmLButtonUp, wmLButtonDbl:
			if activeURL != "" {
				go openAppWindow(activeURL, false)
			}
		case wmRButtonUp, wmContextMenu:
			showTrayContextMenu(hwnd)
		}
		return 0

	case wmCommand:
		cmdID := int(wParam & 0xffff)
		if activeAppD != nil {
			switch cmdID {
			case cmdOpenMain:
				go openAppWindow(activeURL, false)
			case cmdDownloadClp:
				go activeAppD.handleAction(map[string]any{"type": "downloadClipboard"})
			case cmdOpenDropBox:
				go activeAppD.handleAction(map[string]any{"type": "openDropBox"})
			case cmdPauseAll:
				go func() {
					activeAppD.mu.Lock()
					var ids []string
					for id, j := range activeAppD.jobs {
						st := str(j, "status")
						if st == "starting" || st == "downloading" {
							ids = append(ids, id)
						}
					}
					activeAppD.mu.Unlock()
					for _, id := range ids {
						activeAppD.pauseJob(id)
					}
				}()
			case cmdResumeAll:
				go func() {
					activeAppD.mu.Lock()
					var ids []string
					for id, j := range activeAppD.jobs {
						if str(j, "status") == "paused" {
							ids = append(ids, id)
						}
					}
					activeAppD.mu.Unlock()
					for _, id := range ids {
						activeAppD.resumeJob(id)
					}
				}()
			case cmdQuitApp:
				activeAppD.handleAction(map[string]any{"type": "quit"})
				_, _, _ = procDestroyWindow.Call(uintptr(hwnd))
			}
		}
		return 0

	case wmHotkey:
		if activeAppD != nil {
			go activeAppD.handleAction(map[string]any{"type": "downloadClipboard"})
		}
		return 0

	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func runPlatformApp(baseURL, _ string, d *daemonServer) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	activeAppD = d
	activeURL = baseURL

	hInst, _, _ := procGetModuleHandleW.Call(0)
	// The AnySave icon is linked into the .exe as icon group 1 (scripts/make-app-icons.sh).
	// Fall back to Windows' generic app icon if it's missing.
	icon, _, _ := procLoadIconW.Call(hInst, 1)
	if icon == 0 {
		icon, _, _ = procLoadIconW.Call(0, idiApplication)
	}
	trayIcon = syscall.Handle(icon)

	clsName, _ := syscall.UTF16PtrFromString("AnySaveTrayWindowClass")
	wc := wndClassExW{
		lpfnWndProc:   syscall.NewCallback(trayWndProc),
		hInstance:     syscall.Handle(hInst),
		hIcon:         trayIcon,
		lpszClassName: clsName,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	_, _, _ = procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	winTitle, _ := syscall.UTF16PtrFromString("AnySave")
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(clsName)),
		uintptr(unsafe.Pointer(winTitle)),
		0,
		0, 0, 0, 0,
		0, 0,
		hInst,
		0,
	)
	if hwnd == 0 {
		<-d.quitCh
		return
	}
	trayHwnd = syscall.Handle(hwnd)

	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = trayHwnd
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmTrayIcon
	nid.hIcon = trayIcon
	copyUTF16(nid.szTip[:], "AnySave — Download Manager")
	_, _, _ = procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	trayActive.Store(true)

	// Global shortcut Ctrl+Shift+Y to download copied link immediately.
	_, _, _ = procRegisterHotKey.Call(hwnd, 1, modControl|modShift|modNoRepeat, vkY)

	defer func() {
		trayActive.Store(false)
		_, _, _ = procUnregisterHotKey.Call(hwnd, 1)
		_, _, _ = procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	}()

	// Live tooltip updater + quit watcher.
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		lastTip := ""
		for {
			select {
			case <-d.quitCh:
				_, _, _ = procPostMessageW.Call(hwnd, wmDestroy, 0, 0)
				return
			case <-ticker.C:
				d.mu.Lock()
				activeCount := 0
				totalPct := 0.0
				for _, j := range d.jobs {
					st := str(j, "status")
					if st == "starting" || st == "downloading" {
						activeCount++
						if p, ok := j["percent"].(float64); ok {
							totalPct += p
						}
					}
				}
				d.mu.Unlock()
				tip := "AnySave — Ready"
				if activeCount > 0 {
					avg := totalPct / float64(activeCount)
					tip = fmt.Sprintf("AnySave — %d downloading (%.0f%%)", activeCount, avg)
				}
				if tip != lastTip {
					lastTip = tip
					var u notifyIconDataW
					u.cbSize = uint32(unsafe.Sizeof(u))
					u.hWnd = trayHwnd
					u.uID = 1
					u.uFlags = nifTip
					copyUTF16(u.szTip[:], tip)
					_, _, _ = procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&u)))
				}
			}
		}
	}()

	var m winMsg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
