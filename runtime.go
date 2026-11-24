package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// The bridge is embedded so the binary is self-contained; it is unpacked into
// the user cache dir (keyed by content hash) the first time it runs.

//go:embed bridge/dist/paseo-bridge.mjs
var embeddedBridge []byte

//go:embed bridge/package.json
var embeddedBridgePkg []byte

// resolveBridge picks the bridge script: an explicit path, a dev build next to
// the binary, or the embedded copy.
func resolveBridge(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		p := filepath.Join(filepath.Dir(exe), "bridge", "dist", "paseo-bridge.mjs")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return unpackBridge()
}

func unpackBridge() (string, error) {
	if len(embeddedBridge) == 0 {
		return "", errors.New("no embedded bridge")
	}
	sum := sha256.Sum256(embeddedBridge)
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "paseo-tui", "bridge-"+hex.EncodeToString(sum[:])[:12])
	script := filepath.Join(dir, "dist", "paseo-bridge.mjs")
	if st, err := os.Stat(script); err == nil && st.Size() == int64(len(embeddedBridge)) {
		return script, nil
	}
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		return "", err
	}
	// the CLI code reads its version from ../package.json relative to the bundle
	if err := os.WriteFile(filepath.Join(dir, "package.json"), embeddedBridgePkg, 0o644); err != nil {
		return "", err
	}
	tmp := script + ".tmp"
	if err := os.WriteFile(tmp, embeddedBridge, 0o644); err != nil {
		return "", err
	}
	return script, os.Rename(tmp, script)
}

// resolveNode finds a JavaScript runtime for the bridge: an explicit binary,
// `node` on PATH, or the Node runtime built into the Paseo app (Electron in
// node mode, exactly how the `paseo` CLI itself runs).
func resolveNode(override string) (bin string, env []string, err error) {
	if override != "" {
		p, err := exec.LookPath(override)
		return p, nil, err
	}
	if p, err := exec.LookPath("node"); err == nil {
		return p, nil, nil
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		for _, app := range []string{"/Applications/Paseo.app", filepath.Join(home, "Applications", "Paseo.app")} {
			// the Helper, not the main executable, so no app UI/dock lifecycle is involved
			candidates = append(candidates, filepath.Join(app, "Contents", "Frameworks", "Paseo Helper.app", "Contents", "MacOS", "Paseo Helper"))
		}
	case "windows":
		// the desktop installer's per-user and machine-wide locations
		for _, base := range []string{os.Getenv("LOCALAPPDATA") + `\Programs`, os.Getenv("ProgramFiles")} {
			if base != `\Programs` && base != "" {
				candidates = append(candidates, filepath.Join(base, "Paseo", "Paseo.exe"))
			}
		}
	default:
		candidates = []string{"/opt/Paseo/Paseo.bin", "/opt/Paseo/paseo", "/usr/lib/paseo/Paseo.bin"}
	}
	for _, c := range candidates {
		st, err := os.Stat(c)
		if err != nil || st.IsDir() {
			continue
		}
		if runtime.GOOS == "windows" || st.Mode()&0o111 != 0 {
			return c, []string{"ELECTRON_RUN_AS_NODE=1", "PASEO_NODE_ENV=production"}, nil
		}
	}
	return "", nil, errors.New("no JavaScript runtime found: install node, or Paseo (its built-in runtime is used automatically)")
}
