package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewerAndIsRelease(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.1", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false},
		{"v0.2.0", "v0.2.0-rc.1", true},
		{"v0.2.0", "dev", false},
		{"garbage", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
	for v, want := range map[string]bool{
		"v0.1.0": true, "v0.2.0-rc.1": true, "dev": false,
		"v0.1.0-dirty": false, "v0.1.0-3-g1a2b3c4": false, "v0.1.0-3-g1a2b3c4-dirty": false,
	} {
		if got := isRelease(v); got != want {
			t.Errorf("isRelease(%q) = %v, want %v", v, got, want)
		}
	}
}

func testKey(t *testing.T) ([]byte, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), priv
}

func TestVerifySignature(t *testing.T) {
	pubPEM, priv := testKey(t)
	msg := []byte("abc  paseo-tui_linux_amd64.tar.gz\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg)) + "\n")

	if err := verifySignature(pubPEM, msg, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := verifySignature(pubPEM, append([]byte("x"), msg...), sig); err == nil {
		t.Error("signature over different content accepted")
	}
	otherPEM, _ := testKey(t)
	if err := verifySignature(otherPEM, msg, sig); err == nil {
		t.Error("signature from another key accepted")
	}
	if err := verifySignature(pubPEM, msg, []byte("not base64!")); err == nil {
		t.Error("garbage signature accepted")
	}
}

func TestEmbeddedReleaseKey(t *testing.T) {
	// The embedded key must parse; a signature from another key must not verify.
	_, priv := testKey(t)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte("x")))
	err := verifySignature(releasePublicKeyPEM, []byte("x"), []byte(sig))
	if err == nil || strings.Contains(err.Error(), "release public key") {
		t.Fatalf("embedded key: want a signature mismatch, got %v", err)
	}
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("archive bytes")
	sum := sha256.Sum256(data)
	sums := []byte(hex.EncodeToString(sum[:]) + "  paseo-tui_linux_amd64.tar.gz\n" +
		strings.Repeat("0", 64) + "  paseo-tui_windows_amd64.zip\n")

	if err := verifyChecksum(sums, "paseo-tui_linux_amd64.tar.gz", data); err != nil {
		t.Errorf("matching checksum rejected: %v", err)
	}
	if err := verifyChecksum(sums, "paseo-tui_windows_amd64.zip", data); err == nil {
		t.Error("mismatching checksum accepted")
	}
	if err := verifyChecksum(sums, "paseo-tui_darwin_arm64.tar.gz", data); err == nil {
		t.Error("missing entry accepted")
	}
}

func TestExtractBinary(t *testing.T) {
	want := []byte("#!binary")

	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	for _, f := range []struct{ name, body string }{{"d/README.md", "readme"}, {"d/paseo-tui", string(want)}} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(f.body))
	}
	tw.Close()
	gz.Close()
	got, err := extractBinary("x.tar.gz", tgz.Bytes(), "d/paseo-tui")
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("tar.gz: got %q, %v", got, err)
	}
	if _, err := extractBinary("x.tar.gz", tgz.Bytes(), "d/missing"); err == nil {
		t.Error("tar.gz: missing member found")
	}

	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("d/paseo-tui.exe")
	_, _ = w.Write(want)
	zw.Close()
	got, err = extractBinary("x.zip", zb.Bytes(), "d/paseo-tui.exe")
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("zip: got %q, %v", got, err)
	}
}

func TestReplaceExecutable(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "paseo-tui")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceExecutable(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Errorf("binary not replaced: %q", got)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("new binary not executable: %v", fi.Mode())
		}
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".paseo-tui-update-*"))
	if len(left) > 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestPackageManager(t *testing.T) {
	for path, want := range map[string]string{
		"/opt/homebrew/Cellar/paseo-tui/0.1.0/bin/paseo-tui":    "Homebrew",
		"/home/linuxbrew/.linuxbrew/bin/paseo-tui":              "Homebrew",
		`C:\Users\x\scoop\apps\paseo-tui\current\paseo-tui.exe`: "Scoop",
		"/nix/store/abc-paseo-tui/bin/paseo-tui":                "Nix",
		"/usr/local/bin/paseo-tui":                              "",
	} {
		if got, _ := packageManager(strings.ReplaceAll(path, `\`, "/")); got != want {
			t.Errorf("packageManager(%q) = %q, want %q", path, got, want)
		}
	}

	// /usr/bin belongs to the system package manager: the AUR on Arch, a .deb/.rpm/.apk elsewhere.
	defer func(f func() bool) { hasPacman = f }(hasPacman)
	hasPacman = func() bool { return true }
	if got, cmd := packageManager("/usr/bin/paseo-tui"); !strings.Contains(got, "AUR") || !strings.Contains(cmd, "paseo-tui-bin") {
		t.Errorf("Arch /usr/bin: got %q, %q", got, cmd)
	}
	hasPacman = func() bool { return false }
	if got, cmd := packageManager("/usr/bin/paseo-tui"); !strings.Contains(got, ".deb") || !strings.Contains(cmd, "/releases/latest") {
		t.Errorf("deb/rpm /usr/bin: got %q, %q", got, cmd)
	}
}
