package stores

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/felixzsh/botflota"
)

// TurnStore commits one message turn in one transaction: the session, the
// stored messages, the chat activity, and the guard state land together or
// not at all (ADR 0026, ADR 0027).
type TurnStore struct{ db *sql.DB }

func NewTurnStore(db *sql.DB) *TurnStore { return &TurnStore{db: db} }

func (s *TurnStore) CommitTurn(ctx context.Context, turn botflota.TurnRecord) error {
	if turn.Session == nil {
		return errors.New("sqlite: turn has no session")
	}
	if turn.Inbound == nil {
		return errors.New("sqlite: turn has no inbound message")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin turn: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	sessions := SessionStore{db: tx}
	messages := MessageStore{db: tx}
	chats := ChatStore{db: tx}
	if err := sessions.Save(ctx, turn.Session); err != nil {
		return err
	}
	turn.Inbound.SessionID = turn.Session.ID
	if err := messages.Append(ctx, turn.Inbound); err != nil {
		return err
	}
	for _, outbound := range turn.Outbound {
		if outbound == nil {
			return errors.New("sqlite: turn has an empty reply")
		}
		outbound.SessionID = turn.Session.ID
		if err := messages.Append(ctx, outbound); err != nil {
			return err
		}
	}
	if err := chats.RecordMessage(ctx, turn.ChatID, turn.At); err != nil {
		return err
	}
	if turn.Guards != nil {
		if err := chats.SaveGuards(ctx, turn.ChatID, turn.Guards); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: commit turn: %w", err)
	}
	return nil
}
