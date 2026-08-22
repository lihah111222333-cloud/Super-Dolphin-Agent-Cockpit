package remotecicli

import (
	"encoding/json"
	"errors"
	"io"
	"time"
)

func writePortableValidationStages(stderr io.Writer, operationID, manifestDigest string) error {
	stages := []string{"descriptor_path_accepted", "descriptor_bytes_read", "descriptor_decoded"}
	for sequence, code := range stages {
		if err := writePortableValidationEvent(stderr, operationID, manifestDigest, sequence+1, "request_validation", code); err != nil {
			return err
		}
	}
	return nil
}

func writePortableValidationEvent(stderr io.Writer, operationID, manifestDigest string, sequence int, stage, code string) error {
	event := struct {
		Schema         string `json:"schema"`
		OperationID    string `json:"operation_id"`
		ManifestDigest string `json:"manifest_digest"`
		Seq            int    `json:"seq"`
		Time           string `json:"time"`
		Stage          string `json:"stage"`
		State          string `json:"state"`
		Code           string `json:"code"`
		NodeIDDigest   string `json:"node_id_digest"`
	}{"remote-ci-event/v1", operationID, manifestDigest, sequence, time.Now().UTC().Format(time.RFC3339Nano), stage, "completed", code, digestBytes([]byte("remote-ci-validation-node/v1"))}
	data, err := json.Marshal(event)
	if err != nil {
		return errors.New("encode validation event")
	}
	data = append(data, '\n')
	_, err = stderr.Write(data)
	return err
}
