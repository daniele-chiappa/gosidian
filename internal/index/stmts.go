package index

import (
	"database/sql"
	"sync"
)

// stmtCache keeps the statements of the index's writes prepared, one per
// text, on each connection that runs them. An Upsert runs some thirty, and
// SQLite parsing each again at every run was close to a third of a write's
// CPU (IMP-185). Only texts from a bounded set come here: the constants of
// the writes and of link resolution, and the few shapes built from them
// (inboundQuery for a number of keys, syncAuthorFields for a path or all),
// never the text of a user's query, which would grow it without end.
type stmtCache struct {
	db *sql.DB
	mu sync.Mutex
	m  map[string]*sql.Stmt
}

func newStmtCache(db *sql.DB) *stmtCache {
	return &stmtCache{db: db, m: map[string]*sql.Stmt{}}
}

func (c *stmtCache) get(query string) (*sql.Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.m[query]; ok {
		return s, nil
	}
	s, err := c.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	c.m[query] = s
	return s, nil
}

func (c *stmtCache) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for q, s := range c.m {
		s.Close()
		delete(c.m, q)
	}
}

// prepTx runs the statements of a transaction prepared. database/sql binds
// a cached statement to the transaction's connection, preparing it there
// only the first time that connection runs it.
type prepTx struct {
	tx *sql.Tx
	c  *stmtCache
}

func (p prepTx) Exec(query string, args ...any) (sql.Result, error) {
	s, err := p.c.get(query)
	if err != nil {
		return nil, err
	}
	return p.tx.Stmt(s).Exec(args...)
}

func (p prepTx) Query(query string, args ...any) (*sql.Rows, error) {
	s, err := p.c.get(query)
	if err != nil {
		return nil, err
	}
	return p.tx.Stmt(s).Query(args...)
}

func (p prepTx) QueryRow(query string, args ...any) *sql.Row {
	s, err := p.c.get(query)
	if err != nil {
		// The same error, from a Row that carries it.
		return p.tx.QueryRow(query, args...)
	}
	return p.tx.Stmt(s).QueryRow(args...)
}

// prepDB is prepTx outside a transaction, for link resolution read through
// the database.
type prepDB struct{ c *stmtCache }

func (p prepDB) QueryRow(query string, args ...any) *sql.Row {
	s, err := p.c.get(query)
	if err != nil {
		return p.c.db.QueryRow(query, args...)
	}
	return s.QueryRow(args...)
}
