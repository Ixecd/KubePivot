// 追加到 scaffold.go，并在 InitProject 里加一行调用：
// if err := writeMigrationSkeleton(outputDir, name); err != nil {
//     return err
// }
// 放在 writeInternalSkeleton 之后

package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

func writeMigrationSkeleton(outputDir, name string) error {
	migrationsDir := filepath.Join(outputDir, "internal", "db", "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		return err
	}

	upSQL := fmt.Sprintf(`-- 000001_%s_init.up.sql
-- 在此添加建表语句
-- 示例：
-- CREATE TABLE IF NOT EXISTS users (
--     id         BIGSERIAL PRIMARY KEY,
--     email      TEXT NOT NULL UNIQUE,
--     password   TEXT NOT NULL,
--     created_at TIMESTAMPTZ DEFAULT NOW()
-- );
`, name)

	downSQL := `-- 000001_init.down.sql
-- 在此添加回滚语句（与 up 顺序相反）
-- 示例：
-- DROP TABLE IF EXISTS users;
`

	connectGo := fmt.Sprintf(`package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func NewDB() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://user:pass@localhost:5432/%s?sslmode=disable"
	}

	database, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %%w", err)
	}
	if err := database.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %%w", err)
	}

	if err := runMigrations(database); err != nil {
		return nil, fmt.Errorf("migrate db: %%w", err)
	}

	slog.Info("数据库已连接")
	return database, nil
}

func runMigrations(database *sql.DB) error {
	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "internal/db/migrations"
	}

	driver, err := postgres.WithInstance(database, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("migrate driver: %%w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://"+migrationsPath,
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("migrate init: %%w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %%w", err)
	}

	v, _, _ := m.Version()
	slog.Info("数据库迁移完成", "version", v)
	return nil
}
`, name)

	files := map[string]string{
		filepath.Join(migrationsDir, fmt.Sprintf("000001_%s_init.up.sql", name)):   upSQL,
		filepath.Join(migrationsDir, fmt.Sprintf("000001_%s_init.down.sql", name)): downSQL,
		filepath.Join(outputDir, "internal", "db", "connect.go"):                   connectGo,
	}

	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("gen migration file %s: %w", path, err)
		}
	}
	return nil
}
