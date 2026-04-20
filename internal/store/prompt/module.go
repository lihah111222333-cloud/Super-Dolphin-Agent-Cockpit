package prompt

import (
	"context"
	"errors"
	"reflect"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

var Module = fx.Module("store.prompt",
	fx.Provide(
		newStoreWithPool,
		AsReader,
	),
)

func AsReader(store Store) Reader {
	return store
}

func newStoreWithPool(pool *pgxpool.Pool, q *sqlc.Queries) Store {
	return newStore(q, func(ctx context.Context, fn func(*sqlc.Queries) error) error {
		tx, err := beginPromptTx(ctx, pool)
		if err != nil {
			return err
		}
		txQueries, err := bindPromptQueries(q, tx)
		if err != nil {
			_ = finishPromptTx(ctx, tx, false)
			return err
		}
		err = fn(txQueries)
		finishErr := finishPromptTx(ctx, tx, err == nil)
		if err != nil || finishErr != nil {
			return errors.Join(err, finishErr)
		}
		return nil
	})
}

func beginPromptTx(ctx context.Context, pool *pgxpool.Pool) (reflect.Value, error) {
	results := reflect.ValueOf(pool).MethodByName("Begin").Call([]reflect.Value{reflect.ValueOf(ctx)})
	if err := valueError(results[1]); err != nil {
		return reflect.Value{}, err
	}
	return results[0], nil
}

func bindPromptQueries(q *sqlc.Queries, tx reflect.Value) (*sqlc.Queries, error) {
	results := reflect.ValueOf(q).MethodByName("WithTx").Call([]reflect.Value{tx})
	if len(results) != 1 || results[0].IsNil() {
		return nil, errors.New("prompt store failed to bind tx queries")
	}
	txQueries, ok := results[0].Interface().(*sqlc.Queries)
	if !ok || txQueries == nil {
		return nil, errors.New("prompt store returned invalid tx queries")
	}
	return txQueries, nil
}

func finishPromptTx(ctx context.Context, tx reflect.Value, success bool) error {
	method := "Rollback"
	if success {
		method = "Commit"
	}
	results := tx.MethodByName(method).Call([]reflect.Value{reflect.ValueOf(ctx)})
	return valueError(results[0])
}

func valueError(v reflect.Value) error {
	if !v.IsValid() || v.IsNil() {
		return nil
	}
	err, _ := v.Interface().(error)
	return err
}
