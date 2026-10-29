package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	rtogtor "github.com/TuanFaied/rtogtor/engine"
	"github.com/TuanFaied/rtogtor/engine/scaffold"
)

func newScaffoldCmd(opts *options) *cobra.Command {
	var (
		sourceEnv, reverse, output, name string
		schemas                          []string
		force                            bool
		timeout                          time.Duration
	)
	cmd := &cobra.Command{
		Use:   "scaffold (--source-env VAR | --reverse FILE) [--output FILE]",
		Short: "Draft a mapping from a database schema, or reverse a mapping",
		Long: `Draft a mapping to review and edit, never to run blindly.

--source-env inspects a PostgreSQL database (read-only) and drafts a
relational-to-graph mapping: one entity and label per table with a primary
key (or a unique key on NOT NULL columns), one property per column of a
supported type, and one relationship per foreign key between mapped tables.
Names are derived mechanically (order_items → OrderItem, unit_price →
unitPrice, customer_id → CUSTOMER), and every choice is written as a comment.

--reverse drafts the graph-to-relational mapping that reads back what a
relational-to-graph mapping writes.

The draft is validated before it is written; check it against your data
with 'rtogtor plan'.

Examples:
  rtogtor scaffold --source-env SOURCE_URL > mapping.yaml
  rtogtor scaffold --reverse mapping.yaml --output mapping-reverse.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (sourceEnv == "") == (reverse == "") {
				return errors.New("give exactly one of --source-env and --reverse")
			}
			var d *scaffold.Draft
			if reverse != "" {
				m, err := loadMapping(cmd, opts, reverse)
				if err != nil {
					return err
				}
				if d, err = scaffold.Reverse(m); err != nil {
					return reportError(cmd, opts, err)
				}
			} else {
				conn, err := connFromEnv(sourceEnv)
				if err != nil {
					return reportError(cmd, opts, err)
				}
				if conn.Product() != rtogtor.ProductPostgreSQL {
					return reportError(cmd, opts, fmt.Errorf("scaffolding from a database needs a PostgreSQL source; for a graph source, scaffold the relational-to-graph mapping and use --reverse"))
				}
				ctx, stop := interruptible(cmd)
				defer stop()
				ctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				doc, err := rtogtor.Inspect(ctx, conn, schemas...)
				if err != nil {
					return reportError(cmd, opts, err)
				}
				if d, err = scaffold.FromRelational(doc, scaffold.Options{Name: name}); err != nil {
					return reportError(cmd, opts, err)
				}
			}
			if name != "" {
				d.Mapping.Name = name
			}
			y, err := d.YAML()
			if err != nil {
				return reportError(cmd, opts, err)
			}
			if output == "" {
				_, err = cmd.OutOrStdout().Write(y)
				return err
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
			if force {
				flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			f, err := os.OpenFile(output, flags, 0o644) //nolint:gosec // a mapping is not secret; the user chose the path
			if err != nil {
				if errors.Is(err, os.ErrExist) {
					return fmt.Errorf("%s exists; use --force to overwrite it", output)
				}
				return reportError(cmd, opts, err)
			}
			if _, err := f.Write(y); err != nil {
				_ = f.Close()
				return reportError(cmd, opts, err)
			}
			if err := f.Close(); err != nil {
				return reportError(cmd, opts, err)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "wrote %s (%d entities, %d relationships, %d notes); review it, then run 'rtogtor plan'\n",
				output, len(d.Mapping.Entities), len(d.Mapping.Relationships), notes(d))
			return nil
		},
	}
	cmd.Flags().StringVar(&sourceEnv, "source-env", "", "name of the environment variable holding the PostgreSQL URL to inspect")
	cmd.Flags().StringVar(&reverse, "reverse", "", "relational-to-graph mapping file to reverse")
	cmd.Flags().StringSliceVar(&schemas, "schema", nil, `PostgreSQL schema to inspect; repeatable (default "public")`)
	cmd.Flags().StringVar(&name, "name", "", "mapping name (default: the database name, or <mapping>-reverse)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "write the draft to this file instead of stdout")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite --output if it exists")
	cmd.Flags().DurationVar(&timeout, "timeout", 60*time.Second, "time limit for connecting and inspecting")
	return cmd
}

func notes(d *scaffold.Draft) int {
	n := len(d.Notes)
	for _, ns := range d.EntityNotes {
		n += len(ns)
	}
	return n
}
