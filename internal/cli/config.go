package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/squadrn/squadrn-cli/internal/config"
	"github.com/squadrn/squadrn-cli/internal/fsx"
	"github.com/squadrn/squadrn-cli/internal/install"
)

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
			if asJSON {
				values := map[string]any{"file": path}
				for _, k := range config.Keys {
					values[k], _ = a.cfg.Value(k)
				}
				return a.printJSON(values)
			}
			values := map[string]string{}
			for _, k := range config.Keys {
				values[k], _ = a.cfg.Get(k)
			}
			a.out("%-19s %s\n", "file", a.tilde(path))
			env := config.EnvOverrides()
			for _, k := range config.Keys {
				if v, ok := env[k]; ok {
					a.out("%-19s %s  (from %s)\n", k, dash(values[k]), v)
					continue
				}
				a.out("%-19s %s\n", k, dash(values[k]))
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
			if getJSON {
				v, err := a.cfg.Value(args[0])
				if err != nil {
					return err
				}
				return a.printJSON(map[string]any{"key": args[0], "value": v})
			}
			v, err := a.cfg.Get(args[0])
			if err != nil {
				return err
			}
			a.out("%s\n", v)
			return nil
		},
	}
	get.Flags().BoolVar(&getJSON, "json", false, "print JSON")
	cmd.AddCommand(get)
	var force bool
	set := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change one setting",
		Long: `Change one setting. Lists are comma separated; "" clears a list, or restores the default
protected_branches. Changing command or aliases relinks them in the bin dir.`,
		Args:      exactArgs(2),
		ValidArgs: config.Keys,
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := config.Path()
			if err != nil {
				return err
			}
			cfg, err := config.Read(path)
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
				v, _ := cfg.WithDefaults().Get(args[0])
				if raw, _ := cfg.Get(args[0]); raw != v {
					v += " (default)"
				}
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
	set.Flags().BoolVarP(&force, "force", "f", false, "use a command name even if another program on PATH has it")
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
