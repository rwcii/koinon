package core

import (
	"context"
	"database/sql"
)

// fixtureExec runs one raw fixture statement while it holds the storage boundary, so the
// statement cannot land inside a daemon write's log proof and block storage (#367). It adds
// no other step: the result and error are the statement's own.
func fixtureExec(s *Store, query string, args ...any) (sql.Result, error) {
	return fixtureExecContext(context.Background(), s, query, args...)
}

func fixtureExecContext(ctx context.Context, s *Store, query string, args ...any) (sql.Result, error) {
	s.storage.mu.Lock()
	defer s.storage.mu.Unlock()
	return s.db.ExecContext(ctx, query, args...)
}
