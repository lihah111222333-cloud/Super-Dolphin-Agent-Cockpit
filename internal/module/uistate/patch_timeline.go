package uistate

import (
	"reflect"
	"strings"

	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
)

type threadTimelinePatchState struct {
	items map[string]uidto.PatchTimelineItem
	order []string
}

// applyThreadTimelineLocked 将线程 timeline 的增量写入 UIThreadPatch。
// 它会记住上次发送给前端的状态，只传 changed/removed/order，降低 patch payload 体积。
func (s *service) applyThreadTimelineLocked(patch *uidto.UIThreadPatch, threadID string) {
	if patch == nil || s.timeline == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	current := buildTimelinePatchState(s.timeline.GetByThread(threadID))
	previous := s.timelinePatchByThread[threadID]
	changed, removed, order := diffTimelinePatch(previous, current)
	if len(changed) > 0 {
		patch.TimelineItems = changed
	}
	if len(removed) > 0 {
		patch.RemovedItemIds = removed
	}
	if len(order) > 0 {
		patch.TimelineOrder = order
	}
	if s.timelinePatchByThread == nil {
		s.timelinePatchByThread = map[string]threadTimelinePatchState{}
	}
	if len(current.order) == 0 {
		delete(s.timelinePatchByThread, threadID)
		return
	}
	s.timelinePatchByThread[threadID] = current
}

func (s *service) applyThreadActivityStatsLocked(patch *uidto.UIThreadPatch, threadID string) {
	if patch == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	patch.ActivityStats = patchActivityStats(s.state.ActivityStatsByThread[threadID])
}

func buildTimelinePatchState(items []timeline.Item) threadTimelinePatchState {
	if len(items) == 0 {
		return threadTimelinePatchState{}
	}
	state := threadTimelinePatchState{
		items: make(map[string]uidto.PatchTimelineItem, len(items)),
		order: make([]string, 0, len(items)),
	}
	for _, item := range items {
		patchItem := toPatchTimelineItem(item)
		if patchItem.ID == "" {
			continue
		}
		state.items[patchItem.ID] = patchItem
		state.order = append(state.order, patchItem.ID)
	}
	return state
}

// diffTimelinePatch 比较上次和当前 timeline patch 状态。
// 返回 changed、removed 和可选 order；顺序未变时不发送 order，避免前端重复排序。
func diffTimelinePatch(previous, current threadTimelinePatchState) ([]uidto.PatchTimelineItem, []string, []string) {
	changed := make([]uidto.PatchTimelineItem, 0, len(current.order))
	for _, itemID := range current.order {
		nextItem, ok := current.items[itemID]
		if !ok {
			continue
		}
		if prevItem, exists := previous.items[itemID]; exists && reflect.DeepEqual(prevItem, nextItem) {
			continue
		}
		changed = append(changed, clonePatchTimelineItem(nextItem))
	}
	removed := make([]string, 0, len(previous.order))
	for _, itemID := range previous.order {
		if _, exists := current.items[itemID]; exists {
			continue
		}
		removed = append(removed, itemID)
	}
	if sameStringSlice(previous.order, current.order) {
		return changed, removed, nil
	}
	return changed, removed, append([]string(nil), current.order...)
}

func toPatchTimelineItem(item timeline.Item) uidto.PatchTimelineItem {
	return uidto.PatchTimelineItem{
		ID:          strings.TrimSpace(item.ID),
		Ts:          strings.TrimSpace(item.Ts),
		Kind:        strings.TrimSpace(item.Kind),
		Tool:        strings.TrimSpace(firstNonEmptyString(item.Tool, item.ToolName)),
		Text:        firstNonEmptyString(item.Text, item.Error),
		Command:     item.Command,
		File:        item.File,
		Status:      strings.TrimSpace(item.Status),
		CallID:      strings.TrimSpace(item.CallID),
		RequestID:   item.RequestID,
		ElapsedMS:   cloneIntPtr(item.ElapsedMS),
		Preview:     item.Preview,
		Output:      item.Output,
		ExitCode:    cloneIntPtr(item.ExitCode),
		Done:        item.Done,
		Internal:    item.Internal,
		Attachments: cloneAttachments(item.Attachments),
	}
}

func clonePatchTimelineItem(item uidto.PatchTimelineItem) uidto.PatchTimelineItem {
	item.ElapsedMS = cloneIntPtr(item.ElapsedMS)
	item.ExitCode = cloneIntPtr(item.ExitCode)
	item.Attachments = cloneAttachments(item.Attachments)
	return item
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func cloneAttachments(input []any) []any {
	if len(input) == 0 {
		return nil
	}
	out := make([]any, len(input))
	copy(out, input)
	return out
}

func sameStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
