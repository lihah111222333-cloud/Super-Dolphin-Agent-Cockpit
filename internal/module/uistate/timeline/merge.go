package timeline

// mergeItem 将同一时间线条目的增量字段合并到已有条目。
func mergeItem(dst *Item, src Item) {
	if dst == nil {
		return
	}
	mergeStringIfEmpty(&dst.ID, src.ID)
	mergeString(&dst.Kind, src.Kind)
	mergeStatusField(dst, src.Status)
	mergeString(&dst.SessionScope, src.SessionScope)
	mergeString(&dst.CallID, src.CallID)
	mergeInt64(&dst.RequestID, src.RequestID)
	mergeString(&dst.Command, src.Command)
	mergeString(&dst.File, src.File)
	mergeString(&dst.Tool, src.Tool)
	mergeString(&dst.Preview, src.Preview)
	mergeIntPtr(&dst.ElapsedMS, src.ElapsedMS)
	mergeString(&dst.Output, src.Output)
	mergeIntPtr(&dst.ExitCode, src.ExitCode)
	mergeTrue(&dst.Done, src.Done)
	mergeString(&dst.Text, src.Text)
	mergeTrue(&dst.Internal, src.Internal)
	mergeAnySlice(&dst.Attachments, src.Attachments)
	mergeString(&dst.Error, src.Error)
	mergeBoolPtr(&dst.Success, src.Success)
	mergeString(&dst.AgentID, src.AgentID)
	mergeString(&dst.TurnID, src.TurnID)
	mergeString(&dst.ToolName, src.ToolName)
	mergeString(&dst.ItemType, src.ItemType)
	mergeString(&dst.Ts, src.Ts)
	mergeString(&dst.lookupKey, src.lookupKey)
}

func mergeStatusField(dst *Item, src string) {
	if src != "" && (!dst.Done || src != "running") {
		dst.Status = src
	}
}

func mergeStringIfEmpty(dst *string, src string) {
	if *dst == "" && src != "" {
		*dst = src
	}
}

func mergeString(dst *string, src string) {
	if src != "" {
		*dst = src
	}
}

func mergeInt64(dst *int64, src int64) {
	if src != 0 {
		*dst = src
	}
}

func mergeIntPtr(dst **int, src *int) {
	if src != nil {
		*dst = src
	}
}

func mergeBoolPtr(dst **bool, src *bool) {
	if src != nil {
		*dst = src
	}
}

func mergeTrue(dst *bool, src bool) {
	if src {
		*dst = true
	}
}

func mergeAnySlice(dst *[]any, src []any) {
	if len(src) != 0 {
		*dst = append([]any(nil), src...)
	}
}
