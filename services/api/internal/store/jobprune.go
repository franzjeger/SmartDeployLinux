package store

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// DeleteJob removes a single deployment job. deployment_events rows
// cascade-delete with it.
func (s *Store) DeleteJob(ctx context.Context, id uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM deployment_jobs WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneTerminalJobs deletes terminal (completed/failed/cancelled) jobs.
// olderThan <= 0 removes all terminal jobs; otherwise only those whose
// created_at is older than the given age. Returns the count removed.
func (s *Store) PruneTerminalJobs(ctx context.Context, olderThan time.Duration) (int64, error) {
	q := `DELETE FROM deployment_jobs WHERE state IN ('completed','failed','cancelled')`
	args := []any{}
	if olderThan > 0 {
		q += ` AND created_at < now() - make_interval(hours => $1)`
		args = append(args, int(olderThan.Hours()))
	}
	ct, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
