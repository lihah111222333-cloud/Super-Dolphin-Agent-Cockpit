package memory

import (
	"context"
	"errors"
	"strings"
)

func (h *MemoryLifecycleHooks) intentDiskStores(ctx context.Context, threadID string, memoryType MemoryType) (memoryStructuredStore, memoryStructuredStore, error) {
	privateStore, err := h.diskStore()
	if err != nil {
		return nil, nil, err
	}
	teamStore, err := h.teamDiskStore(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	if teamStore == nil {
		return privateStore, nil, nil
	}
	if defaultTeamMemoryType(memoryType) {
		return teamStore, privateStore, nil
	}
	return privateStore, teamStore, nil
}

func (h *MemoryLifecycleHooks) teamDiskStore(ctx context.Context, threadID string) (memoryStructuredStore, error) {
	if h == nil || h.team == nil {
		return nil, nil
	}
	buildCtx := h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(threadID)).buildCtx()
	if !h.team.IsTeamMemoryEnabled(buildCtx) {
		return nil, nil
	}
	root, err := configuredTeamMemPath(h.team, buildCtx)
	if err != nil {
		return nil, err
	}
	return newDiskStoreWithGuard(root, NewTeamMemoryGuard(h.team))
}

func selectExplicitWriteStore(name string, primary, secondary memoryStructuredStore) (memoryStructuredStore, error) {
	for _, store := range []memoryStructuredStore{primary, secondary} {
		if store == nil {
			continue
		}
		if _, err := store.Read(name); err == nil {
			return store, nil
		} else if !errors.Is(err, ErrMemoryNotFound) {
			return nil, err
		}
	}
	if primary != nil {
		return primary, nil
	}

	return nil, errors.New("memory store is nil")
}

func upsertStructuredMemory(store memoryStructuredStore, entry MemoryWriteRequest, options WriteOptions) error {
	if store == nil {
		return errors.New("memory store is nil")
	}
	if _, err := store.CreateStructured(entry, options); err == nil {
		return nil
	} else if !errors.Is(err, ErrMemoryAlreadyExists) {
		return err
	}
	_, err := store.UpdateStructured(entry, options)
	return err
}

func deleteMemoryAcrossStores(name string, options WriteOptions, stores ...memoryStructuredStore) error {
	deleted := false
	for _, store := range stores {
		if store == nil {
			continue
		}
		if err := store.Delete(name, options); err == nil {
			deleted = true
			continue
		} else if !errors.Is(err, ErrMemoryNotFound) {
			return err
		}
	}
	if deleted {
		return nil
	}
	return ErrMemoryNotFound
}

func defaultTeamMemoryType(memoryType MemoryType) bool {
	switch ParseMemoryType(string(memoryType)) {
	case MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}
