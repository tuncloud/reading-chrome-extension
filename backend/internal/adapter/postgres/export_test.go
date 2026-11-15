package postgres

// WithTxFault makes every transaction call f first and fail with its error when non-nil.
func WithTxFault(f func() error) Option { return func(s *Store) { s.txFault = f } }
