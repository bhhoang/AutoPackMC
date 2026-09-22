package java

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// jdkPackage is the downloadable archive of a JDK release.
type jdkPackage struct {
	Name     string `json:"name"`
	Link     string `json:"link"`
	Checksum string `json:"checksum"` // SHA-256, hex
}

// latestPackage asks the Adoptium API for the latest Temurin JDK archive of
// the given major version for osName/archName (Adoptium's names).
func latestPackage(version int, osName, archName string) (*jdkPackage, error) {
	url := fmt.Sprintf("%s/assets/latest/%d/hotspot?architecture=%s&image_type=jdk&os=%s&vendor=eclipse",
		adoptiumAPIBase, version, archName, osName)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) // #nosec G107
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Adoptium API returned HTTP %d for %s", resp.StatusCode, url)
	}

	var assets []struct {
		Binary struct {
			Package jdkPackage `json:"package"`
		} `json:"binary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&assets); err != nil {
		return nil, fmt.Errorf("parse Adoptium response: %w", err)
	}
	if len(assets) == 0 || assets[0].Binary.Package.Link == "" {
		return nil, fmt.Errorf("no Temurin %d JDK is published for %s/%s", version, osName, archName)
	}
	pkg := assets[0].Binary.Package
	if pkg.Checksum == "" {
		return nil, fmt.Errorf("Adoptium published no checksum for %s", pkg.Name)
	}
	return &pkg, nil
}

// verifySHA256 checks that the file at path has the given hex SHA-256.
func verifySHA256(path, want string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("archive checksum mismatch: SHA-256 %s, want %s", got, want)
	}
	return nil
}
