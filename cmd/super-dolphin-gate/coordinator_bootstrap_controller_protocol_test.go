package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const productionBootstrapControllerTestKeyFile = "bootstrap-controller-test-key.json"

type productionBootstrapControllerTestKey struct {
	Signer     gatecontract.SignerIdentity `json:"signer"`
	PrivateKey string                      `json:"private_key"`
}

func TestMain(m *testing.M) {
	if len(os.Args) == 3 && os.Args[1] == "bootstrap" && os.Args[2] == "--protocol-version=1" {
		os.Exit(runProductionBootstrapControllerTestProcess())
	}
	os.Exit(m.Run())
}

func runProductionBootstrapControllerTestProcess() int {
	keyData, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), productionBootstrapControllerTestKeyFile))
	if err != nil {
		return 91
	}
	decoder := json.NewDecoder(bytes.NewReader(keyData))
	decoder.DisallowUnknownFields()
	var key productionBootstrapControllerTestKey
	if err := decoder.Decode(&key); err != nil {
		return 92
	}
	privateKey, err := base64.StdEncoding.DecodeString(key.PrivateKey)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return 93
	}
	requestDecoder := json.NewDecoder(os.Stdin)
	requestDecoder.DisallowUnknownFields()
	var request productionBootstrapRequest
	if err := requestDecoder.Decode(&request); err != nil {
		return 94
	}
	requestDigest, err := productionBootstrapJSONDigest(request)
	if err != nil {
		return 95
	}
	started := time.Now().UTC().Truncate(time.Millisecond)
	record, argvDigest, err := signProductionBootstrapTestProcessRecord(request, requestDigest, key.Signer, privateKey, started)
	if err != nil {
		return 96
	}
	attestation := productionBootstrapAttestation{
		SchemaVersion: productionBootstrapProtocolVersion, Challenge: request.Challenge,
		RootDigest: request.RootDigest, RequestDigest: requestDigest,
		ControllerDigest: request.Controller.BinaryDigest, Record: record,
		ContainerID: strings.Repeat("c", 64), ContainerArgvDigest: argvDigest,
		ContainerLogDigest: productionDigest("d"), ContainerInspectDigest: productionDigest("e"),
		StartedAt: started, CompletedAt: started.Add(2 * time.Second),
	}
	unsigned, err := json.Marshal(attestation)
	if err != nil {
		return 98
	}
	attestation.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, unsigned))
	if err := json.NewEncoder(os.Stdout).Encode(attestation); err != nil {
		return 99
	}
	return 0
}

func signProductionBootstrapTestProcessRecord(
	request productionBootstrapRequest,
	requestDigest string,
	signer gatecontract.SignerIdentity,
	privateKey ed25519.PrivateKey,
	started time.Time,
) (gatecontract.AcceptedImageRecord, string, error) {
	record := productionBootstrapRecordForTestProcess(request, signer, started)
	payload, err := gatecontract.AcceptedImageSigningPayload(record)
	if err != nil {
		return gatecontract.AcceptedImageRecord{}, "", err
	}
	record.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
	argvDigest, err := productionBootstrapJSONDigest(productionBootstrapContainerArgv(requestDigest))
	return record, argvDigest, err
}

func productionBootstrapRecordForTestProcess(
	request productionBootstrapRequest,
	signer gatecontract.SignerIdentity,
	acceptedAt time.Time,
) gatecontract.AcceptedImageRecord {
	image := request.Runner
	image.Registry = request.CandidateRegistry
	return gatecontract.AcceptedImageRecord{
		SchemaVersion: gatecontract.AcceptedImageRecordSchemaVersion,
		RepoID:        request.RepoID, TrustedRef: request.TrustedRef, TrustedCommit: request.BaselineCommit,
		SourceTree: request.BaselineTree, PolicyDigest: request.PolicyDigest, ImageInputDigest: request.ImageInputDigest,
		Image: image, Runner: gatecontract.TrustedRunnerIdentity{
			BinaryDigest: request.Controller.BinaryDigest, Signer: request.Controller.Signer, PolicyDigest: request.PolicyDigest,
		},
		Generation: 1, AcceptedAt: acceptedAt.Add(time.Second), Signer: signer,
	}
}
