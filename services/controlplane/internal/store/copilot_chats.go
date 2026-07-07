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

// copilot_chats.go — CRUD für gespeicherte Copilot-Vorgänge, scoped auf
// (Cluster, User). Der volle Render-Zustand liegt als JSONB in data.

// ListCopilotChats liefert die Zusammenfassungen (ohne data) für die Seitenleiste.
func (s *Store) ListCopilotChats(ctx context.Context, clusterID, userID uuid.UUID) ([]model.CopilotChat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, summary, created_at, updated_at
		FROM copilot_chats WHERE cluster_id=$1 AND user_id=$2
		ORDER BY updated_at DESC LIMIT 200`, clusterID, userID)
	if err != nil {
		return nil, fmt.Errorf("list copilot chats: %w", err)
	}
	defer rows.Close()
	out := []model.CopilotChat{}
	for rows.Next() {
		var c model.CopilotChat
		if err := rows.Scan(&c.ID, &c.Title, &c.Summary, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCopilotChat liefert einen Vorgang inkl. data (zum Wiederaufrufen).
func (s *Store) GetCopilotChat(ctx context.Context, id, clusterID, userID uuid.UUID) (*model.CopilotChat, error) {
	var c model.CopilotChat
	err := s.pool.QueryRow(ctx, `
		SELECT id, cluster_id, user_id, title, summary, data, created_at, updated_at
		FROM copilot_chats WHERE id=$1 AND cluster_id=$2 AND user_id=$3`, id, clusterID, userID).
		Scan(&c.ID, &c.ClusterID, &c.UserID, &c.Title, &c.Summary, &c.Data, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

// UpsertCopilotChat legt einen Vorgang an oder aktualisiert ihn (Client-seitige
// id). Die ON CONFLICT-WHERE-Klausel erzwingt Ownership.
func (s *Store) UpsertCopilotChat(ctx context.Context, id, clusterID, userID uuid.UUID, title, summary string, data json.RawMessage) (*model.CopilotChat, error) {
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	var c model.CopilotChat
	err := s.pool.QueryRow(ctx, `
		INSERT INTO copilot_chats (id, cluster_id, user_id, title, summary, data)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title, summary=EXCLUDED.summary, data=EXCLUDED.data, updated_at=now()
		WHERE copilot_chats.cluster_id=$2 AND copilot_chats.user_id=$3
		RETURNING id, cluster_id, user_id, title, summary, created_at, updated_at`,
		id, clusterID, userID, title, summary, data).
		Scan(&c.ID, &c.ClusterID, &c.UserID, &c.Title, &c.Summary, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound // Konflikt auf fremdem Vorgang
		}
		return nil, err
	}
	return &c, nil
}

func (s *Store) DeleteCopilotChat(ctx context.Context, id, clusterID, userID uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, `DELETE FROM copilot_chats WHERE id=$1 AND cluster_id=$2 AND user_id=$3`, id, clusterID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
