package orgs

import "database/sql"

// nullable is s as a nullable column: empty is NULL.
func nullable(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
