package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rocketplaneio/rocketplane/services/controlplane/internal/model"
)

// copilot_investigations.go — Persistenz des Investigation-Graphs: pro Chat
// eine Investigation, pro Hypothese/Action/Frage/Fazit ein Knoten. Der Master
// liest beim Resume die Verdicts, das UI den ganzen Baum.

// EnsureCopilotChatRow legt die Chat-Zeile an, falls sie noch fehlt (der Client
// autosaved erst später) — OHNE Titel/Daten zu überschreiben. Nötig, damit die
// Investigation-FK schon beim ersten Master-Turn greift.
func (s *Store) EnsureCopilotChatRow(ctx context.Context, id, clusterID, userID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO copilot_chats (id, cluster_id, user_id)
		VALUES ($1,$2,$3) ON CONFLICT (id) DO NOTHING`, id, clusterID, userID)
	return err
}

// EnsureInvestigation liefert die Investigation eines Chats (legt sie bei
// Bedarf an). Ein Chat hat höchstens eine Investigation (unique chat_id).
func (s *Store) EnsureInvestigation(ctx context.Context, chatID, clusterID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO copilot_investigations (chat_id, cluster_id)
		VALUES ($1,$2)
		ON CONFLICT (chat_id) DO UPDATE SET updated_at=now()
		RETURNING id`, chatID, clusterID).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("ensure investigation: %w", err)
	}
	return id, nil
}

// CreateNode legt einen Knoten an; seq wird atomar fortgezählt.
func (s *Store) CreateNode(ctx context.Context, investigationID uuid.UUID, parentID *uuid.UUID, kind, hypothesis string, task json.RawMessage) (*model.InvestigationNode, error) {
	if len(task) == 0 {
		task = json.RawMessage("{}")
	}
	var n model.InvestigationNode
	err := s.pool.QueryRow(ctx, `
		INSERT INTO copilot_investigation_nodes (investigation_id, parent_id, seq, kind, hypothesis, task, status)
		VALUES ($1,$2,
			(SELECT COALESCE(MAX(seq),0)+1 FROM copilot_investigation_nodes WHERE investigation_id=$1),
			$3,$4,$5,'running')
		RETURNING id, investigation_id, parent_id, seq, kind, hypothesis, task, status, tokens_in, tokens_out, created_at`,
		investigationID, parentID, kind, hypothesis, task).
		Scan(&n.ID, &n.InvestigationID, &n.ParentID, &n.Seq, &n.Kind, &n.Hypothesis, &n.Task, &n.Status, &n.TokensIn, &n.TokensOut, &n.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create node: %w", err)
	}
	return &n, nil
}

// FinishNode schreibt Verdict + Status + Tokens und stempelt finished_at.
func (s *Store) FinishNode(ctx context.Context, nodeID uuid.UUID, status string, verdict json.RawMessage, confidence *float32, tokensIn, tokensOut int) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE copilot_investigation_nodes
		SET status=$2, verdict=$3, confidence=$4, tokens_in=tokens_in+$5, tokens_out=tokens_out+$6, finished_at=now()
		WHERE id=$1`, nodeID, status, verdict, confidence, tokensIn, tokensOut)
	if err != nil {
		return fmt.Errorf("finish node: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListNodes liefert alle Knoten einer Investigation in seq-Reihenfolge.
func (s *Store) ListNodes(ctx context.Context, investigationID uuid.UUID) ([]model.InvestigationNode, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, investigation_id, parent_id, seq, kind, hypothesis, task, verdict, status, confidence, tokens_in, tokens_out, created_at, finished_at
		FROM copilot_investigation_nodes WHERE investigation_id=$1 ORDER BY seq`, investigationID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	defer rows.Close()
	out := []model.InvestigationNode{}
	for rows.Next() {
		var n model.InvestigationNode
		if err := rows.Scan(&n.ID, &n.InvestigationID, &n.ParentID, &n.Seq, &n.Kind, &n.Hypothesis, &n.Task, &n.Verdict, &n.Status, &n.Confidence, &n.TokensIn, &n.TokensOut, &n.CreatedAt, &n.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// InvestigationForChat liefert die Investigation-ID eines Chats (ErrNotFound,
// wenn der Chat noch keinen Graph hat).
func (s *Store) InvestigationForChat(ctx context.Context, chatID, clusterID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id FROM copilot_investigations WHERE chat_id=$1 AND cluster_id=$2`, chatID, clusterID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, err
	}
	return id, nil
}

// ConcludeInvestigation setzt den Endstatus (concluded/abandoned).
func (s *Store) ConcludeInvestigation(ctx context.Context, investigationID uuid.UUID, status string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE copilot_investigations SET status=$2, updated_at=now() WHERE id=$1`, investigationID, status)
	return err
}
