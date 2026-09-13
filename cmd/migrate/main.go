package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	direction := flag.String("direction", "up", "migration direction: up, down, version, force")
	steps := flag.Int("steps", 1, "number of migrations for up/down; ignored by version")
	version := flag.Uint("version", 0, "version to force only when direction=force")
	databaseURL := flag.String("database-url", os.Getenv("EASYHR_DATABASE_URL"), "PostgreSQL connection URL")
	migrationsPath := flag.String("migrations-path", "file://db/migrations", "migration source URL")
	flag.Parse()

	if *databaseURL == "" {
		slog.Error("database URL is required; set EASYHR_DATABASE_URL or -database-url")
		os.Exit(1)
	}
	migrator, err := migrate.New(*migrationsPath, *databaseURL)
	if err != nil {
		slog.Error("open migrator", "error", err)
		os.Exit(1)
	}
	defer func() { _, _ = migrator.Close() }()

	switch *direction {
	case "up":
		err = migrator.Steps(*steps)
	case "down":
		if *steps < 1 {
			err = fmt.Errorf("steps must be at least 1")
		} else {
			err = migrator.Steps(-*steps)
		}
	case "version":
		currentVersion, dirty, versionErr := migrator.Version()
		if errors.Is(versionErr, migrate.ErrNilVersion) {
			fmt.Println("version=none dirty=false")
			return
		}
		if versionErr != nil {
			err = versionErr
		} else {
			fmt.Printf("version=%d dirty=%t\n", currentVersion, dirty)
			return
		}
	case "force":
		if *version == 0 {
			err = fmt.Errorf("-version must be greater than zero when direction=force")
		} else {
			err = migrator.Force(int(*version))
		}
	default:
		err = fmt.Errorf("unsupported direction %q", *direction)
	}

	if errors.Is(err, migrate.ErrNoChange) {
		slog.Info("database schema already at requested version")
		return
	}
	if err != nil {
		slog.Error("migration failed", "direction", *direction, "error", err)
		os.Exit(1)
	}
	slog.Info("migration completed", "direction", *direction, "steps", *steps)
}
