package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/proc"
	"github.com/squadrn/squadrn/internal/release"
	"github.com/squadrn/squadrn/internal/update"
)

// Repo is the GitHub repository releases come from, set at build time with
// -ldflags "-X github.com/squadrn/squadrn/internal/cli.Repo=squadrn/squadrn". Only the --repo flag
// overrides it: an environment variable could be planted to redirect updates.
var Repo = ""

// githubAPI is where releases are looked up; tests point it at a fake server.
var githubAPI = update.DefaultAPI

const updateTimeout = 5 * time.Minute

// maxDownload bounds one downloaded release file.
const maxDownload = 256 << 20

// github is the release source. A token (GH_TOKEN, GITHUB_TOKEN, else gh's login when gh is
// installed) only raises the API rate limit; releases are public.
func (a *app) github(ctx context.Context) update.GitHub {
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		if bin, err := exec.LookPath("gh"); err == nil {
			tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if out, err := proc.Output(tctx, proc.Options{}, bin, "auth", "token"); err == nil {
				token = strings.TrimSpace(string(out))
			}
			cancel()
		}
	}
	return update.GitHub{API: githubAPI, Token: token}
}

func (a *app) selfUpdateCmd() *cobra.Command {
	var o updateOptions
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update squadrn and its plugin in every account (--check: only look, --rollback: undo)",
		Example: fmt.Sprintf(`  %[1]s self-update --check       what is new
  %[1]s self-update               install the newest release of your channel
  %[1]s self-update --rollback    go back to the release installed before`, a.cmd),
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
			if o.asJSON && !o.check {
				return usagef("--json needs --check")
			}
			if o.rollback {
				if o.check || o.tag != "" {
					return usagef("--rollback cannot be combined with --check or --version")
				}
				return a.rollback()
			}
			return a.selfUpdate(o)
		},
	}
	cmd.Flags().BoolVar(&o.check, "check", false, "only report whether an update is available, with its release notes")
	cmd.Flags().StringVar(&o.tag, "version", "", "install this release tag instead of the newest")
	cmd.Flags().StringVar(&o.repo, "repo", "", "GitHub repository to update from (owner/name)")
	cmd.Flags().BoolVar(&o.allowDowngrade, "allow-downgrade", false, "install a release older than the running one")
	cmd.Flags().BoolVar(&o.rollback, "rollback", false, "reinstall the release that was installed before this one")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "with --check: print JSON (current, latest, channel, available, notes)")
	cmd.Flags().BoolVar(&o.background, "background", false, "refresh the update notice cache quietly")
	_ = cmd.Flags().MarkHidden("background")
	return cmd
}

type updateOptions struct {
	check, allowDowngrade, rollback, background, asJSON bool
	tag, repo                                           string
}

func (a *app) updateStatePath() string {
	return filepath.Join(config.DirsFor(a.store.Home, config.Slug).Cache, "update.json")
}

func (a *app) selfUpdate(o updateOptions) error {
	repo := o.repo
	if repo == "" {
		repo = Repo
	}
	channel := a.cfg.Channel()
	if repo == "" {
		if o.background {
			return nil
		}
		return usagef("no release repository configured; pass --repo owner/name")
	}
	timeout := updateTimeout
	if o.background {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	gh := a.github(ctx)
	tag := normalizeTag(o.tag)
	if tag == "" {
		rels, err := gh.Releases(ctx, repo)
		if err != nil {
			if o.background {
				update.Save(a.updateStatePath(), update.State{CheckedAt: a.now(), Channel: channel})
				return nil
			}
			return fmt.Errorf("cannot list releases of %s: %s", repo, releaseFailure(err, repo, ""))
		}
		if tag = update.Newest(rels, channel); tag == "" {
			if o.background {
				update.Save(a.updateStatePath(), update.State{CheckedAt: a.now(), Channel: channel})
				return nil
			}
			return fmt.Errorf("%s has no %s release yet", repo, channel)
		}
		prev := update.Load(a.updateStatePath())
		update.Save(a.updateStatePath(), update.State{CheckedAt: a.now(), Latest: tag, Channel: channel, NotifiedAt: prev.NotifiedAt})
	}
	if o.background {
		return nil
	}
	rel, err := gh.Release(ctx, repo, tag)
	if err != nil {
		return fmt.Errorf("cannot install %s: %s", tag, releaseFailure(err, repo, tag))
	}
	if !o.asJSON {
		a.warnOutsideLayout()
	}
	current := release.Tag(Version)
	cmp, comparable := release.Compare(tag, current)
	upToDate := tag == current || (comparable && cmp == 0)
	repair := upToDate && a.layout.Incomplete(tag)
	if o.check {
		return a.reportCheck(rel.Body, tag, current, channel, upToDate, repair, o.asJSON)
	}
	switch {
	case upToDate && !repair:
		a.out("squadrn %s is up to date (%s channel)\n", current, channel)
		return nil
	case repair:
		a.out("the installed copy of %s is incomplete (VERSION %q); reinstalling it\n", current, a.layout.Version())
	case comparable && cmp < 0 && !o.allowDowngrade:
		return usagef("%s is older than the installed %s; pass --allow-downgrade to install it", tag, current)
	}
	tmp, err := os.MkdirTemp("", "squadrn-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	asset := release.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	// The signature comes first and on its own: a release without one is refused with a clear
	// reason, before the archive is downloaded.
	if a.signers != "" {
		sig, ok := rel.Asset(release.SignatureName)
		if !ok {
			return fmt.Errorf("release %s has no signature (%s); refusing to install it", tag, release.SignatureName)
		}
		if err := gh.Download(ctx, sig, filepath.Join(tmp, release.SignatureName), maxDownload); err != nil {
			return fmt.Errorf("download the signature of %s: %s", tag, releaseFailure(err, repo, tag))
		}
	}
	for _, name := range []string{asset, release.ChecksumsName} {
		file, ok := rel.Asset(name)
		if !ok {
			return fmt.Errorf("release %s has no %s or %s (no build for %s/%s?)", tag, asset, release.ChecksumsName, runtime.GOOS, runtime.GOARCH)
		}
		if err := gh.Download(ctx, file, filepath.Join(tmp, name), maxDownload); err != nil {
			return fmt.Errorf("download %s: %s", name, releaseFailure(err, repo, tag))
		}
	}
	if err := a.verifyRelease(tmp, asset); err != nil {
		return err
	}
	src := filepath.Join(tmp, "release")
	if err := release.Extract(filepath.Join(tmp, asset), src); err != nil {
		return err
	}
	return a.installRelease(src, tag, nameChoice{})
}

// reportCheck prints what self-update --check found.
func (a *app) reportCheck(body, tag, current, channel string, upToDate, repair, asJSON bool) error {
	available := !upToDate
	if c, ok := release.Compare(tag, current); ok && c < 0 {
		available = false
	}
	notes := ""
	if available {
		notes = strings.TrimSpace(body)
	}
	if asJSON {
		return a.printJSON(map[string]any{"current": current, "latest": tag, "channel": channel, "available": available, "repair": repair, "notes": notes})
	}
	switch {
	case repair:
		a.out("the installed copy of %s is incomplete; repair it with: %s self-update\n", current, a.cmd)
	case !available:
		a.out("squadrn %s is up to date (%s channel)\n", current, channel)
	default:
		a.out("update available: %s → %s\n", current, tag)
		if notes != "" {
			a.out("\n%s\n\n", releaseNotes(notes, 25))
		}
		a.out("install it with: %s self-update\n", a.cmd)
	}
	return nil
}

// warnOutsideLayout says when the running binary is not the installed one (a development build,
// go install, a package manager): self-update replaces the installed copy, not this one.
func (a *app) warnOutsideLayout() {
	self, err := os.Executable()
	if err != nil || !fsx.IsFile(a.layout.Binary()) || a.layout.IsInstalledCopy(self) {
		return
	}
	msg := fmt.Sprintf("this is %s (%s), not the installed copy %s (%s); self-update replaces the installed copy",
		a.tilde(self), Version, a.tilde(a.layout.Binary()), a.layout.Version())
	if p, err := exec.LookPath(a.cmd); err == nil {
		msg += fmt.Sprintf("; %s on PATH is %s", a.cmd, a.tilde(p))
	}
	a.warnf("%s", msg)
}

// normalizeTag adds the v prefix release tags carry (1.2.0 → v1.2.0).
func normalizeTag(tag string) string {
	if tag != "" && !strings.HasPrefix(tag, "v") && release.Valid(tag) {
		return "v" + tag
	}
	return tag
}

// releaseFailure turns a release lookup error into what the user can act on.
func releaseFailure(err error, repo, tag string) string {
	switch {
	case errors.Is(err, update.ErrNotFound) && tag != "":
		return fmt.Sprintf("release %s not found in %s (see: https://github.com/%s/releases)", tag, repo, repo)
	case errors.Is(err, update.ErrNotFound):
		return fmt.Sprintf("%s not found, or it is not a public repository", repo)
	}
	return err.Error()
}

// verifyRelease checks the signature of checksums.txt against the keys built into this binary,
// then the archive's checksum.
func (a *app) verifyRelease(dir, asset string) error {
	sums, err := os.ReadFile(filepath.Join(dir, release.ChecksumsName))
	if err != nil {
		return err
	}
	sig, sigErr := os.ReadFile(filepath.Join(dir, release.SignatureName))
	switch err := release.VerifySignature(sums, sig, a.signers); {
	case errors.Is(err, release.ErrNoSigners):
		a.warnf("this build has no release signing key; %s is verified by checksum only", asset)
	case sigErr != nil:
		return fmt.Errorf("the release has no signature (%s); refusing to install it", release.SignatureName)
	case err != nil:
		return fmt.Errorf("%s: %w; refusing to install it", release.ChecksumsName, err)
	default:
		a.out("verified release signature\n")
	}
	return release.VerifyChecksum(filepath.Join(dir, release.ChecksumsName), filepath.Join(dir, asset))
}

// releaseNotes indents the first max lines of a release body.
func releaseNotes(body string, max int) string {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n")), "\n")
	if len(lines) > max {
		lines = append(lines[:max], "…")
	}
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

func (a *app) rollback() error {
	prev := a.layout.Previous()
	version := release.SnapshotVersion(prev)
	if !fsx.IsFile(filepath.Join(prev, release.BinaryName)) || version == "" {
		return errors.New("no previous release to roll back to")
	}
	a.out("rolling back v%s → v%s\n", strings.TrimPrefix(a.layout.Version(), "v"), version)
	return a.installRelease(prev, version, nameChoice{})
}
