// Command macapp assembles ShoreDrop.app from a macOS binary and an .icns
// icon, or (with -zip) packs a finished, signed bundle into a zip archive
// that keeps the executable bit, as Finder's Archive Utility expects.
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleDisplayName</key>
	<string>ShoreDrop</string>
	<key>CFBundleExecutable</key>
	<string>ShoreDrop</string>
	<key>CFBundleIconFile</key>
	<string>ShoreDrop</string>
	<key>CFBundleIdentifier</key>
	<string>io.github.adamglassner.shoredrop</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>ShoreDrop</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>{{VERSION}}</string>
	<key>CFBundleVersion</key>
	<string>{{VERSION}}</string>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.developer-tools</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>LSArchitecturePriority</key>
	<array>
		<string>arm64</string>
	</array>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSSupportsAutomaticGraphicsSwitching</key>
	<true/>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
	<key>NSHumanReadableCopyright</key>
	<string>ShoreDrop {{VERSION}}</string>
</dict>
</plist>
`

func main() {
	bin := flag.String("bin", "", "macOS executable")
	icns := flag.String("icns", "", ".icns icon")
	out := flag.String("out", "ShoreDrop.app", "bundle to create")
	version := flag.String("version", "1.0.0", "version")
	zipOut := flag.String("zip", "", "pack the bundle given by -out into this zip instead")
	flag.Parse()

	if *zipOut != "" {
		must(zipBundle(*out, *zipOut))
		fmt.Println("wrote", *zipOut)
		return
	}
	if *bin == "" || *icns == "" {
		fmt.Fprintln(os.Stderr, "usage: macapp -bin ShoreDrop -icns ShoreDrop.icns -out ShoreDrop.app")
		os.Exit(2)
	}
	must(os.RemoveAll(*out))
	contents := filepath.Join(*out, "Contents")
	must(os.MkdirAll(filepath.Join(contents, "MacOS"), 0o755))
	must(os.MkdirAll(filepath.Join(contents, "Resources"), 0o755))
	must(copyFile(*bin, filepath.Join(contents, "MacOS", "ShoreDrop"), 0o755))
	must(copyFile(*icns, filepath.Join(contents, "Resources", "ShoreDrop.icns"), 0o644))
	must(os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(strings.ReplaceAll(plist, "{{VERSION}}", *version)), 0o644))
	must(os.WriteFile(filepath.Join(contents, "PkgInfo"), []byte("APPL????"), 0o644))
	fmt.Println("wrote", *out)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	o, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(o, in); err != nil {
		o.Close()
		return err
	}
	return o.Close()
}

func zipBundle(bundle, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	base := filepath.Dir(bundle)
	now := time.Now()
	err = filepath.WalkDir(bundle, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		h.Name = rel
		h.Modified = now
		if d.IsDir() {
			h.Name += "/"
			h.Method = zip.Store
			_, err = zw.CreateHeader(h)
			return err
		}
		h.Method = zip.Deflate
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			w, err := zw.CreateHeader(h)
			if err != nil {
				return err
			}
			_, err = w.Write([]byte(target))
			return err
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}
