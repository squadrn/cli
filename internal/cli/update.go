package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"sprawling-agents/internal/config"
	"sprawling-agents/internal/fsx"
	"sprawling-agents/internal/install"
	"sprawling-agents/internal/proc"
	"sprawling-agents/internal/release"
	"sprawling-agents/internal/update"
)

// Repo is the GitHub repository releases come from, set at build time with
// -ldflags "-X sprawling-agents/internal/cli.Repo=owner/sprawling-agents". Only the --repo flag
// overrides it: an environment variable could be planted to redirect updates.
var Repo = ""

const ghTimeout = 5 * time.Minute

func gh(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := proc.Output(ctx, proc.Options{}, bin, args...)
	return strings.TrimSpace(string(out)), err
}

func (a *app) selfUpdateCmd() *cobra.Command {
	var o updateOptions
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update sprawl and its plugin in every account (--check: only look, --rollback: undo)",
		Example: fmt.Sprintf(`  %[1]s self-update --check       what is new
  %[1]s self-update               install the newest release of your channel
  %[1]s self-update --rollback    go back to the release installed before`, a.cmd),
		Args: noArgs,
		RunE: func(*cobra.Command, []string) error {
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
	cmd.Flags().BoolVar(&o.background, "background", false, "refresh the update notice cache quietly")
	_ = cmd.Flags().MarkHidden("background")
	return cmd
}

type updateOptions struct {
	check, allowDowngrade, rollback, background bool
	tag, repo                                   string
}

func (a *app) updateStatePath() string {
	return filepath.Join(config.DirsFor(a.store.Home, config.Slug).Cache, "update.json")
}

// releases lists the published releases of repo.
func releases(ctx context.Context, ghBin, repo string) ([]update.Release, error) {
	out, err := gh(ctx, ghBin, "release", "list", "--repo", repo, "--limit", "50", "--json", "tagName,isPrerelease,isDraft")
	if err != nil {
		return nil, err
	}
	var list []struct {
		TagName      string `json:"tagName"`
		IsPrerelease bool   `json:"isPrerelease"`
		IsDraft      bool   `json:"isDraft"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return nil, fmt.Errorf("parse release list: %w", err)
	}
	rels := make([]update.Release, len(list))
	for i, r := range list {
		rels[i] = update.Release{Tag: r.TagName, Prerelease: r.IsPrerelease, Draft: r.IsDraft}
	}
	return rels, nil
}

func (a *app) selfUpdate(o updateOptions) error {
	repo := o.repo
	if repo == "" {
		repo = Repo
	}
	if repo == "" {
		if o.background {
			return nil
		}
		return usagef("no release repository configured; pass --repo owner/name")
	}
	ghBin, err := exec.LookPath("gh")
	if err != nil {
		if o.background {
			update.Save(a.updateStatePath(), update.State{CheckedAt: a.now(), Err: "gh is not installed"})
			return nil
		}
		return fmt.Errorf("gh is required, logged in with access to %s", repo)
	}
	timeout := ghTimeout
	if o.background {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tag := o.tag
	if tag == "" {
		rels, err := releases(ctx, ghBin, repo)
		if err != nil {
			if o.background {
				update.Save(a.updateStatePath(), update.State{CheckedAt: a.now(), Err: err.Error()})
				return nil
			}
			return fmt.Errorf("cannot list releases of %s (check gh auth status and your access): %w", repo, err)
		}
		if tag = update.Newest(rels, a.cfg.Channel()); tag == "" {
			if o.background {
				update.Save(a.updateStatePath(), update.State{CheckedAt: a.now()})
				return nil
			}
			return fmt.Errorf("%s has no %s release yet", repo, a.cfg.Channel())
		}
		prev := update.Load(a.updateStatePath())
		update.Save(a.updateStatePath(), update.State{CheckedAt: a.now(), Latest: tag, NotifiedAt: prev.NotifiedAt})
	}
	if o.background {
		return nil
	}
	current := "v" + strings.TrimPrefix(Version, "v")
	cmp, comparable := release.Compare(tag, current)
	switch {
	case tag == current || (comparable && cmp == 0):
		a.out("sprawl %s is up to date (%s channel)\n", current, a.cfg.Channel())
		return nil
	case comparable && cmp < 0 && !o.allowDowngrade:
		return usagef("%s is older than the installed %s; pass --allow-downgrade to install it", tag, current)
	case o.check:
		a.out("update available: %s → %s\n", current, tag)
		if notes, err := gh(ctx, ghBin, "release", "view", tag, "--repo", repo, "--json", "body", "--jq", ".body"); err == nil && strings.TrimSpace(notes) != "" {
			a.out("\n%s\n\n", releaseNotes(notes, 25))
		}
		a.out("install it with: %s self-update\n", a.cmd)
		return nil
	}
	tmp, err := os.MkdirTemp("", "sprawl-update-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	asset := release.AssetName(tag, runtime.GOOS, runtime.GOARCH)
	// The signature comes first and on its own: a release without one is refused with a clear
	// reason, before the archive is downloaded.
	if a.signers != "" {
		if _, err := gh(ctx, ghBin, "release", "download", tag, "--repo", repo, "--pattern", release.SignatureName, "--dir", tmp); err != nil {
			return fmt.Errorf("release %s has no signature (%s); refusing to install it: %w", tag, release.SignatureName, err)
		}
	}
	if _, err := gh(ctx, ghBin, "release", "download", tag, "--repo", repo, "--pattern", asset, "--pattern", release.ChecksumsName, "--dir", tmp); err != nil {
		return fmt.Errorf("download %s: %w", tag, err)
	}
	if err := a.verifyRelease(ctx, ghBin, tmp, asset, repo); err != nil {
		return err
	}
	src := filepath.Join(tmp, "release")
	if err := release.Extract(filepath.Join(tmp, asset), src); err != nil {
		return err
	}
	return a.installRelease(src, tag, nameChoice{})
}

// verifyRelease checks, in order: the signature of checksums.txt against the keys built into
// this binary, the archive's checksum, and GitHub build provenance where it exists.
func (a *app) verifyRelease(ctx context.Context, ghBin, dir, asset, repo string) error {
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
	return a.verifyAttestation(ctx, ghBin, archive, repo)
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

// verifyAttestation checks the build provenance GitHub attached to the archive. Private
// repositories on plans without artifact attestations have none; the signature covers them.
func (a *app) verifyAttestation(ctx context.Context, ghBin, archive, repo string) error {
	owner, _, _ := strings.Cut(repo, "/")
	_, err := gh(ctx, ghBin, "attestation", "verify", archive, "--owner", owner)
	if err == nil {
		a.out("verified build provenance of %s\n", filepath.Base(archive))
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "no attestations found") || strings.Contains(msg, "unknown command") || strings.Contains(msg, "HTTP 404") || strings.Contains(msg, "HTTP 403") {
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
			return a.installRelease(args[0], Version, nc)
		},
	}
	cmd.Flags().StringVar(&nc.command, "command", "", "command name to install under (default: configured, else sprawl)")
	cmd.Flags().StringSliceVar(&nc.aliases, "alias", nil, "extra command names, comma separated")
	cmd.Flags().BoolVar(&nc.force, "force", false, "install even if a name is another program on PATH")
	return cmd
}

// installRelease places the release, links the command names, migrates installs of previous
// product names once, and installs or refreshes the plugin in every account.
func (a *app) installRelease(src, version string, nc nameChoice) error {
	cfgPath, err := config.Path()
	if err != nil {
		return err
	}
	legacy, err := config.LegacyPaths()
	if err != nil {
		return err
	}
	cfg, migratedFrom, fresh, err := install.LoadConfig(cfgPath, legacy)
	if err != nil {
		return err
	}
	accepted := install.Names(cfg)
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
	if err := a.nameCheck(accepted, nc.force).Check(cfg); err != nil {
		return err
	}
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
		_ = os.RemoveAll(a.layout.Previous())
		_ = os.Rename(staged, a.layout.Previous())
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		return err
	}
	a.cmd, a.cfg, a.cfgErr = cfg.Command, cfg, nil
	a.out("installed sprawl %s → %s\n", version, a.tilde(a.layout.Binary()))
	if migratedFrom != "" {
		a.out("  settings migrated from %s\n", a.tilde(migratedFrom))
	}
	if err := a.linkNames(cfg); err != nil {
		return err
	}
	moved, errs := install.MigrateLegacy(a.store.Home, a.now())
	for _, m := range moved {
		a.out("  previous %s files moved to %s\n", m.Name, a.tilde(m.Backup))
	}
	for _, e := range errs {
		a.warnf("%v", e)
	}
	a.installPlugins()
	if !onPATH(a.layout.BinDir) {
		a.out("add to your shell rc: export PATH=\"%s:$PATH\"\n", a.layout.BinDir)
	}
	a.out("next: %s doctor\n", cfg.Command)
	return nil
}

func (a *app) nameCheck(accepted map[string]bool, force bool) install.NameCheck {
	self, _ := os.Executable()
	return install.NameCheck{Layout: a.layout, Accepted: accepted, Force: force, Self: self}
}

func (a *app) linkNames(cfg config.Config) error {
	names := append([]string{cfg.Command}, cfg.Aliases...)
	var ours []string
	for _, l := range release.Legacies(a.store.Home) {
		ours = append(ours, l.Binary)
	}
	res, err := a.layout.LinkCommands(names, ours, a.now())
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
// that exist, and removes the ones sprawl generated for names that are gone.
func (a *app) writeCompletions(names, removedLinks []string) {
	use := a.root.Use
	defer func() { a.root.Use = use }()
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
			for _, id := range r.Removed {
				a.out("  %s: removed plugin %s\n", r.Account.Label(), id)
			}
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
			for _, k := range config.Keys {
				a.out("%-19s %s\n", k, values[k])
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	cmd.AddCommand(&cobra.Command{
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
			a.out("%s\n", v)
			return nil
		},
	})
	var force bool
	set := &cobra.Command{
		Use:       "set <key> <value>",
		Short:     "Change one setting (lists are comma separated; \"\" clears)",
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
			if err := cfg.Set(args[0], args[1]); err != nil {
				return err
			}
			relink := args[0] == "command" || args[0] == "aliases"
			if relink {
				if !fsx.IsFile(a.layout.Binary()) {
					return fmt.Errorf("sprawl is not installed in %s; run install.sh first", a.tilde(a.layout.DataDir))
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
			return a.linkNames(cfg)
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
