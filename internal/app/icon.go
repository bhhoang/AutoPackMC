package app

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	_ "image/gif"  // decode GIF pictures
	_ "image/jpeg" // decode JPEG pictures
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	_ "golang.org/x/image/bmp"  // decode BMP pictures
	_ "golang.org/x/image/tiff" // decode TIFF pictures
	_ "golang.org/x/image/webp" // decode WebP pictures
)

// pictureFilter lists the picture files AutoPack can read. Each is converted
// to PNG, the only format Minecraft reads for server-icon.png.
const pictureFilter = "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp;*.tif;*.tiff"

// iconSize is the size Minecraft needs for server-icon.png.
const iconSize = 64

// maxIconSource caps the pictures AutoPack reads (bytes).
const maxIconSource = 20 << 20

func iconPath(dir string) string { return filepath.Join(dir, "server-icon.png") }

// ServerIcon returns the server's picture as a data: URL, or "" when it has none.
func (s *Service) ServerIcon(id string) (string, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return "", &Error{Code: "no_server"}
	}
	return iconDataURL(rec.Dir), nil
}

func iconDataURL(dir string) string {
	data, err := os.ReadFile(iconPath(dir))
	if err != nil || len(data) > 256<<10 {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

// PickServerIcon asks for a picture and makes it the server's picture. It
// returns the new picture as a data: URL, or "" if the user cancelled.
func (s *Service) PickServerIcon(id string) (string, error) {
	files, err := s.ui.PickFiles("", pictureFilter, false)
	if err != nil || len(files) == 0 {
		return "", err
	}
	return s.SetServerIconFromFile(id, files[0])
}

// SetServerIconFromFile makes the picture at path the server's picture.
func (s *Service) SetServerIconFromFile(id, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", userError(err)
	}
	defer f.Close()
	return s.setServerIcon(id, f)
}

// UseModpackLogoAsIcon makes the modpack's CurseForge logo the server's picture.
func (s *Service) UseModpackLogoAsIcon(id string) (string, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return "", &Error{Code: "no_server"}
	}
	if rec.LogoURL == "" {
		return "", &Error{Code: "no_logo"}
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(rec.LogoURL)
	if err != nil {
		return "", userError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &Error{Code: "network", Detail: rec.LogoURL + ": " + resp.Status}
	}
	return s.setServerIcon(id, resp.Body)
}

// RemoveServerIcon deletes the server's picture.
func (s *Service) RemoveServerIcon(id string) error {
	rec, ok := s.store.Server(id)
	if !ok {
		return &Error{Code: "no_server"}
	}
	if err := os.Remove(iconPath(rec.Dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return userError(err)
	}
	s.emitServer(id)
	return nil
}

func (s *Service) setServerIcon(id string, r io.Reader) (string, error) {
	rec, ok := s.store.Server(id)
	if !ok {
		return "", &Error{Code: "no_server"}
	}
	data, err := io.ReadAll(io.LimitReader(r, maxIconSource+1))
	if err != nil {
		return "", userError(err)
	}
	if len(data) > maxIconSource {
		return "", &Error{Code: "bad_picture", Detail: "the picture is larger than 20 MB"}
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// HEIC, AVIF, SVG and other formats land here.
		return "", &Error{Code: "bad_picture", Detail: err.Error()}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, squareIcon(src, iconSize)); err != nil {
		return "", userError(err)
	}
	if err := os.MkdirAll(rec.Dir, 0o755); err != nil {
		return "", userError(err)
	}
	tmp := iconPath(rec.Dir) + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return "", userError(err)
	}
	if err := os.Rename(tmp, iconPath(rec.Dir)); err != nil {
		return "", userError(err)
	}
	s.emitServer(id)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), nil
}

// squareIcon crops src to its centred square and scales it to size×size.
// Shrinking averages every source pixel under each target pixel, so photos
// stay smooth; growing (pixel art smaller than 64) repeats pixels, so it
// stays sharp.
func squareIcon(src image.Image, size int) *image.NRGBA {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	if side <= 0 {
		return dst
	}
	for ty := 0; ty < size; ty++ {
		sy0 := y0 + ty*side/size
		sy1 := max(y0+(ty+1)*side/size, sy0+1)
		for tx := 0; tx < size; tx++ {
			sx0 := x0 + tx*side/size
			sx1 := max(x0+(tx+1)*side/size, sx0+1)
			var r, g, bl, a, n uint64
			for y := sy0; y < sy1; y++ {
				for x := sx0; x < sx1; x++ {
					c := color.NRGBA64Model.Convert(src.At(x, y)).(color.NRGBA64)
					// Weight colour by alpha so transparent edges do not darken.
					r += uint64(c.R) * uint64(c.A)
					g += uint64(c.G) * uint64(c.A)
					bl += uint64(c.B) * uint64(c.A)
					a += uint64(c.A)
					n++
				}
			}
			if a == 0 {
				continue
			}
			dst.SetNRGBA(tx, ty, color.NRGBA{
				R: uint8(r / a >> 8), G: uint8(g / a >> 8), B: uint8(bl / a >> 8), A: uint8(a / n >> 8),
			})
		}
	}
	return dst
}
