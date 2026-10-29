// Command ecommerce migrates the e-commerce example from PostgreSQL to Neo4j
// and back, verifying every value both ways.
//
//	export POSTGRES_URL='postgres://user:password@localhost:5432/ecommerce'
//	export NEO4J_URL='neo4j://neo4j:password@localhost:7687'
//	go run .
//
// Load ../schema.sql into the PostgreSQL database first. The run is safe to
// repeat: writes are idempotent.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	rtogtor "github.com/TuanFaied/rtogtor/engine"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	postgres := rtogtor.PostgreSQLFromEnv("POSTGRES_URL")
	neo4j := rtogtor.Neo4jFromEnv("NEO4J_URL")

	// PostgreSQL → Neo4j, then Neo4j → PostgreSQL with the reverse mapping.
	if err := migrate(ctx, "mapping.yaml", postgres, neo4j); err != nil {
		return err
	}
	return migrate(ctx, "mapping-reverse.yaml", neo4j, postgres)
}

func migrate(ctx context.Context, file string, source, destination rtogtor.Connection) error {
	m, err := rtogtor.LoadMapping(filepath.Join("..", file))
	if err != nil {
		return err // an rtgerr.List: every problem, each with an RTG-* code
	}
	mig := rtogtor.NewMigration(source, destination, m)
	defer func() { _ = mig.Close() }()

	plan, err := mig.Plan(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("%s: %s -> %s, %d steps\n", m.Name, plan.Source.Product, plan.Destination.Product, len(plan.Steps))

	report, err := mig.Run(ctx, rtogtor.RunOptions{
		BatchSize: 500,
		Progress: func(p rtogtor.Progress) {
			if p.Done {
				fmt.Printf("  %-36s %6d written\n", p.StepID, p.Written)
			}
		},
	})
	if err != nil {
		if report != nil {
			return fmt.Errorf("run %s stopped (%w); resume it with RunOptions{ResumeRunID: %q}", report.RunID, err, report.RunID)
		}
		return err
	}

	v, err := mig.VerifyWith(ctx, report.RunID, rtogtor.VerifyOptions{Values: true})
	if err != nil {
		return err
	}
	fmt.Printf("  run %s %s, %d records written; verification %s\n", report.RunID, report.Status, report.Totals.Written, v.Status)
	if report.Status != "succeeded" || v.Status != "passed" {
		return fmt.Errorf("%s did not round-trip cleanly; see rtogtor report --run-id %s", m.Name, report.RunID)
	}
	return nil
}
