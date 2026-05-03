package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/bhhoang/AutoPackMC/internal/resolver"
	"github.com/bhhoang/AutoPackMC/pkg/logger"
	"github.com/bhhoang/AutoPackMC/pkg/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newDownloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download mods or files",
		RunE:  runDownload,
	}

	cmd.Flags().String("mod", "", "CurseForge mod ID (project ID)")
	cmd.Flags().String("file", "", "CurseForge file ID")
	cmd.Flags().String("output", "./mods", "output directory")
	cmd.Flags().String("url", "", "direct URL to download")
	cmd.Flags().String("api-key", "", "CurseForge API key")

	return cmd
}

func runDownload(cmd *cobra.Command, _ []string) error {
	log := logger.Get()

	modID, _ := cmd.Flags().GetString("mod")
	fileID, _ := cmd.Flags().GetString("file")
	output, _ := cmd.Flags().GetString("output")
	url, _ := cmd.Flags().GetString("url")
	apiKey, _ := cmd.Flags().GetString("api-key")

	if apiKey == "" {
		apiKey = viper.GetString("curseforge_api_key")
	}
	if output == "" {
		output = "./mods"
	}

	if err := utils.EnsureDir(output); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if modID != "" && fileID != "" {
		return downloadCurseForgeMod(modID, fileID, output, apiKey)
	}

	if url != "" {
		if resolver.IsCurseForgeFileURL(url) {
			// URL points to a specific file: .../mc-mods/{slug}/files/{fileId}
			slug, fileID, err := resolver.ExtractFileURL(url)
			if err != nil {
				return fmt.Errorf("parse CurseForge URL: %w", err)
			}
			log.Info().Str("slug", slug).Int("fileID", fileID).Msg("resolving CurseForge mod ID from URL")
			r := resolver.New(apiKey)
			modIDInt, err := r.ResolveModID(slug)
			if err != nil {
				return fmt.Errorf("resolve mod ID for %q: %w", slug, err)
			}
			return downloadCurseForgeMod(strconv.Itoa(modIDInt), strconv.Itoa(fileID), output, apiKey)
		}

		if resolver.IsCurseForgeURL(url) {
			// URL points to a mod page with no specific file: resolve latest file.
			log.Info().Str("url", url).Msg("resolving latest file for CurseForge mod")
			r := resolver.New(apiKey)
			downloadURL, err := r.Resolve(url)
			if err != nil {
				return fmt.Errorf("resolve CurseForge mod: %w", err)
			}
			filename := filepath.Base(downloadURL)
			dest := filepath.Join(output, filename)
			log.Info().Str("url", downloadURL).Str("dest", dest).Msg("downloading mod")
			headers := map[string]string{}
			if apiKey != "" {
				headers["X-Api-Key"] = apiKey
			}
			if err := utils.DownloadFile(downloadURL, dest, headers); err != nil {
				return fmt.Errorf("download: %w", err)
			}
			log.Info().Str("file", dest).Msg("mod downloaded")
			return nil
		}

		filename := filepath.Base(url)
		dest := filepath.Join(output, filename)
		log.Info().Str("url", url).Str("dest", dest).Msg("downloading file")
		if err := utils.DownloadFile(url, dest, nil); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		log.Info().Str("file", dest).Msg("downloaded")
		return nil
	}

	return fmt.Errorf("provide --mod and --file, or --url")
}

func downloadCurseForgeMod(modID, fileID, output, apiKey string) error {
	log := logger.Get()

	projectID, err := strconv.Atoi(modID)
	if err != nil {
		return fmt.Errorf("invalid mod ID: %w", err)
	}
	fileIDInt, err := strconv.Atoi(fileID)
	if err != nil {
		return fmt.Errorf("invalid file ID: %w", err)
	}

	fileInfoURL := fmt.Sprintf("https://www.curseforge.com/api/v1/mods/%d/files/%d", projectID, fileIDInt)
	resp, err := http.Get(fileInfoURL)
	if err != nil {
		return fmt.Errorf("fetch file info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CurseForge API returned HTTP %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Data struct {
			FileName string `json:"fileName"`
			DownloadURL string `json:"downloadUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	filename := result.Data.FileName
	downloadURL := result.Data.DownloadURL

	if filename == "" {
		filename = fmt.Sprintf("mod-%d-%d.jar", projectID, fileIDInt)
	}
	if downloadURL == "" {
		downloadURL = fmt.Sprintf("https://www.curseforge.com/api/v1/mods/%d/files/%d/download", projectID, fileIDInt)
	}

	dest := filepath.Join(output, filename)
	log.Info().
		Int("projectID", projectID).
		Int("fileID", fileIDInt).
		Str("filename", filename).
		Msg("downloading mod")

	headers := map[string]string{}
	if apiKey != "" {
		headers["X-Api-Key"] = apiKey
	}

	if err := utils.DownloadFile(downloadURL, dest, headers); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	log.Info().Str("file", dest).Msg("mod downloaded")
	return nil
}