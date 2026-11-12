package main

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

func sqlOpen(path string) (*sql.DB, error) { return sql.Open("sqlite", path) }
