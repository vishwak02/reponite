//go:build sqlite

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
)

// dbtx is what a Store's statements run on: the connection pool, or a
// Replace writer's transaction.
type dbtx interface {
	Exec(string, ...interface{}) (sql.Result, error)
	Query(string, ...interface{}) (*sql.Rows, error)
	QueryRow(string, ...interface{}) *sql.Row
}

var errStoreClosed = errors.New("sqlite: store closed")

// Replace runs fn — the writes of an index run, made through the writer it is
// handed — as ONE transaction. Re-indexing a ref clears it and then writes it
// back symbol by symbol; done straight on the store, every reader in between
// (this process's other goroutines, another process) saw the ref empty or
// half-written and stamped incomplete, and a run cut short — an error, the
// process exiting — left it that way for good. Through Replace readers keep
// the previous complete index until the new one commits whole, and a run that
// fails, is cancelled, or never finishes leaves the previous index as it was.
//
// The transaction opens at fn's first write (parsing before it holds no
// lock) on a connection of its own, so this Store's readers are not blocked
// meanwhile — except in an in-memory store, which has one connection: there
// fn must not read through s, only through w, until it returns.
//
// Cancelling ctx abandons the run: its transaction is rolled back at once and
// its later writes fail; Replace then returns ctx's error. Close waits for a
// transaction still open, so cancel first to abandon one at shutdown.
func (s *Store) Replace(ctx context.Context, fn func(w *Store) error) error {
	if s.lz != nil {
		return fn(s) // already a writer: one transaction
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h, owned, err := s.writeHandle()
	if err != nil {
		return err
	}
	l := &lazyTx{parent: s, h: h, owned: owned}
	stop := context.AfterFunc(ctx, func() { l.finish(false, ctx.Err()) })
	defer stop()
	w := &Store{db: l, lz: l, path: s.path, names: map[string]int64{}, refs: map[[2]string]int64{}}
	if err := fn(w); err != nil {
		if aerr := l.finish(false, err); aerr != nil {
			return aerr // abandoned: say so rather than the failed write it caused
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		// cancelled after its last write: abandoned all the same
		l.finish(false, err)
		return err
	}
	if err := l.finish(true, nil); err != nil {
		return err
	}
	// what this Store cached was read before the commit
	s.invalidate()
	s.resetIDs()
	return nil
}

// writeHandle opens the connection a Replace writes on: its own for a file
// (WAL lets this Store's readers go on reading the last commit meanwhile),
// the Store's pool for an in-memory database (another connection would open
// another database).
func (s *Store) writeHandle() (*sql.DB, bool, error) {
	if isMemoryPath(s.path) {
		return s.pool, false, nil
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return nil, false, err
	}
	db.SetMaxOpenConns(1)
	for _, p := range []string{`PRAGMA busy_timeout = 30000`, `PRAGMA synchronous = NORMAL`} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, false, err
		}
	}
	return db, true, nil
}

func isMemoryPath(p string) bool {
	return p == "" || p == ":memory:" || strings.HasPrefix(p, "file::memory:") || strings.Contains(p, "mode=memory")
}

// lazyTx is a Replace writer's transaction, begun at its first write. Reads
// before that go to the last commit, which is what the transaction would see.
type lazyTx struct {
	parent *Store
	h      *sql.DB
	owned  bool // h was opened for this writer: closed when it finishes

	mu   sync.Mutex
	tx   *sql.Tx
	done bool  // committed, rolled back or abandoned
	why  error // why it ended without a commit
}

// begin opens the transaction (once) and returns it.
func (l *lazyTx) begin() (*sql.Tx, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		if l.why != nil {
			return nil, l.why
		}
		return nil, sql.ErrTxDone
	}
	if l.tx != nil {
		return l.tx, nil
	}
	l.parent.wmu.RLock()
	if l.parent.closed {
		l.parent.wmu.RUnlock()
		return nil, errStoreClosed
	}
	tx, err := l.h.Begin()
	if err != nil {
		l.parent.wmu.RUnlock()
		return nil, err
	}
	l.tx = tx
	return tx, nil
}

// finish commits (commit) or rolls back the transaction, once; a second call
// reports how the first ended.
func (l *lazyTx) finish(commit bool, why error) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		if l.why == nil {
			return sql.ErrTxDone
		}
		return l.why
	}
	l.done, l.why = true, why
	var err error
	if l.tx != nil {
		if commit {
			err = l.tx.Commit()
		} else {
			_ = l.tx.Rollback()
		}
		l.parent.wmu.RUnlock()
	}
	if l.owned {
		_ = l.h.Close()
	}
	if err != nil {
		l.why = err
	}
	return err
}

// current is the open transaction, nil before the first write.
func (l *lazyTx) current() *sql.Tx {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tx
}

func (l *lazyTx) Exec(q string, args ...interface{}) (sql.Result, error) {
	tx, err := l.begin()
	if err != nil {
		return nil, err
	}
	return tx.Exec(q, args...)
}

func (l *lazyTx) Query(q string, args ...interface{}) (*sql.Rows, error) {
	if tx := l.current(); tx != nil {
		return tx.Query(q, args...)
	}
	return l.h.Query(q, args...)
}

func (l *lazyTx) QueryRow(q string, args ...interface{}) *sql.Row {
	if tx := l.current(); tx != nil {
		return tx.QueryRow(q, args...)
	}
	return l.h.QueryRow(q, args...)
}
