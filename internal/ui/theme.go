package ui

import "math"

// Color is an sRGB colour with alpha.
type Color struct{ R, G, B, A uint8 }

func hex(v uint32) Color { return Color{uint8(v >> 16), uint8(v >> 8), uint8(v), 255} }

// WithAlpha returns the colour with a different opacity.
func (c Color) WithAlpha(a uint8) Color { c.A = a; return c }

// Mix blends c towards o by t (0..1).
func (c Color) Mix(o Color, t float64) Color {
	m := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }
	return Color{m(c.R, o.R), m(c.G, o.G), m(c.B, o.B), 255}
}

// Palette is a full set of UI colours for one appearance.
type Palette struct {
	Dark bool

	Window    Color // outer window background
	Surface   Color // panels, lists
	Surface2  Color // headers, toolbars, inactive tabs
	Sunken    Color // input backgrounds
	Border    Color
	BorderStr Color // stronger borders (inputs)
	Hover     Color
	Selection Color
	SelText   Color

	Text      Color
	Text2     Color // secondary
	Text3     Color // tertiary / placeholders
	TitleText Color

	Accent      Color
	AccentHover Color
	AccentText  Color // text on accent fill
	AccentSoft  Color // tinted backgrounds (badges)
	AccentInk   Color // accent used as text/icon colour on Surface

	Success     Color
	SuccessSoft Color
	Warning     Color
	WarningSoft Color
	Danger      Color
	DangerSoft  Color
	Info        Color

	Folder    Color
	FolderTab Color
	FileFill  Color
	FileLine  Color

	ToggleOff Color
	Shadow    Color
	Logo      Color
	Track     Color // progress / slider track
}

// Light and Dark palettes, derived from the ShoreDrop design screenshots.
// Every text/background pair is checked for WCAG contrast in theme_test.go.
var Light = Palette{
	Window:    hex(0xF1F3F4),
	Surface:   hex(0xFFFFFF),
	Surface2:  hex(0xF6F7F8),
	Sunken:    hex(0xFFFFFF),
	Border:    hex(0xDDE2E5),
	BorderStr: hex(0xC3CBD0),
	Hover:     hex(0xEEF3F4),
	Selection: hex(0xD3EDEE),
	SelText:   hex(0x0E2A2C),

	Text:      hex(0x1D2327),
	Text2:     hex(0x56616A),
	Text3:     hex(0x626C75),
	TitleText: hex(0x2D3236),

	Accent:      hex(0x02787C),
	AccentHover: hex(0x026468),
	AccentText:  hex(0xFFFFFF),
	AccentSoft:  hex(0xDFF2F2),
	AccentInk:   hex(0x02777B),

	Success:     hex(0x13703F),
	SuccessSoft: hex(0xDDF3E6),
	Warning:     hex(0x995100),
	WarningSoft: hex(0xFDEBD3),
	Danger:      hex(0xC0343B),
	DangerSoft:  hex(0xFBE3E4),
	Info:        hex(0x3E5F8A),

	Folder:    hex(0xF2BE4A),
	FolderTab: hex(0xDDA331),
	FileFill:  hex(0xFFFFFF),
	FileLine:  hex(0x8C969E),

	ToggleOff: hex(0xB9C1C7),
	Shadow:    Color{0, 0, 0, 40},
	Logo:      hex(0x028A8E),
	Track:     hex(0xE1E6E9),
}

var Dark = Palette{
	Dark:      true,
	Window:    hex(0x141619),
	Surface:   hex(0x1C1F23),
	Surface2:  hex(0x23272C),
	Sunken:    hex(0x121417),
	Border:    hex(0x30353B),
	BorderStr: hex(0x434A52),
	Hover:     hex(0x2A2F35),
	Selection: hex(0x164446),
	SelText:   hex(0xF1FBFB),

	Text:      hex(0xECEFF1),
	Text2:     hex(0xAEB7BF),
	Text3:     hex(0x949EA7),
	TitleText: hex(0xF5F7F8),

	Accent:      hex(0x22C3BC),
	AccentHover: hex(0x45D3CD),
	AccentText:  hex(0x062A2A),
	AccentSoft:  hex(0x15393A),
	AccentInk:   hex(0x3CCFC8),

	Success:     hex(0x4CCB86),
	SuccessSoft: hex(0x173527),
	Warning:     hex(0xF2A649),
	WarningSoft: hex(0x3D2A12),
	Danger:      hex(0xF27A7A),
	DangerSoft:  hex(0x3E1E20),
	Info:        hex(0x8DB4E8),

	Folder:    hex(0xE9B949),
	FolderTab: hex(0xC9982E),
	FileFill:  hex(0x2B3036),
	FileLine:  hex(0x9AA4AD),

	ToggleOff: hex(0x4A525B),
	Shadow:    Color{0, 0, 0, 110},
	Logo:      hex(0x22C3BC),
	Track:     hex(0x343A41),
}

func luminance(c Color) float64 {
	ch := func(v uint8) float64 {
		f := float64(v) / 255
		if f <= 0.03928 {
			return f / 12.92
		}
		return math.Pow((f+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// Contrast returns the WCAG contrast ratio between two colours.
func Contrast(a, b Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
