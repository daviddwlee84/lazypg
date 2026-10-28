package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/daviddwlee84/lazypg/internal/output"
)

func newBranch() *cobra.Command {
	c := &cobra.Command{
		Use:   "branch",
		Short: "Cheap database copies for trial and error (local/dev only)",
		Long: `Branches copy every owned database with CREATE DATABASE … TEMPLATE into
<db>__<branch>. While a branch is active, every command (migrate, exec, url, …)
targets the copies; ` + "`branch use main`" + ` returns to the originals.`,
	}
	var from string
	var noSwitch bool
	create := &cobra.Command{
		Use:   "create <name>",
		Short: "Copy the current databases into a new branch and switch to it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, t, err := target(cmd.Context(), false)
			if err != nil {
				return err
			}
			a.Banner(t, "branch "+args[0])
			if err := a.BranchCreate(cmd.Context(), t, args[0], from, !noSwitch); err != nil {
				return err
			}
			if !noSwitch {
				a.Out.Info("now on branch %s", args[0])
			}
			return nil
		},
	}
	create.Flags().StringVar(&from, "from", "", "source branch (default: the active one; \"main\" for the originals)")
	create.Flags().BoolVar(&noSwitch, "no-switch", false, "create without switching")

	list := &cobra.Command{
		Use:   "list",
		Short: "List branches",
		RunE: func(cmd *cobra.Command, args []string) error {
			a, t, err := target(cmd.Context(), false)
			if err != nil {
				return err
			}
			bs, err := a.BranchList(cmd.Context(), t)
			if err != nil {
				return err
			}
			return a.Out.Result(bs, func(w io.Writer) {
				for _, b := range bs {
					mark := "  "
					name := b.Name
					if b.Current {
						mark, name = "* ", output.BoldStyle.Render(b.Name)
					}
					fmt.Fprintf(w, "%s%s  %s\n", mark, name, output.Dim(strings.Join(b.DBNames, ", ")))
				}
			})
		},
	}
	use := &cobra.Command{
		Use:   "use <name|main>",
		Short: "Switch the active branch",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, t, err := target(cmd.Context(), false)
			if err != nil {
				return err
			}
			if err := a.BranchUse(cmd.Context(), t, args[0]); err != nil {
				return err
			}
			a.Out.Info("now on branch %s", args[0])
			return nil
		},
	}
	del := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Drop a branch's databases",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, t, err := target(cmd.Context(), false)
			if err != nil {
				return err
			}
			a.Banner(t, "branch "+args[0])
			return a.BranchDelete(cmd.Context(), t, args[0])
		},
	}
	c.AddCommand(create, list, use, del)
	return c
}
