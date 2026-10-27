//go:build windows

package ui

import (
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	dwmapi   = syscall.NewLazyDLL("dwmapi.dll")
	uxtheme  = syscall.NewLazyDLL("uxtheme.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	advapi   = syscall.NewLazyDLL("advapi32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")

	pRegisterClassExW         = user32.NewProc("RegisterClassExW")
	pCreateWindowExW          = user32.NewProc("CreateWindowExW")
	pDefWindowProcW           = user32.NewProc("DefWindowProcW")
	pDestroyWindow            = user32.NewProc("DestroyWindow")
	pShowWindow               = user32.NewProc("ShowWindow")
	pUpdateWindow             = user32.NewProc("UpdateWindow")
	pGetMessageW              = user32.NewProc("GetMessageW")
	pTranslateMessage         = user32.NewProc("TranslateMessage")
	pDispatchMessageW         = user32.NewProc("DispatchMessageW")
	pPostMessageW             = user32.NewProc("PostMessageW")
	pSendMessageW             = user32.NewProc("SendMessageW")
	pPostQuitMessage          = user32.NewProc("PostQuitMessage")
	pBeginPaint               = user32.NewProc("BeginPaint")
	pEndPaint                 = user32.NewProc("EndPaint")
	pInvalidateRect           = user32.NewProc("InvalidateRect")
	pGetClientRect            = user32.NewProc("GetClientRect")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pSetWindowPos             = user32.NewProc("SetWindowPos")
	pMoveWindow               = user32.NewProc("MoveWindow")
	pLoadCursorW              = user32.NewProc("LoadCursorW")
	pSetCursor                = user32.NewProc("SetCursor")
	pSetCapture               = user32.NewProc("SetCapture")
	pReleaseCapture           = user32.NewProc("ReleaseCapture")
	pGetCapture               = user32.NewProc("GetCapture")
	pSetFocus                 = user32.NewProc("SetFocus")
	pGetFocus                 = user32.NewProc("GetFocus")
	pEnableWindow             = user32.NewProc("EnableWindow")
	pIsWindowEnabled          = user32.NewProc("IsWindowEnabled")
	pIsZoomed                 = user32.NewProc("IsZoomed")
	pIsIconic                 = user32.NewProc("IsIconic")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pGetDC                    = user32.NewProc("GetDC")
	pReleaseDC                = user32.NewProc("ReleaseDC")
	pSetWindowTextW           = user32.NewProc("SetWindowTextW")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	pScreenToClient           = user32.NewProc("ScreenToClient")
	pClientToScreen           = user32.NewProc("ClientToScreen")
	pGetCursorPos             = user32.NewProc("GetCursorPos")
	pSetTimer                 = user32.NewProc("SetTimer")
	pKillTimer                = user32.NewProc("KillTimer")
	pGetKeyState              = user32.NewProc("GetKeyState")
	pTrackMouseEvent          = user32.NewProc("TrackMouseEvent")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pGetSystemMetricsForDpi   = user32.NewProc("GetSystemMetricsForDpi")
	pGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	pGetDpiForSystem          = user32.NewProc("GetDpiForSystem")
	pSetProcessDpiAwarenessCx = user32.NewProc("SetProcessDpiAwarenessContext")
	pMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	pMonitorFromPoint         = user32.NewProc("MonitorFromPoint")
	pGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	pSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	pSetActiveWindow          = user32.NewProc("SetActiveWindow")
	pGetActiveWindow          = user32.NewProc("GetActiveWindow")
	pGetAncestor              = user32.NewProc("GetAncestor")
	pGetParent                = user32.NewProc("GetParent")
	pSetWindowLongPtrW        = user32.NewProc("SetWindowLongPtrW")
	pGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	pMessageBeep              = user32.NewProc("MessageBeep")
	pOpenClipboard            = user32.NewProc("OpenClipboard")
	pCloseClipboard           = user32.NewProc("CloseClipboard")
	pEmptyClipboard           = user32.NewProc("EmptyClipboard")
	pSetClipboardData         = user32.NewProc("SetClipboardData")
	pFillRect                 = user32.NewProc("FillRect")
	pDrawTextW                = user32.NewProc("DrawTextW")
	pLoadImageW               = user32.NewProc("LoadImageW")
	pSendMessageTimeout       = user32.NewProc("SendMessageTimeoutW")
	pSystemParametersInfoW    = user32.NewProc("SystemParametersInfoW")
	pGetDoubleClickTime       = user32.NewProc("GetDoubleClickTime")
	pRedrawWindow             = user32.NewProc("RedrawWindow")
	pGetWindowPlacement       = user32.NewProc("GetWindowPlacement")
	pSetWindowPlacement       = user32.NewProc("SetWindowPlacement")
	pIsWindow                 = user32.NewProc("IsWindow")
	pFlashWindow              = user32.NewProc("FlashWindow")
	pCallWindowProcW          = user32.NewProc("CallWindowProcW")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pCreateFontW            = gdi32.NewProc("CreateFontW")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pSetBkColor             = gdi32.NewProc("SetBkColor")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	pGetTextExtentPoint32W  = gdi32.NewProc("GetTextExtentPoint32W")
	pGetTextMetricsW        = gdi32.NewProc("GetTextMetricsW")
	pSaveDC                 = gdi32.NewProc("SaveDC")
	pRestoreDC              = gdi32.NewProc("RestoreDC")
	pIntersectClipRect      = gdi32.NewProc("IntersectClipRect")
	pEnumFontFamiliesExW    = gdi32.NewProc("EnumFontFamiliesExW")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
	pGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	pGlobalLock       = kernel32.NewProc("GlobalLock")
	pGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	pGetLogicalDrives = kernel32.NewProc("GetLogicalDrives")
	pGetDriveTypeW    = kernel32.NewProc("GetDriveTypeW")
	pGetVolumeInfoW   = kernel32.NewProc("GetVolumeInformationW")

	pDragAcceptFiles   = shell32.NewProc("DragAcceptFiles")
	pDragQueryFileW    = shell32.NewProc("DragQueryFileW")
	pDragQueryPoint    = shell32.NewProc("DragQueryPoint")
	pDragFinish        = shell32.NewProc("DragFinish")
	pShellExecuteW     = shell32.NewProc("ShellExecuteW")
	pSHFileOperationW  = shell32.NewProc("SHFileOperationW")
	pSHBrowseForFolder = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDL  = shell32.NewProc("SHGetPathFromIDListW")
	pSHGetKnownFolder  = shell32.NewProc("SHGetKnownFolderPath")

	pDwmSetWindowAttribute        = dwmapi.NewProc("DwmSetWindowAttribute")
	pDwmExtendFrameIntoClientArea = dwmapi.NewProc("DwmExtendFrameIntoClientArea")

	pSetWindowTheme = uxtheme.NewProc("SetWindowTheme")

	pGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	pCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	pCoTaskMemFree    = ole32.NewProc("CoTaskMemFree")

	pRegGetValueW = advapi.NewProc("RegGetValueW")
)

const (
	WM_CREATE           = 0x0001
	WM_DESTROY          = 0x0002
	WM_MOVE             = 0x0003
	WM_SIZE             = 0x0005
	WM_ACTIVATE         = 0x0006
	WM_SETFOCUS         = 0x0007
	WM_KILLFOCUS        = 0x0008
	WM_ENABLE           = 0x000A
	WM_SETTEXT          = 0x000C
	WM_PAINT            = 0x000F
	WM_CLOSE            = 0x0010
	WM_ERASEBKGND       = 0x0014
	WM_SETTINGCHANGE    = 0x001A
	WM_ACTIVATEAPP      = 0x001C
	WM_SETCURSOR        = 0x0020
	WM_GETMINMAXINFO    = 0x0024
	WM_SETFONT          = 0x0030
	WM_WINDOWPOSCHANGED = 0x0047
	WM_NCCALCSIZE       = 0x0083
	WM_NCHITTEST        = 0x0084
	WM_NCACTIVATE       = 0x0086
	WM_KEYDOWN          = 0x0100
	WM_KEYUP            = 0x0101
	WM_CHAR             = 0x0102
	WM_SYSKEYDOWN       = 0x0104
	WM_SYSKEYUP         = 0x0105
	WM_SYSCHAR          = 0x0106
	WM_COMMAND          = 0x0111
	WM_SYSCOMMAND       = 0x0112
	WM_TIMER            = 0x0113
	WM_CTLCOLOREDIT     = 0x0133
	WM_CTLCOLORSTATIC   = 0x0138
	WM_MOUSEMOVE        = 0x0200
	WM_LBUTTONDOWN      = 0x0201
	WM_LBUTTONUP        = 0x0202
	WM_LBUTTONDBLCLK    = 0x0203
	WM_RBUTTONDOWN      = 0x0204
	WM_RBUTTONUP        = 0x0205
	WM_MBUTTONDOWN      = 0x0207
	WM_XBUTTONDOWN      = 0x020B
	WM_MOUSEWHEEL       = 0x020A
	WM_MOUSEHWHEEL      = 0x020E
	WM_CAPTURECHANGED   = 0x0215
	WM_DROPFILES        = 0x0233
	WM_MOUSELEAVE       = 0x02A3
	WM_DPICHANGED       = 0x02E0
	WM_THEMECHANGED     = 0x031A
	WM_APP              = 0x8000

	WS_OVERLAPPED       = 0x00000000
	WS_POPUP            = 0x80000000
	WS_CHILD            = 0x40000000
	WS_VISIBLE          = 0x10000000
	WS_DISABLED         = 0x08000000
	WS_CLIPSIBLINGS     = 0x04000000
	WS_CLIPCHILDREN     = 0x02000000
	WS_CAPTION          = 0x00C00000
	WS_BORDER           = 0x00800000
	WS_VSCROLL          = 0x00200000
	WS_SYSMENU          = 0x00080000
	WS_THICKFRAME       = 0x00040000
	WS_MINIMIZEBOX      = 0x00020000
	WS_MAXIMIZEBOX      = 0x00010000
	WS_TABSTOP          = 0x00010000
	WS_OVERLAPPEDWINDOW = WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_THICKFRAME | WS_MINIMIZEBOX | WS_MAXIMIZEBOX

	WS_EX_TOOLWINDOW  = 0x00000080
	WS_EX_TOPMOST     = 0x00000008
	WS_EX_ACCEPTFILES = 0x00000010
	WS_EX_APPWINDOW   = 0x00040000
	WS_EX_NOACTIVATE  = 0x08000000

	CS_DBLCLKS    = 0x0008
	CS_HREDRAW    = 0x0002
	CS_VREDRAW    = 0x0001
	CS_DROPSHADOW = 0x00020000

	SW_HIDE           = 0
	SW_SHOWNORMAL     = 1
	SW_SHOWMINIMIZED  = 2
	SW_MAXIMIZE       = 3
	SW_SHOWNOACTIVATE = 4
	SW_SHOW           = 5
	SW_MINIMIZE       = 6
	SW_SHOWNA         = 8
	SW_RESTORE        = 9

	SWP_NOSIZE       = 0x0001
	SWP_NOMOVE       = 0x0002
	SWP_NOZORDER     = 0x0004
	SWP_NOACTIVATE   = 0x0010
	SWP_FRAMECHANGED = 0x0020
	SWP_SHOWWINDOW   = 0x0040

	HTCLIENT      = 1
	HTCAPTION     = 2
	HTLEFT        = 10
	HTRIGHT       = 11
	HTTOP         = 12
	HTTOPLEFT     = 13
	HTTOPRIGHT    = 14
	HTBOTTOM      = 15
	HTBOTTOMLEFT  = 16
	HTBOTTOMRIGHT = 17

	ES_LEFT        = 0x0000
	ES_MULTILINE   = 0x0004
	ES_PASSWORD    = 0x0020
	ES_AUTOVSCROLL = 0x0040
	ES_AUTOHSCROLL = 0x0080
	ES_NUMBER      = 0x2000
	ES_WANTRETURN  = 0x1000
	ES_READONLY    = 0x0800

	EM_SETSEL          = 0x00B1
	EM_SETREADONLY     = 0x00CF
	EM_SETMARGINS      = 0x00D3
	EM_SETCUEBANNER    = 0x1501
	EM_SETPASSWORDCHAR = 0x00CC
	EN_SETFOCUS        = 0x0100
	EN_KILLFOCUS       = 0x0200
	EN_CHANGE          = 0x0300
	EC_LEFTMARGIN      = 0x0001
	EC_RIGHTMARGIN     = 0x0002

	TRANSPARENT = 1
	SRCCOPY     = 0x00CC0020

	DT_LEFT          = 0x0000
	DT_CENTER        = 0x0001
	DT_RIGHT         = 0x0002
	DT_VCENTER       = 0x0004
	DT_WORDBREAK     = 0x0010
	DT_SINGLELINE    = 0x0020
	DT_NOCLIP        = 0x0100
	DT_CALCRECT      = 0x0400
	DT_NOPREFIX      = 0x0800
	DT_END_ELLIPSIS  = 0x8000
	DT_PATH_ELLIPSIS = 0x4000

	MK_LBUTTON = 0x0001
	MK_RBUTTON = 0x0002
	MK_SHIFT   = 0x0004
	MK_CONTROL = 0x0008

	TME_LEAVE = 0x00000002

	SM_CXSCREEN       = 0
	SM_CYSCREEN       = 1
	SM_CXFRAME        = 32
	SM_CYFRAME        = 33
	SM_CXPADDEDBORDER = 92
	SM_CXDRAG         = 68
	SM_CYDRAG         = 69

	GA_ROOT       = 2
	GWLP_WNDPROC  = -4
	GWLP_USERDATA = -21
	GWL_STYLE     = -16

	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002

	MONITOR_DEFAULTTONEAREST = 2
)

type POINT struct{ X, Y int32 }
type RECT struct{ Left, Top, Right, Bottom int32 }

type MSG struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      POINT
	private uint32
}

type WNDCLASSEXW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type PAINTSTRUCT struct {
	Hdc         uintptr
	Erase       int32
	RcPaint     RECT
	Restore     int32
	IncUpdate   int32
	RgbReserved [32]byte
}

type MINMAXINFO struct {
	Reserved     POINT
	MaxSize      POINT
	MaxPosition  POINT
	MinTrackSize POINT
	MaxTrackSize POINT
}

type NCCALCSIZE_PARAMS struct {
	Rgrc  [3]RECT
	Lppos uintptr
}

type TRACKMOUSEEVENT struct {
	Size      uint32
	Flags     uint32
	Hwnd      uintptr
	HoverTime uint32
}

type MONITORINFO struct {
	Size    uint32
	Monitor RECT
	Work    RECT
	Flags   uint32
}

type SIZE struct{ CX, CY int32 }

type TEXTMETRICW struct {
	Height           int32
	Ascent           int32
	Descent          int32
	InternalLeading  int32
	ExternalLeading  int32
	AveCharWidth     int32
	MaxCharWidth     int32
	Weight           int32
	Overhang         int32
	DigitizedAspectX int32
	DigitizedAspectY int32
	FirstChar        uint16
	LastChar         uint16
	DefaultChar      uint16
	BreakChar        uint16
	Italic           byte
	Underlined       byte
	StruckOut        byte
	PitchAndFamily   byte
	CharSet          byte
}

type WINDOWPLACEMENT struct {
	Length         uint32
	Flags          uint32
	ShowCmd        uint32
	MinPosition    POINT
	MaxPosition    POINT
	NormalPosition RECT
}

type MARGINS struct{ Left, Right, Top, Bottom int32 }

func utf16p(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

func up(s string) uintptr { return uintptr(unsafe.Pointer(utf16p(s))) }

func loword(v uintptr) int { return int(int16(v & 0xffff)) }
func hiword(v uintptr) int { return int(int16((v >> 16) & 0xffff)) }

func rgb(c Color) uintptr { return uintptr(c.R) | uintptr(c.G)<<8 | uintptr(c.B)<<16 }

func keyDown(vk int) bool {
	r, _, _ := pGetKeyState.Call(uintptr(vk))
	return int16(r) < 0
}

func getWindowText(h uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(h)
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func setClipboardText(s string) {
	u, _ := syscall.UTF16FromString(s)
	if r, _, _ := pOpenClipboard.Call(0); r == 0 {
		return
	}
	defer pCloseClipboard.Call()
	pEmptyClipboard.Call()
	h, _, _ := pGlobalAlloc.Call(GMEM_MOVEABLE, uintptr(len(u)*2))
	if h == 0 {
		return
	}
	p, _, _ := pGlobalLock.Call(h)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(u)), u)
	pGlobalUnlock.Call(h)
	pSetClipboardData.Call(CF_UNICODETEXT, h)
}

func cursorPos() (int, int) {
	var p POINT
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

func loadCursor(id int) uintptr {
	c, _, _ := pLoadCursorW.Call(0, uintptr(id))
	return c
}
