//go:build windows || darwin

package ui

// Fonts is the set of fonts used at one DPI scale.
type Fonts struct {
	Body, BodySemi, BodyBold *Font
	Small, SmallSemi         *Font
	Tiny                     *Font
	Label                    *Font // field labels
	Title                    *Font // pane titles
	H1                       *Font // dialog headings
	Brand                    *Font // "ShoreDrop"
	BrandSub                 *Font // "v1.0.0", "Site Manager"
	Mono                     *Font
	Icon, IconSmall, IconBig *Font
	Menu                     *Font
}

// Icon identifiers. The values are Segoe MDL2 / Fluent code points, drawn
// directly on Windows; macOS maps each one to an SF Symbol.
const (
	icBack        = ''
	icForward     = ''
	icUp          = ''
	icRefresh     = ''
	icSettings    = ''
	icSearch      = ''
	icCheck       = ''
	icClose       = ''
	icPause       = ''
	icPlay        = ''
	icDownload    = ''
	icUpload      = ''
	icAdd         = ''
	icDelete      = ''
	icRename      = ''
	icSync        = ''
	icHome        = ''
	icPC          = ''
	icCloud       = ''
	icChevDown    = ''
	icChevRight   = ''
	icChevUp      = ''
	icMin         = ''
	icMax         = ''
	icRestore     = ''
	icChromeX     = ''
	icInfo        = ''
	icWarning     = ''
	icLock        = ''
	icUnlock      = ''
	icFolderAdd   = ''
	icRetry       = ''
	icServer      = ''
	icGlobe       = ''
	icLink        = ''
	icKey         = ''
	icCopy        = ''
	icOpen        = ''
	icFolder      = ''
	icList        = ''
	icClear       = ''
	icPermissions = ''
	icSun         = ''
	icMoon        = ''
	icStatus      = ''
	icMore        = ''
	icEdit        = ''
	icSave        = ''
	icPlug        = ''
	icHistory     = ''
	icDocument    = ''
	icShield      = ''
	icSliders     = ''
	icPalette     = ''
	icTransfer    = ''
	icDisconnect  = ''
)
