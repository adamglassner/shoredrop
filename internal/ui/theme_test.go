package ui

import "testing"

// TestReadable checks that every text colour used on every background it
// appears on meets WCAG AA (4.5:1 for normal text, 3:1 for large/UI glyphs).
func TestReadable(t *testing.T) {
	for _, p := range []Palette{Light, Dark} {
		name := "light"
		if p.Dark {
			name = "dark"
		}
		bgs := map[string]Color{"Window": p.Window, "Surface": p.Surface, "Surface2": p.Surface2, "Sunken": p.Sunken, "Hover": p.Hover}
		texts := map[string]Color{"Text": p.Text, "Text2": p.Text2, "Text3": p.Text3, "TitleText": p.TitleText, "AccentInk": p.AccentInk,
			"Success": p.Success, "Warning": p.Warning, "Danger": p.Danger, "Info": p.Info}
		for bn, bg := range bgs {
			for tn, fg := range texts {
				if c := Contrast(fg, bg); c < 4.5 {
					t.Errorf("%s: %s on %s = %.2f", name, tn, bn, c)
				}
			}
		}
		pairs := []struct {
			n      string
			fg, bg Color
			min    float64
		}{
			{"AccentText on Accent", p.AccentText, p.Accent, 4.5},
			{"AccentText on AccentHover", p.AccentText, p.AccentHover, 4.5},
			{"SelText on Selection", p.SelText, p.Selection, 7},
			{"Text on Selection", p.Text, p.Selection, 4.5},
			{"Text2 on Selection", p.Text2, p.Selection, 3.5},
			{"AccentInk on AccentSoft", p.AccentInk, p.AccentSoft, 4.5},
			{"Success on SuccessSoft", p.Success, p.SuccessSoft, 4.5},
			{"Warning on WarningSoft", p.Warning, p.WarningSoft, 4.5},
			{"Danger on DangerSoft", p.Danger, p.DangerSoft, 4.5},
			{"Logo on Window", p.Logo, p.Window, 3},
			{"Accent on Surface (UI)", p.Accent, p.Surface, 3},
			{"BorderStr on Sunken (UI)", p.BorderStr, p.Sunken, 1.3},
		}
		for _, x := range pairs {
			if c := Contrast(x.fg, x.bg); c < x.min {
				t.Errorf("%s: %s = %.2f (want %.1f)", name, x.n, c, x.min)
			}
		}
	}
}
