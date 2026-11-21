// Package durable owns Go's acceptance journal and settlement work, never market inventory.
package durable

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrUnavailable = errors.New("storage unavailable; restart the service after PostgreSQL recovers")

type Store struct {
	mu     sync.Mutex
	conn   *pgx.Conn
	failed bool
}
type Intent struct {
	RfqID     string `json:"rfq_id"`
	QuoteID   string `json:"quote_id"`
	TradeID   string `json:"trade_id"`
	Signature string `json:"signature"`
}
type Job struct {
	ID      string
	Payload []byte
	Raw     []byte
	Hash    string
	Nonce   *int64
}

func Open(ctx context.Context, url, identity string) (*Store, error) {
	if identity == "" {
		return nil, ErrUnavailable
	}
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return nil, ErrUnavailable
	}
	s := &Store{conn: conn}
	ok := false
	defer func() {
		if !ok {
			conn.Close(ctx)
		}
	}()
	var locked bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock(734201, 2)").Scan(&locked); err != nil || !locked {
		return nil, ErrUnavailable
	}
	if _, err = conn.Exec(ctx, "SET statement_timeout = '3s'"); err != nil {
		return nil, ErrUnavailable
	}
	var marketIdentity string
	if err = conn.QueryRow(ctx, "SELECT identity FROM market_state WHERE singleton").Scan(&marketIdentity); err != nil || marketIdentity != identity {
		return nil, errors.New("Rust market identity differs; start Rust with the same database and MARKET_ID")
	}
	if _, err = conn.Exec(ctx, "INSERT INTO settlement_identity(singleton,identity) VALUES(true,$1) ON CONFLICT DO NOTHING", identity); err != nil {
		return nil, ErrUnavailable
	}
	var stored string
	if err = conn.QueryRow(ctx, "SELECT identity FROM settlement_identity WHERE singleton").Scan(&stored); err != nil || stored != identity {
		return nil, errors.New("database belongs to another deployment or chain")
	}
	ok = true
	return s, nil
}
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s.conn.Close(ctx)
}
func (s *Store) perform(f func(context.Context) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := f(ctx); err != nil {
		s.failed = true
		return ErrUnavailable
	}
	return nil
}
func (s *Store) Check() error {
	return s.perform(func(ctx context.Context) error { return s.conn.Ping(ctx) })
}
func (s *Store) Intent(v Intent) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.perform(func(ctx context.Context) error {
		_, err := s.conn.Exec(ctx, "INSERT INTO acceptance_intents(trade_id,rfq_id,payload) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", v.TradeID, v.RfqID, raw)
		return err
	})
}
func (s *Store) Intents() ([]Intent, error) {
	values := []Intent{}
	err := s.perform(func(ctx context.Context) error {
		rows, err := s.conn.Query(ctx, "SELECT payload FROM acceptance_intents WHERE NOT completed ORDER BY trade_id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var value Intent
			if err = rows.Scan(&raw); err != nil {
				return err
			}
			if err = json.Unmarshal(raw, &value); err != nil {
				return err
			}
			values = append(values, value)
		}
		return rows.Err()
	})
	return values, err
}
func (s *Store) Complete(id string) error {
	return s.perform(func(ctx context.Context) error {
		_, err := s.conn.Exec(ctx, "UPDATE acceptance_intents SET completed=true WHERE trade_id=$1", id)
		return err
	})
}
func (s *Store) Save(v Job) error {
	return s.perform(func(ctx context.Context) error {
		_, err := s.conn.Exec(ctx, `INSERT INTO settlement_jobs(trade_id,payload,submitter_nonce,transaction_hash,raw_transaction) VALUES($1,$2,$3,NULLIF($4,''),$5)
 ON CONFLICT(trade_id) DO UPDATE SET payload=EXCLUDED.payload,submitter_nonce=EXCLUDED.submitter_nonce,transaction_hash=EXCLUDED.transaction_hash,raw_transaction=EXCLUDED.raw_transaction`, v.ID, v.Payload, v.Nonce, v.Hash, v.Raw)
		return err
	})
}
func (s *Store) Jobs() ([]Job, error) {
	values := []Job{}
	err := s.perform(func(ctx context.Context) error {
		rows, err := s.conn.Query(ctx, "SELECT trade_id,payload,submitter_nonce,COALESCE(transaction_hash,''),raw_transaction FROM settlement_jobs ORDER BY submitter_nonce NULLS LAST,trade_id")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var v Job
			if err = rows.Scan(&v.ID, &v.Payload, &v.Nonce, &v.Hash, &v.Raw); err != nil {
				return err
			}
			values = append(values, v)
		}
		return rows.Err()
	})
	return values, err
}
