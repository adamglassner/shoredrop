// Command mkres generates Windows resources for ShoreDrop: a multi-size
// icon (.ico) drawn from the logo geometry, an application manifest and
// version information, written as a COFF object (.syso) that the Go
// linker embeds automatically.
//
//	go run ./tools/mkres -o cmd/shoredrop/rsrc_windows_amd64.syso -ico build/ShoreDrop.ico \
//	    -name ShoreDrop -desc "ShoreDrop FTP/SFTP client" -file ShoreDrop.exe
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"sort"
	"strings"
	"unicode/utf16"

	"shoredrop/internal/logo"
)

var sizes = []int{16, 20, 24, 32, 40, 48, 64, 256}

func main() {
	out := flag.String("o", "rsrc.syso", "output .syso")
	icoOut := flag.String("ico", "", "also write the .ico here")
	pngOut := flag.String("png", "", "also write a 256px PNG here")
	name := flag.String("name", "ShoreDrop", "product name")
	desc := flag.String("desc", "ShoreDrop", "file description")
	file := flag.String("file", "ShoreDrop.exe", "original file name")
	version := flag.String("version", "1.0.0", "version x.y.z")
	flag.Parse()

	var images [][]byte
	var imgs []*image.RGBA
	for _, s := range sizes {
		img := logo.Rasterize(s, logo.Teal, 0.04)
		imgs = append(imgs, img)
		if s >= 256 {
			var b bytes.Buffer
			png.Encode(&b, img)
			images = append(images, b.Bytes())
		} else {
			images = append(images, dib(img))
		}
	}
	if *icoOut != "" {
		must(os.WriteFile(*icoOut, icoFile(images), 0o644))
	}
	if *pngOut != "" {
		var b bytes.Buffer
		png.Encode(&b, imgs[len(imgs)-1])
		must(os.WriteFile(*pngOut, b.Bytes(), 0o644))
	}

	res := map[uint16]map[uint16][]byte{}
	add := func(typ, id uint16, data []byte) {
		if res[typ] == nil {
			res[typ] = map[uint16][]byte{}
		}
		res[typ][id] = data
	}
	// RT_ICON = 3, RT_GROUP_ICON = 14, RT_VERSION = 16, RT_MANIFEST = 24
	var grp bytes.Buffer
	binary.Write(&grp, binary.LittleEndian, [3]uint16{0, 1, uint16(len(images))})
	for i, data := range images {
		id := uint16(i + 1)
		add(3, id, data)
		s := sizes[i]
		w := byte(s)
		if s >= 256 {
			w = 0
		}
		grp.Write([]byte{w, w, 0, 0})
		binary.Write(&grp, binary.LittleEndian, uint16(1))
		binary.Write(&grp, binary.LittleEndian, uint16(32))
		binary.Write(&grp, binary.LittleEndian, uint32(len(data)))
		binary.Write(&grp, binary.LittleEndian, id)
	}
	add(14, 1, grp.Bytes())
	add(16, 1, versionInfo(*version, *name, *desc, *file))
	add(24, 1, []byte(manifest(*name, *version)))
	must(os.WriteFile(*out, coff(res), 0o644))
	fmt.Println("wrote", *out)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// dib encodes an image as a 32-bit icon DIB (BITMAPINFOHEADER + BGRA + AND mask).
func dib(img *image.RGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, struct {
		Size                  uint32
		Width, Height         int32
		Planes, BitCount      uint16
		Compression, SizeImg  uint32
		XPels, YPels          int32
		ClrUsed, ClrImportant uint32
	}{40, int32(w), int32(h * 2), 1, 32, 0, uint32(w * h * 4), 0, 0, 0, 0})
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			// DIB icons use straight (non-premultiplied) alpha
			r, g, bl := c.R, c.G, c.B
			if c.A > 0 && c.A < 255 {
				r = uint8(int(c.R) * 255 / int(c.A))
				g = uint8(int(c.G) * 255 / int(c.A))
				bl = uint8(int(c.B) * 255 / int(c.A))
			}
			b.Write([]byte{bl, g, r, c.A})
		}
	}
	stride := ((w + 31) / 32) * 4
	for y := 0; y < h; y++ {
		b.Write(make([]byte, stride)) // AND mask all zero: alpha channel decides
	}
	return b.Bytes()
}

func icoFile(images [][]byte) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, [3]uint16{0, 1, uint16(len(images))})
	off := 6 + 16*len(images)
	for i, data := range images {
		s := sizes[i]
		w := byte(s)
		if s >= 256 {
			w = 0
		}
		b.Write([]byte{w, w, 0, 0})
		binary.Write(&b, binary.LittleEndian, uint16(1))
		binary.Write(&b, binary.LittleEndian, uint16(32))
		binary.Write(&b, binary.LittleEndian, uint32(len(data)))
		binary.Write(&b, binary.LittleEndian, uint32(off))
		off += len(data)
	}
	for _, d := range images {
		b.Write(d)
	}
	return b.Bytes()
}

func manifest(name, version string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
  <assemblyIdentity version="` + version + `.0" processorArchitecture="amd64" name="` + name + `" type="win32"/>
  <description>` + name + `</description>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security><requestedPrivileges><requestedExecutionLevel level="asInvoker" uiAccess="false"/></requestedPrivileges></security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
      <supportedOS Id="{1f676c76-80e1-4239-95bb-83d0f6d0da78}"/>
      <supportedOS Id="{4a2f28e3-53b9-4441-ba9c-d69d4a4a6e38}"/>
    </application>
  </compatibility>
  <asmv3:application>
    <asmv3:windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2</dpiAwareness>
      <longPathAware xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">true</longPathAware>
    </asmv3:windowsSettings>
  </asmv3:application>
</assembly>
`
}

// ---------------- VERSIONINFO ----------------

func u16z(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2+2)
	for _, c := range u {
		b = binary.LittleEndian.AppendUint16(b, c)
	}
	return append(b, 0, 0)
}

func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// node builds a version-info block: header, key, value and children.
func node(key string, value []byte, text bool, children ...[]byte) []byte {
	b := make([]byte, 6)
	b = append(b, u16z(key)...)
	b = pad4(b)
	b = append(b, value...)
	for _, c := range children {
		b = pad4(b)
		b = append(b, c...)
	}
	vl := len(value)
	typ := uint16(0)
	if text {
		vl = len(value) / 2
		typ = 1
	}
	binary.LittleEndian.PutUint16(b[0:], uint16(len(b)))
	binary.LittleEndian.PutUint16(b[2:], uint16(vl))
	binary.LittleEndian.PutUint16(b[4:], typ)
	return b
}

func versionInfo(version, name, desc, file string) []byte {
	var v [4]uint16
	parts := strings.Split(version, ".")
	for i := 0; i < len(parts) && i < 4; i++ {
		fmt.Sscanf(parts[i], "%d", &v[i])
	}
	ms := uint32(v[0])<<16 | uint32(v[1])
	ls := uint32(v[2])<<16 | uint32(v[3])
	fixed := new(bytes.Buffer)
	binary.Write(fixed, binary.LittleEndian, []uint32{0xFEEF04BD, 0x00010000, ms, ls, ms, ls, 0x3F, 0, 0x40004, 1, 0, 0, 0})
	str := func(k, val string) []byte { return node(k, u16z(val), true) }
	full := fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])
	table := node("040904B0", nil, true,
		str("CompanyName", name),
		str("FileDescription", desc),
		str("FileVersion", full),
		str("InternalName", strings.TrimSuffix(file, ".exe")),
		str("LegalCopyright", "© 2026 "+name),
		str("OriginalFilename", file),
		str("ProductName", name),
		str("ProductVersion", version),
	)
	sfi := node("StringFileInfo", nil, true, table)
	trans := make([]byte, 4)
	binary.LittleEndian.PutUint16(trans[0:], 0x0409)
	binary.LittleEndian.PutUint16(trans[2:], 0x04B0)
	vfi := node("VarFileInfo", nil, true, node("Translation", trans, false))
	root := node("VS_VERSION_INFO", fixed.Bytes(), false, sfi, vfi)
	return root
}

// ---------------- COFF .rsrc writer ----------------

func coff(res map[uint16]map[uint16][]byte) []byte {
	types := make([]int, 0, len(res))
	for t := range res {
		types = append(types, int(t))
	}
	sort.Ints(types)

	// Layout: root dir, type dirs, name dirs (language level), data entries, then data.
	type leaf struct {
		data []byte
	}
	var sec bytes.Buffer
	dirSize := func(n int) int { return 16 + 8*n }

	// Pre-compute offsets.
	rootOff := 0
	off := dirSize(len(types))
	typeOff := map[int]int{}
	for _, t := range types {
		typeOff[t] = off
		off += dirSize(len(res[uint16(t)]))
	}
	nameOff := map[[2]int]int{}
	for _, t := range types {
		ids := sortedIDs(res[uint16(t)])
		for _, id := range ids {
			nameOff[[2]int{t, id}] = off
			off += dirSize(1)
		}
	}
	dataEntryOff := map[[2]int]int{}
	for _, t := range types {
		for _, id := range sortedIDs(res[uint16(t)]) {
			dataEntryOff[[2]int{t, id}] = off
			off += 16
		}
	}
	dataOff := map[[2]int]int{}
	for _, t := range types {
		for _, id := range sortedIDs(res[uint16(t)]) {
			for off%8 != 0 {
				off++
			}
			dataOff[[2]int{t, id}] = off
			off += len(res[uint16(t)][uint16(id)])
		}
	}
	buf := make([]byte, off)
	putDir := func(at, n int) {
		binary.LittleEndian.PutUint16(buf[at+14:], uint16(n))
	}
	putEntry := func(at, i int, id uint32, target uint32) {
		p := at + 16 + 8*i
		binary.LittleEndian.PutUint32(buf[p:], id)
		binary.LittleEndian.PutUint32(buf[p+4:], target)
	}
	putDir(rootOff, len(types))
	var relocs []int
	for i, t := range types {
		putEntry(rootOff, i, uint32(t), uint32(typeOff[t])|0x80000000)
		ids := sortedIDs(res[uint16(t)])
		putDir(typeOff[t], len(ids))
		for j, id := range ids {
			k := [2]int{t, id}
			putEntry(typeOff[t], j, uint32(id), uint32(nameOff[k])|0x80000000)
			putDir(nameOff[k], 1)
			putEntry(nameOff[k], 0, 0x0409, uint32(dataEntryOff[k]))
			de := dataEntryOff[k]
			data := res[uint16(t)][uint16(id)]
			binary.LittleEndian.PutUint32(buf[de:], uint32(dataOff[k])) // relocated to RVA
			binary.LittleEndian.PutUint32(buf[de+4:], uint32(len(data)))
			relocs = append(relocs, de)
			copy(buf[dataOff[k]:], data)
		}
	}
	_ = leaf{}
	sec.Write(buf)

	const hdr = 20 + 40
	rawPtr := hdr
	relPtr := rawPtr + sec.Len()
	symPtr := relPtr + 10*len(relocs)
	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, struct {
		Machine              uint16
		NumberOfSections     uint16
		TimeDateStamp        uint32
		PointerToSymbolTable uint32
		NumberOfSymbols      uint32
		SizeOfOptionalHeader uint16
		Characteristics      uint16
	}{0x8664, 1, 0, uint32(symPtr), 1, 0, 0})
	var name [8]byte
	copy(name[:], ".rsrc")
	binary.Write(&out, binary.LittleEndian, struct {
		Name                 [8]byte
		VirtualSize          uint32
		VirtualAddress       uint32
		SizeOfRawData        uint32
		PointerToRawData     uint32
		PointerToRelocations uint32
		PointerToLinenumbers uint32
		NumberOfRelocations  uint16
		NumberOfLinenumbers  uint16
		Characteristics      uint32
	}{name, 0, 0, uint32(sec.Len()), uint32(rawPtr), uint32(relPtr), 0, uint16(len(relocs)), 0, 0x40000040})
	out.Write(sec.Bytes())
	for _, r := range relocs {
		binary.Write(&out, binary.LittleEndian, uint32(r)) // VirtualAddress
		binary.Write(&out, binary.LittleEndian, uint32(0)) // symbol index
		binary.Write(&out, binary.LittleEndian, uint16(3)) // IMAGE_REL_AMD64_ADDR32NB
	}
	// symbol table: the section symbol
	var sym [18]byte
	copy(sym[:8], ".rsrc")
	binary.LittleEndian.PutUint32(sym[8:], 0)  // value
	binary.LittleEndian.PutUint16(sym[12:], 1) // section number
	binary.LittleEndian.PutUint16(sym[14:], 0) // type
	sym[16] = 3                                // IMAGE_SYM_CLASS_STATIC
	sym[17] = 0
	out.Write(sym[:])
	binary.Write(&out, binary.LittleEndian, uint32(4)) // empty string table
	return out.Bytes()
}

func sortedIDs(m map[uint16][]byte) []int {
	var ids []int
	for id := range m {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	return ids
}
