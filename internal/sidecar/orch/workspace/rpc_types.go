package workspace

import (
	"encoding/json"
	"strings"
)

type createRunParams = CreateRunRequest

type mergeRunParams struct {
	RunKey        string `json:"run_key"`
	UpdatedBy     string `json:"updated_by,omitempty"`
	DryRun        bool   `json:"dry_run,omitempty"`
	DeleteRemoved bool   `json:"delete_removed,omitempty"`
}

// UnmarshalJSON 解码JSON。
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

type runKeyParams struct {
	RunKey string `json:"run_key"`
}

// UnmarshalJSON 解码JSON。
func (p *runKeyParams) UnmarshalJSON(data []byte) error {
	type raw runKeyParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = runKeyParams(current)
	return fillLegacyRunKey(data, &p.RunKey)
}

type abortRunParams struct {
	RunKey    string `json:"run_key"`
	UpdatedBy string `json:"updated_by,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// UnmarshalJSON 解码JSON。
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

type listRunsParams struct {
	Status string `json:"status,omitempty"`
	DagKey string `json:"dag_key,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// UnmarshalJSON 解码JSON。
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

type listRunFilesParams struct {
	RunKey string `json:"run_key"`
	State  string `json:"state,omitempty"`
}

// UnmarshalJSON 解码JSON。
func (p *listRunFilesParams) UnmarshalJSON(data []byte) error {
	type raw listRunFilesParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = listRunFilesParams(current)
	return fillLegacyRunKey(data, &p.RunKey)
}

type runFileParams struct {
	RunKey string `json:"run_key"`
	Path   string `json:"path"`
}

// UnmarshalJSON 解码JSON。
func (p *runFileParams) UnmarshalJSON(data []byte) error {
	type raw runFileParams
	var current raw
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	*p = runFileParams(current)
	return fillLegacyRunKey(data, &p.RunKey)
}

type runResult struct {
	Run *Run `json:"run"`
}

type mergeResult struct {
	Result *MergeRunResult `json:"result"`
}

type runsResult struct {
	Runs []Run `json:"runs"`
}

type runFileResult struct {
	File *RunFile `json:"file"`
}

type runFilesResult struct {
	Files []RunFile `json:"files"`
}

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
