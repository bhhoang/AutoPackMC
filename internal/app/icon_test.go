package app

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSquareIconCropsAndScales(t *testing.T) {
	// 300x200: a red band on the left, blue in the middle, green on the right.
	src := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			c := color.NRGBA{0, 0, 255, 255}
			if x < 50 {
				c = color.NRGBA{255, 0, 0, 255}
			} else if x >= 250 {
				c = color.NRGBA{0, 255, 0, 255}
			}
			src.SetNRGBA(x, y, c)
		}
	}
	out := squareIcon(src, 64)
	if out.Bounds().Dx() != 64 || out.Bounds().Dy() != 64 {
		t.Fatalf("size = %v", out.Bounds())
	}
	// The centred 200x200 square is all blue: the red and green bands are cropped.
	for _, p := range []image.Point{{0, 0}, {63, 63}, {32, 32}} {
		if c := out.NRGBAAt(p.X, p.Y); c != (color.NRGBA{0, 0, 255, 255}) {
			t.Errorf("pixel %v = %v, want blue", p, c)
		}
	}
}

func TestSquareIconKeepsTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 128, 128)) // fully transparent
	for y := 32; y < 96; y++ {
		for x := 32; x < 96; x++ {
			src.SetNRGBA(x, y, color.NRGBA{200, 100, 50, 255})
		}
	}
	out := squareIcon(src, 64)
	if a := out.NRGBAAt(0, 0).A; a != 0 {
		t.Errorf("corner alpha = %d, want transparent", a)
	}
	if c := out.NRGBAAt(32, 32); c != (color.NRGBA{200, 100, 50, 255}) {
		t.Errorf("centre = %v", c)
	}
}

func TestSetServerIconFromFile(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)

	var jpg bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 500, 400))
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, jpg.String())

	url, err := s.SetServerIconFromFile(rec.ID, src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("url = %.40q", url)
	}
	f, err := os.Open(filepath.Join(rec.Dir, "server-icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(f)
	f.Close()
	if err != nil || cfg.Width != 64 || cfg.Height != 64 {
		t.Fatalf("server-icon.png is %dx%d (%v), want 64x64 PNG", cfg.Width, cfg.Height, err)
	}
	if v := s.view(mustServer(t, s, rec.ID)); v.Icon != url {
		t.Error("the server view does not carry the new picture")
	}

	if err := s.RemoveServerIcon(rec.ID); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(rec.Dir, "server-icon.png")) || s.view(mustServer(t, s, rec.ID)).Icon != "" {
		t.Error("the picture was not removed")
	}
}

func TestSetServerIconRejectsNonPictures(t *testing.T) {
	s, _ := newTestService(t)
	rec := addServer(t, s)
	src := filepath.Join(t.TempDir(), "notes.png")
	writeFile(t, src, "not a picture")
	var ue *Error
	if _, err := s.SetServerIconFromFile(rec.ID, src); !errors.As(err, &ue) || ue.Code != "bad_picture" {
		t.Fatalf("err = %v, want bad_picture", err)
	}
	if exists(filepath.Join(rec.Dir, "server-icon.png")) {
		t.Error("a broken picture was written")
	}
}
