// Command reconcile matches an internal ledger export against an external
// settlement file and reports every categorized gap (FR-C). It is a cobra CLI
// configured via viper (flags + env); adapters normalize each input to
// canonical.Record before the streaming matcher runs.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/reconcile/adapters"
	"github.com/Philip-Nwabuwa/Invariant-Core/internal/serviceboot"
	"github.com/Philip-Nwabuwa/Invariant-Core/pkg/canonical"
)

const defaultDBURL = "postgres://invariantcore:invariantcore@localhost:5432/invariantcore?sslmode=disable"

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
			if err := v.BindPFlags(cmd.Flags()); err != nil {
				return fmt.Errorf("bind flags: %w", err)
			}
			return runReconcile(cmd, config{
				internal:  v.GetString("internal"),
				external:  v.GetString("external"),
				extFormat: v.GetString("external-format"),
				window:    v.GetDuration("tolerance-window"),
				format:    v.GetString("format"),
				dbURL:     v.GetString("db-url"),
				noPersist: v.GetBool("no-persist"),
			})
		},
	}
	c.Flags().String("internal", "", "path to the internal ledger export (JSONL)")
	c.Flags().String("external", "", "path to the external settlement file")
	c.Flags().String("external-format", "nibss", "external file format: nibss|csv")
	c.Flags().Duration("tolerance-window", 120*time.Second, "max initiated_at difference for a match")
	c.Flags().String("format", "text", "report format: text|json")
	c.Flags().String("db-url", serviceboot.EnvOr("DB_URL", defaultDBURL), "Postgres URL for run persistence")
	c.Flags().Bool("no-persist", false, "skip writing the run to Postgres")
	return c
}

type config struct {
	internal  string
	external  string
	extFormat string
	window    time.Duration
	format    string
	dbURL     string
	noPersist bool
}

func runReconcile(cmd *cobra.Command, cfg config) error {
	if cfg.internal == "" || cfg.external == "" {
		return fmt.Errorf("both --internal and --external are required")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	internal, err := readInternal(cfg.internal)
	if err != nil {
		return err
	}

	external, closeExt, err := openExternal(cfg.external, cfg.extFormat)
	if err != nil {
		return err
	}
	defer closeExt()

	res, err := reconcile.Match(internal, external, cfg.window)
	if err != nil {
		return err
	}

	report := reconcile.NewReport(cfg.internal, cfg.external, res)
	if err := render(cmd.OutOrStdout(), report, cfg.format); err != nil {
		return err
	}

	if cfg.noPersist {
		return nil
	}
	return persist(ctx, cmd, cfg, res)
}

// readInternal drains the JSONL ledger export into a slice; the internal side is
// indexed in memory by the matcher.
func readInternal(path string) ([]canonical.Record, error) {
	rd, err := adapters.OpenLedgerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rd.Close() }()

	var recs []canonical.Record
	for {
		rec, err := rd.Next()
		if err == io.EOF {
			return recs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read internal export: %w", err)
		}
		recs = append(recs, rec)
	}
}

// openExternal builds the streaming external adapter for the requested format.
func openExternal(path, format string) (reconcile.Stream, func(), error) {
	switch format {
	case "nibss":
		rd, err := adapters.OpenNIBSSFile(path)
		if err != nil {
			return nil, nil, err
		}
		return rd, func() { _ = rd.Close() }, nil
	case "csv":
		rd, err := adapters.OpenCSVFile(path)
		if err != nil {
			return nil, nil, err
		}
		return rd, func() { _ = rd.Close() }, nil
	default:
		return nil, nil, fmt.Errorf("unknown --external-format %q (want nibss|csv)", format)
	}
}

// render writes the report in the requested format.
func render(w io.Writer, report reconcile.Report, format string) error {
	switch format {
	case "text":
		_, err := io.WriteString(w, report.Text())
		return err
	case "json":
		b, err := report.JSON()
		if err != nil {
			return err
		}
		_, err = w.Write(append(b, '\n'))
		return err
	default:
		return fmt.Errorf("unknown --format %q (want text|json)", format)
	}
}

// persist writes the run to Postgres unless an identical run (same input
// fingerprint) already exists, in which case it is skipped (AC-4).
func persist(ctx context.Context, cmd *cobra.Command, cfg config, res reconcile.Result) error {
	pool, err := pgxpool.New(ctx, cfg.dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	store := reconcile.NewStore(pool)
	fingerprint, err := reconcile.FileFingerprint(cfg.internal, cfg.external)
	if err != nil {
		return err
	}

	if id, found, err := store.FindByFingerprint(ctx, fingerprint); err != nil {
		return err
	} else if found {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "reconcile: identical inputs already reconciled (run %s); not persisting again\n", id)
		return nil
	}

	id, err := store.Persist(ctx, cfg.internal, cfg.external, fingerprint, res)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "reconcile: persisted run %s\n", id)
	return nil
}
