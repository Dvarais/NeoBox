package service

import (
	"NeoBox/backend/i18n"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"NeoBox/backend/security"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

// Update checking and installation: querying the GitHub release API,
// verifying the release signature and running the installer.

// currentVersion is the application version. Update this before each release.
// It lives here rather than inline in CheckUpdates so preparing a release does
// not depend on remembering to edit a string buried in a function body.
const currentVersion = "1.7.5"

// maxUpdateSize caps an installer download. The real installer is a few tens of
// megabytes; the limit only exists so a hostile or broken server cannot stream
// unbounded data onto the user's disk.
const maxUpdateSize = 256 * 1024 * 1024

// CheckUpdates queries GitHub API to check if a new version is available.
func (s *AppService) CheckUpdates() map[string]interface{} {
	response := map[string]interface{}{"available": false}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "https://api.github.com/repos/Dvarais/NeoBox/releases/latest", nil)
	if err != nil {
		return response
	}
	req.Header.Set("User-Agent", "NeoBox-App")

	resp, err := client.Do(req)
	if err != nil {
		return response
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return response
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1*1024*1024)) // 1 MB limit
	if err != nil {
		return response
	}

	var releaseInfo map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &releaseInfo); err != nil {
		return response
	}

	latestTag, _ := releaseInfo["tag_name"].(string)
	// TrimPrefix, not Replace: Replace(tag, "v", "", 1) strips the first "v"
	// anywhere in the string, so a tag like "version-2" became "ersion-2".
	latestVersion := strings.TrimPrefix(strings.TrimSpace(latestTag), "v")
	if s.isNewer(latestVersion, currentVersion) {
		htmlURL, _ := releaseInfo["html_url"].(string)
		body, _ := releaseInfo["body"].(string)

		response["available"] = true
		response["version"] = latestVersion
		response["url"] = htmlURL
		response["body"] = body

		// Extract download URL for the Windows .exe installer, prioritizing the setup/installer package
		if assets, ok := releaseInfo["assets"].([]interface{}); ok {
			var fallbackURL string
			var fallbackName string
			var exeURL string
			var exeName string

			for _, assetVal := range assets {
				if asset, ok := assetVal.(map[string]interface{}); ok {
					name, _ := asset["name"].(string)
					url, _ := asset["browser_download_url"].(string)
					nameLower := strings.ToLower(name)
					if strings.HasSuffix(nameLower, ".exe") {
						if strings.Contains(nameLower, "setup") || strings.Contains(nameLower, "installer") {
							exeURL = url
							exeName = name
							fallbackURL = "" // Found the preferred setup installer
							break
						} else if fallbackURL == "" {
							fallbackURL = url
							fallbackName = name
						}
					}
				}
			}
			if fallbackURL != "" {
				exeURL = fallbackURL
				exeName = fallbackName
			}

			if exeURL != "" {
				// An in-app install is offered only when a valid release signature
				// is available. Without one the response deliberately carries no
				// downloadUrl, and the frontend falls back to opening the release
				// page so the user downloads through the browser instead — see the
				// !update.downloadUrl branch in showUpdateModal().
				//
				// Publishing downloadUrl without a signature is what made the
				// verification below optional: an attacker able to shape the API
				// response only had to omit the .sig asset to disable it entirely.
				sigHex, sigErr := s.fetchAssetSignature(assets, exeName)
				if sigErr != nil {
					fmt.Printf("[update] no in-app install for %s: %v\n", exeName, sigErr)
					response["signatureMissing"] = true
				} else {
					response["downloadUrl"] = exeURL
					response["assetName"] = exeName
					response["signatureHex"] = sigHex
				}
			}
		}
	}

	return response
}

// fetchAssetSignature locates the "<exeName>.sig" release asset, downloads it
// and returns its contents once they are a well-formed Ed25519 signature.
// Any failure means no in-app install is offered.
func (s *AppService) fetchAssetSignature(assets []interface{}, exeName string) (string, error) {
	sigName := exeName + ".sig"
	for _, assetVal := range assets {
		asset, ok := assetVal.(map[string]interface{})
		if !ok {
			continue
		}
		if name, _ := asset["name"].(string); name != sigName {
			continue
		}

		sigURL, _ := asset["browser_download_url"].(string)
		// The signature URL comes from the same API response as everything else,
		// so it gets the same host check as the installer itself.
		if err := validateUpdateURL(sigURL); err != nil {
			return "", fmt.Errorf("signature asset %s: %w", sigName, err)
		}
		sigHex, err := s.downloadSignatureText(sigURL)
		if err != nil {
			return "", fmt.Errorf("failed to download %s: %w", sigName, err)
		}
		if err := security.ValidateSignatureHex(sigHex); err != nil {
			return "", fmt.Errorf("malformed %s: %w", sigName, err)
		}
		return sigHex, nil
	}
	return "", fmt.Errorf("release asset %s not found", sigName)
}

// validateUpdateURL restricts update traffic to HTTPS on GitHub-controlled hosts.
// This is what stops a tampered API response from pointing either the installer
// or its signature at an attacker-controlled server.
func validateUpdateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return fmt.Errorf("must use HTTPS, got scheme %q", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "github.com" ||
		strings.HasSuffix(host, ".github.com") ||
		strings.HasSuffix(host, ".githubusercontent.com") {
		return nil
	}
	return fmt.Errorf("untrusted host %q: expected github.com or githubusercontent.com", host)
}

// downloadSignatureText downloads the hex signature content from the given URL.
func (s *AppService) downloadSignatureText(downloadURL string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NeoBox-App")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status code: %d", resp.StatusCode)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(bodyBytes)), nil
}

// DownloadAndInstallUpdate downloads the installer from the given URL,
// reporting progress to the frontend, verifies its Ed25519 signature,
// and runs it upon successful verification.
func (s *AppService) DownloadAndInstallUpdate(downloadURL string, signatureHex string) error {
	s.wailsCtxMu.RLock()
	wCtx := s.wailsCtx
	s.wailsCtxMu.RUnlock()

	if wCtx == nil {
		return fmt.Errorf("wails context is not initialized")
	}

	// Security: only allow downloads from GitHub over HTTPS. This prevents an
	// attacker who can tamper with the GitHub API response from redirecting the
	// download to a malicious binary.
	if err := validateUpdateURL(downloadURL); err != nil {
		return fmt.Errorf("invalid download URL: %w", err)
	}

	// SECURITY: signature verification is mandatory and is checked BEFORE the
	// download starts. This binding is reachable from the frontend, so refusing
	// an empty signature here — not only when publishing downloadUrl — is what
	// makes an unsigned installer unrunnable through any path.
	if err := security.ValidateSignatureHex(signatureHex); err != nil {
		return fmt.Errorf("refusing to install unsigned update: %w", err)
	}

	// A fresh randomly-named directory rather than a fixed %TEMP%\neobox_update.exe:
	// a predictable path lets anything else running as this user pre-plant or swap
	// the file between verification and launch.
	tempDir, err := os.MkdirTemp("", "neobox-update-")
	if err != nil {
		return fmt.Errorf("failed to create update directory: %w", err)
	}
	// Restrict the directory to the current user as defence in depth.
	if err := security.ProtectFile(tempDir); err != nil {
		fmt.Printf("[update] warning: failed to restrict ACL on update directory: %v\n", err)
	}
	installerPath := filepath.Join(tempDir, "NeoBox_Setup.exe")

	// Start downloading in a background goroutine so we return immediately to the frontend,
	// allowing it to show the progress bar.
	go func() {
		fail := func(message string) {
			// The installer is untrusted or unusable — leave nothing executable behind.
			_ = os.RemoveAll(tempDir)
			wailsruntime.EventsEmit(wCtx, "update-error", message)
		}

		if err := s.performDownload(wCtx, downloadURL, installerPath); err != nil {
			fail(err.Error())
			return
		}

		// Verify the installer's Ed25519 signature before execution. This ensures
		// the binary was signed by the legitimate NeoBox release key, so a tampered
		// installer cannot run even if the release or the API response is forged.
		if verifyErr := security.VerifyFileSignature(installerPath, signatureHex); verifyErr != nil {
			fail(i18n.T(i18n.ErrSignatureRejected, verifyErr))
			return
		}

		// Download and verification complete!
		wailsruntime.EventsEmit(wCtx, "update-complete", nil)

		// Wait a split second for frontend to process before starting installer
		time.Sleep(1 * time.Second)

		// Start installer asynchronously using ShellExecute so that it can request UAC elevation
		verbPtr, _ := windows.UTF16PtrFromString("runas") // "runas" triggers Windows UAC prompt
		exePtr, _ := windows.UTF16PtrFromString(installerPath)
		dirPtr, _ := windows.UTF16PtrFromString(filepath.Dir(installerPath))
		argsPtr, _ := windows.UTF16PtrFromString("")

		if err := windows.ShellExecute(0, verbPtr, exePtr, argsPtr, dirPtr, windows.SW_SHOWNORMAL); err != nil {
			fail("Failed to start installer: " + err.Error())
			return
		}

		// The installer now owns tempDir — it must NOT be removed from here.
		// Quit our application immediately so the installer can overwrite NeoBox.exe
		s.Quit()
		wailsruntime.Quit(wCtx)
	}()

	return nil
}

func (s *AppService) performDownload(ctx context.Context, url, destPath string) error {
	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "NeoBox-App")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	totalSize := resp.ContentLength
	if totalSize > maxUpdateSize {
		return fmt.Errorf("update is too large: %d bytes (limit %d)", totalSize, maxUpdateSize)
	}

	buffer := make([]byte, 32*1024)
	var downloaded int64
	var lastPercent int = -1

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			// Cap the write as well as the advertised size: Content-Length is
			// supplied by the server and a lying one must not fill the user's disk.
			if downloaded+int64(n) > maxUpdateSize {
				return fmt.Errorf("update exceeded the %d byte size limit", maxUpdateSize)
			}
			_, writeErr := out.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			downloaded += int64(n)

			if totalSize > 0 {
				percentage := int(float64(downloaded) / float64(totalSize) * 100)
				if percentage != lastPercent {
					wailsruntime.EventsEmit(ctx, "update-progress", percentage)
					lastPercent = percentage
				}
			}
		}

		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	return nil
}

func (s *AppService) isNewer(latest, current string) bool {
	lParts := parseVersion(latest)
	cParts := parseVersion(current)

	for i := 0; i < len(lParts) || i < len(cParts); i++ {
		l := 0
		c := 0
		if i < len(lParts) {
			l = lParts[i]
		}
		if i < len(cParts) {
			c = cParts[i]
		}
		if l > c {
			return true
		}
		if l < c {
			return false
		}
	}
	return false
}

// parseVersion splits a version string into its numeric components.
//
// Each component keeps only its leading digits, so a pre-release suffix like
// "1.7.1-beta.2" reads as [1 7 1] instead of silently collapsing to [1 7 0] —
// strconv.Atoi("1-beta") fails and returns 0, which made a newer pre-release
// compare as OLDER than the release it follows.
func parseVersion(v string) []int {
	fields := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		end := 0
		for end < len(field) && field[end] >= '0' && field[end] <= '9' {
			end++
		}
		if end == 0 {
			// No leading digits at all: nothing comparable follows, so stop rather
			// than treat it as a zero component.
			break
		}
		n, err := strconv.Atoi(field[:end])
		if err != nil {
			break
		}
		parts = append(parts, n)

		if end < len(field) {
			// The field had trailing non-numeric text, as in the "1-beta" of
			// "1.7.1-beta.2". Everything past this point is pre-release metadata,
			// not further version components — without this the trailing ".2"
			// would be read as a fourth component and rank the build too high.
			break
		}
	}
	return parts
}
