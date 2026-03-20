package workspace

type createRunParams = CreateRunRequest

type mergeRunParams struct {
	RunKey        string `json:"runKey"`
	UpdatedBy     string `json:"updatedBy,omitempty"`
	DryRun        bool   `json:"dryRun,omitempty"`
	DeleteRemoved bool   `json:"deleteRemoved,omitempty"`
}

type runKeyParams struct {
	RunKey string `json:"runKey"`
}

type abortRunParams struct {
	RunKey    string `json:"runKey"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type listRunsParams struct {
	Status string `json:"status,omitempty"`
	DagKey string `json:"dagKey,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type updateRunStatusParams struct {
	RunKey string `json:"runKey"`
	Status string `json:"status"`
}

type listRunFilesParams struct {
	RunKey string `json:"runKey"`
	State  string `json:"state,omitempty"`
}

type runFileParams struct {
	RunKey string `json:"runKey"`
	Path   string `json:"path"`
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
