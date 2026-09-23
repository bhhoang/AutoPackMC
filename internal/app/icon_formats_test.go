package app

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"path/filepath"
	"strings"
	"testing"
)

// The pictures in testdata are 80x60, red on the left half and blue on the
// right. picture-alpha.webp also has a transparent block in the top left
// corner of its centred square.
func TestServerIconFromOtherFormats(t *testing.T) {
	for _, name := range []string{"picture-lossy.webp", "picture-alpha.webp", "picture.bmp", "picture.tiff"} {
		t.Run(name, func(t *testing.T) {
			s, _ := newTestService(t)
			rec := addServer(t, s)
			url, err := s.SetServerIconFromFile(rec.ID, filepath.Join("testdata", name))
			if err != nil {
				t.Fatalf("SetServerIconFromFile: %v", err)
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("not saved as PNG: %v", err)
			}
			if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
				t.Fatalf("size %v, want 64x64", b)
			}
			// Lossy formats shift colours a little, so check the dominant channel.
			left, right := img.At(8, 40), img.At(56, 40)
			lr, _, lb, _ := left.RGBA()
			rr, _, rb, _ := right.RGBA()
			if lr < lb || rb < rr {
				t.Errorf("left %v should be red and right %v blue", left, right)
			}
			if name == "picture-alpha.webp" {
				if _, _, _, a := img.At(2, 2).RGBA(); a != 0 {
					t.Errorf("corner alpha = %d, want transparent", a)
				}
			}
		})
	}
}
