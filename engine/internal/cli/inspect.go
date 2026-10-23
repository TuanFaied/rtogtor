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

	rtogtor "github.com/TuanFaied/rtogtor/engine"
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
		Short: "Inspect a database's schema (read-only)",
		Long: `Inspect tables, columns, keys and types of a PostgreSQL database, or the
labels, relationship types and property types of a Neo4j database, and print
metadata conforming to specs/schema-metadata.schema.json. The URL scheme
selects the database.

The connection URL is read from the environment variable named by
--source-env, never from a flag, so it does not appear in process lists.
Inspection runs in read-only transactions and never modifies the database.
Neo4j property types are sampled from the stored data.

Examples:
  SOURCE_URL=postgres://user:password@localhost:5432/shop rtogtor inspect --source-env SOURCE_URL
  GRAPH_URL=neo4j://neo4j:password@localhost:7687 rtogtor inspect --source-env GRAPH_URL`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := connFromEnv(sourceEnv)
			if err != nil {
				return reportError(cmd, opts, err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
			defer stop()
			ctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			doc, err := rtogtor.Inspect(ctx, conn, schemas...)
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
	cmd.Flags().StringVar(&sourceEnv, "source-env", "", "name of the environment variable holding the PostgreSQL or Neo4j URL")
	cmd.Flags().StringSliceVar(&schemas, "schema", nil, `PostgreSQL schema to inspect; repeatable (default "public")`)
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "overall time limit for connecting and inspecting")
	_ = cmd.MarkFlagRequired("source-env")
	return cmd
}

func printInspection(out io.Writer, doc *schemameta.Document) {
	fmt.Fprintf(out, "%s %s, database %q\n", doc.Database.Product, doc.Database.Version, doc.Database.Name)
	for _, l := range doc.Labels {
		fmt.Fprintf(out, "\n:%s (%d properties)\n", l.Name, len(l.Properties))
		for _, p := range l.Properties {
			types := make([]string, len(p.Types))
			for i, t := range p.Types {
				types[i] = t.NativeType
			}
			mandatory := ""
			if p.Mandatory {
				mandatory = " (on every node)"
			}
			fmt.Fprintf(out, "      %-24s %s%s\n", p.Name, strings.Join(types, " | "), mandatory)
		}
	}
	for _, r := range doc.RelationshipTypes {
		fmt.Fprintf(out, "\n[:%s] (%d properties)\n", r.Name, len(r.Properties))
	}
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
