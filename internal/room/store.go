package room

import "context"

type Commit struct {
	Record      Record
	BaseVersion int64
	Receipt     Receipt
}

// Store is consumed by actors. It returns isolated values; database adapters own transactions.
type Store interface {
	Acquire(context.Context, string, string) (Record, error)
	Create(context.Context, Record, Receipt) (Record, error)
	Commit(context.Context, Commit) (Record, error)
	Resolve(context.Context, Commit) (Record, bool, error)
	Receipt(context.Context, string, string, string) (Receipt, error)
	Renew(context.Context, string, Owner) (Owner, error)
	Release(context.Context, string, Owner) error
	List(context.Context, *Cursor, int) ([]Summary, error)
	ByCode(context.Context, string) (Summary, error)
	Ping(context.Context) error
}
