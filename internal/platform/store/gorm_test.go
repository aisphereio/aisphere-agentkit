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

package store

import (
	"testing"

	"google.golang.org/adk/internal/runtimeconfig"
)

func TestOpenGORMSQLiteAndPoolOptions(t *testing.T) {
	db, err := OpenGORM(runtimeconfig.DatabaseConfig{
		Type:            "sqlite",
		DSN:             "file::memory:?cache=shared",
		MaxOpenConns:    5,
		MaxIdleConns:    2,
		ConnMaxLifetime: "30m",
		ConnMaxIdleTime: "5m",
	})
	if err != nil {
		t.Fatalf("OpenGORM() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("DB() error = %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestOpenGORMPostgresRequiresDSN(t *testing.T) {
	_, err := OpenGORM(runtimeconfig.DatabaseConfig{Type: "postgres"})
	if err == nil {
		t.Fatalf("OpenGORM() error = nil, want missing dsn error")
	}
}
