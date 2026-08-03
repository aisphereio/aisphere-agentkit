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

// Package store contains shared relational database helpers for platform
// services. The default local backend remains SQLite, while production should
// use PostgreSQL through GORM's postgres driver, which uses pgx underneath.
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"google.golang.org/adk/internal/platform/pgutil"
	"google.golang.org/adk/internal/runtimeconfig"
)

// OpenGORM opens the shared platform relational database.
func OpenGORM(cfg runtimeconfig.DatabaseConfig) (*gorm.DB, error) {
	dbType := normalizeType(cfg.Type)
	if dbType == "" {
		dbType = "sqlite"
	}
	dsn := firstNonEmpty(cfg.DSN, envValue(cfg.DSNEnv))

	var (
		db  *gorm.DB
		err error
	)
	switch dbType {
	case "sqlite", "sqlite3":
		if dsn == "" {
			root := cfg.Root
			if root == "" {
				root = filepath.Join(".adk", "data", "database")
			}
			dsn = filepath.Join(root, "adk.db")
		}
		if !looksLikeMemorySQLiteDSN(dsn) {
			if err := os.MkdirAll(filepath.Dir(dsn), 0o755); err != nil {
				return nil, fmt.Errorf("create platform sqlite database directory: %w", err)
			}
		}
		db, err = gorm.Open(sqlite.Open(dsn))
		if err != nil {
			return nil, fmt.Errorf("open platform sqlite database: %w", err)
		}
	case "postgres", "postgresql", "pg":
		if dsn == "" {
			return nil, fmt.Errorf("postgres platform database requires storage.database.dsn or storage.database.dsn_env")
		}
		if cfg.AutoCreateDatabase {
			maintenanceDB := strings.TrimSpace(cfg.MaintenanceDB)
			if maintenanceDB == "" {
				maintenanceDB = "postgres"
			}
			if err := pgutil.EnsureDatabase(context.Background(), dsn, pgutil.EnsureOptions{MaintenanceDatabase: maintenanceDB}); err != nil {
				return nil, fmt.Errorf("ensure postgres platform database: %w", err)
			}
		}
		db, err = gorm.Open(postgres.Open(dsn))
		if err != nil {
			return nil, fmt.Errorf("open platform postgres database: %w", err)
		}
	case "mysql":
		if dsn == "" {
			return nil, fmt.Errorf("mysql platform database requires storage.database.dsn or storage.database.dsn_env")
		}
		return nil, fmt.Errorf("mysql platform database is configured but the mysql GORM driver is not linked in this build yet; use postgres or sqlite")
	default:
		return nil, fmt.Errorf("unsupported platform database type %q", cfg.Type)
	}

	if err := configurePool(db, cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime); err != nil {
		return nil, err
	}
	return db, nil
}

func configurePool(db *gorm.DB, maxOpen, maxIdle int, maxLifetime, maxIdleTime string) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql database handle: %w", err)
	}
	if maxOpen > 0 {
		sqlDB.SetMaxOpenConns(maxOpen)
	}
	if maxIdle > 0 {
		sqlDB.SetMaxIdleConns(maxIdle)
	}
	if strings.TrimSpace(maxLifetime) != "" {
		d, err := time.ParseDuration(maxLifetime)
		if err != nil {
			return fmt.Errorf("parse storage.database.conn_max_lifetime: %w", err)
		}
		sqlDB.SetConnMaxLifetime(d)
	}
	if strings.TrimSpace(maxIdleTime) != "" {
		d, err := time.ParseDuration(maxIdleTime)
		if err != nil {
			return fmt.Errorf("parse storage.database.conn_max_idle_time: %w", err)
		}
		sqlDB.SetConnMaxIdleTime(d)
	}
	return nil
}

func normalizeType(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func envValue(name string) string {
	if strings.TrimSpace(name) == "" {
		return ""
	}
	return os.Getenv(name)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func looksLikeMemorySQLiteDSN(dsn string) bool {
	dsn = strings.TrimSpace(strings.ToLower(dsn))
	return dsn == ":memory:" || strings.HasPrefix(dsn, "file::memory:")
}
