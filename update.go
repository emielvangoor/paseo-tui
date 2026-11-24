package main

// Update notices and self-update.
//
// The app checks GitHub for a newer release at most once a day, in the background, and shows it in the
// footer. `paseo-tui update` installs it. A download is installed only when checksums.txt carries a valid
// Ed25519 signature from the release key (keys/release.pub.pem, embedded below) and the archive's SHA-256
// matches. The private key is only available to the release workflow.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/mod/semver"
)

const (
	repoSlug       = "emielvangoor/paseo-tui"
	checkInterval  = 24 * time.Hour
	noCheckEnv     = "PASEO_TUI_NO_UPDATE_CHECK"
	maxArchiveSize = 256 << 20
)

//go:embed keys/release.pub.pem
var releasePublicKeyPEM []byte

// updateCheck is turned off by --no-update-check or PASEO_TUI_NO_UPDATE_CHECK.
var updateCheck = true

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (r *ghRelease) assetURL(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// isRelease reports whether v is a released version (not "dev" or a dirty local build).
func isRelease(v string) bool {
	return semver.IsValid(v) && !strings.Contains(v, "dirty") && !strings.Contains(semver.Prerelease(v), "-g")
}

// newer reports whether latest is a newer version than current.
func newer(latest, current string) bool {
	return semver.IsValid(latest) && semver.IsValid(current) && semver.Compare(latest, current) > 0
}

var httpClient = &http.Client{Timeout: 60 * time.Second}

func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "paseo-tui/"+version)
	if strings.HasPrefix(url, "https://api.github.com/") {
		req.Header.Set("Accept", "application/vnd.github+json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: response larger than %d bytes", url, limit)
	}
	return data, nil
}

func fetchLatestRelease(ctx context.Context) (*ghRelease, error) {
	data, err := httpGet(ctx, "https://api.github.com/repos/"+repoSlug+"/releases/latest", 1<<20)
	if err != nil {
		return nil, err
	}
	var rel ghRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return nil, fmt.Errorf("reading the latest release: %w", err)
	}
	if !semver.IsValid(rel.TagName) {
		return nil, fmt.Errorf("latest release has an unexpected tag %q", rel.TagName)
	}
	return &rel, nil
}

// ---------- background check for the TUI ----------

type updateAvailableMsg struct{ version string }

type updateCache struct {
	Checked time.Time `json:"checked"`
	Latest  string    `json:"latest"`
}

func updateCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "paseo-tui", "update-check.json")
}

// checkForUpdateCmd reports a newer release, if any. The network is asked at most once per
// checkInterval; failures are silent and retried on the next interval.
func checkForUpdateCmd() tea.Cmd {
	if !updateCheck || os.Getenv(noCheckEnv) != "" || !isRelease(version) {
		return nil
	}
	return func() tea.Msg {
		path := updateCachePath()
		var c updateCache
		if data, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(data, &c)
		}
		if time.Since(c.Checked) > checkInterval || c.Checked.After(time.Now()) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			rel, err := fetchLatestRelease(ctx)
			c.Checked = time.Now()
			if err == nil {
				c.Latest = rel.TagName
			}
			if path != "" {
				if data, err := json.Marshal(c); err == nil {
					_ = os.MkdirAll(filepath.Dir(path), 0o755)
					_ = os.WriteFile(path, data, 0o644)
				}
			}
		}
		if newer(c.Latest, version) {
			return updateAvailableMsg{version: c.Latest}
		}
		return nil
	}
}

// ---------- paseo-tui update ----------

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "only report whether a newer release exists")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: paseo-tui update [--check]\n\nInstall the latest release of paseo-tui in place of this binary.")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rel, err := fetchLatestRelease(ctx)
	if err != nil {
		return err
	}
	if !isRelease(version) {
		fmt.Printf("This is a development build (%s); the latest release is %s.\n", version, rel.TagName)
		fmt.Println("Update a source checkout with: git pull && make")
		return nil
	}
	if !newer(rel.TagName, version) {
		fmt.Printf("paseo-tui %s is up to date.\n", version)
		return nil
	}
	if *checkOnly {
		fmt.Printf("paseo-tui %s is available (you have %s): %s\n", rel.TagName, version, rel.HTMLURL)
		fmt.Println("Install it with: paseo-tui update")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if pm, cmd := packageManager(exe); pm != "" {
		return fmt.Errorf("paseo-tui was installed with %s; update it with: %s", pm, cmd)
	}

	base := fmt.Sprintf("paseo-tui_%s_%s", runtime.GOOS, runtime.GOARCH)
	archive := base + ".tar.gz"
	binName := "paseo-tui"
	if runtime.GOOS == "windows" {
		archive, binName = base+".zip", "paseo-tui.exe"
	}
	fmt.Printf("Updating paseo-tui %s → %s\n", version, rel.TagName)

	get := func(name string, limit int64) ([]byte, error) {
		url := rel.assetURL(name)
		if url == "" {
			return nil, fmt.Errorf("release %s has no %s", rel.TagName, name)
		}
		return httpGet(ctx, url, limit)
	}
	sums, err := get("checksums.txt", 1<<20)
	if err != nil {
		return err
	}
	sig, err := get("checksums.txt.sig", 4<<10)
	if err != nil {
		return err
	}
	if err := verifySignature(releasePublicKeyPEM, sums, sig); err != nil {
		return err
	}
	data, err := get(archive, maxArchiveSize)
	if err != nil {
		return err
	}
	if err := verifyChecksum(sums, archive, data); err != nil {
		return err
	}
	bin, err := extractBinary(archive, data, base+"/"+binName)
	if err != nil {
		return err
	}
	if err := replaceExecutable(exe, bin); err != nil {
		return err
	}
	fmt.Printf("✓ paseo-tui %s installed at %s (signature and checksum verified)\n", rel.TagName, exe)
	return nil
}

// verifySignature checks a base64 Ed25519 signature of msg against a PEM public key.
func verifySignature(pubPEM, msg, sigB64 []byte) error {
	block, _ := pem.Decode(pubPEM)
	if block == nil {
		return errors.New("release public key: not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("release public key: %w", err)
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return errors.New("release public key: not Ed25519")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || !ed25519.Verify(pub, msg, sig) {
		return errors.New("checksums.txt has no valid signature from the paseo-tui release key; not installing")
	}
	return nil
}

// verifyChecksum checks data against the "<sha256>  <name>" line for name in sums.
func verifyChecksum(sums []byte, name string, data []byte) error {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			sum := sha256.Sum256(data)
			if !strings.EqualFold(f[0], hex.EncodeToString(sum[:])) {
				return fmt.Errorf("%s: checksum mismatch; not installing", name)
			}
			return nil
		}
	}
	return fmt.Errorf("checksums.txt has no entry for %s", name)
}

// extractBinary returns the file at member from a .tar.gz or .zip archive.
func extractBinary(archive string, data []byte, member string) ([]byte, error) {
	if strings.HasSuffix(archive, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if f.Name == member {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxArchiveSize))
			}
		}
		return nil, fmt.Errorf("%s not found in %s", member, archive)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not found in %s", member, archive)
		}
		if err != nil {
			return nil, err
		}
		if h.Name == member && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxArchiveSize))
		}
	}
}

// replaceExecutable swaps exe for bin. The new file is written next to exe and renamed over it, so an
// interrupted update never leaves a half-written binary. Windows can't overwrite a running .exe but can
// rename it, so the old one is moved aside and removed on the next start.
func replaceExecutable(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".paseo-tui-update-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("no permission to write to %s; run the update with sudo, or as the user that installed it", dir)
		}
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(tmpName, exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	return os.Rename(tmpName, exe)
}

// cleanupOldExecutable removes the binary a Windows update moved aside.
func cleanupOldExecutable() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}

// packageManager names the package manager that owns exe, if any, and how to update with it.
func packageManager(exe string) (name, cmd string) {
	p := filepath.ToSlash(exe)
	switch {
	case strings.Contains(p, "/Cellar/") || strings.Contains(p, "/homebrew/") || strings.Contains(p, "/linuxbrew/"):
		return "Homebrew", "brew upgrade paseo-tui"
	case strings.Contains(strings.ToLower(p), "/scoop/apps/"):
		return "Scoop", "scoop update paseo-tui"
	case strings.HasPrefix(p, "/nix/store/"):
		return "Nix", "your Nix configuration"
	}
	return "", ""
}
