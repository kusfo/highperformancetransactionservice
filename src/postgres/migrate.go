// Package postgres provides PostgreSQL migration execution.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var migrationName = regexp.MustCompile(`^([0-9]{6})_[a-z0-9_]+\.sql$`)

type migration struct {
	version             int
	name, sql, checksum string
}

// Migrate applies append-only SQL files and their history in one transaction.
// Files must not contain transaction control or nontransactional DDL.
// The caller owns conn and must not concurrently use it.
func Migrate(ctx context.Context, conn *pgx.Conn, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	var list []migration
	versions := make(map[int]bool)
	for _, entry := range entries {
		if entry.IsDir() || fs.ValidPath(entry.Name()) && !hasSQLSuffix(entry.Name()) {
			continue
		}
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			return fmt.Errorf("invalid migration filename: %s", entry.Name())
		}
		version, _ := strconv.Atoi(match[1])
		if version == 0 || versions[version] {
			return fmt.Errorf("invalid or duplicate migration version: %s", entry.Name())
		}
		versions[version] = true
		data, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return fmt.Errorf("read %s: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(data)
		list = append(list, migration{version, entry.Name(), string(data), hex.EncodeToString(sum[:])})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].version < list[j].version })
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	// Transaction-scoped lock protects first-time table creation as well as replay.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(718604206)`); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.schema_migrations (
 version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL,
 applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("create migration history: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT version, name, checksum FROM public.schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	applied := make(map[int]migration)
	maxVersion := 0
	for rows.Next() {
		var m migration
		if err = rows.Scan(&m.version, &m.name, &m.checksum); err != nil {
			rows.Close()
			return err
		}
		applied[m.version] = m
		maxVersion = m.version
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	for version := range applied {
		if !versions[version] {
			return fmt.Errorf("applied migration %d is missing", version)
		}
	}
	for _, m := range list {
		if old, ok := applied[m.version]; ok {
			if old.name != m.name || old.checksum != m.checksum {
				return fmt.Errorf("applied migration %s was changed", old.name)
			}
			continue
		}
		if m.version <= maxVersion {
			return fmt.Errorf("migration %s precedes applied history", m.name)
		}
		if _, err = tx.Exec(ctx, m.sql); err != nil {
			return fmt.Errorf("apply %s: %w", m.name, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO public.schema_migrations (version,name,checksum) VALUES ($1,$2,$3)`, m.version, m.name, m.checksum); err != nil {
			return fmt.Errorf("record %s: %w", m.name, err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

func hasSQLSuffix(name string) bool { return len(name) >= 4 && name[len(name)-4:] == ".sql" }
