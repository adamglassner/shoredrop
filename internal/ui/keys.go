//go:build windows || darwin

package ui

// Cursor ids and virtual-key codes (Win32 values; macOS maps onto them).
const (
	IDC_ARROW       = 32512
	IDC_IBEAM       = 32513
	IDC_WAIT        = 32514
	IDC_SIZENWSE    = 32642
	IDC_SIZENESW    = 32643
	IDC_SIZEWE      = 32644
	IDC_SIZENS      = 32645
	IDC_HAND        = 32649
	IDC_APPSTARTING = 32650
	IDC_NO          = 32648

	VK_BACK            = 0x08
	VK_TAB             = 0x09
	VK_RETURN          = 0x0D
	VK_SHIFT           = 0x10
	VK_CONTROL         = 0x11
	VK_MENU            = 0x12
	VK_ESCAPE          = 0x1B
	VK_SPACE           = 0x20
	VK_PRIOR           = 0x21
	VK_NEXT            = 0x22
	VK_END             = 0x23
	VK_HOME            = 0x24
	VK_LEFT            = 0x25
	VK_UP              = 0x26
	VK_RIGHT           = 0x27
	VK_DOWN            = 0x28
	VK_INSERT          = 0x2D
	VK_DELETE          = 0x2E
	VK_APPS            = 0x5D
	VK_F1              = 0x70
	VK_F2              = 0x71
	VK_F5              = 0x74
	VK_F7              = 0x76
	VK_F10             = 0x79
	VK_OEM_COMMA       = 0xBC
	VK_OEM_PERIOD      = 0xBE
	VK_OEM_4           = 0xDB // [
	VK_OEM_6           = 0xDD // ]
	VK_BROWSER_BACK    = 0xA6
	VK_BROWSER_FORWARD = 0xA7
)
