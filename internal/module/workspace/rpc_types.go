package workspace

type createRunParams = CreateRunRequest

type runKeyParams struct {
	RunKey string `json:"runKey"`
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

type runFileParams struct {
	RunKey string `json:"runKey"`
	Path   string `json:"path"`
}

type runResult struct {
	Run *Run `json:"run"`
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
