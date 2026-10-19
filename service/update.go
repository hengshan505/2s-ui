package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/shenaba/2s-ui/config"
	"github.com/shenaba/2s-ui/logger"
	"github.com/shenaba/2s-ui/util"
)

// Self-update runs on Linux only. Bare-metal installs restart through a
// transient systemd unit; Docker containers instead swap the binary in the
// writable layer and re-exec the entrypoint in place (release binaries are
// static musl builds, so they run on Alpine). The container-layer update
// survives `docker restart` but recreating the container reverts to the
// image's version. On Windows a running .exe cannot be replaced, so the panel
// keeps the "new version" chip as a plain link there.
const (
	releaseAPIURL = "https://api.github.com/repos/shenaba/2s-ui/releases/latest"
	releaseDLBase = "https://github.com/shenaba/2s-ui/releases/download"
	checksumsName = "SHA256SUMS"
	serviceUnit   = "s-ui"
)

type UpdatePhase string

const (
	UpdateIdle        UpdatePhase = "idle"
	UpdateChecking    UpdatePhase = "checking"
	UpdateDownloading UpdatePhase = "downloading"
	UpdateVerifying   UpdatePhase = "verifying"
	UpdateExtracting  UpdatePhase = "extracting"
	UpdateSwapping    UpdatePhase = "swapping"
	UpdateRestarting  UpdatePhase = "restarting"
	UpdateDone        UpdatePhase = "done"
	UpdateFailed      UpdatePhase = "failed"
)

type UpdateStatus struct {
	Phase   UpdatePhase `json:"phase"`
	Target  string      `json:"target"`
	Message string      `json:"message"`
	Error   string      `json:"error"`
}

type UpdateService struct{}

// Package-level so the state machine is a singleton per process: only one
// self-update may run at a time, and the status survives across HTTP requests
// (the frontend polls updateStatus).
var (
	updMu      sync.Mutex
	updStatus  = UpdateStatus{Phase: UpdateIdle}
	updRunning bool
)

// CanSelfUpdate reports whether an in-panel update can run here, and if not, a
// short human-readable reason for the UI.
func (s *UpdateService) CanSelfUpdate() (bool, string) {
	if runtime.GOOS != "linux" {
		return false, "self-update is only supported on Linux"
	}
	if _, err := archAsset(); err != nil {
		return false, err.Error()
	}
	// Docker restarts by re-execing the entrypoint in place — no systemd needed.
	if inDocker() {
		return true, ""
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return false, "systemd not detected"
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false, "systemctl not found"
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false, "systemd-run not found"
	}
	return true, ""
}

// InDocker is exposed so the API layer can tell the UI to warn that a
// container-layer update reverts when the container is recreated.
func (s *UpdateService) InDocker() bool {
	return inDocker()
}

// stopCoreForExec closes sing-box the way main.go's SIGTERM handler does before
// the exec restart. execve replaces the process image without running any Go
// cleanup, and the core's kernel-side state is not all reclaimed for us: socket
// and TUN descriptors close via FD_CLOEXEC, but auto-route nftables rules are
// not tied to a descriptor and would survive into the new process, which then
// re-adds them on top. The systemd path gets this for free via SIGTERM.
func stopCoreForExec() {
	// corePtr is set by NewConfigService during app.Init; nil only if the update
	// somehow ran before init, where there is no core to stop anyway.
	if corePtr == nil {
		return
	}
	// Same as a shutdown: the exec leaves no next flush for what the core has
	// counted since the last one.
	FlushStats()
	if err := (&ConfigService{}).RetireCore(); err != nil {
		logger.Warning("stop core before in-place restart: ", err)
	}
}

// startCoreAfterFailedExec restores the core when the exec never happened. We
// are still the old process, so leaving the core stopped would drop all proxy
// traffic until someone restarts the container by hand.
func startCoreAfterFailedExec() {
	if corePtr == nil {
		return
	}
	s := &ConfigService{}
	s.ReclaimCore()
	if err := s.StartCore(); err != nil {
		logger.Error("restore core after failed in-place restart: ", err)
	}
}

func (s *UpdateService) GetStatus() UpdateStatus {
	updMu.Lock()
	defer updMu.Unlock()
	return updStatus
}

// StartUpdate validates the environment, then kicks off the update in the
// background. It returns immediately; callers poll GetStatus.
func (s *UpdateService) StartUpdate() error {
	if ok, reason := s.CanSelfUpdate(); !ok {
		return fmt.Errorf("%s", reason)
	}
	updMu.Lock()
	if updRunning {
		updMu.Unlock()
		return fmt.Errorf("update already in progress")
	}
	updRunning = true
	updStatus = UpdateStatus{Phase: UpdateChecking, Message: "checking latest release"}
	updMu.Unlock()

	go s.run()
	return nil
}

func (s *UpdateService) setStatus(p UpdatePhase, msg string) {
	updMu.Lock()
	updStatus.Phase = p
	updStatus.Message = msg
	updMu.Unlock()
}

func (s *UpdateService) fail(msg string, err error) {
	logger.Error("self-update failed: ", msg, ": ", err)
	updMu.Lock()
	updStatus.Phase = UpdateFailed
	updStatus.Message = msg
	if err != nil {
		updStatus.Error = err.Error()
	}
	updMu.Unlock()
}

func (s *UpdateService) run() {
	defer func() {
		updMu.Lock()
		updRunning = false
		updMu.Unlock()
	}()

	// 1) Resolve the latest release and this host's asset name.
	tag, err := s.LatestRelease()
	if err != nil {
		s.fail("cannot resolve latest release", err)
		return
	}
	updMu.Lock()
	updStatus.Target = tag
	updMu.Unlock()

	if !shouldInstall(tag, config.GetVersion()) {
		s.setStatus(UpdateDone, "already up to date")
		return
	}

	plat, err := archAsset()
	if err != nil {
		s.fail("unsupported architecture", err)
		return
	}
	assetName := fmt.Sprintf("s-ui-linux-%s.tar.gz", plat)
	tarURL := fmt.Sprintf("%s/%s/%s", releaseDLBase, tag, assetName)
	sumURL := fmt.Sprintf("%s/%s/%s", releaseDLBase, tag, checksumsName)

	exe, err := os.Executable()
	if err != nil {
		s.fail("cannot locate current binary", err)
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	installDir := filepath.Dir(exe)

	work, err := os.MkdirTemp("", "sui-update-")
	if err != nil {
		s.fail("cannot create temp dir", err)
		return
	}
	defer os.RemoveAll(work)

	// 2) Download the release tarball and its checksums file.
	s.setStatus(UpdateDownloading, "downloading "+assetName)
	tarPath := filepath.Join(work, assetName)
	if err := download(tarURL, tarPath); err != nil {
		s.fail("download failed", err)
		return
	}
	sumPath := filepath.Join(work, checksumsName)
	if err := download(sumURL, sumPath); err != nil {
		s.fail("checksum download failed", err)
		return
	}

	// 3) Verify integrity BEFORE anything touches the running install. Downloads
	// run as root; without this a MITM or tampered mirror would be code
	// execution. TLS is already enforced (default http client) — this is the
	// second, content-level check against the release's published SHA256SUMS.
	s.setStatus(UpdateVerifying, "verifying checksum")
	want, err := expectedSum(sumPath, assetName)
	if err != nil {
		s.fail("checksum entry not found", err)
		return
	}
	got, err := fileSum(tarPath)
	if err != nil {
		s.fail("cannot hash download", err)
		return
	}
	if !strings.EqualFold(got, want) {
		s.fail("checksum mismatch", fmt.Errorf("expected %s, got %s", want, got))
		return
	}

	// 4) Extract and smoke-test the new binary before swapping it in.
	s.setStatus(UpdateExtracting, "extracting")
	stageDir := filepath.Join(work, "stage")
	if err := extractTarGz(tarPath, stageDir); err != nil {
		s.fail("extract failed", err)
		return
	}
	newBin := filepath.Join(stageDir, "s-ui", "sui")
	if _, err := os.Stat(newBin); err != nil {
		s.fail("binary not found in archive", err)
		return
	}
	if err := os.Chmod(newBin, 0o755); err != nil {
		s.fail("chmod failed", err)
		return
	}
	// A wrong-arch or corrupt binary must never reach systemd, or the unit would
	// crash-loop with no way back. `sui -v` prints the version and exits, so a
	// clean exit proves the binary at least runs on this host.
	if err := smokeTest(newBin); err != nil {
		s.fail("new binary failed to run", err)
		return
	}

	// 5) Swap the binary. Linux lets us replace a busy executable by renaming a
	// new file over it (the running process keeps the old inode until restart).
	s.setStatus(UpdateSwapping, "installing new binary")
	if err := swapBinary(newBin, exe); err != nil {
		s.fail("install failed", err)
		return
	}
	// Best-effort refresh of the bundled management script; never fatal.
	copyIfExists(filepath.Join(stageDir, "s-ui", "s-ui.sh"), filepath.Join(installDir, "s-ui.sh"))

	// 6) Restart. In Docker there is no systemd: after a short delay (so the
	// frontend can observe the "restarting" phase) the process re-execs the
	// container entrypoint, which runs migrate and starts the new binary as the
	// same PID — the container itself never restarts. On bare metal a transient
	// systemd unit restarts the service from outside our own cgroup (calling
	// `systemctl restart` directly would kill us mid-restart and leave the unit
	// down).
	s.setStatus(UpdateRestarting, "restarting service")
	if inDocker() {
		time.Sleep(2 * time.Second)
		stopCoreForExec()
		if err := execRestart(installDir); err != nil {
			startCoreAfterFailedExec()
			s.fail("installed, but in-place restart failed; restart the container", err)
		}
		return
	}
	if err := detachedRestart(); err != nil {
		s.fail("installed, but automatic restart failed; run: systemctl restart s-ui", err)
		return
	}
	// systemd will terminate us shortly; leave the status at "restarting".
}

// LatestRelease returns the tag name of the newest GitHub release.
func (s *UpdateService) LatestRelease() (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, releaseAPIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github api returned status %d", resp.StatusCode)
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return "", err
	}
	if r.TagName == "" {
		return "", fmt.Errorf("empty tag_name in release response")
	}
	return r.TagName, nil
}

func inDocker() bool {
	if os.Getenv("S_UI_IN_DOCKER") != "" {
		return true
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}

// archAsset maps the running binary to its release asset platform suffix. arm
// needs GOARM to pick the right variant, which is recorded in the build info.
func archAsset() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64", nil
	case "386":
		return "386", nil
	case "arm64":
		return "arm64", nil
	case "s390x":
		return "s390x", nil
	case "arm":
		switch goarm() {
		case "6":
			return "armv6", nil
		case "5":
			return "armv5", nil
		default:
			// Builds default GOARM to 7; treat unknown as the most common target.
			return "armv7", nil
		}
	}
	return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
}

func goarm() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "GOARM" {
				return strings.Split(setting.Value, ",")[0]
			}
		}
	}
	return ""
}

func normalizeVer(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// shouldInstall reports whether the published release is worth replacing the
// running binary with.
//
// Ordered, not "different from". config/version is embedded and bumped by hand
// in the commit that prepares a release, so between that commit and the publish
// -- and on every build from main -- the binary is *newer* than the newest
// release. An equality check calls that "not up to date" and installs the older
// tarball over it, which is a downgrade the operator asked for as an update:
// the panel loses whatever the newer build fixed, while the database stays
// stamped at the newer version so no migration re-runs to meet it.
//
// A tag whose numbers do not order as newer is left alone, which also covers
// the shapes VersionBefore reads as zero -- a release candidate, a retagged
// build. Refusing to install something we cannot order is the safe direction;
// the operator can still install it by hand.
func shouldInstall(tag, current string) bool {
	return util.VersionBefore(normalizeVer(current), normalizeVer(tag))
}

func download(url, dest string) error {
	// Default client verifies TLS certificates — never disable that here.
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d for %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// expectedSum reads a sha256sum-format file ("<hex>  <name>") and returns the
// hash for the given asset.
func expectedSum(sumFile, asset string) (string, error) {
	data, err := os.ReadFile(sumFile)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum for %s", asset)
}

func extractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	root := filepath.Clean(dest)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.Clean("/"+hdr.Name))
		// Guard against path traversal (../ entries escaping the stage dir).
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode).Perm())
			if err != nil {
				return err
			}
			if _, err := io.CopyN(out, tr, hdr.Size); err != nil && err != io.EOF {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}

func smokeTest(bin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, bin, "-v").Run()
}

func swapBinary(newBin, curBin string) error {
	backup := curBin + ".bak"
	_ = os.Remove(backup)
	// Keep the previous binary so a broken update can be reverted by hand:
	//   mv sui.bak sui && systemctl restart s-ui
	if err := copyFile(curBin, backup, 0o755); err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}
	// Stage next to the target so the final rename is same-filesystem (atomic);
	// renaming straight from /tmp would fail with EXDEV.
	staged := curBin + ".new"
	if err := copyFile(newBin, staged, 0o755); err != nil {
		return err
	}
	if err := os.Rename(staged, curBin); err != nil {
		_ = os.Remove(staged)
		return err
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyIfExists(src, dst string) {
	if _, err := os.Stat(src); err == nil {
		_ = copyFile(src, dst, 0o755)
	}
}

func detachedRestart() error {
	unit := fmt.Sprintf("s-ui-selfupdate-%d", time.Now().Unix())
	// A transient systemd service runs under PID 1, outside s-ui.service's
	// cgroup, so `systemctl restart s-ui` (which kills our cgroup) cannot kill
	// it. The short sleep lets the HTTP response flush before we go down.
	cmd := exec.Command(
		"systemd-run",
		"--collect",
		"--unit", unit,
		"/bin/sh", "-c", "sleep 2; systemctl restart "+serviceUnit,
	)
	return cmd.Start()
}
