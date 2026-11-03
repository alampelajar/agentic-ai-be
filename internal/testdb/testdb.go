// Package testdb helps integration tests that share one PostgreSQL database.
// Test packages run in parallel, and on an empty database several of them would
// create the same tables at the same moment and fail. Migrate serialises that
// step with a database-wide lock.
package testdb

import (
	"context"

	"gorm.io/gorm"
)

const migrationLockID = 7272740001

// Migrate runs AutoMigrate for the models while holding an advisory lock, so only
// one test package creates tables at a time.
func Migrate(db *gorm.DB, models ...any) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	ctx := context.Background()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockID) //nolint:errcheck
	return db.AutoMigrate(models...)
}
