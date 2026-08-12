package gate

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// verifiedExecutedWorkloadIdentities 重验执行投影并按运行结果顺序返回权威身份。
func verifiedExecutedWorkloadIdentities(results []RemoteCIWorkloadResult, verified map[GateID]WorkloadPassIdentity) ([]WorkloadPassIdentity, error) {
	identities := make([]WorkloadPassIdentity, 0, len(results))
	for _, result := range results {
		if result.Disposition != WorkloadDispositionExecuted {
			continue
		}
		identity, ok := verified[result.Identity.WorkloadID]
		if !ok || identity != result.Identity {
			return nil, fmt.Errorf("executed workload result %q is not in the verified authority identity set", result.Identity.WorkloadID)
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

// validatedCurrentWorkloadPassEvidence 批量发现并重验当前代已经存在的规范 proof。
// 同一 identity 的重复成功执行保留首个权威来源，不覆盖 canonical 行；损坏行仍阻断。
func validatedCurrentWorkloadPassEvidence(tx *sql.Tx, generation uint64, identities []WorkloadPassIdentity) (map[string]struct{}, error) {
	canonical := make(map[string]struct{})
	candidates, err := queryCurrentWorkloadPassEvidenceIdentities(tx, generation, identities)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return canonical, nil
	}
	validated, err := loadWorkloadPassEvidenceForIdentitiesWithStats(tx, candidates, generation, nil)
	if err != nil {
		return nil, fmt.Errorf("validate canonical current workload pass evidence: %w", err)
	}
	for _, evidence := range validated {
		if evidence.OriginAcceptedGeneration == generation {
			canonical[evidence.Identity.IdentityDigest] = struct{}{}
		}
	}
	if len(canonical) != len(candidates) {
		return nil, errors.New("current workload pass evidence did not reload as canonical authority")
	}
	return canonical, nil
}

// queryCurrentWorkloadPassEvidenceIdentities 批量发现当前代已经存在的精确 identity。
func queryCurrentWorkloadPassEvidenceIdentities(tx *sql.Tx, generation uint64, identities []WorkloadPassIdentity) ([]WorkloadPassIdentity, error) {
	generationText := strconv.FormatUint(generation, 10)
	candidates := make([]WorkloadPassIdentity, 0, len(identities))
	for start := 0; start < len(identities); start += workloadPassEvidenceLookupBatchSize {
		end := min(start+workloadPassEvidenceLookupBatchSize, len(identities))
		batch := identities[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch)+1)
		args = append(args, generationText)
		for _, identity := range batch {
			args = append(args, identity.IdentityDigest)
		}
		rows, err := tx.Query(`SELECT identity_digest FROM ci_workload_pass_evidence WHERE accepted_generation = ? AND identity_digest IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, mapDurationLedgerSQLiteError("query current workload pass evidence", err)
		}
		found, scanErr := scanCurrentWorkloadPassEvidenceIdentities(rows, batch)
		closeErr := rows.Close()
		if scanErr != nil {
			return nil, scanErr
		}
		if closeErr != nil {
			return nil, mapDurationLedgerSQLiteError("close current workload pass evidence query", closeErr)
		}
		candidates = append(candidates, found...)
	}
	return candidates, nil
}

// scanCurrentWorkloadPassEvidenceIdentities 将精确当前代行恢复为原始请求身份。
func scanCurrentWorkloadPassEvidenceIdentities(rows *sql.Rows, requested []WorkloadPassIdentity) ([]WorkloadPassIdentity, error) {
	byDigest := make(map[string]WorkloadPassIdentity, len(requested))
	for _, identity := range requested {
		byDigest[identity.IdentityDigest] = identity
	}
	result := make([]WorkloadPassIdentity, 0, len(requested))
	for rows.Next() {
		var digest string
		if err := rows.Scan(&digest); err != nil {
			return nil, mapDurationLedgerSQLiteError("scan current workload pass evidence", err)
		}
		identity, ok := byDigest[digest]
		if !ok {
			return nil, errors.New("current workload pass evidence returned an unrequested identity")
		}
		result = append(result, identity)
		delete(byDigest, digest)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDurationLedgerSQLiteError("iterate current workload pass evidence", err)
	}
	return result, nil
}
