package sqlcgen

// rowScanner is the common shape of pgx.Row and pgx.Rows: hand-written queries
// in the *_manual.go files share one scan function between the single-row and
// multi-row paths through it.
//
// It lives in its own file (not inside a generated *.sql.go) so that running
// `sqlc generate` cannot wipe it — an earlier copy sat in campaigns.sql.go and
// was lost on every regeneration, which is what made the generator unusable.
type rowScanner interface {
	Scan(dest ...interface{}) error
}
