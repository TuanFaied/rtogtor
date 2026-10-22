package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TuanFaied/rtogtor/engine/adapters/relational"
	"github.com/TuanFaied/rtogtor/engine/adapters/relational/postgresql"
	"github.com/TuanFaied/rtogtor/engine/rtgerr"
	"github.com/TuanFaied/rtogtor/engine/schemameta"
)

func newInspectCmd(opts *options) *cobra.Command {
	var (
		sourceEnv string
		schemas   []string
		timeout   time.Duration
	)
	cmd := &cobra.Command{
		Use:   "inspect --source-env VAR",
		Short: "Inspect a PostgreSQL database's schema (read-only)",
		Long: `Inspect tables, columns, keys and types of a PostgreSQL database and print
metadata conforming to specs/schema-metadata.schema.json.

The connection URL is read from the environment variable named by
--source-env, never from a flag, so it does not appear in process lists.
Inspection runs in a read-only transaction and never modifies the database.

Example:
  SOURCE_URL=postgres://user:password@localhost:5432/shop rtogtor inspect --source-env SOURCE_URL`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			connURL, err := urlFromEnv(sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			adapter, err := postgresql.Open(ctx, connURL)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			defer func() { _ = adapter.Close() }()

			doc, err := adapter.Inspect(ctx, relational.InspectOptions{Schemas: schemas})
			if err != nil {
				return reportError(cmd, opts, err)
			}
			if opts.format == "json" {
				return writeJSON(cmd.OutOrStdout(), doc)
			}
			printInspection(cmd.OutOrStdout(), doc)
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceEnv, "source-env", "", "name of the environment variable holding the PostgreSQL URL")
	cmd.Flags().StringSliceVar(&schemas, "schema", nil, `schema to inspect; repeatable (default "public")`)
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall time limit for connecting and inspecting")
	_ = cmd.MarkFlagRequired("source-env")
	return cmd
}

// urlFromEnv reads a PostgreSQL URL from the named environment variable.
// The value itself is never included in errors.
func urlFromEnv(name string) (string, error) {
	v, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(v) == "" {
		return "", rtgerr.New(rtgerr.ConnConfig, "environment variable %s is not set or is empty", name)
	}
	if !strings.HasPrefix(v, "postgres://") && !strings.HasPrefix(v, "postgresql://") {
		return "", rtgerr.New(rtgerr.ConnConfig, "environment variable %s must hold a postgres:// or postgresql:// URL", name)
	}
	return v, nil
}

func printInspection(out io.Writer, doc *schemameta.Document) {
	fmt.Fprintf(out, "%s %s, database %q\n", doc.Database.Product, doc.Database.Version, doc.Database.Name)
	for _, s := range doc.Schemas {
		fmt.Fprintf(out, "\nschema %s (%d tables)\n", s.Name, len(s.Tables))
		for _, t := range s.Tables {
			key := "no primary key"
			if t.PrimaryKey != nil {
				key = "key (" + strings.Join(t.PrimaryKey.Columns, ", ") + ")"
			}
			fmt.Fprintf(out, "  %s — %d columns, %s\n", t.Name, len(t.Columns), key)
			for _, c := range t.Columns {
				typ := "UNSUPPORTED"
				if c.TypeInfo.Type != nil {
					typ = string(*c.TypeInfo.Type)
				}
				null := ""
				if !c.Nullable {
					null = " not null"
				}
				fmt.Fprintf(out, "      %-24s %-12s %s%s\n", c.Name, typ, c.TypeInfo.NativeType, null)
			}
			for _, fk := range t.ForeignKeys {
				fmt.Fprintf(out, "      FK (%s) -> %s.%s (%s)\n", strings.Join(fk.Columns, ", "),
					fk.ReferencedSchema, fk.ReferencedTable, strings.Join(fk.ReferencedColumns, ", "))
			}
		}
	}
	if len(doc.Warnings) > 0 {
		fmt.Fprintln(out)
		for _, w := range doc.Warnings {
			fmt.Fprintf(out, "warning %s at %s: %s\n", w.Code, w.Path, w.Message)
		}
	}
}
