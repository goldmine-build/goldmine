// Package sqlignorestore contains a SQL implementation of ignore.Store.
package sqlignorestore

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"

	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/golden/go/ignore"
	"go.skia.org/infra/golden/go/sql/schema"
)

type StoreImpl struct {
	db *pgxpool.Pool
}

// New returns a SQL based implementation of ignore.Store.
func New(db *pgxpool.Pool) *StoreImpl {
	return &StoreImpl{db: db}
}

// Create implements the ignore.Store interface.
func (s *StoreImpl) Create(ctx context.Context, rule ignore.Rule) error {
	v, err := url.ParseQuery(rule.Query)
	if err != nil {
		return skerr.Wrapf(err, "invalid ignore query %q", rule.Query)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return skerr.Wrap(err)
	}
	defer rollbackAndLogIfError(ctx, tx)
	_, err = tx.Exec(ctx, `
INSERT INTO IgnoreRules (creator_email, updated_email, expires, note, query)
VALUES ($1, $2, $3, $4, $5)`, rule.CreatedBy, rule.CreatedBy, rule.Expires, rule.Note, v)
	if err != nil {
		return skerr.Wrap(err)
	}
	return skerr.Wrap(updateTracesAndCommit(ctx, tx))
}

func rollbackAndLogIfError(ctx context.Context, tx pgx.Tx) {
	err := tx.Rollback(ctx)
	if err != nil && err != pgx.ErrTxClosed {
		sklog.Warningf("Error while rolling back: %s", err)
	}
}

// List implements the ignore.Store interface.
func (s *StoreImpl) List(ctx context.Context) ([]ignore.Rule, error) {
	var rv []ignore.Rule
	rows, err := s.db.Query(ctx, `SELECT * FROM IgnoreRules ORDER BY expires ASC`)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r schema.IgnoreRuleRow
		err := rows.Scan(&r.IgnoreRuleID, &r.CreatorEmail, &r.UpdatedEmail, &r.Expires, &r.Note, &r.Query)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		rv = append(rv, ignore.Rule{
			ID:        r.IgnoreRuleID.String(),
			CreatedBy: r.CreatorEmail,
			UpdatedBy: r.UpdatedEmail,
			Expires:   r.Expires.UTC(),
			Query:     url.Values(r.Query).Encode(),
			Note:      r.Note,
		})
	}
	return rv, nil
}

// Update implements the ignore.Store interface.
func (s *StoreImpl) Update(ctx context.Context, rule ignore.Rule) error {
	v, err := url.ParseQuery(rule.Query)
	if err != nil {
		return skerr.Wrapf(err, "invalid ignore query %q", rule.Query)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return skerr.Wrap(err)
	}
	defer rollbackAndLogIfError(ctx, tx)
	_, err = tx.Exec(ctx, `
UPDATE IgnoreRules SET (updated_email, expires, note, query) = ($1, $2, $3, $4)
WHERE ignore_rule_id = $5`, rule.UpdatedBy, rule.Expires, rule.Note, v, rule.ID)
	if err != nil {
		return skerr.Wrap(err)
	}
	return skerr.Wrap(updateTracesAndCommit(ctx, tx))
}

// Delete implements the ignore.Store interface.
func (s *StoreImpl) Delete(ctx context.Context, id string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return skerr.Wrap(err)
	}
	defer rollbackAndLogIfError(ctx, tx)
	_, err = tx.Exec(ctx, `
DELETE FROM IgnoreRules WHERE ignore_rule_id = $1`, id)
	if err != nil {
		return skerr.Wrap(err)
	}
	return skerr.Wrap(updateTracesAndCommit(ctx, tx))
}

// updateTracesAndCommit gets all current ignore rules and apply them to all Traces and
// ValuesAtHead. This is done in the passed in transaction, so if it fails, we can rollback.
// If successful, the transaction is committed.
func updateTracesAndCommit(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, "SELECT query FROM IgnoreRules")
	if err != nil {
		return skerr.Wrap(err)
	}
	var ignoreRules []paramtools.ParamSet
	for rows.Next() {
		rule := paramtools.ParamSet{}
		err := rows.Scan(&rule)
		if err != nil {
			return skerr.Wrap(err)
		}
		ignoreRules = append(ignoreRules, rule)
	}
	sklog.Infof("Applying %d rules", len(ignoreRules))

	condition, arguments := convertIgnoreRules(ignoreRules)

	statement := `UPDATE Traces SET matches_any_ignore_rule = `
	statement += condition
	// RETURNING NOTHING hints the SQL engine that it can shard this out w/o having to count the
	// total number of rows modified, which can lead to slightly faster queries.
	statement += ` RETURNING NOTHING`
	_, err = tx.Exec(ctx, statement, arguments...)
	if err != nil {
		return skerr.Wrapf(err, "Updating traces with statement: %s", statement)
	}

	headStatement := `UPDATE ValuesAtHead SET matches_any_ignore_rule = `
	headStatement += condition
	headStatement += ` RETURNING NOTHING`
	_, err = tx.Exec(ctx, headStatement, arguments...)
	if err != nil {
		return skerr.Wrapf(err, "Updating head table with statement: %s", headStatement)
	}

	if err := tx.Commit(ctx); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// convertIgnoreRules turns a Paramset into a SQL clause that would match rows using a column
// named "keys". It is currently implemented with AND/OR clauses, but could potentially be done
// with UNION/INTERSECT depending on performance needs.
func convertIgnoreRules(rules []paramtools.ParamSet) (string, []interface{}) {
	if len(rules) == 0 {
		return "false", nil
	}
	conditions := make([]string, 0, len(rules))
	var arguments []interface{}
	argIdx := 1

	for _, rule := range rules {
		rule.Normalize()
		keys := make([]string, 0, len(rule))
		for key := range rule {
			keys = append(keys, key)
		}
		sort.Strings(keys) // sort the keys for determinism

		andParts := make([]string, 0, len(rules))
		for _, key := range keys {
			values := rule[key]
			subCondition := fmt.Sprintf("keys ->> $%d::STRING IN (", argIdx)
			argIdx++
			arguments = append(arguments, key)
			for i, value := range values {
				if i != 0 {
					subCondition += ", "
				}
				subCondition += fmt.Sprintf("$%d", argIdx)
				argIdx++
				arguments = append(arguments, value)
			}
			subCondition += ")"
			andParts = append(andParts, subCondition)
		}
		condition := "(" + strings.Join(andParts, " AND ") + ")"
		conditions = append(conditions, condition)
	}
	combined := "(" + strings.Join(conditions, "\nOR ") + ")"
	return combined, arguments
}

// Make sure Store fulfills the ignore.Store interface
var _ ignore.Store = (*StoreImpl)(nil)
