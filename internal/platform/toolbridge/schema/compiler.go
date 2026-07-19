package schema

import (
	"bytes"
	"encoding/json"
	"fmt"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

type denyURLLoader struct{}

// Load 拒绝 jsonschema 编译器的所有外部 URL 加载。
func (denyURLLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema resource forbidden: %s", url)
}

// executeLocal 在 helper 进程内重验身份和摘要后完成一次编译或校验。
func executeLocal(request protocolRequest) protocolResponse {
	response := baseResponse(request)
	canonical, err := Canonicalize(request.CanonicalSchema)
	if err != nil {
		return errorResponse(response, ErrorCode(err), "schema pre-scan failed")
	}
	if !bytes.Equal(canonical.Bytes, request.CanonicalSchema) ||
		canonical.Digest != request.SchemaDigest ||
		canonical.Draft != request.Draft ||
		recomputeDigest(request.CanonicalSchema) != request.SchemaDigest {
		return errorResponse(response, CodeDigestMismatch, "schema digest mismatch")
	}

	compiled, err := compileCanonical(canonical)
	if err != nil {
		return errorResponse(response, CodeCompileFailed, "schema compilation failed")
	}
	if request.Operation == OperationValidate {
		return validateCompiledArguments(request, response, canonical.Digest, compiled)
	}
	response.OK = true
	response.CompiledDigest = canonical.Digest
	return response
}

func validateCompiledArguments(
	request protocolRequest,
	response protocolResponse,
	digest string,
	compiled *jsonschema.Schema,
) protocolResponse {
	arguments, _, err := parseJSON(request.Arguments, false)
	if err != nil {
		return errorResponse(response, ErrorCode(err), "arguments JSON is invalid")
	}
	argumentValue, err := arguments.toInterface()
	if err != nil {
		return errorResponse(response, CodeInvalidEnvelope, "arguments contain an unsupported JSON value")
	}
	if err := compiled.Validate(argumentValue); err != nil {
		return errorResponse(response, CodeArgumentInvalid, "arguments do not satisfy schema")
	}
	valid := true
	response.ArgumentsValid = &valid
	response.OK = true
	response.CompiledDigest = digest
	return response
}

func compileCanonical(canonical CanonicalSchema) (*jsonschema.Schema, error) {
	draft, err := jsonschemaDraft(canonical.Draft)
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(canonical.Bytes))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(draft)
	compiler.UseLoader(denyURLLoader{})
	location := "urn:reasonix:mcp-schema:" + canonical.Digest
	if err := compiler.AddResource(location, document); err != nil {
		return nil, err
	}
	return compiler.Compile(location)
}

// jsonschemaDraft 将冻结草案 URI 映射到库内置草案。
func jsonschemaDraft(uri string) (*jsonschema.Draft, error) {
	switch uri {
	case "http://json-schema.org/draft-04/schema":
		return jsonschema.Draft4, nil
	case "http://json-schema.org/draft-06/schema":
		return jsonschema.Draft6, nil
	case "http://json-schema.org/draft-07/schema":
		return jsonschema.Draft7, nil
	case "https://json-schema.org/draft/2019-09/schema":
		return jsonschema.Draft2019, nil
	case defaultDraftURI:
		return jsonschema.Draft2020, nil
	default:
		return nil, newDiagnostic(CodeDraftUnsupported, "unsupported draft URI", nil)
	}
}

func baseResponse(request protocolRequest) protocolResponse {
	return protocolResponse{
		Protocol:            request.Protocol,
		Operation:           request.Operation,
		RequestID:           request.RequestID,
		ServerID:            request.ServerID,
		ToolName:            request.ToolName,
		AuthorityGeneration: request.AuthorityGeneration,
		Draft:               request.Draft,
		SchemaDigest:        request.SchemaDigest,
	}
}

func errorResponse(response protocolResponse, code Code, message string) protocolResponse {
	if !isKnownCode(code) {
		code = CodeCompileFailed
	}
	if response.Operation == OperationValidate && response.ArgumentsValid == nil {
		valid := false
		response.ArgumentsValid = &valid
	}
	response.OK = false
	response.Code = code
	response.Message = message
	response.CompiledDigest = ""
	return response
}

// toInterface 将严格解析值转换为 jsonschema 校验输入。
func (value *jsonValue) toInterface() (any, error) {
	switch value.kind {
	case kindNull:
		return nil, nil
	case kindBool:
		return value.boolean, nil
	case kindString:
		return value.text, nil
	case kindNumber:
		return json.Number(value.text), nil
	case kindArray:
		return value.arrayToInterface()
	case kindObject:
		return value.objectToInterface()
	default:
		return nil, fmt.Errorf("unsupported JSON value kind %d", value.kind)
	}
}

func (value *jsonValue) arrayToInterface() ([]any, error) {
	result := make([]any, len(value.array))
	for index, item := range value.array {
		converted, err := item.toInterface()
		if err != nil {
			return nil, err
		}
		result[index] = converted
	}
	return result, nil
}

func (value *jsonValue) objectToInterface() (map[string]any, error) {
	result := make(map[string]any, len(value.object))
	for key, item := range value.object {
		converted, err := item.toInterface()
		if err != nil {
			return nil, err
		}
		result[key] = converted
	}
	return result, nil
}
