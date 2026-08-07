// Command dbreset truncates every application table in DATABASE_URL, for
// wiping local dev data clean (make db-reset). It refuses to run unless
// APP_ENV=development, since there is no way to recover from running this
// against a real database.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "db-reset:", err)
		os.Exit(1)
	}
}

func run() error {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}
	if env != "development" {
		return fmt.Errorf("refusing to run — APP_ENV=%q, this only runs when APP_ENV=development", env)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	// schema_migrations is golang-migrate's own bookkeeping table, not
	// application data — truncating it would desync the DB from the
	// migration files on disk.
	rows, err := conn.Query(ctx, `
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public' AND tablename != 'schema_migrations'
		ORDER BY tablename`)
	if err != nil {
		return fmt.Errorf("list tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return fmt.Errorf("scan table name: %w", err)
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list tables: %w", err)
	}

	if len(tables) == 0 {
		fmt.Println("db-reset: no tables found, nothing to do")
		return nil
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// CASCADE handles foreign-key ordering automatically (e.g. users ->
	// discord_registrations) — some tables in the loop will already be empty
	// by the time we reach them because an earlier CASCADE cleared them too,
	// which is harmless.
	for _, t := range tables {
		stmt := fmt.Sprintf("TRUNCATE TABLE %s CASCADE", pgx.Identifier{t}.Sanitize())
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("truncate %s: %w", t, err)
		}
		fmt.Println("truncated:", t)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	fmt.Printf("db-reset: %d table(s) truncated\n", len(tables))
	return nil
}
