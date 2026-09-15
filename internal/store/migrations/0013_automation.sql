CREATE TABLE automation_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  trigger_type TEXT NOT NULL,
  trigger_config TEXT NOT NULL DEFAULT '{}',
  actions_json TEXT NOT NULL DEFAULT '[]',
  status TEXT NOT NULL DEFAULT 'running',
  next_run_at TEXT DEFAULT '',
  last_run_at TEXT DEFAULT '',
  last_run_status TEXT NOT NULL DEFAULT '',
  last_run_message TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_automation_rules_status ON automation_rules(status);
CREATE INDEX idx_automation_rules_next_run_at ON automation_rules(next_run_at);

CREATE TABLE automation_runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  rule_id INTEGER NOT NULL,
  trigger_source TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'running',
  message TEXT NOT NULL DEFAULT '',
  result_json TEXT NOT NULL DEFAULT '{}',
  started_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at TEXT DEFAULT '',
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(rule_id) REFERENCES automation_rules(id) ON DELETE CASCADE
);

CREATE INDEX idx_automation_runs_rule_id ON automation_runs(rule_id);
CREATE INDEX idx_automation_runs_started_at ON automation_runs(started_at);
