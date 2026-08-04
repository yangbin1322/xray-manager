package main

import (
	"testing"
	"xray-manager/internal/models"
)

func TestGenerateUniqueRuleIDsAreUniqueAndAvoidExisting(t *testing.T) {
	existing := []models.ProxyRule{{ID: "rule_1"}, {ID: "rule_2"}}

	const n = 10000
	ids := generateUniqueRuleIDs(existing, n)
	if len(ids) != n {
		t.Fatalf("expected %d ids, got %d", n, len(ids))
	}

	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		if id == "" {
			t.Fatal("generated an empty id")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = struct{}{}
	}

	for _, rule := range existing {
		if _, clash := seen[rule.ID]; clash {
			t.Fatalf("generated id collides with existing rule %s", rule.ID)
		}
	}
}

func TestGenerateUniqueRuleIDsHandlesZero(t *testing.T) {
	if ids := generateUniqueRuleIDs(nil, 0); len(ids) != 0 {
		t.Fatalf("expected no ids, got %d", len(ids))
	}
}

// 构造与即将生成的 ID 完全撞车的现有规则，确认冲突分支能产出唯一 ID 且不死循环
func TestGenerateUniqueRuleIDsResolvesCollisions(t *testing.T) {
	probe := generateUniqueRuleIDs(nil, 5)
	existing := make([]models.ProxyRule, 0, len(probe))
	for _, id := range probe {
		existing = append(existing, models.ProxyRule{ID: id})
	}

	taken := make(map[string]struct{}, len(existing))
	for _, rule := range existing {
		taken[rule.ID] = struct{}{}
	}

	ids := generateUniqueRuleIDs(existing, len(probe))
	if len(ids) != len(probe) {
		t.Fatalf("expected %d ids, got %d", len(probe), len(ids))
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, clash := taken[id]; clash {
			t.Fatalf("id %s collides with an existing rule", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}
