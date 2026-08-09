package store

import (
	"context"
	"fmt"
)

// HasNonterminalAgentSwitch is the forward-rollback boot preflight. It first
// checks sqlite_master so databases from before the agent_switches migration
// remain bootable rather than failing with "no such table".
func (s *Store) HasNonterminalAgentSwitch(ctx context.Context) (bool, error) {
	var tableExists bool
	if err := s.readDB.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM sqlite_master
			WHERE type = 'table' AND name = 'agent_switches'
		)
	`).Scan(&tableExists); err != nil {
		return false, fmt.Errorf("inspect agent_switches table: %w", err)
	}
	if !tableExists {
		return false, nil
	}
	var active bool
	if err := s.readDB.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM agent_switches
			WHERE state NOT IN ('completed', 'failed')
		)
	`).Scan(&active); err != nil {
		return false, fmt.Errorf("inspect nonterminal agent_switches rows: %w", err)
	}
	return active, nil
}
