package store

import (
	"context"
	"database/sql"

	"litepan/internal/domain"
)

type automationRuleRepo struct{ db *DB }

func (r *automationRuleRepo) Create(ctx context.Context, rule *domain.AutomationRule) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO automation_rules
		  (name, trigger_type, trigger_config, actions_json, status, next_run_at, last_run_at, last_run_status, last_run_message)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rule.Name, rule.TriggerType, string(rule.TriggerConfig), string(rule.Actions), rule.Status,
		tsValue(rule.NextRunAt), tsValue(rule.LastRunAt), rule.LastRunStatus, rule.LastRunMessage)
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *automationRuleRepo) Update(ctx context.Context, rule *domain.AutomationRule) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE automation_rules
		 SET name=?, trigger_type=?, trigger_config=?, actions_json=?, status=?,
		     next_run_at=?, last_run_at=?, last_run_status=?, last_run_message=?, updated_at=CURRENT_TIMESTAMP
		 WHERE id=?`,
		rule.Name, rule.TriggerType, string(rule.TriggerConfig), string(rule.Actions), rule.Status,
		tsValue(rule.NextRunAt), tsValue(rule.LastRunAt), rule.LastRunStatus, rule.LastRunMessage, rule.ID)
	return wrapDB(err)
}

func (r *automationRuleRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.write.ExecContext(ctx, `DELETE FROM automation_rules WHERE id=?`, id)
	return wrapDB(err)
}

func (r *automationRuleRepo) Get(ctx context.Context, id int64) (*domain.AutomationRule, error) {
	row := r.db.read.QueryRowContext(ctx, selectAutomationRuleCols+` WHERE id=?`, id)
	return scanAutomationRule(row)
}

func (r *automationRuleRepo) List(ctx context.Context, includePaused bool) ([]*domain.AutomationRule, error) {
	query := selectAutomationRuleCols
	args := []any{}
	if !includePaused {
		query += ` WHERE status<>?`
		args = append(args, domain.AutomationStatusPaused)
	}
	query += ` ORDER BY id DESC`
	rows, err := r.db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.AutomationRule
	for rows.Next() {
		rule, err := scanAutomationRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, wrapDB(rows.Err())
}

const selectAutomationRuleCols = `SELECT id, name, trigger_type, trigger_config, actions_json, status, next_run_at, last_run_at, last_run_status, last_run_message, created_at, updated_at
FROM automation_rules`

func scanAutomationRule(s rowScanner) (*domain.AutomationRule, error) {
	var (
		rule          domain.AutomationRule
		triggerConfig sql.NullString
		actionsJSON   sql.NullString
		nextRunAt     sql.NullString
		lastRunAt     sql.NullString
		createdAt     sql.NullString
		updatedAt     sql.NullString
	)
	err := s.Scan(
		&rule.ID, &rule.Name, &rule.TriggerType, &triggerConfig, &actionsJSON, &rule.Status,
		&nextRunAt, &lastRunAt, &rule.LastRunStatus, &rule.LastRunMessage, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, wrapDB(err)
	}
	if triggerConfig.Valid {
		rule.TriggerConfig = []byte(triggerConfig.String)
	}
	if actionsJSON.Valid {
		rule.Actions = []byte(actionsJSON.String)
	}
	rule.NextRunAt = parseTS(nextRunAt)
	rule.LastRunAt = parseTS(lastRunAt)
	rule.CreatedAt = parseTS(createdAt)
	rule.UpdatedAt = parseTS(updatedAt)
	return &rule, nil
}

type automationRunRepo struct{ db *DB }

func (r *automationRunRepo) Create(ctx context.Context, run *domain.AutomationRun) (int64, error) {
	res, err := r.db.write.ExecContext(ctx,
		`INSERT INTO automation_runs
		  (rule_id, trigger_source, status, message, result_json, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.RuleID, run.TriggerSource, run.Status, run.Message, string(run.Result), tsValue(run.StartedAt), tsValue(run.FinishedAt))
	if err != nil {
		return 0, wrapDB(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, wrapDB(err)
	}
	return id, nil
}

func (r *automationRunRepo) Update(ctx context.Context, run *domain.AutomationRun) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE automation_runs
		 SET status=?, message=?, result_json=?, finished_at=?
		 WHERE id=?`,
		run.Status, run.Message, string(run.Result), tsValue(run.FinishedAt), run.ID)
	return wrapDB(err)
}

func (r *automationRunRepo) List(ctx context.Context, ruleID int64, limit int) ([]*domain.AutomationRun, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	query := selectAutomationRunCols
	args := []any{}
	if ruleID > 0 {
		query += ` WHERE rule_id=?`
		args = append(args, ruleID)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	var out []*domain.AutomationRun
	for rows.Next() {
		run, err := scanAutomationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, wrapDB(rows.Err())
}

func (r *automationRunRepo) Clear(ctx context.Context) (int, error) {
	res, err := r.db.write.ExecContext(ctx, `DELETE FROM automation_runs`)
	if err != nil {
		return 0, wrapDB(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, wrapDB(err)
	}
	return int(n), nil
}

const selectAutomationRunCols = `SELECT id, rule_id, trigger_source, status, message, result_json, started_at, finished_at, created_at
FROM automation_runs`

func scanAutomationRun(s rowScanner) (*domain.AutomationRun, error) {
	var (
		run        domain.AutomationRun
		resultJSON sql.NullString
		startedAt  sql.NullString
		finishedAt sql.NullString
		createdAt  sql.NullString
	)
	err := s.Scan(&run.ID, &run.RuleID, &run.TriggerSource, &run.Status, &run.Message, &resultJSON, &startedAt, &finishedAt, &createdAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	if resultJSON.Valid {
		run.Result = []byte(resultJSON.String)
	}
	run.StartedAt = parseTS(startedAt)
	run.FinishedAt = parseTS(finishedAt)
	run.CreatedAt = parseTS(createdAt)
	return &run, nil
}
