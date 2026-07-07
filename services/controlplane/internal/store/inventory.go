package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// inventory.go — Persistenz des K8s-Inventars (eine Zeile je Kind, Items als
// JSONB). Voll-Sync-Semantik: der Agent liefert je Push den kompletten Stand.

type InventoryKind struct {
	Kind      string          `json:"kind"`
	Items     json.RawMessage `json:"items"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// UpsertInventory ersetzt die Items der gelieferten Kinds (Full-Sync je Kind).
func (s *Store) UpsertInventory(ctx context.Context, clusterID uuid.UUID, kinds map[string]json.RawMessage) error {
	for kind, items := range kinds {
		if len(items) == 0 {
			items = json.RawMessage("[]")
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO cluster_inventory (cluster_id, kind, items, updated_at)
			VALUES ($1,$2,$3,now())
			ON CONFLICT (cluster_id, kind) DO UPDATE SET items=EXCLUDED.items, updated_at=now()`,
			clusterID, kind, items); err != nil {
			return fmt.Errorf("upsert inventory %s: %w", kind, err)
		}
	}
	return nil
}

// ListInventory liefert alle Kinds (kind="") oder genau einen.
func (s *Store) ListInventory(ctx context.Context, clusterID uuid.UUID, kind string) ([]InventoryKind, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, items, updated_at FROM cluster_inventory
		WHERE cluster_id=$1 AND ($2='' OR kind=$2) ORDER BY kind`, clusterID, kind)
	if err != nil {
		return nil, fmt.Errorf("list inventory: %w", err)
	}
	defer rows.Close()
	out := []InventoryKind{}
	for rows.Next() {
		var k InventoryKind
		if err := rows.Scan(&k.Kind, &k.Items, &k.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
