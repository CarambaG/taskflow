package cache

import (
	"strings"
	"testing"

	"github.com/CarambaG/taskflow/internal/domain"
)

func TestCacheKeyIncludesGenerationAndFilters(t *testing.T) {
	status := domain.StatusInProgress
	assigneeID := int64(42)
	filter := domain.TaskFilter{
		TeamID: 7, Status: &status, AssigneeID: &assigneeID, Limit: 20, Offset: 40,
	}

	firstGeneration := cacheKey(filter, 3)
	secondGeneration := cacheKey(filter, 4)
	if firstGeneration == secondGeneration {
		t.Fatal("cache key must change after generation increment")
	}
	if !strings.HasPrefix(firstGeneration, "taskflow:tasks:7:3:") {
		t.Fatalf("cache key %q does not contain team and generation", firstGeneration)
	}

	differentPage := filter
	differentPage.Offset = 60
	if cacheKey(filter, 3) == cacheKey(differentPage, 3) {
		t.Fatal("cache key must include pagination and filters")
	}
}
