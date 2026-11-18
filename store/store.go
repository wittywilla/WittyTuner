// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (C) 2026 WittyWilla and contributors

// Package store is WittyTuner's own database (module API 5): a SQLite file in the module's own
// folder (WITTYMODULE_DATA), with migrations WittyTuner owns. It keeps the tuners, their channels
// and the guide sources. The guide itself, recordings, favourites and sets stay the server's,
// which names a channel by the id it has here ("tun-<id>"). It starts empty: nothing is brought
// over from a server's database.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// File is the database's name in the module's folder.
const File = "wittytuner.db"

// migrations are the schema, in order; each runs once.
var migrations = []string{
	// 1: tuners, their channels and the guide sources.
	//   - tuner_channels: what the source says (number, name, …), what the owner chose (*_set), what a
	//     stream showed (encrypted_seen, seen_clear), the last check (health*), and a backup's
	//     channel (merged_into).
	//   - epg_sources: XMLTV addresses, the owner's or a tuner's own (tuner_id); the server keeps
	//     what each one lists, under its id.
	`CREATE TABLE IF NOT EXISTS tuners (
	   id INTEGER PRIMARY KEY, driver TEXT NOT NULL, name TEXT NOT NULL, options TEXT NOT NULL,
	   enabled INTEGER NOT NULL DEFAULT 1, max_streams INTEGER NOT NULL DEFAULT 0,
	   last_scan INTEGER, last_error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL);
	 CREATE TABLE IF NOT EXISTS tuner_channels (
	   id INTEGER PRIMARY KEY, tuner_id INTEGER NOT NULL REFERENCES tuners(id) ON DELETE CASCADE,
	   source_id TEXT NOT NULL, number TEXT NOT NULL DEFAULT '', name TEXT NOT NULL,
	   grp TEXT NOT NULL DEFAULT '', callsign TEXT NOT NULL DEFAULT '', locator TEXT NOT NULL DEFAULT '',
	   logo TEXT NOT NULL DEFAULT '', hd INTEGER NOT NULL DEFAULT 0, radio INTEGER NOT NULL DEFAULT 0,
	   encrypted INTEGER NOT NULL DEFAULT 0, encrypted_why TEXT NOT NULL DEFAULT '',
	   program INTEGER NOT NULL DEFAULT 0, enabled INTEGER NOT NULL DEFAULT 1,
	   number_set TEXT NOT NULL DEFAULT '', name_set TEXT NOT NULL DEFAULT '',
	   epg_id TEXT NOT NULL DEFAULT '', epg_set TEXT NOT NULL DEFAULT '',
	   encrypted_seen TEXT NOT NULL DEFAULT '', checked INTEGER NOT NULL DEFAULT 0, seen_clear INTEGER NOT NULL DEFAULT 0,
	   atsc3 INTEGER NOT NULL DEFAULT 0, video_codec TEXT NOT NULL DEFAULT '', audio_codec TEXT NOT NULL DEFAULT '',
	   no_record TEXT NOT NULL DEFAULT '', no_record_set INTEGER NOT NULL DEFAULT 0, logo_set TEXT NOT NULL DEFAULT '',
	   catchup_days INTEGER NOT NULL DEFAULT 0, catchup TEXT NOT NULL DEFAULT '', catchup_source TEXT NOT NULL DEFAULT '',
	   health TEXT NOT NULL DEFAULT '', health_at INTEGER NOT NULL DEFAULT 0, health_msg TEXT NOT NULL DEFAULT '',
	   merged_into INTEGER,
	   UNIQUE(tuner_id, source_id));
	 CREATE INDEX IF NOT EXISTS tuner_channels_epg ON tuner_channels(epg_id);
	 CREATE INDEX IF NOT EXISTS tuner_channels_merged ON tuner_channels(merged_into);
	 CREATE TABLE IF NOT EXISTS epg_sources (
	   id INTEGER PRIMARY KEY, name TEXT NOT NULL, url TEXT NOT NULL, auth TEXT NOT NULL DEFAULT '', tuner_id INTEGER,
	   last_fetch INTEGER, last_error TEXT NOT NULL DEFAULT '', programmes INTEGER NOT NULL DEFAULT 0,
	   UNIQUE(url));`,
	// 2: what a playlist says about a channel beyond its address (extra, JSON: user agent, referrer,
	//   other addresses, language, country, guide shift); when it went missing from its source
	//   (missing_since, kept a while so a playlist that drops it briefly loses nothing); how sure an
	//   automatic guide match was (epg_score, 0-100), what it went by (epg_how) and the guide id the
	//   source itself gave (epg_src, which an automatic match may have corrected); and guide matches
	//   made by hand, by a channel's name, kept for channels that come back or are added again.
	`ALTER TABLE tuner_channels ADD COLUMN extra TEXT NOT NULL DEFAULT '';
	 ALTER TABLE tuner_channels ADD COLUMN missing_since INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE tuner_channels ADD COLUMN epg_score INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE tuner_channels ADD COLUMN epg_how TEXT NOT NULL DEFAULT '';
	 ALTER TABLE tuner_channels ADD COLUMN epg_src TEXT NOT NULL DEFAULT '';
	 UPDATE tuner_channels SET epg_src = epg_id;
	 CREATE TABLE IF NOT EXISTS epg_memory (name_key TEXT PRIMARY KEY, epg_id TEXT NOT NULL, at INTEGER NOT NULL);`,
}

// DB is WittyTuner's database.
type DB struct{ *sql.DB }

// Open opens (and makes, or brings up to date) the database in dir.
func Open(dir string) (*DB, error) {
	if dir == "" {
		return nil, fmt.Errorf("no data folder (WITTYMODULE_DATA): WittyTuner needs the data permission")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return OpenFile(filepath.Join(dir, File))
}

// OpenFile opens the database in one file, for tests.
func OpenFile(path string) (*DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	d := &DB{db}
	if err := d.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}

func (d *DB) migrate(ctx context.Context) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	var v int
	if err := d.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := d.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM schema`); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema(version) VALUES(?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
