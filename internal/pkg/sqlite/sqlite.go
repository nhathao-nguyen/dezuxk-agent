package sqlite

import (
	"database/sql"

	"modernc.org/sqlite"
)

func init() {
	found := false
	for _, d := range sql.Drivers() {
		if d == "sqlite3" {
			found = true
			break
		}
	}
	if !found {
		sql.Register("sqlite3", &sqlite.Driver{})
	}
}
