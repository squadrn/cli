package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn/internal/config"
	"github.com/squadrn/squadrn/internal/doctor"
	"github.com/squadrn/squadrn/internal/fsx"
	"github.com/squadrn/squadrn/internal/gitver"
	"github.com/squadrn/squadrn/internal/install"
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
				update.Save(a.updateStatePath(), update.State{CheckedAt: a.now(), Channel: channel, Err: err.Error()})
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
	current := "v" + strings.TrimPrefix(Version, "v")
	cmp, comparable := release.Compare(tag, current)
	upToDate := tag == current || (comparable && cmp == 0)
	repair := upToDate && a.needsRepair(tag)
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
	if err := a.verifyRelease(ctx, tmp, asset, repo); err != nil {
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

// needsRepair reports whether the installed release is not fully tag: an install that failed
// half way leaves a new binary next to an old plugin and VERSION.
func (a *app) needsRepair(tag string) bool {
	if !fsx.IsFile(a.layout.Binary()) {
		return false // not installed here (a development build); nothing to repair
	}
	return a.layout.Version() != strings.TrimPrefix(tag, "v") || a.layout.Marketplace() == "" ||
		!fsx.IsDir(filepath.Join(a.layout.DataDir, "plugin"))
}

// warnOutsideLayout says when the running binary is not the installed one (a development build,
// go install, a package manager): self-update replaces the installed copy, not this one.
func (a *app) warnOutsideLayout() {
	self, err := os.Executable()
	if err != nil || !fsx.IsFile(a.layout.Binary()) || fsx.SameFile(self, a.layout.Binary()) || a.isCommandCopy(self) {
		return
	}
	msg := fmt.Sprintf("this is %s (%s), not the installed copy %s (%s); self-update replaces the installed copy",
		a.tilde(self), Version, a.tilde(a.layout.Binary()), a.layout.Version())
	if p, err := exec.LookPath(a.cmd); err == nil {
		msg += fmt.Sprintf("; %s on PATH is %s", a.cmd, a.tilde(p))
	}
	a.warnf("%s", msg)
}

// isCommandCopy reports whether path is one of the command copies in the bin dir (Windows).
func (a *app) isCommandCopy(path string) bool {
	return filepath.Clean(filepath.Dir(path)) == filepath.Clean(a.layout.BinDir)
}

// normalizeTag adds the v prefix release tags carry (1.2.0 → v1.2.0).
func normalizeTag(tag string) string {
	if tag == "" || strings.HasPrefix(tag, "v") {
		return tag
	}
	if _, ok := release.Compare(tag, tag); ok {
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

// verifyRelease checks, in order: the signature of checksums.txt against the keys built into
// this binary, the archive's checksum, and GitHub build provenance where it exists.
func (a *app) verifyRelease(ctx context.Context, dir, asset, repo string) error {
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
	archive := filepath.Join(dir, asset)
	if err := release.VerifyChecksum(filepath.Join(dir, release.ChecksumsName), archive); err != nil {
		return err
	}
	return a.verifyAttestation(ctx, archive, repo)
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

// verifyAttestation checks the build provenance GitHub attached to the archive, as an extra
// when gh is installed; the signature is what installs rely on.
func (a *app) verifyAttestation(ctx context.Context, archive, repo string) error {
	bin, _ := exec.LookPath("gh")
	if bin == "" {
		return nil
	}
	owner, _, _ := strings.Cut(repo, "/")
	out, err := proc.Output(ctx, proc.Options{}, bin, "attestation", "verify", archive, "--owner", owner)
	if err == nil {
		a.out("verified build provenance of %s\n", filepath.Base(archive))
		return nil
	}
	msg := err.Error() + " " + string(out)
	if strings.Contains(msg, "no attestations found") || strings.Contains(msg, "unknown command") || strings.Contains(msg, "HTTP 404") ||
		strings.Contains(msg, "HTTP 403") || strings.Contains(msg, "gh auth login") || strings.Contains(msg, "not logged") {
		if a.signers == "" {
			a.warnf("%s has no build attestation; verified by checksum only", filepath.Base(archive))
		}
		return nil
	}
	return fmt.Errorf("build provenance check failed for %s: %w", filepath.Base(archive), err)
}

// nameChoice is what the user asked for at install; empty fields keep the configured names.
type nameChoice struct {
	command  string
	aliases  []string
	aliasSet bool
	force    bool
	// selfInstall: the installers run this release (not self-update or rollback).
	selfInstall bool
	// interactive: ask for the names on the terminal (installers without name flags).
	interactive bool
}

// openTTY opens a terminal for prompts when stdin or stdout are redirected; tests replace it.
var openTTY = openTerminal

// terminalIO is where install prompts go: stdin/stdout when both are a terminal, else the
// controlling terminal. ok is false when there is none (CI, ssh -T): nothing is asked then.
func (a *app) terminalIO() (io.Reader, io.Writer, func(), bool) {
	if a.interactive {
		return a.stdin, a.stdout, func() {}, true
	}
	return openTTY()
}

func (a *app) selfInstallCmd() *cobra.Command {
	var nc nameChoice
	cmd := &cobra.Command{
		Use:    "self-install <unpacked-release-dir>",
		Short:  "Install an unpacked release (used by install.sh)",
		Hidden: true,
		Args:   oneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			nc.aliasSet = cmd.Flags().Changed("alias")
			nc.selfInstall = true
			return a.installRelease(args[0], Version, nc)
		},
	}
	cmd.Flags().StringVar(&nc.command, "command", "", "command name to install under (default: configured, else squadrn)")
	cmd.Flags().StringSliceVar(&nc.aliases, "alias", nil, "extra command names, comma separated")
	cmd.Flags().BoolVar(&nc.force, "force", false, "install even if a name is another program on PATH")
	cmd.Flags().BoolVar(&nc.interactive, "interactive", false, "ask for the command name and aliases on the terminal")
	return cmd
}

// installRelease places the release, links the command names and installs or refreshes the
// plugin in every account.
func (a *app) installRelease(src, version string, nc nameChoice) error {
	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	cfg, fresh, err := install.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	if err := release.CheckInstallDir(a.layout.DataDir); err != nil {
		return err
	}
	accepted := install.Names(cfg)
	oldCmd, oldAliases := cfg.Command, cfg.Aliases
	if nc.command != "" {
		cfg.Command = nc.command
	}
	if cfg.Command == "" {
		cfg.Command = config.DefaultCommand
	}
	switch {
	case nc.aliasSet:
		cfg.Aliases = nc.aliases
	case fresh:
		cfg.Aliases = config.DefaultAliases()
	}
	if nc.interactive && nc.command == "" && !nc.aliasSet {
		if in, out, done, ok := a.terminalIO(); ok {
			p := namePrompt{in: bufio.NewReader(in), out: out, check: a.nameCheck(accepted, nc.force)}
			var forced map[string]bool
			cfg.Command, cfg.Aliases, forced = p.run(cfg.Command, cfg.Aliases)
			done()
			for n := range forced {
				accepted[n] = true
			}
		}
	}
	if err := a.nameCheck(accepted, nc.force).Check(cfg); err != nil {
		return err
	}
	unlock, err := fsx.Lock(a.installLockPath())
	var held fsx.LockedError
	if errors.As(err, &held) {
		return fmt.Errorf("another squadrn install or update is running (pid %d); try again when it has finished", held.PID)
	} else if err != nil {
		return err
	}
	defer unlock()
	// Keep the installed release for --rollback. The snapshot is staged first because src may be
	// the previous snapshot itself (a rollback).
	staged := a.layout.Previous() + ".new"
	if err := a.layout.Snapshot(staged); err != nil {
		return fmt.Errorf("keep the installed release for rollback: %w", err)
	}
	if err := a.layout.Install(src, version); err != nil {
		_ = os.RemoveAll(staged)
		return err
	}
	if fsx.IsDir(staged) {
		if err := fsx.MoveDir(staged, a.layout.Previous()); err != nil {
			a.warnf("could not keep the previous release for --rollback (%v); a rollback may reinstall an older one", err)
			_ = os.RemoveAll(staged)
		}
	}
	beta := nc.selfInstall && cfg.UpdateChannel == "" && release.Prerelease(version)
	if beta {
		cfg.UpdateChannel = update.Beta
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}
	a.cmd, a.cfg, a.cfgErr = cfg.Command, cfg, nil
	a.out("installed squadrn %s → %s\n", version, a.tilde(a.layout.Binary()))
	if beta {
		a.out("  pre-release: following the beta channel (back to stable: %s config set update_channel stable)\n", cfg.Command)
	}
	if err := a.linkNames(cfg); err != nil {
		return err
	}
	if !fresh && oldCmd != "" {
		if msg := renameSummary(oldCmd, oldAliases, cfg.Command, cfg.Aliases); msg != "" {
			a.out("%s\n", msg)
		}
	}
	a.installPlugins()
	// An update must never be blocked by Git (that would strand users on old releases); say it.
	if err := gitver.Require(context.Background(), runtime.GOOS); err != nil {
		a.warnf("%v", err)
	}
	if !onPATH(a.layout.BinDir) {
		a.out("%s: %s\n", pathWhere, doctor.PathFix(a.layout.BinDir))
	}
	a.out("next: %s doctor\n", cfg.Command)
	return nil
}

// installLockPath keeps two installs, updates or rollbacks from running at once.
func (a *app) installLockPath() string { return filepath.Join(a.layout.DataDir, "install.lock") }

func (a *app) nameCheck(accepted map[string]bool, force bool) install.NameCheck {
	self, _ := os.Executable()
	return install.NameCheck{Layout: a.layout, Accepted: accepted, Force: force, Self: self}
}

func (a *app) linkNames(cfg config.Config) error {
	names := append([]string{cfg.Command}, cfg.Aliases...)
	res, err := a.layout.LinkCommands(names, a.now())
	if err != nil {
		return err
	}
	for _, l := range res.Created {
		a.out("  linked %s\n", a.tilde(l))
	}
	for _, l := range res.Removed {
		a.out("  removed %s\n", a.tilde(l))
	}
	for _, l := range res.BackedUp {
		a.out("  moved an unrelated file aside: %s\n", a.tilde(l))
	}
	a.writeCompletions(names, res.Removed)
	return nil
}

// completionDirs are the per-user completion dirs of each shell; only existing ones are used.
func (a *app) completionDirs() map[string]string {
	dirs := config.DirsFor(a.store.Home, "")
	return map[string]string{
		"zsh":  filepath.Join(a.store.Home, ".zsh", "completions"),
		"bash": filepath.Join(dirs.Data, "bash-completion", "completions"),
		"fish": filepath.Join(dirs.Config, "fish", "completions"),
	}
}

func completionFile(shell, dir, name string) string {
	switch shell {
	case "zsh":
		return filepath.Join(dir, "_"+name)
	case "fish":
		return filepath.Join(dir, name+".fish")
	}
	return filepath.Join(dir, name)
}

// writeCompletions generates completion scripts for every name into the shells' completion dirs
// that exist, and removes the ones squadrn generated for names that are gone.
func (a *app) writeCompletions(names, removedLinks []string) {
	use := a.root.Use
	defer func() { a.root.Use = use }()
	if runtime.GOOS == "windows" {
		a.writePowerShellCompletion(names)
		return
	}
	for shell, dir := range a.completionDirs() {
		if !fsx.IsDir(dir) {
			continue
		}
		for _, link := range removedLinks {
			file := completionFile(shell, dir, filepath.Base(link))
			if b, err := os.ReadFile(file); err == nil && generatedCompletion(b, filepath.Base(link)) {
				_ = os.Remove(file)
			}
		}
		for _, n := range names {
			a.root.Use = n
			var buf bytes.Buffer
			var err error
			switch shell {
			case "zsh":
				err = a.root.GenZshCompletion(&buf)
			case "bash":
				err = a.root.GenBashCompletionV2(&buf, true)
			case "fish":
				err = a.root.GenFishCompletion(&buf, true)
			}
			if err == nil {
				_ = fsx.WriteBytes(completionFile(shell, dir, n), buf.Bytes(), 0o644)
			}
		}
		a.out("  %s completion → %s\n", shell, a.tilde(dir))
	}
}

// PowerShellCompletion is the completion script for every command name, which install.ps1 offers
// to load from the user's PowerShell profile.
func (a *app) powerShellCompletionPath() string {
	return filepath.Join(a.layout.DataDir, "completion.ps1")
}

func (a *app) writePowerShellCompletion(names []string) {
	var buf bytes.Buffer
	for _, n := range names {
		a.root.Use = n
		if err := a.root.GenPowerShellCompletionWithDesc(&buf); err != nil {
			return
		}
		buf.WriteString("\n")
	}
	if fsx.WriteBytes(a.powerShellCompletionPath(), buf.Bytes(), 0o644) == nil {
		a.out("  PowerShell completion → %s (install.ps1 -AddToProfile loads it)\n", a.tilde(a.powerShellCompletionPath()))
	}
}

// generatedCompletion recognises a cobra completion script for name (ours, not the user's).
func generatedCompletion(b []byte, name string) bool {
	return bytes.Contains(b, []byte(name)) && bytes.Contains(b, []byte("__complete"))
}

func (a *app) installPlugins() {
	accts, err := a.store.List()
	if err != nil {
		a.warnf("%v", err)
		return
	}
	bySpec := map[string]bool{}
	for _, acct := range accts {
		if bySpec[acct.Provider] {
			continue
		}
		bySpec[acct.Provider] = true
		ag, err := a.agent(acct.Spec)
		if err != nil {
			a.warnf("%v; install the plugin later by re-running install.sh", err)
			continue
		}
		same := accts[:0:0]
		for _, x := range accts {
			if x.Provider == acct.Provider {
				same = append(same, x)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		for _, r := range install.Plugins(ctx, same, a.store, ag, a.layout.DataDir) {
			if r.Action == install.PluginFailed {
				a.errf("  %s: plugin update failed: %v\n", r.Account.Label(), r.Err)
				continue
			}
			a.out("  %s: plugin %s\n", r.Account.Label(), r.Action)
		}
		cancel()
	}
}

func (a *app) configCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show settings; get or set one (command and aliases relink ~/.local/bin)",
		Example: fmt.Sprintf(`  %[1]s config
  %[1]s config get protected_branches
  %[1]s config set protected_branches main,release/*
  %[1]s config set command ai`, a.cmd),
		Args: subcommandArgs,
		RunE: func(*cobra.Command, []string) error {
			if a.cfgErr != nil {
				return a.cfgErr
			}
			path, _ := config.Path()
			values := map[string]string{}
			for _, k := range config.Keys {
				values[k], _ = a.cfg.Get(k)
			}
			if asJSON {
				values["file"] = path
				return a.printJSON(values)
			}
			a.out("%-19s %s\n", "file", a.tilde(path))
			env := config.EnvOverrides()
			for _, k := range config.Keys {
				if v, ok := env[k]; ok {
					a.out("%-19s %s  (from %s)\n", k, values[k], v)
					continue
				}
				a.out("%-19s %s\n", k, values[k])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	var getJSON bool
	get := &cobra.Command{
		Use:       "get <key>",
		Short:     "Print one setting",
		Args:      oneArg,
		ValidArgs: config.Keys,
		RunE: func(_ *cobra.Command, args []string) error {
			if a.cfgErr != nil {
				return a.cfgErr
			}
			v, err := a.cfg.Get(args[0])
			if err != nil {
				return err
			}
			if getJSON {
				return a.printJSON(map[string]string{"key": args[0], "value": v})
			}
			a.out("%s\n", v)
			return nil
		},
	}
	get.Flags().BoolVar(&getJSON, "json", false, "print JSON")
	cmd.AddCommand(get)
	var force bool
	set := &cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Change one setting (lists are comma separated; \"\" clears a list, or restores the default protected_branches)",
		Args:      exactArgs(2),
		ValidArgs: config.Keys,
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := config.Path()
			if err != nil {
				return err
			}
			cfg, err := config.Read(path)
			if errors.Is(err, os.ErrNotExist) {
				cfg, err = config.Config{}, nil
			}
			if err != nil {
				return err
			}
			if cfg.Command == "" {
				cfg.Command = config.DefaultCommand
			}
			accepted := install.Names(cfg)
			old := cfg
			if err := cfg.Set(args[0], args[1]); err != nil {
				return err
			}
			relink := args[0] == "command" || args[0] == "aliases"
			if relink {
				if !fsx.IsFile(a.layout.Binary()) {
					return fmt.Errorf("squadrn is not installed in %s; run install.sh first", a.tilde(a.layout.DataDir))
				}
				if err := a.nameCheck(accepted, force).Check(cfg); err != nil {
					return err
				}
			}
			if err := config.Save(path, cfg); err != nil {
				return err
			}
			if !relink {
				v, _ := cfg.Get(args[0])
				a.out("%s = %s\n", args[0], v)
				return nil
			}
			a.cmd = cfg.Command
			if err := a.linkNames(cfg); err != nil {
				return err
			}
			if msg := renameSummary(old.Command, old.Aliases, cfg.Command, cfg.Aliases); msg != "" {
				a.out("%s\n", msg)
			}
			return nil
		},
	}
	set.Flags().BoolVar(&force, "force", false, "use a command name even if another program on PATH has it")
	cmd.AddCommand(set)
	return cmd
}

func onPATH(dir string) bool {
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(d) == filepath.Clean(dir) {
			return true
		}
	}
	return false
}
