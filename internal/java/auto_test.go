package java

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRequiredVersion(t *testing.T) {
	cases := map[string]int{
		"1.7.10": 8,
		"1.12.2": 8,
		"1.16.5": 8,
		"1.17.1": 17,
		"1.18.2": 17,
		"1.20.1": 17,
		"1.20.4": 17,
		"1.20.5": 21,
		"1.20.6": 21,
		"1.21":   21,
		"1.21.1": 21,
		"26.1":   25,
		"":       0,
		"latest": 0,
	}
	for mc, want := range cases {
		if got := RequiredVersion(mc); got != want {
			t.Errorf("RequiredVersion(%q) = %d, want %d", mc, got, want)
		}
	}
}

func TestParseMajor(t *testing.T) {
	cases := map[string]int{
		"1.8.0_401": 8,
		"17.0.12":   17,
		"21":        21,
		"25-ea":     25,
	}
	for v, want := range cases {
		got, err := parseMajor(v)
		if err != nil || got != want {
			t.Errorf("parseMajor(%q) = %d, %v; want %d", v, got, err, want)
		}
	}
}

func TestFindLocalPicksNewest(t *testing.T) {
	dir := t.TempDir()
	if FindLocal(dir) != "" {
		t.Fatal("expected no local JDK in empty dir")
	}
	for _, v := range []string{"jdk-8", "jdk-21", "jdk-17"} {
		bin := filepath.Join(dir, v, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, JavaBinaryName()), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(dir, "jdk-21", "bin", JavaBinaryName())
	if got := FindLocal(dir); got != want {
		t.Errorf("FindLocal = %q, want %q", got, want)
	}
}
