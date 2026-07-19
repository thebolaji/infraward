// Package baseline persists drift scans to a local SQLite database so
// `infraward drift --since-last` can report only what changed since the
// previous run: every scan is stored in SQLite.
//
// It deliberately doesn't import internal/drift: Store.Record takes a
// generic Finding slice instead of drift.Result, so drift can depend on
// baseline without a cycle.
package baseline

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultPath is where the baseline lives by default. Not yet overridable
// (no --db-path / .infraward.yml wiring yet).
const DefaultPath = ".infraward/infraward.db"

// Finding is one unmanaged or missing resource from a scan, as recorded in the baseline.
type Finding struct {
	Status        string // "unmanaged" or "missing"
	TerraformType string
	ID            string
	Region        string
}

// Key identifies a resource across scans for --since-last comparison.
func Key(terraformType, id string) string {
	return terraformType + "\x00" + id
}

// Store is an open baseline database.
type Store struct {
	db *sql.DB
}

// Open opens (creating and migrating if needed) the baseline database at DefaultPath.
func Open(ctx context.Context) (*Store, error) {
	return openAt(ctx, DefaultPath)
}

// openAt is Open with the path broken out, so tests can point it at a temp
// file instead of the real DefaultPath.
func openAt(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("baseline: create %s: %w", filepath.Dir(path), err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("baseline: open %s: %w", path, err)
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS scans (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS scan_resources (
	scan_id INTEGER NOT NULL REFERENCES scans(id),
	status TEXT NOT NULL,
	terraform_type TEXT NOT NULL,
	id TEXT NOT NULL,
	region TEXT NOT NULL
);
`)
	if err != nil {
		return fmt.Errorf("baseline: migrate: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// LastFindings returns the unmanaged and missing resource keys from the
// most recently recorded scan. Both maps are empty (not nil) when there is
// no previous scan, so callers can treat "no baseline yet" the same as
// "empty baseline" without a special case.
func (s *Store) LastFindings(ctx context.Context) (unmanaged, missing map[string]bool, err error) {
	unmanaged, missing = map[string]bool{}, map[string]bool{}

	var scanID int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM scans ORDER BY id DESC LIMIT 1`).Scan(&scanID)
	if err == sql.ErrNoRows {
		return unmanaged, missing, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("baseline: find last scan: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT status, terraform_type, id FROM scan_resources WHERE scan_id = ?`, scanID)
	if err != nil {
		return nil, nil, fmt.Errorf("baseline: load last scan resources: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status, typ, id string
		if err := rows.Scan(&status, &typ, &id); err != nil {
			return nil, nil, fmt.Errorf("baseline: scan row: %w", err)
		}
		switch status {
		case "unmanaged":
			unmanaged[Key(typ, id)] = true
		case "missing":
			missing[Key(typ, id)] = true
		}
	}
	return unmanaged, missing, rows.Err()
}

// Record stores a new scan's findings as the latest baseline.
func (s *Store) Record(ctx context.Context, findings []Finding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("baseline: begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `INSERT INTO scans (created_at) VALUES (?)`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("baseline: insert scan: %w", err)
	}
	scanID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("baseline: get scan id: %w", err)
	}

	for _, f := range findings {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO scan_resources (scan_id, status, terraform_type, id, region) VALUES (?, ?, ?, ?, ?)`,
			scanID, f.Status, f.TerraformType, f.ID, f.Region,
		); err != nil {
			return fmt.Errorf("baseline: insert %s resource: %w", f.Status, err)
		}
	}

	return tx.Commit()
}
