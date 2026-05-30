// Command reconcile is the reconciliation CLI. Sprint 0: the command tree and
// flag/env wiring exist; `run` is a stub. The matching engine, adapters, and
// reports arrive in Sprint 4.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "reconcile",
		Short:         "Reconcile an internal ledger export against an external settlement file",
		SilenceUsage:  true,
		SilenceErrors: false,
	}
	root.AddCommand(runCmd())
	return root
}

func runCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "run",
		Short: "Match two inputs and emit a categorized exceptions report",
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := viper.New()
			v.AutomaticEnv()
			_ = v.BindPFlags(cmd.Flags())
			internal := v.GetString("internal")
			external := v.GetString("external")
			return fmt.Errorf("reconcile run: not implemented (internal=%q external=%q) — arrives in Sprint 4",
				internal, external)
		},
	}
	c.Flags().String("internal", "", "path to the internal ledger export")
	c.Flags().String("external", "", "path to the external settlement file")
	return c
}
