// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package pgutil contains PostgreSQL bootstrap helpers shared by runtime and
// platform stores.
package pgutil

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const defaultMaintenanceDatabase = "postgres"

// EnsureOptions controls PostgreSQL database bootstrap behavior.
type EnsureOptions struct {
	MaintenanceDatabase string
}

// EnsureDatabase connects to the maintenance database and creates the target
// database from dsn when it does not exist yet. It supports URL DSNs such as
// postgres://user:pass@host:5432/adk?sslmode=disable and keyword/value DSNs
// such as host=... user=... password=... dbname=adk sslmode=disable.
func EnsureDatabase(ctx context.Context, dsn string, opts EnsureOptions) error {
	info, err := parseDSN(dsn)
	if err != nil {
		return err
	}
	if info.database == "" {
		return fmt.Errorf("postgres auto_create_database requires a target database in DSN, for example dbname=adk or postgres://.../adk")
	}

	maintenanceDB := strings.TrimSpace(opts.MaintenanceDatabase)
	if maintenanceDB == "" {
		maintenanceDB = defaultMaintenanceDatabase
	}
	if info.database == maintenanceDB {
		return nil
	}

	adminDSN, err := info.withDatabase(maintenanceDB)
	if err != nil {
		return err
	}

	db, err := gorm.Open(postgres.Open(adminDSN), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("connect postgres maintenance database %q for auto_create_database: %w", maintenanceDB, err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get postgres maintenance sql handle: %w", err)
	}
	defer sqlDB.Close()

	var exists bool
	if err := db.WithContext(ctx).Raw("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = ?)", info.database).Scan(&exists).Error; err != nil {
		return fmt.Errorf("check postgres database %q existence: %w", info.database, err)
	}
	if exists {
		return nil
	}
	if err := db.WithContext(ctx).Exec("CREATE DATABASE " + quoteIdentifier(info.database)).Error; err != nil {
		return fmt.Errorf("create postgres database %q: %w", info.database, err)
	}
	return nil
}

type dsnInfo struct {
	original string
	database string
	urlDSN   *url.URL
	kvParts  []kvPart
}

type kvPart struct {
	key   string
	value string
}

func parseDSN(dsn string) (*dsnInfo, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, fmt.Errorf("postgres auto_create_database requires non-empty DSN")
	}
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return nil, fmt.Errorf("parse postgres URL DSN for auto_create_database: %w", err)
		}
		database := strings.TrimPrefix(u.Path, "/")
		if idx := strings.Index(database, "/"); idx >= 0 {
			database = database[:idx]
		}
		return &dsnInfo{original: dsn, database: database, urlDSN: u}, nil
	}

	parts, err := parseKeywordValueDSN(dsn)
	if err != nil {
		return nil, err
	}
	info := &dsnInfo{original: dsn, kvParts: parts}
	for _, p := range parts {
		if strings.EqualFold(p.key, "dbname") || strings.EqualFold(p.key, "database") {
			info.database = p.value
			break
		}
	}
	return info, nil
}

func (i *dsnInfo) withDatabase(database string) (string, error) {
	if i.urlDSN != nil {
		u := *i.urlDSN
		u.Path = "/" + database
		return u.String(), nil
	}
	if len(i.kvParts) == 0 {
		return "", fmt.Errorf("cannot rewrite postgres DSN database for auto_create_database")
	}
	parts := make([]string, 0, len(i.kvParts)+1)
	seenDBName := false
	for _, p := range i.kvParts {
		key := p.key
		value := p.value
		if strings.EqualFold(key, "dbname") || strings.EqualFold(key, "database") {
			key = "dbname"
			value = database
			seenDBName = true
		}
		parts = append(parts, key+"="+quoteKVValue(value))
	}
	if !seenDBName {
		parts = append(parts, "dbname="+quoteKVValue(database))
	}
	return strings.Join(parts, " "), nil
}

func parseKeywordValueDSN(dsn string) ([]kvPart, error) {
	fields := strings.Fields(dsn)
	parts := make([]kvPart, 0, len(fields))
	for _, f := range fields {
		idx := strings.IndexByte(f, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("parse postgres keyword/value DSN for auto_create_database: invalid token %q", f)
		}
		key := strings.TrimSpace(f[:idx])
		value := strings.TrimSpace(f[idx+1:])
		value = strings.Trim(value, "'")
		if key == "" {
			return nil, fmt.Errorf("parse postgres keyword/value DSN for auto_create_database: empty key in %q", f)
		}
		parts = append(parts, kvPart{key: key, value: value})
	}
	return parts, nil
}

func quoteKVValue(value string) string {
	if value == "" {
		return "''"
	}
	if strings.ContainsAny(value, " \t\n\r'") {
		return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
	}
	return value
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
