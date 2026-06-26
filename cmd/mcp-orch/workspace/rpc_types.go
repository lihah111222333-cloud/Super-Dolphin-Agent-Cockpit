package workspace

import (
	"encoding/json"
	"strings"
)

// createRunParams 复用服务层创建请求，保留自定义 JSON 兼容逻辑。
type createRunParams = CreateRunRequest

// mergeRunParams 是 workspace/run/merge RPC 的入参。
type mergeRunParams struct {
	RunKey        string `json:"run_key"`
	UpdatedBy     string `json:"updated_by,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	DeleteRemoved bool   `json:"delete_removed,omitempty"`
}

// UnmarshalJSON 同时接受 snake_case 和旧 camelCase merge 字段。
func (p *mergeRunParams) UnmarshalJSON(data []byte) error {
	type raw mergeRunParams
	var legacy struct {
		RunKey        string `json:"runKey"`
		UpdatedBy     string `json:"updatedBy"`
		DryRun        *bool  `json:"dryRun"`
		DeleteRemoved *bool  `json:"deleteRemoved"`
	}
	return decodeLegacyRunParams(data, func() error {
		var current raw
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
		*p = mergeRunParams(current)
		return nil
	}, &legacy, func(legacy struct {
		RunKey        string `json:"runKey"`
		UpdatedBy     string `json:"updatedBy"`
		DryRun        *bool  `json:"dryRun"`
		DeleteRemoved *bool  `json:"deleteRemoved"`
	}) {
		if strings.TrimSpace(p.RunKey) == "" {
			p.RunKey = strings.TrimSpace(legacy.RunKey)
		}
		if strings.TrimSpace(p.UpdatedBy) == "" {
			p.UpdatedBy = strings.TrimSpace(legacy.UpdatedBy)
		}
		if !p.DryRun && legacy.DryRun != nil {
			p.DryRun = *legacy.DryRun
		}
		if !p.DeleteRemoved && legacy.DeleteRemoved != nil {
			p.DeleteRemoved = *legacy.DeleteRemoved
		}
	})
}

// runKeyParams 是只需要 run_key 的 RPC 入参。
type runKeyParams struct {
	RunKey string `json:"run_key"`
}

// UnmarshalJSON 兼容旧 runKey 字段。
func (p *runKeyParams) UnmarshalJSON(data []byte) error {
	type raw runKeyParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = runKeyParams(current)
	return fillLegacyRunKey(data, &p.RunKey)
}

// abortRunParams 是 workspace/run/abort RPC 的入参。
type abortRunParams struct {
	RunKey    string `json:"run_key"`
	UpdatedBy string `json:"updated_by,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// UnmarshalJSON 兼容旧 camelCase abort 字段。
func (p *abortRunParams) UnmarshalJSON(data []byte) error {
	type raw abortRunParams
	var legacy struct {
		RunKey    string `json:"runKey"`
		UpdatedBy string `json:"updatedBy"`
	}
	return decodeLegacyRunParams(data, func() error {
		var current raw
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
		*p = abortRunParams(current)
		return nil
	}, &legacy, func(legacy struct {
		RunKey    string `json:"runKey"`
		UpdatedBy string `json:"updatedBy"`
	}) {
		if strings.TrimSpace(p.RunKey) == "" {
			p.RunKey = strings.TrimSpace(legacy.RunKey)
		}
		if strings.TrimSpace(p.UpdatedBy) == "" {
			p.UpdatedBy = strings.TrimSpace(legacy.UpdatedBy)
		}
	})
}

// listRunsParams 是 workspace/run/list RPC 的过滤条件。
type listRunsParams struct {
	Status string `json:"status,omitempty"`
	DagKey string `json:"dag_key,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// UnmarshalJSON 兼容旧 dagKey 字段。
func (p *listRunsParams) UnmarshalJSON(data []byte) error {
	type raw listRunsParams
	var legacy struct {
		DagKey string `json:"dagKey"`
	}
	return decodeLegacyRunParams(data, func() error {
		var current raw
		if err := json.Unmarshal(data, &current); err != nil {
			return err
		}
		*p = listRunsParams(current)
		return nil
	}, &legacy, func(legacy struct {
		DagKey string `json:"dagKey"`
	}) {
		if strings.TrimSpace(p.DagKey) == "" {
			p.DagKey = strings.TrimSpace(legacy.DagKey)
		}
	})
}

// listRunFilesParams 是 workspace/run/files/list RPC 的入参。
type listRunFilesParams struct {
	RunKey string `json:"run_key"`
	State  string `json:"state,omitempty"`
}

// UnmarshalJSON 兼容旧 runKey 字段。
func (p *listRunFilesParams) UnmarshalJSON(data []byte) error {
	type raw listRunFilesParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = listRunFilesParams(current)
	return fillLegacyRunKey(data, &p.RunKey)
}

// runFileParams 是 workspace/run/file/get RPC 的入参。
type runFileParams struct {
	RunKey string `json:"run_key"`
	Path   string `json:"path"`
}

// UnmarshalJSON 兼容旧 runKey 字段。
func (p *runFileParams) UnmarshalJSON(data []byte) error {
	type raw runFileParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = runFileParams(current)
	return fillLegacyRunKey(data, &p.RunKey)
}

// runResult 是返回单个 run 的 RPC 包装。
type runResult struct {
	Run *Run `json:"run"`
}

// mergeResult 是返回 merge 摘要的 RPC 包装。
type mergeResult struct {
	Result *MergeRunResult `json:"result"`
}

// runsResult 是返回 run 列表的 RPC 包装。
type runsResult struct {
	Runs []Run `json:"runs"`
}

// runFileResult 是返回单个 run file 的 RPC 包装。
type runFileResult struct {
	File *RunFile `json:"file"`
}

// runFilesResult 是返回 run file 列表的 RPC 包装。
type runFilesResult struct {
	Files []RunFile `json:"files"`
}

// fillLegacyRunKey 在 run_key 为空时读取旧 runKey。
func fillLegacyRunKey(data []byte, runKey *string) error {
	if strings.TrimSpace(*runKey) != "" {
		return nil
	}
	var legacy struct {
		RunKey string `json:"runKey"`
	}
	return decodeLegacyRunParams(data, func() error { return nil }, &legacy, func(legacy struct {
		RunKey string `json:"runKey"`
	}) {
		*runKey = strings.TrimSpace(legacy.RunKey)
	})
}
