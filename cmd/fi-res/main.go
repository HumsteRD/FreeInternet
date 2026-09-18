// fi-res готовит ресурсы Windows для программ FI: значок, манифест и сведения
// о версии. Пишет rsrc_windows_amd64.syso рядом с main.go каждой программы — go build
// подхватывает такие файлы сам — и build/fi.ico. Запускается из scripts/build.ps1.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"

	"fi/internal/brand"
)

type program struct {
	dir, file, description string
	level                  winres.ExecutionLevel
	accent                 brand.Accent // цвет стойки «I»: чтобы файлы различались в папке
}

var programs = []program{
	{"cmd/fi", "fi.exe", "FI — окно и значок в трее", winres.AsInvoker, brand.AccentRed},
	{"cmd/fi-service", "fi-service.exe", "FI — служба обхода блокировок", winres.AsInvoker, brand.AccentViolet},
	// Установщик и программа удаления сразу просят права администратора — Windows рисует на них щит.
	{"cmd/fi-setup", "fi-setup.exe", "Установка и удаление FI", winres.RequireAdministrator, brand.AccentGrey},
	{"cmd/fi-probe", "fi-probe.exe", "FI — стенд проверки стратегий", winres.AsInvoker, brand.AccentAmber},
}

func main() {
	ver := flag.String("version", "0.1.0", "версия программ")
	preview := flag.String("preview", "", "только сохранить PNG со всеми кадрами значка — посмотреть глазами")
	flag.Parse()

	var err error
	if *preview != "" {
		err = savePreview(*preview)
	} else {
		err = run(*ver)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
}

func run(ver string) error {
	icon, err := winres.NewIconFromImages(brand.AppIconImages())
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join("build", "fi.ico"), icon.SaveICO); err != nil {
		return err
	}

	for _, p := range programs {
		programIcon := icon
		if p.accent != brand.AccentRed {
			if programIcon, err = winres.NewIconFromImages(brand.AppIconImagesFor(p.accent)); err != nil {
				return err
			}
		}
		rs := winres.ResourceSet{}
		// 3 — номер, под которым Wails ищет значок приложения; 32512 (IDI_APPLICATION) — значок окна.
		for _, id := range []winres.ID{3, 32512} {
			if err := rs.SetIcon(id, programIcon); err != nil {
				return err
			}
		}
		rs.SetManifest(winres.AppManifest{
			Identity:            winres.AssemblyIdentity{Name: "FI." + strings.TrimSuffix(p.file, ".exe"), Version: versionParts(ver)},
			Description:         p.description,
			Compatibility:       winres.Win10AndAbove,
			ExecutionLevel:      p.level,
			DPIAwareness:        winres.DPIPerMonitorV2,
			UseCommonControlsV6: true,
			LongPathAware:       true,
		})

		var vi version.Info
		vi.SetFileVersion(ver)
		vi.SetProductVersion(ver)
		fields := map[string]string{
			version.ProductName:      "FI",
			version.CompanyName:      "FI",
			version.FileDescription:  p.description,
			version.OriginalFilename: p.file,
			version.Comments:         "Стратегии: Flowseal/zapret-discord-youtube. Движок: bol-van/zapret.",
		}
		for key, value := range fields {
			if err := vi.Set(version.LangDefault, key, value); err != nil {
				return err
			}
		}
		rs.SetVersionInfo(vi)

		syso := filepath.Join(p.dir, "rsrc_windows_amd64.syso")
		if err := writeFile(syso, func(w io.Writer) error { return rs.WriteObject(w, winres.ArchAMD64) }); err != nil {
			return fmt.Errorf("%s: %w", p.file, err)
		}
		fmt.Printf("  %-18s %s\n", p.file, p.description)
	}
	return nil
}

func writeFile(path string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// savePreview рисует все кадры значка на тёмном и светлом фоне, а мелкие — ещё и под лупой.
func savePreview(path string) error {
	const gap = 24
	dark := image.NewUniform(color.NRGBA{0x0B, 0x0A, 0x0D, 0xFF})
	light := image.NewUniform(color.NRGBA{0xEE, 0xEA, 0xF1, 0xFF})

	width := gap
	for _, s := range brand.IconSizes {
		width += s + gap
	}
	row := 256 + 2*gap
	zoomed := []struct{ size, k int }{{16, 8}, {20, 6}, {24, 5}, {32, 4}, {48, 3}}
	zoomRow := 128 + 2*gap

	sheet := image.NewNRGBA(image.Rect(0, 0, width, 2*row+zoomRow))
	draw.Draw(sheet, image.Rect(0, 0, width, row), dark, image.Point{}, draw.Src)
	draw.Draw(sheet, image.Rect(0, row, width, 2*row), light, image.Point{}, draw.Src)
	draw.Draw(sheet, image.Rect(0, 2*row, width, 2*row+zoomRow), dark, image.Point{}, draw.Src)

	for r := range 2 {
		x := gap
		for _, s := range brand.IconSizes {
			y := r*row + gap + 256 - s
			draw.Draw(sheet, image.Rect(x, y, x+s, y+s), brand.AppIcon(s), image.Point{}, draw.Over)
			x += s + gap
		}
	}
	x := gap
	for _, z := range zoomed {
		big := scaleUp(brand.AppIcon(z.size), z.k)
		side := z.size * z.k
		y := 2*row + gap
		draw.Draw(sheet, image.Rect(x, y, x+side, y+side), big, image.Point{}, draw.Over)
		x += side + gap
	}
	return writeFile(path, func(w io.Writer) error { return png.Encode(w, sheet) })
}

// scaleUp увеличивает картинку без сглаживания, чтобы были видны пиксели.
func scaleUp(src *image.NRGBA, k int) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx()*k, b.Dy()*k))
	for y := range dst.Bounds().Dy() {
		for x := range dst.Bounds().Dx() {
			dst.SetNRGBA(x, y, src.NRGBAAt(x/k, y/k))
		}
	}
	return dst
}

// versionParts: «0.1.0» → [0 1 0 0].
func versionParts(v string) [4]uint16 {
	var parts [4]uint16
	for i, s := range strings.SplitN(v, ".", 4) {
		n, _ := strconv.Atoi(s)
		parts[i] = uint16(n)
	}
	return parts
}
